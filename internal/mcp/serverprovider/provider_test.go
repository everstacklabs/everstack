package serverprovider

import (
	"context"
	"strings"
	"testing"
)

func names(p *Provider, tenantID string, t *testing.T) []string {
	t.Helper()
	tools, err := p.ListTools(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	out := make([]string, len(tools))
	for i, tl := range tools {
		out[i] = tl.Name
	}
	return out
}

func TestListToolsRequiresTenant(t *testing.T) {
	p := New(Deps{})
	if _, err := p.ListTools(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty tenant")
	}
}

func TestCallToolRequiresTenant(t *testing.T) {
	p := New(Deps{})
	if _, err := p.CallTool(context.Background(), "", "everstack_echo", nil); err == nil {
		t.Fatal("expected error for empty tenant")
	}
}

func TestBuiltinsPresentWithoutMemoryBackend(t *testing.T) {
	p := New(Deps{})
	got := names(p, "tenant-A", t)
	want := map[string]bool{"everstack_whoami": false, "everstack_echo": false}
	for _, n := range got {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, seen := range want {
		if !seen {
			t.Errorf("missing builtin tool %q (got %v)", n, got)
		}
	}
	// No memory backend => memory tools must be absent.
	for _, n := range got {
		if strings.HasPrefix(n, "memory_") {
			t.Errorf("memory tool %q exposed without a backend", n)
		}
	}
}

func TestToolsCarryInputSchema(t *testing.T) {
	p := New(Deps{})
	tools, err := p.ListTools(context.Background(), "tenant-A")
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.InputSchema == nil {
			t.Errorf("tool %q has nil input schema", tl.Name)
		}
		if tl.InputSchema["type"] != "object" {
			t.Errorf("tool %q schema type = %v, want object", tl.Name, tl.InputSchema["type"])
		}
	}
}

func TestWhoamiReturnsAuthenticatedTenant(t *testing.T) {
	p := New(Deps{})
	res, err := p.CallTool(context.Background(), "tenant-XYZ", "everstack_whoami", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "tenant-XYZ") {
		t.Errorf("whoami did not report the authenticated tenant: %s", res.Content[0].Text)
	}
}

func TestEchoRoundTrips(t *testing.T) {
	p := New(Deps{})
	res, err := p.CallTool(context.Background(), "tenant-A", "everstack_echo", map[string]interface{}{"message": "ping"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "ping" {
		t.Errorf("echo = %+v, want 'ping'", res.Content)
	}
}

func TestUnknownToolErrors(t *testing.T) {
	p := New(Deps{})
	if _, err := p.CallTool(context.Background(), "tenant-A", "does_not_exist", nil); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
