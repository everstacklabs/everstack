package tools

// Sandbox LSP tools exposed to agents via the tool registry (POR-95).
//
// These tools let coding agents call LSP operations (diagnostics, symbols)
// as tool calls rather than as raw HTTP requests, giving them structured
// IDE-quality code intelligence through the standard tool-calling interface.

import (
	"context"
	"encoding/json"
	"fmt"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// ─── sandbox_lsp_diagnostics ─────────────────────────────────────────────────

type sandboxLSPDiagnosticsHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxLSPDiagnosticsHandler) Name() string { return "sandbox_lsp_diagnostics" }
func (h *sandboxLSPDiagnosticsHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxLSPDiagnosticsHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_lsp_diagnostics",
			Description: "Get compiler/linter diagnostics (errors, warnings) for a file inside the sandbox. Returns structured results with line numbers and severity. Requires pylint/flake8 (Python) or tsc (TypeScript).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path to the file to diagnose (e.g. /repo/main.py).",
					},
					"language": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"python", "typescript"},
						"description": "Programming language of the file.",
					},
				},
				"required": []string{"path", "language"},
			},
		},
	}
}

func (h *sandboxLSPDiagnosticsHandler) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	path, _ := params["path"].(string)
	lang, _ := params["language"].(string)
	if path == "" || lang == "" {
		return nil, fmt.Errorf("path and language are required")
	}

	mgr := h.ctx.Manager
	if mgr == nil {
		return nil, fmt.Errorf("sandbox not available")
	}

	var cmd string
	switch lang {
	case "python":
		cmd = fmt.Sprintf(`pylint --output-format=json %q 2>/dev/null || flake8 --format=json %q 2>/dev/null || echo "[]"`, path, path)
	case "typescript":
		cmd = fmt.Sprintf(`tsc --noEmit %q 2>&1 | head -200 || echo "[]"`, path)
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}

	result, err := h.ctx.ExecWithRecovery(ctx, sandbox.ExecRequest{
		Command: []string{"/bin/sh", "-c", cmd},
		Timeout: 25,
	})
	if err != nil {
		return nil, err
	}

	var out interface{}
	if json.Valid([]byte(result.Stdout)) {
		_ = json.Unmarshal([]byte(result.Stdout), &out)
	} else {
		out = result.Stdout
	}
	return out, nil
}

// ─── sandbox_lsp_symbols ─────────────────────────────────────────────────────

type sandboxLSPSymbolsHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxLSPSymbolsHandler) Name() string { return "sandbox_lsp_symbols" }
func (h *sandboxLSPSymbolsHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxLSPSymbolsHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_lsp_symbols",
			Description: "List functions, classes, and variables defined in a file. Useful for understanding code structure without reading the full file.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path to the file.",
					},
					"language": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"python", "typescript"},
						"description": "Programming language.",
					},
				},
				"required": []string{"path", "language"},
			},
		},
	}
}

func (h *sandboxLSPSymbolsHandler) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	path, _ := params["path"].(string)
	lang, _ := params["language"].(string)

	mgr := h.ctx.Manager
	if mgr == nil {
		return nil, fmt.Errorf("sandbox not available")
	}

	var cmd string
	switch lang {
	case "python":
		cmd = fmt.Sprintf(`python3 -c "
import ast, json
with open(%q) as f:
    tree = ast.parse(f.read())
syms = [{'name': n.name, 'kind': type(n).__name__, 'line': n.lineno}
        for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef))]
print(json.dumps(syms))
" 2>/dev/null || echo "[]"`, path)
	case "typescript":
		cmd = fmt.Sprintf(`grep -n "^\(export \)\?\(function\|class\|const\|interface\|type\|enum\)" %q | head -100`, path)
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}

	result, err := h.ctx.ExecWithRecovery(ctx, sandbox.ExecRequest{
		Command: []string{"/bin/sh", "-c", cmd}, Timeout: 10,
	})
	if err != nil {
		return nil, err
	}

	var out interface{}
	if json.Valid([]byte(result.Stdout)) {
		_ = json.Unmarshal([]byte(result.Stdout), &out)
	} else {
		out = result.Stdout
	}
	return out, nil
}

// ─── sandbox_screenshot ──────────────────────────────────────────────────────

type sandboxScreenshotHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxScreenshotHandler) Name() string { return "sandbox_screenshot" }
func (h *sandboxScreenshotHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxScreenshotHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_screenshot",
			Description: "Take a screenshot of the sandbox desktop. Requires computer_use=true on the sandbox. Returns base64-encoded JPEG/PNG image. Use this for visual verification of GUI operations.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"format": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"jpeg", "png"},
						"description": "Image format. jpeg is smaller (default); png is lossless.",
					},
				},
			},
		},
	}
}

func (h *sandboxScreenshotHandler) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	format, _ := params["format"].(string)
	if format == "" {
		format = "jpeg"
	}

	mgr := h.ctx.Manager
	if mgr == nil {
		return nil, fmt.Errorf("sandbox not available")
	}

	outFile := "/tmp/screenshot." + format
	cmd := fmt.Sprintf(
		`DISPLAY=:99 scrot %q 2>/dev/null || DISPLAY=:99 import -window root -quality 85 %q 2>/dev/null; base64 %q`,
		outFile, outFile, outFile,
	)
	result, err := h.ctx.ExecWithRecovery(ctx, sandbox.ExecRequest{
		Command: []string{"/bin/sh", "-c", cmd}, Timeout: 15,
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"format": format,
		"data":   result.Stdout, // base64-encoded image
	}, nil
}

// RegisterSandboxOverhaulTools adds POR-95 MCP tools to the tool registry.
func RegisterSandboxOverhaulTools(registry ToolRegistry, ctx *SandboxSessionContext) {
	registry.Register(&sandboxLSPDiagnosticsHandler{ctx: ctx})
	registry.Register(&sandboxLSPSymbolsHandler{ctx: ctx})
	registry.Register(&sandboxScreenshotHandler{ctx: ctx})
}

// ToolRegistry is the minimal interface needed to register tools.
type ToolRegistry interface {
	Register(handler interface{})
}
