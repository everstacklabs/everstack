// Package fnexec holds the transport-agnostic runtime dispatch for
// isolated functions: given an already-running guest (a Firecracker
// microVM), write the user's code + optional packages + a per-runtime
// wrapper into a scratch workdir, execute it, and parse the result.
//
// The guest is reached through the small Execer interface, so the same
// dispatch logic runs behind two different transports:
//   - in-gateway (legacy): a *sandbox/firecracker.MicroVM toolbox, and
//   - agent-side (persistent envs): the sandbox.Backend Exec/WriteFile
//     surface of a tracked function-environment VM.
//
// fnexec owns NONE of the VM lifecycle (create/reuse/destroy) — that is
// the caller's concern. It only knows how to run one invocation inside a
// guest that is already up.
package fnexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/google/uuid"
)

// Execer is the minimal in-guest execution surface a function invocation
// needs. Both the Firecracker MicroVM toolbox and the sandbox.Backend
// satisfy it via thin adapters.
type Execer interface {
	// Exec runs a command inside the guest and returns its outcome.
	Exec(ctx context.Context, cmd ExecCall) (*ExecOutcome, error)
	// WriteFile writes content to an absolute path inside the guest.
	WriteFile(ctx context.Context, path string, content []byte) error
}

// ExecCall describes a single command to run inside the guest.
type ExecCall struct {
	Command   []string
	WorkDir   string
	Env       map[string]string
	TimeoutMS int
}

// ExecOutcome is the result of an ExecCall.
type ExecOutcome struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// Dispatch runs one function invocation inside an already-running guest.
//
// guestWorkDir is the root under which a unique per-request scratch dir
// is created (e.g. "/tmp/function"). When cleanupOnExit is true the
// scratch dir is removed after the run — leave it false for warm envs
// that will be reused (each request gets its own scratch dir anyway, so
// leftover dirs are harmless and cleanup just adds an exec round-trip).
func Dispatch(ctx context.Context, ex Execer, guestWorkDir string, cleanupOnExit bool, req isolation.ExecutionRequest) *isolation.ExecutionResult {
	// Scratch dir is per-invocation. A uuid suffix is ALWAYS appended so
	// two concurrent invocations can never share a workdir even if they
	// carry the same (or empty) RequestID — filesystem isolation must not
	// depend on caller-supplied ids being unique.
	workDir := path.Join(guestWorkDir, SafeID(req.RequestID)+"-"+uuid.NewString()[:8])

	if _, err := ex.Exec(ctx, ExecCall{
		Command:   []string{"mkdir", "-p", workDir},
		TimeoutMS: 5000,
	}); err != nil {
		return runtimeErr("failed to prepare workdir: %v", err)
	}

	if cleanupOnExit {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = ex.Exec(cleanupCtx, ExecCall{
				Command:   []string{"rm", "-rf", workDir},
				TimeoutMS: 5000,
			})
		}()
	}

	runtimeCfg, err := runtimeExecutionConfig(req.Runtime, workDir)
	if err != nil {
		return runtimeErr("%s", err.Error())
	}

	codePath := path.Join(workDir, runtimeCfg.CodeFilename)
	if len(req.Files) > 0 {
		entrypoint, err := safeProjectPath(req.Entrypoint)
		if err != nil {
			return runtimeErr("invalid project entrypoint: %v", err)
		}
		seen := make(map[string]struct{}, len(req.Files))
		dirs := map[string]struct{}{path.Join(workDir, ".everstack"): {}}
		filesByMode := make(map[int32][]string)
		for _, file := range req.Files {
			filePath, err := safeProjectPath(file.Path)
			if err != nil {
				return runtimeErr("invalid project file %q: %v", file.Path, err)
			}
			if _, duplicate := seen[filePath]; duplicate {
				return runtimeErr("duplicate project file %q", filePath)
			}
			seen[filePath] = struct{}{}
			mode := file.Mode
			if mode == 0 {
				mode = 0o644
			}
			if mode < 0 || mode > 0o777 {
				return runtimeErr("project file %q has invalid mode %#o", filePath, mode)
			}
			filesByMode[mode] = append(filesByMode[mode], path.Join(workDir, filePath))
			if dir := path.Dir(path.Join(workDir, filePath)); dir != workDir {
				dirs[dir] = struct{}{}
			}
		}
		if _, ok := seen[entrypoint]; !ok {
			return runtimeErr("project entrypoint %q is not present in files", entrypoint)
		}
		dirList := make([]string, 0, len(dirs))
		for dir := range dirs {
			dirList = append(dirList, dir)
		}
		sort.Strings(dirList)
		mkdir := append([]string{"mkdir", "-p"}, dirList...)
		if _, err := ex.Exec(ctx, ExecCall{Command: mkdir, TimeoutMS: 5000}); err != nil {
			return runtimeErr("failed to prepare project directories: %v", err)
		}
		for _, file := range req.Files {
			if err := ex.WriteFile(ctx, path.Join(workDir, file.Path), file.Content); err != nil {
				return runtimeErr("failed to write project file %q: %v", file.Path, err)
			}
		}
		modes := make([]int, 0, len(filesByMode))
		for mode := range filesByMode {
			modes = append(modes, int(mode))
		}
		sort.Ints(modes)
		for _, rawMode := range modes {
			mode := int32(rawMode)
			files := filesByMode[mode]
			sort.Strings(files)
			command := append([]string{"chmod", strconv.FormatInt(int64(mode), 8)}, files...)
			if _, err := ex.Exec(ctx, ExecCall{Command: command, TimeoutMS: 5000}); err != nil {
				return runtimeErr("failed to apply project file mode %#o: %v", mode, err)
			}
		}
		if req.Runtime == isolation.RuntimeNodeJS20 {
			if _, declared := seen["package.json"]; !declared {
				if err := ex.WriteFile(ctx, path.Join(workDir, "package.json"), []byte(`{"type":"module"}`)); err != nil {
					return runtimeErr("failed to write project package.json: %v", err)
				}
			}
		}
		codePath = path.Join(workDir, entrypoint)
	} else {
		if err := ex.WriteFile(ctx, codePath, []byte(req.Code)); err != nil {
			return runtimeErr("failed to write code file: %v", err)
		}
	}

	if len(req.Packages) > 0 {
		if err := installPackages(ctx, ex, req, runtimeCfg, workDir); err != nil {
			return runtimeErr("%s", err.Error())
		}
	}

	wrapperDir := path.Join(workDir, ".everstack")
	if _, err := ex.Exec(ctx, ExecCall{Command: []string{"mkdir", "-p", wrapperDir}, TimeoutMS: 5000}); err != nil {
		return runtimeErr("failed to prepare wrapper directory: %v", err)
	}
	wrapperPath := path.Join(wrapperDir, runtimeCfg.WrapperFilename)
	if err := ex.WriteFile(ctx, wrapperPath, []byte(runtimeCfg.WrapperSource)); err != nil {
		return runtimeErr("failed to write runtime wrapper: %v", err)
	}

	argsJSON, err := json.Marshal(req.Arguments)
	if err != nil {
		return runtimeErr("failed to encode function arguments: %v", err)
	}
	argsPath := path.Join(wrapperDir, "args.json")
	if err := ex.WriteFile(ctx, argsPath, argsJSON); err != nil {
		return runtimeErr("failed to write function arguments: %v", err)
	}
	if _, err := ex.Exec(ctx, ExecCall{Command: []string{"chmod", "600", argsPath}, TimeoutMS: 5000}); err != nil {
		return runtimeErr("failed to protect function arguments: %v", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ex.Exec(cleanupCtx, ExecCall{Command: []string{"rm", "-f", argsPath}, TimeoutMS: 5000})
	}()

	cmd, env := runtimeCfg.BuildCommand(codePath, wrapperPath, argsPath, req)
	outcome, err := ex.Exec(ctx, ExecCall{
		Command:   cmd,
		WorkDir:   workDir,
		Env:       env,
		TimeoutMS: req.TimeoutMS,
	})
	if err != nil {
		return runtimeErr("runtime execution failed: %v", err)
	}

	return ParseExecutionResult(outcome.Stdout, outcome.Stderr, outcome.ExitCode, outcome.TimedOut)
}

func runtimeErr(format string, args ...interface{}) *isolation.ExecutionResult {
	return &isolation.ExecutionResult{
		Success:   false,
		Error:     fmt.Sprintf(format, args...),
		ErrorType: isolation.ErrorTypeRuntime,
	}
}

func installPackages(ctx context.Context, ex Execer, req isolation.ExecutionRequest, runtimeCfg runtimeConfig, workDir string) error {
	switch req.Runtime {
	case isolation.RuntimeNodeJS20:
		pkgJSON := `{"name":"function","type":"module"}`
		if err := ex.WriteFile(ctx, path.Join(workDir, "package.json"), []byte(pkgJSON)); err != nil {
			return fmt.Errorf("failed to write package.json: %w", err)
		}
		cmd := append([]string{"npm", "install", "--prefix", workDir}, req.Packages...)
		res, err := ex.Exec(ctx, ExecCall{
			Command:   cmd,
			WorkDir:   workDir,
			TimeoutMS: InstallTimeoutMs(req.TimeoutMS),
		})
		if err != nil {
			return fmt.Errorf("npm install failed: %w", err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("npm install failed: %s", strings.TrimSpace(nonEmpty(res.Stderr, res.Stdout)))
		}
	case isolation.RuntimePython3:
		cmd := []string{"python3", "-m", "pip", "install", "--target", workDir}
		cmd = append(cmd, req.Packages...)
		res, err := ex.Exec(ctx, ExecCall{
			Command:   cmd,
			WorkDir:   workDir,
			TimeoutMS: InstallTimeoutMs(req.TimeoutMS),
		})
		if err != nil {
			return fmt.Errorf("pip install failed: %w", err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("pip install failed: %s", strings.TrimSpace(nonEmpty(res.Stderr, res.Stdout)))
		}
	case isolation.RuntimeDeno:
		// Deno resolves URL/module imports directly — no install step.
	default:
		return fmt.Errorf("unsupported runtime %q for package install", runtimeCfg.Runtime)
	}
	return nil
}

type runtimeConfig struct {
	Runtime         isolation.Runtime
	CodeFilename    string
	WrapperFilename string
	WrapperSource   string
	BuildCommand    func(codePath, wrapperPath, argsPath string, req isolation.ExecutionRequest) ([]string, map[string]string)
}

func runtimeExecutionConfig(rt isolation.Runtime, workDir string) (runtimeConfig, error) {
	switch rt {
	case isolation.RuntimeNodeJS20:
		return runtimeConfig{
			Runtime:         rt,
			CodeFilename:    "function.mjs",
			WrapperFilename: "wrapper.mjs",
			WrapperSource:   nodeWrapperSource,
			BuildCommand: func(codePath, wrapperPath, argsPath string, req isolation.ExecutionRequest) ([]string, map[string]string) {
				return []string{"node", wrapperPath, codePath, argsPath, req.Export}, map[string]string{
					"NODE_PATH": path.Join(workDir, "node_modules"),
				}
			},
		}, nil
	case isolation.RuntimeDeno:
		return runtimeConfig{
			Runtime:         rt,
			CodeFilename:    "function.ts",
			WrapperFilename: "wrapper.ts",
			WrapperSource:   denoWrapperSource,
			BuildCommand: func(codePath, wrapperPath, argsPath string, req isolation.ExecutionRequest) ([]string, map[string]string) {
				cmd := []string{"deno", "run", "--allow-read=" + workDir, "--allow-write=/workspace", "--allow-env"}
				switch req.NetworkMode {
				case isolation.NetworkAllow:
					cmd = append(cmd, "--allow-net")
				case isolation.NetworkWhitelist:
					if len(req.AllowedHosts) > 0 {
						cmd = append(cmd, "--allow-net="+strings.Join(req.AllowedHosts, ","))
					}
				}
				cmd = append(cmd, wrapperPath, codePath, argsPath, req.Export)
				return cmd, nil
			},
		}, nil
	case isolation.RuntimePython3:
		return runtimeConfig{
			Runtime:         rt,
			CodeFilename:    "function.py",
			WrapperFilename: "wrapper.py",
			WrapperSource:   pythonWrapperSource,
			BuildCommand: func(codePath, wrapperPath, argsPath string, req isolation.ExecutionRequest) ([]string, map[string]string) {
				return []string{"python3", wrapperPath, codePath, argsPath, req.Export}, map[string]string{
					"PYTHONPATH": path.Dir(codePath) + ":" + workDir,
				}
			},
		}, nil
	default:
		return runtimeConfig{}, fmt.Errorf("unsupported runtime: %s", rt)
	}
}

// ParseExecutionResult interprets a runtime process's stdout/stderr/exit
// into an isolation.ExecutionResult. The wrappers print a final
// `{"__result__": <value>}` line on success; a bare `{success, result,
// error}` object is also accepted.
func ParseExecutionResult(stdout, stderr string, exitCode int, timedOut bool) *isolation.ExecutionResult {
	result := &isolation.ExecutionResult{
		Success: exitCode == 0 && !timedOut,
		Stdout:  truncateOutput(stdout),
		Stderr:  truncateOutput(stderr),
	}

	if timedOut {
		result.Success = false
		result.ErrorType = isolation.ErrorTypeTimeout
		result.Error = "execution timeout exceeded"
		return result
	}

	if exitCode != 0 {
		result.Success = false
		result.ErrorType = isolation.ErrorTypeRuntime
		result.Error = strings.TrimSpace(nonEmpty(stderr, fmt.Sprintf("process exited with code %d", exitCode)))
		if exitCode == 137 {
			result.ErrorType = isolation.ErrorTypeOOM
			result.Error = "out of memory"
		} else if strings.Contains(stderr, "SyntaxError") {
			result.ErrorType = isolation.ErrorTypeSyntax
		}
		return result
	}

	// Scan from the last non-empty line back. The wrappers print a final
	// `{"__result__": <value>}` line, so that is normally the last line.
	// We match a result line ONLY when the expected key is actually
	// present — a bare JSON object the user happened to print (e.g.
	// `{"success":true,"result":42}`) must not be mistaken for the
	// wrapper envelope and collapse the real result to nil.
	lines := bytes.Split([]byte(stdout), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}

		var obj map[string]json.RawMessage
		if err := json.Unmarshal(line, &obj); err != nil {
			continue // not a JSON object — keep scanning earlier lines
		}
		if raw, ok := obj["__result__"]; ok {
			var v interface{}
			_ = json.Unmarshal(raw, &v)
			result.Result = v
			return result
		}
		if _, ok := obj["success"]; ok {
			var direct struct {
				Success bool        `json:"success"`
				Result  interface{} `json:"result"`
				Error   string      `json:"error"`
			}
			if err := json.Unmarshal(line, &direct); err == nil {
				result.Success = direct.Success
				result.Result = direct.Result
				if !direct.Success && direct.Error != "" {
					result.Error = direct.Error
					result.ErrorType = isolation.ErrorTypeRuntime
				}
				return result
			}
		}
		// A JSON object without a recognized envelope key — keep scanning.
	}

	result.Result = stdout
	return result
}

// SafeID sanitizes an arbitrary id into a filesystem/vm-safe token.
func SafeID(s string) string {
	if s == "" {
		return uuid.NewString()
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// SupportsRuntime reports whether fnexec can execute the given runtime.
func SupportsRuntime(rt isolation.Runtime) bool {
	switch rt {
	case isolation.RuntimeNodeJS20, isolation.RuntimeDeno, isolation.RuntimePython3:
		return true
	default:
		return false
	}
}

// InstallTimeoutMs clamps the package-install timeout to a sane window
// independent of the (usually shorter) per-invocation execution timeout.
func InstallTimeoutMs(timeoutMS int) int {
	if timeoutMS <= 0 {
		return 60000
	}
	if timeoutMS < 30000 {
		return 30000
	}
	if timeoutMS > 120000 {
		return 120000
	}
	return timeoutMS
}

func nonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func truncateOutput(s string) string {
	const maxLen = 1 << 20 // 1MB
	if len(s) > maxLen {
		return s[:maxLen] + "\n... (output truncated at 1MB)"
	}
	return s
}

func safeProjectPath(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.Contains(raw, `\`) || strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("must be a normalized relative path")
	}
	clean := path.Clean(raw)
	if clean == "." || clean != raw || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("must be a normalized relative path")
	}
	if clean == ".everstack" || strings.HasPrefix(clean, ".everstack/") {
		return "", fmt.Errorf("uses the reserved .everstack runtime directory")
	}
	return clean, nil
}

const nodeWrapperSource = `import { readFile } from "node:fs/promises";
const codePath = process.argv[2];
const args = JSON.parse(await readFile(process.argv[3], "utf8"));
const exportName = process.argv[4] || "";
const mod = await import("file://" + codePath);
const handler = exportName ? mod[exportName] : (mod.default ?? mod.handler ?? mod.main ?? mod);
if (exportName && !(exportName in mod)) throw new Error(` + "`" + `export ${exportName} not found in ${codePath}` + "`" + `);
const value = typeof handler === "function" ? await handler(args) : handler;
console.log(JSON.stringify({ __result__: value }));`

const denoWrapperSource = `const codePath = Deno.args[0];
const args = JSON.parse(await Deno.readTextFile(Deno.args[1]));
const exportName = Deno.args[2] || "";
const mod = await import("file://" + codePath);
const handler = exportName ? mod[exportName] : (mod.default ?? mod.handler ?? mod.main ?? mod);
if (exportName && !(exportName in mod)) throw new Error(` + "`" + `export ${exportName} not found in ${codePath}` + "`" + `);
const value = typeof handler === "function" ? await handler(args) : handler;
console.log(JSON.stringify({ __result__: value }));`

const pythonWrapperSource = `import asyncio
import importlib.util
import json
import sys

code_path = sys.argv[1]
with open(sys.argv[2], "r", encoding="utf-8") as args_file:
    args = json.load(args_file)
export_name = sys.argv[3] if len(sys.argv) > 3 else ""

spec = importlib.util.spec_from_file_location("function", code_path)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

if export_name:
    if not hasattr(mod, export_name):
        raise AttributeError(f"export {export_name} not found in {code_path}")
    handler = getattr(mod, export_name)
else:
    handler = getattr(mod, "handler", None) or getattr(mod, "main", None) or getattr(mod, "default", None)
if callable(handler):
    if asyncio.iscoroutinefunction(handler):
        result = asyncio.run(handler(args))
    else:
        result = handler(args)
else:
    result = handler

print(json.dumps({"__result__": result}, default=str))`
