package tools

import (
	"context"
	"errors"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

type testSyntheticHandler struct {
	name string
}

func (h *testSyntheticHandler) Name() string { return h.name }
func (h *testSyntheticHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        h.name,
			Description: "test handler",
		},
	}
}
func (h *testSyntheticHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	return "ok", nil
}

type testFailingSyntheticHandler struct {
	name string
}

func (h *testFailingSyntheticHandler) Name() string { return h.name }
func (h *testFailingSyntheticHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        h.name,
			Description: "failing test handler",
		},
	}
}
func (h *testFailingSyntheticHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	return "", errors.New("boom")
}

func TestBuildToolDefinitionsFiltersSyntheticToolsByAllowedNames(t *testing.T) {
	t.Parallel()

	ti := NewToolInterceptor(nil)
	ti.RegisterHandler(&testSyntheticHandler{name: "sandbox_shell"})
	ti.RegisterHandler(&testSyntheticHandler{name: "sandbox_expose_port"})

	defs, err := ti.BuildToolDefinitions(context.Background(), "tenant-1", []string{"sandbox_shell"})
	if err != nil {
		t.Fatalf("BuildToolDefinitions returned error: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0].Function.Name != "sandbox_shell" {
		t.Fatalf("expected sandbox_shell, got %s", defs[0].Function.Name)
	}
}

func TestBuildToolDefinitionsIncludesAllSyntheticToolsWhenAllowlistEmpty(t *testing.T) {
	t.Parallel()

	ti := NewToolInterceptor(nil)
	ti.RegisterHandler(&testSyntheticHandler{name: "sandbox_shell"})
	ti.RegisterHandler(&testSyntheticHandler{name: "sandbox_expose_port"})

	defs, err := ti.BuildToolDefinitions(context.Background(), "tenant-1", nil)
	if err != nil {
		t.Fatalf("BuildToolDefinitions returned error: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}
}

func TestExecuteSyntheticToolPropagatesHandlerError(t *testing.T) {
	t.Parallel()

	ti := NewToolInterceptor(nil)
	ti.RegisterHandler(&testFailingSyntheticHandler{name: "sandbox_shell"})

	_, err := ti.ExecuteSyntheticTool(context.Background(), "sandbox_shell", `{}`)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err.Error() != "boom" {
		t.Fatalf("expected propagated error 'boom', got %q", err.Error())
	}
}
