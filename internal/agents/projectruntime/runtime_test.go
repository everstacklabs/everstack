package projectruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/agents/revision"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/fnexec"
)

type fakeExecer struct {
	writes map[string][]byte
	calls  []fnexec.ExecCall
}

func (f *fakeExecer) Exec(_ context.Context, call fnexec.ExecCall) (*fnexec.ExecOutcome, error) {
	f.calls = append(f.calls, call)
	if strings.Contains(strings.Join(call.Command, " "), "wrapper.") {
		return &fnexec.ExecOutcome{Stdout: `{"__result__":{"ok":true}}`, ExitCode: 0}, nil
	}
	return &fnexec.ExecOutcome{ExitCode: 0}, nil
}

func (f *fakeExecer) WriteFile(_ context.Context, path string, content []byte) error {
	if f.writes == nil {
		f.writes = map[string][]byte{}
	}
	f.writes[path] = append([]byte(nil), content...)
	return nil
}

func TestRuntimeRunsNamedFunctionFromImmutableManifest(t *testing.T) {
	t.Parallel()

	manifest, err := revision.NewManifest([]revision.File{
		{Path: "src/helper.py", Content: []byte("def answer(): return 42\n")},
		{Path: "src/main.py", Content: []byte("from helper import answer\ndef run(args): return {'ok': answer() == args['expected']}\n")},
	}, []revision.Function{{
		Name: "check_answer", Path: "src/main.py", Export: "run", Runtime: isolation.RuntimePython3,
	}})
	if err != nil {
		t.Fatal(err)
	}
	execer := &fakeExecer{}
	runtime := New(Config{GuestWorkDir: "/workspace/.everstack/functions", CleanupOnExit: true})
	result := runtime.Run(context.Background(), execer, RunRequest{
		RequestID: "call-1",
		Revision:  &revision.Revision{ID: "revision-1", Manifest: *manifest},
		Function:  "check_answer",
		Arguments: map[string]interface{}{
			"expected": 42,
		},
		TimeoutMS: 5000,
	})
	if !result.Success {
		t.Fatalf("Run() = %+v, want success", result)
	}
	value, ok := result.Result.(map[string]interface{})
	if !ok || value["ok"] != true {
		t.Fatalf("result = %#v, want ok=true", result.Result)
	}
	if result.DurationMS < 0 {
		t.Fatalf("duration = %d, want non-negative", result.DurationMS)
	}
	foundHelper := false
	for path := range execer.writes {
		if strings.HasSuffix(path, "src/helper.py") {
			foundHelper = true
		}
	}
	if !foundHelper {
		t.Fatalf("revision helper file was not materialized: %+v", execer.writes)
	}
}

func TestRuntimeRejectsUnknownProjectFunction(t *testing.T) {
	t.Parallel()

	manifest, err := revision.NewManifest(
		[]revision.File{{Path: "main.py", Content: []byte("def handler(args): return args")}},
		[]revision.Function{{Name: "known_function", Path: "main.py", Runtime: isolation.RuntimePython3}},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := New(Config{}).Run(context.Background(), &fakeExecer{}, RunRequest{
		Revision: &revision.Revision{ID: "revision-1", Manifest: *manifest},
		Function: "missing_function",
	})
	if result.Success || !strings.Contains(result.Error, "not declared") {
		t.Fatalf("Run() = %+v, want unknown function failure", result)
	}
}
