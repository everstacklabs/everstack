package v1

// LSP capabilities via exec-based approach (POR-84, POR-85) — ConnectRPC.
//
// Language server operations run tools inside the sandbox via sandboxMgr.Exec.
// Output is tool-dependent freeform JSON, returned as a raw JSON string.
// Auth + tenant/instance ownership are enforced by the AgentsService
// interceptor chain.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// SandboxLSP runs an exec-based language-server operation and returns the raw
// JSON output. REST: POST /v1/sandbox/instances/{sandbox_id}/lsp/{lang}/{op}
func (s *Server) SandboxLSP(
	ctx context.Context,
	req *connect.Request[agentspb.SandboxLSPRequest],
) (*connect.Response[agentspb.SandboxLSPResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errSandboxNotEnabled)
	}
	msg := req.Msg
	inst, ok := s.sandboxMgr.GetBySandboxIDOrName(msg.GetSandboxId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox not found: %s", msg.GetSandboxId()))
	}
	sessionID := inst.Config.SessionID

	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resultJSON string
	var err error
	switch msg.GetLang() {
	case "python":
		resultJSON, err = s.pythonLSP(execCtx, sessionID, msg.GetOp(), msg.GetPath(), msg.GetQuery())
	case "typescript", "javascript":
		resultJSON, err = s.typeScriptLSP(execCtx, sessionID, msg.GetOp(), msg.GetPath())
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported language: %s", msg.GetLang()))
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&agentspb.SandboxLSPResponse{ResultJson: resultJSON}), nil
}

func (s *Server) pythonLSP(ctx context.Context, sessionID, op, path, query string) (string, error) {
	switch op {
	case "diagnostics":
		if path == "" {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path is required"))
		}
		cmd := fmt.Sprintf(
			`if command -v pylint >/dev/null 2>&1; then `+
				`pylint --output-format=json %q 2>/dev/null || true; `+
				`elif command -v flake8 >/dev/null 2>&1; then `+
				`flake8 --format=json %q 2>/dev/null || true; `+
				`else echo "[]"; fi`,
			path, path,
		)
		out, err := s.lspExec(ctx, sessionID, cmd, 25*time.Second)
		return lspResult(out, "diagnostics"), err

	case "symbols", "document-symbols":
		if path == "" {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path is required"))
		}
		cmd := fmt.Sprintf(
			`python3 -c "
import ast, json, sys
try:
    with open(%q) as f:
        tree = ast.parse(f.read())
    syms = []
    for node in ast.walk(tree):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            syms.append({'name': node.name, 'kind': 'function', 'line': node.lineno})
        elif isinstance(node, ast.ClassDef):
            syms.append({'name': node.name, 'kind': 'class', 'line': node.lineno})
    print(json.dumps(syms))
except Exception as e:
    print(json.dumps({'error': str(e)}))
" 2>/dev/null`, path,
		)
		out, err := s.lspExec(ctx, sessionID, cmd, 10*time.Second)
		return lspResult(out, "symbols"), err

	case "workspace-symbols":
		searchPath := path
		if searchPath == "" {
			searchPath = "."
		}
		cmd := fmt.Sprintf(
			`grep -rn "def %s\|class %s" %q 2>/dev/null | head -100 | awk -F: '{print "{\"name\":\""$3"\",\"file\":\""$1"\",\"line\":"$2"}"}' | jq -s '.' 2>/dev/null || echo "[]"`,
			query, query, searchPath,
		)
		out, _ := s.lspExec(ctx, sessionID, cmd, 10*time.Second)
		return lspResult(out, "workspace-symbols"), nil

	default:
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported Python LSP op: %s", op))
	}
}

func (s *Server) typeScriptLSP(ctx context.Context, sessionID, op, path string) (string, error) {
	switch op {
	case "diagnostics":
		if path == "" {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path is required"))
		}
		cmd := fmt.Sprintf(
			`if command -v tsc >/dev/null 2>&1; then `+
				`tsc --noEmit %q 2>&1 | head -200 | awk '{print NR": "$0}' | jq -Rn '[inputs | split(": ") | {line: .[0], message: .[1]}]' 2>/dev/null || echo "[]"; `+
				`else echo "[]"; fi`,
			path,
		)
		out, _ := s.lspExec(ctx, sessionID, cmd, 20*time.Second)
		return lspResult(out, "diagnostics"), nil

	case "symbols", "document-symbols":
		if path == "" {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path is required"))
		}
		cmd := fmt.Sprintf(
			`grep -n "^\(export \)\?\(function\|class\|const\|interface\|type\|enum\)" %q 2>/dev/null | `+
				`awk -F: '{print "{\"name\":\""$2"\",\"line\":"$1"}"}' | `+
				`jq -s '.' 2>/dev/null || echo "[]"`,
			path,
		)
		out, _ := s.lspExec(ctx, sessionID, cmd, 10*time.Second)
		return lspResult(out, "symbols"), nil

	default:
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported TypeScript LSP op: %s", op))
	}
}

func (s *Server) lspExec(ctx context.Context, sessionID, cmd string, timeout time.Duration) (string, error) {
	result, err := s.sandboxMgr.Exec(ctx, sessionID, sandbox.ExecRequest{
		Command: []string{"/bin/sh", "-c", cmd}, Timeout: timeout,
		SilentLog: true, // LSP plumbing; not user activity
	})
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, err)
	}
	return result.Stdout, nil
}

// lspResult normalizes tool output into a JSON string: empty -> "[]"; valid
// JSON -> passthrough; otherwise wrapped as {"<key>": "<raw>"}.
func lspResult(output, defaultKey string) string {
	out := strings.TrimSpace(output)
	if out == "" {
		return "[]"
	}
	if json.Valid([]byte(out)) {
		return out
	}
	wrapped, _ := json.Marshal(map[string]string{defaultKey: out})
	return string(wrapped)
}

// SandboxLSPInfo returns LSP capability info. REST: GET /v1/sandbox/instances/{sandbox_id}/lsp
func (s *Server) SandboxLSPInfo(
	ctx context.Context,
	req *connect.Request[agentspb.SandboxLSPInfoRequest],
) (*connect.Response[agentspb.SandboxLSPInfoResponse], error) {
	sandboxID := req.Msg.GetSandboxId()
	mk := func(lang string) *agentspb.LSPLanguageInfo {
		base := fmt.Sprintf("/v1/sandbox/instances/%s/lsp/%s", sandboxID, lang)
		ops := []string{"diagnostics", "symbols", "document-symbols", "workspace-symbols"}
		eps := make([]string, len(ops))
		for i, op := range ops {
			eps[i] = base + "/" + op
		}
		return &agentspb.LSPLanguageInfo{Language: lang, Endpoints: eps}
	}
	return connect.NewResponse(&agentspb.SandboxLSPInfoResponse{
		SandboxId: sandboxID,
		Languages: []*agentspb.LSPLanguageInfo{mk("python"), mk("typescript")},
		Note:      "Diagnostics require pylint/flake8 (Python) or tsc (TypeScript) in the sandbox. Use everstack-python or everstack-node images for pre-installed tools.",
	}), nil
}
