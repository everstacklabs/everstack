package fnexec

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
)

// hostExecer exercises the same materialization and wrapper path that a
// sandbox backend uses, but against a test-owned temporary directory. It is
// intentionally limited to tests that do not install packages or use the
// network.
type hostExecer struct{}

func (hostExecer) Exec(ctx context.Context, call ExecCall) (*ExecOutcome, error) {
	runCtx := ctx
	cancel := func() {}
	if call.TimeoutMS > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(call.TimeoutMS)*time.Millisecond)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, call.Command[0], call.Command[1:]...)
	cmd.Dir = call.WorkDir
	cmd.Env = os.Environ()
	for key, value := range call.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	outcome := &ExecOutcome{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		TimedOut: runCtx.Err() == context.DeadlineExceeded,
	}
	if err == nil {
		return outcome, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		outcome.ExitCode = exitErr.ExitCode()
		return outcome, nil
	}
	return nil, err
}

func (hostExecer) WriteFile(_ context.Context, filePath string, content []byte) error {
	return os.WriteFile(filePath, content, 0o600)
}

func TestParseExecutionResult_SuccessJSON(t *testing.T) {
	res := ParseExecutionResult(`{"__result__":{"ok":true}}`, "", 0, false)
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	obj, ok := res.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", res.Result)
	}
	if obj["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", obj["ok"])
	}
}

func TestParseExecutionResult_Timeout(t *testing.T) {
	res := ParseExecutionResult("", "", 0, true)
	if res.Success {
		t.Fatalf("expected timeout failure")
	}
	if res.ErrorType != isolation.ErrorTypeTimeout {
		t.Fatalf("expected timeout error type, got %s", res.ErrorType)
	}
}

func TestParseExecutionResult_OOM(t *testing.T) {
	res := ParseExecutionResult("", "killed", 137, false)
	if res.Success || res.ErrorType != isolation.ErrorTypeOOM {
		t.Fatalf("expected OOM error, got %+v", res)
	}
}

func TestParseExecutionResult_Syntax(t *testing.T) {
	res := ParseExecutionResult("", "SyntaxError: unexpected token", 1, false)
	if res.Success || res.ErrorType != isolation.ErrorTypeSyntax {
		t.Fatalf("expected syntax error, got %+v", res)
	}
}

func TestParseExecutionResult_NonZeroExit(t *testing.T) {
	res := ParseExecutionResult("", "boom on stderr", 1, false)
	if res.Success {
		t.Fatalf("expected failure on non-zero exit")
	}
	if res.ErrorType != isolation.ErrorTypeRuntime {
		t.Fatalf("expected runtime error type, got %s", res.ErrorType)
	}
	if res.Error != "boom on stderr" {
		t.Fatalf("expected stderr surfaced as error, got %q", res.Error)
	}
}

func TestParseExecutionResult_PlainStdoutFallback(t *testing.T) {
	res := ParseExecutionResult("just some text", "", 0, false)
	if !res.Success {
		t.Fatalf("expected success")
	}
	if res.Result != "just some text" {
		t.Fatalf("expected raw stdout fallback, got %#v", res.Result)
	}
}

func TestSupportsRuntime(t *testing.T) {
	for _, rt := range []isolation.Runtime{isolation.RuntimeNodeJS20, isolation.RuntimeDeno, isolation.RuntimePython3} {
		if !SupportsRuntime(rt) {
			t.Fatalf("expected %s supported", rt)
		}
	}
	if SupportsRuntime(isolation.Runtime("go")) {
		t.Fatalf("expected 'go' unsupported")
	}
}

// scriptExecer records writes/execs and returns a scripted result for the
// final runtime command (identified by a non-mkdir/non-install command).
type scriptExecer struct {
	mu        sync.Mutex
	writes    map[string][]byte
	runStdout string
	execCmds  [][]string
	execCalls []ExecCall
}

func newScriptExecer(runStdout string) *scriptExecer {
	return &scriptExecer{writes: map[string][]byte{}, runStdout: runStdout}
}

func (e *scriptExecer) Exec(_ context.Context, cmd ExecCall) (*ExecOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.execCmds = append(e.execCmds, cmd.Command)
	e.execCalls = append(e.execCalls, cmd)
	// The runtime command is the one that carries the wrapper path.
	joined := strings.Join(cmd.Command, " ")
	if strings.Contains(joined, "wrapper.") {
		return &ExecOutcome{Stdout: e.runStdout, ExitCode: 0}, nil
	}
	return &ExecOutcome{ExitCode: 0}, nil
}

func (e *scriptExecer) WriteFile(_ context.Context, path string, content []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writes[path] = content
	return nil
}

func (e *scriptExecer) wroteSuffix(suffix string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for p := range e.writes {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

func TestDispatch_NodeHappyPath(t *testing.T) {
	ex := newScriptExecer(`{"__result__":"hi"}`)
	req := isolation.ExecutionRequest{
		RequestID: "req-1",
		Runtime:   isolation.RuntimeNodeJS20,
		Code:      "export default () => 'hi'",
		TimeoutMS: 5000,
	}
	res := Dispatch(context.Background(), ex, "/tmp/function", false, req)
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if res.Result != "hi" {
		t.Fatalf("expected result 'hi', got %#v", res.Result)
	}
	if !ex.wroteSuffix("function.mjs") {
		t.Fatalf("expected code file written")
	}
	if !ex.wroteSuffix("wrapper.mjs") {
		t.Fatalf("expected wrapper written")
	}
}

func TestDispatch_ProjectFilesAndNamedExport(t *testing.T) {
	ex := newScriptExecer(`{"__result__":5}`)
	req := isolation.ExecutionRequest{
		RequestID: "project-1",
		Runtime:   isolation.RuntimeDeno,
		Files: []isolation.SourceFile{
			{Path: "src/math.ts", Content: []byte("export const add = (a, b) => a + b;\n")},
			{Path: "src/main.ts", Content: []byte("import { add } from './math.ts';\nexport const calculate = ({a, b}) => add(a, b);\n"), Mode: 0o755},
		},
		Entrypoint: "src/main.ts",
		Export:     "calculate",
		Arguments:  map[string]interface{}{"a": 2, "b": 3},
		TimeoutMS:  5000,
	}
	res := Dispatch(context.Background(), ex, "/workspace/.everstack/invocations", false, req)
	if !res.Success || res.Result != float64(5) {
		t.Fatalf("Dispatch() = %+v, want result 5", res)
	}
	if !ex.wroteSuffix("src/math.ts") || !ex.wroteSuffix("src/main.ts") {
		t.Fatalf("project files were not materialized: %+v", ex.writes)
	}

	ex.mu.Lock()
	defer ex.mu.Unlock()
	var runtimeCall *ExecCall
	for i := range ex.execCalls {
		if strings.Contains(strings.Join(ex.execCalls[i].Command, " "), "wrapper.ts") {
			runtimeCall = &ex.execCalls[i]
			break
		}
	}
	if runtimeCall == nil {
		t.Fatal("runtime command was not executed")
	}
	joined := strings.Join(runtimeCall.Command, " ")
	if !strings.Contains(joined, "src/main.ts") || !strings.Contains(joined, "calculate") {
		t.Fatalf("runtime command = %q, want entrypoint and named export", joined)
	}
	if !strings.Contains(joined, "--allow-read="+runtimeCall.WorkDir) {
		t.Fatalf("runtime command = %q, want Deno read scope confined to workdir %q", joined, runtimeCall.WorkDir)
	}
	foundExecutableMode := false
	for _, call := range ex.execCalls {
		if len(call.Command) >= 3 && call.Command[0] == "chmod" && call.Command[1] == "755" && strings.HasSuffix(call.Command[2], "src/main.ts") {
			foundExecutableMode = true
		}
	}
	if !foundExecutableMode {
		t.Fatalf("project file mode was not materialized: %+v", ex.execCmds)
	}
}

func TestDispatch_ExecutesMultiFileProjects(t *testing.T) {
	tests := []struct {
		name       string
		binary     string
		runtime    isolation.Runtime
		files      []isolation.SourceFile
		entrypoint string
		export     string
	}{
		{
			name:    "node",
			binary:  "node",
			runtime: isolation.RuntimeNodeJS20,
			files: []isolation.SourceFile{
				{Path: "src/math.js", Content: []byte("export const add = (a, b) => a + b;\n")},
				{Path: "src/main.js", Content: []byte("import { add } from './math.js';\nexport const calculate = ({ a, b }) => add(a, b);\n")},
			},
			entrypoint: "src/main.js",
			export:     "calculate",
		},
		{
			name:    "python",
			binary:  "python3",
			runtime: isolation.RuntimePython3,
			files: []isolation.SourceFile{
				{Path: "src/math_helper.py", Content: []byte("def add(a, b):\n    return a + b\n")},
				{Path: "src/main.py", Content: []byte("from math_helper import add\n\ndef calculate(args):\n    return add(args['a'], args['b'])\n")},
			},
			entrypoint: "src/main.py",
			export:     "calculate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := exec.LookPath(tt.binary); err != nil {
				t.Skipf("%s is not installed", tt.binary)
			}
			res := Dispatch(context.Background(), hostExecer{}, t.TempDir(), true, isolation.ExecutionRequest{
				RequestID:  "integration",
				Runtime:    tt.runtime,
				Files:      tt.files,
				Entrypoint: tt.entrypoint,
				Export:     tt.export,
				Arguments:  map[string]interface{}{"a": 2, "b": 3},
				// This integration test starts the host runtime cold while the full
				// repository suite is running. Timeout behavior is covered separately.
				TimeoutMS: 30_000,
			})
			if !res.Success || res.Result != float64(5) {
				t.Fatalf("Dispatch() = %+v, want result 5", res)
			}
		})
	}
}

func TestDispatch_KeepsArgumentsOutOfProcessArguments(t *testing.T) {
	ex := newScriptExecer(`{"__result__":"ok"}`)
	secret := "sensitive-api-token"
	res := Dispatch(context.Background(), ex, "/tmp/function", false, isolation.ExecutionRequest{
		RequestID: "private-args", Runtime: isolation.RuntimePython3,
		Code: "def handler(args): return 'ok'", Export: "handler",
		Arguments: map[string]interface{}{"token": secret}, TimeoutMS: 1000,
	})
	if !res.Success {
		t.Fatalf("Dispatch() = %+v", res)
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	for _, call := range ex.execCalls {
		if strings.Contains(strings.Join(call.Command, " "), secret) {
			t.Fatalf("function arguments leaked into process argv: %q", call.Command)
		}
	}
	foundProtectedArgs := false
	for filePath, content := range ex.writes {
		if strings.HasSuffix(filePath, "/.everstack/args.json") && strings.Contains(string(content), secret) {
			foundProtectedArgs = true
		}
	}
	if !foundProtectedArgs {
		t.Fatalf("arguments were not written to the protected invocation file: %+v", ex.writes)
	}
}

func TestDispatch_ProjectRejectsUnsafeEntrypoint(t *testing.T) {
	ex := newScriptExecer("")
	res := Dispatch(context.Background(), ex, "/tmp/function", false, isolation.ExecutionRequest{
		RequestID:  "unsafe",
		Runtime:    isolation.RuntimePython3,
		Files:      []isolation.SourceFile{{Path: "main.py", Content: []byte("def handler(args): return args")}},
		Entrypoint: "../main.py",
		TimeoutMS:  1000,
	})
	if res.Success || !strings.Contains(res.Error, "entrypoint") {
		t.Fatalf("Dispatch() = %+v, want unsafe entrypoint failure", res)
	}
}

func TestDispatch_ProjectRejectsReservedRuntimeFiles(t *testing.T) {
	ex := newScriptExecer("")
	res := Dispatch(context.Background(), ex, "/tmp/function", false, isolation.ExecutionRequest{
		RequestID: "reserved", Runtime: isolation.RuntimePython3,
		Files: []isolation.SourceFile{
			{Path: "main.py", Content: []byte("def handler(args): return args")},
			{Path: ".everstack/args.json", Content: []byte(`{"token":"overwrite"}`)},
		},
		Entrypoint: "main.py", TimeoutMS: 1000,
	})
	if res.Success || !strings.Contains(res.Error, "reserved .everstack runtime directory") {
		t.Fatalf("Dispatch() = %+v, want reserved runtime path failure", res)
	}
}

func TestDispatch_NodeProjectDefaultsToESModules(t *testing.T) {
	ex := newScriptExecer(`{"__result__":"ok"}`)
	res := Dispatch(context.Background(), ex, "/tmp/function", false, isolation.ExecutionRequest{
		RequestID: "node-project", Runtime: isolation.RuntimeNodeJS20,
		Files: []isolation.SourceFile{
			{Path: "src/helper.js", Content: []byte("export const value = 'ok';\n")},
			{Path: "src/main.js", Content: []byte("import { value } from './helper.js';\nexport default () => value;\n")},
		},
		Entrypoint: "src/main.js", Export: "default", TimeoutMS: 1000,
	})
	if !res.Success {
		t.Fatalf("Dispatch() = %+v", res)
	}
	if !ex.wroteSuffix("package.json") {
		t.Fatal("Node project did not receive an ESM package.json")
	}
}

func TestDispatch_PythonProjectAddsEntrypointDirectoryToPythonPath(t *testing.T) {
	ex := newScriptExecer(`{"__result__":"ok"}`)
	res := Dispatch(context.Background(), ex, "/tmp/function", false, isolation.ExecutionRequest{
		RequestID: "python-project", Runtime: isolation.RuntimePython3,
		Files: []isolation.SourceFile{
			{Path: "src/helper.py", Content: []byte("value = 'ok'\n")},
			{Path: "src/main.py", Content: []byte("from helper import value\ndef handler(args): return value\n")},
		},
		Entrypoint: "src/main.py", Export: "handler", TimeoutMS: 1000,
	})
	if !res.Success {
		t.Fatalf("Dispatch() = %+v", res)
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	for _, call := range ex.execCalls {
		if strings.Contains(strings.Join(call.Command, " "), "wrapper.py") {
			if got := call.Env["PYTHONPATH"]; !strings.HasPrefix(got, path.Join(call.WorkDir, "src")+":") {
				t.Fatalf("PYTHONPATH = %q, want entrypoint directory first", got)
			}
			return
		}
	}
	t.Fatal("Python runtime command was not executed")
}

func TestParseExecutionResult_ObjectWithoutResultKey(t *testing.T) {
	// A user-printed JSON object without __result__ must not be mistaken
	// for the wrapper envelope; the wrapper's real result line wins.
	res := ParseExecutionResult("{\"log\":\"hi\"}\n{\"__result__\":42}", "", 0, false)
	if !res.Success {
		t.Fatalf("expected success")
	}
	if got, ok := res.Result.(float64); !ok || got != 42 {
		t.Fatalf("expected result 42, got %#v", res.Result)
	}
}

func TestParseExecutionResult_DirectForm(t *testing.T) {
	res := ParseExecutionResult(`{"success":false,"error":"boom"}`, "", 0, false)
	if res.Success {
		t.Fatalf("expected failure from direct form")
	}
	if res.Error != "boom" {
		t.Fatalf("expected error 'boom', got %q", res.Error)
	}
}

func TestDispatch_UniqueScratchDirPerCall(t *testing.T) {
	ex := newScriptExecer(`{"__result__":"ok"}`)
	req := isolation.ExecutionRequest{
		RequestID: "same-id",
		Runtime:   isolation.RuntimeNodeJS20,
		Code:      "x",
		TimeoutMS: 1000,
	}
	Dispatch(context.Background(), ex, "/tmp/function", false, req)
	Dispatch(context.Background(), ex, "/tmp/function", false, req)

	dirs := map[string]bool{}
	ex.mu.Lock()
	for p := range ex.writes {
		if strings.HasSuffix(p, "function.mjs") {
			dirs[path.Dir(p)] = true
		}
	}
	ex.mu.Unlock()
	if len(dirs) != 2 {
		t.Fatalf("same RequestID must still get distinct scratch dirs, got %d: %v", len(dirs), dirs)
	}
}

func TestDispatch_UnsupportedRuntime(t *testing.T) {
	ex := newScriptExecer("")
	req := isolation.ExecutionRequest{RequestID: "r", Runtime: isolation.Runtime("rust"), Code: "x", TimeoutMS: 1000}
	res := Dispatch(context.Background(), ex, "/tmp/function", false, req)
	if res.Success {
		t.Fatalf("expected failure for unsupported runtime")
	}
}
