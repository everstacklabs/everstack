package sandbox

import (
	"context"
	"testing"
)

// updaterStub records workdir propagation calls so the test can assert
// the backend-level update fired. Stubs only the optional interface,
// not full Backend, because UpdateInstanceWorkDir's only contract with
// the backend is the type assertion to instanceWorkDirUpdater.
type updaterStub struct {
	calls []struct {
		id      string
		workDir string
	}
}

func (u *updaterStub) UpdateInstanceWorkDir(id, newWorkDir string) {
	u.calls = append(u.calls, struct {
		id      string
		workDir string
	}{id, newWorkDir})
}

// fullBackendStub embeds nothing — it only exists to satisfy the
// concrete-type position in m.backend so the type-assertion on
// instanceWorkDirUpdater works. It is NOT a valid Backend; tests
// that hit any other Backend method through m must not run.
type fullBackendStub struct {
	Backend // forced nil; calls would panic if anyone tried them
	updaterStub
}

func TestUpdateInstanceWorkDir_InMemory(t *testing.T) {
	stub := &fullBackendStub{}
	inst := &Instance{
		ID:     "sbx_test_1",
		Config: InstanceConfig{WorkDir: "/workspace"},
	}
	m := &SandboxManager{
		backend:            stub,
		instances:          map[string]*Instance{"sess-1": inst},
		instancesBySandbox: map[string]*Instance{"sbx_test_1": inst},
	}

	if err := m.UpdateInstanceWorkDir(context.Background(), "sbx_test_1", "/workspace"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if inst.Config.WorkDir != "/workspace" {
		t.Fatalf("in-memory: got %q, want /workspace", inst.Config.WorkDir)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("backend updater calls: got %d, want 1", len(stub.calls))
	}
	if got := stub.calls[0]; got.id != "sbx_test_1" || got.workDir != "/workspace" {
		t.Fatalf("backend updater args: got %+v", got)
	}
}

func TestUpdateInstanceWorkDir_EmptyIsNoOp(t *testing.T) {
	stub := &fullBackendStub{}
	inst := &Instance{Config: InstanceConfig{WorkDir: "/keep"}}
	m := &SandboxManager{
		backend:            stub,
		instances:          map[string]*Instance{"sess-1": inst},
		instancesBySandbox: map[string]*Instance{"sbx_x": inst},
	}

	// Empty input must not reset the in-memory value to "" or trip
	// resolveWorkDir back to the default — an operator editing the
	// field could clear it by accident.
	for _, in := range []string{"", "   ", "\t\n"} {
		if err := m.UpdateInstanceWorkDir(context.Background(), "sbx_x", in); err != nil {
			t.Fatalf("update %q: %v", in, err)
		}
		if inst.Config.WorkDir != "/keep" {
			t.Fatalf("input %q clobbered workdir to %q", in, inst.Config.WorkDir)
		}
		if len(stub.calls) != 0 {
			t.Fatalf("input %q triggered backend update", in)
		}
	}
}

func TestUpdateInstanceWorkDir_UnknownSandbox(t *testing.T) {
	// When the sandbox isn't tracked locally, the call must still
	// succeed silently — typical for cross-fcagent scenarios where
	// only one gateway pod has the route. No in-memory write happens
	// (there's nothing to write to) but the backend updater is still
	// invoked so the owning agent can apply it.
	stub := &fullBackendStub{}
	m := &SandboxManager{
		backend:            stub,
		instances:          map[string]*Instance{},
		instancesBySandbox: map[string]*Instance{},
	}
	if err := m.UpdateInstanceWorkDir(context.Background(), "sbx_unknown", "/wd"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("backend updater calls: got %d, want 1", len(stub.calls))
	}
}

func TestUpdateInstanceWorkDir_TrimsWhitespace(t *testing.T) {
	stub := &fullBackendStub{}
	inst := &Instance{Config: InstanceConfig{WorkDir: "/old"}}
	m := &SandboxManager{
		backend:            stub,
		instances:          map[string]*Instance{"sess-1": inst},
		instancesBySandbox: map[string]*Instance{"sbx_t": inst},
	}
	if err := m.UpdateInstanceWorkDir(context.Background(), "sbx_t", "  /padded  "); err != nil {
		t.Fatalf("update: %v", err)
	}
	if inst.Config.WorkDir != "/padded" {
		t.Fatalf("trim: got %q, want /padded", inst.Config.WorkDir)
	}
	if stub.calls[0].workDir != "/padded" {
		t.Fatalf("backend updater untrimmed: got %q", stub.calls[0].workDir)
	}
}
