package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// fakeEnvBackend simulates the Firecracker backend for env-manager tests
// without KVM. Guests are "alive" unless marked dead, in which case Exec
// fails (mimicking a vsock/route error from a crashed VM).
type fakeEnvBackend struct {
	mu        sync.Mutex
	created   []string
	destroyed []string
	dead      map[string]bool
	createErr error
	listIDs   []string // ids returned by List (for startup-sweep tests)
}

func (f *fakeEnvBackend) List(_ context.Context) ([]*sandbox.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*sandbox.Instance, 0, len(f.listIDs))
	for _, id := range f.listIDs {
		out = append(out, &sandbox.Instance{ID: id})
	}
	return out, nil
}

func newFakeEnvBackend() *fakeEnvBackend {
	return &fakeEnvBackend{dead: map[string]bool{}}
}

func (f *fakeEnvBackend) Create(_ context.Context, id string, _ sandbox.InstanceConfig) (*sandbox.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, id)
	return &sandbox.Instance{ID: id}, nil
}

func (f *fakeEnvBackend) Exec(_ context.Context, id string, req sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	f.mu.Lock()
	dead := f.dead[id]
	f.mu.Unlock()
	if dead {
		return nil, fmt.Errorf("VM not found for sandbox %s", id)
	}
	stdout := ""
	if commandHasWrapper(req.Command) {
		stdout = `{"__result__":"ok"}`
	}
	return &sandbox.ExecResult{ExitCode: 0, Stdout: stdout}, nil
}

func (f *fakeEnvBackend) WriteFile(_ context.Context, _ string, _ string, _ []byte) error {
	return nil
}

func (f *fakeEnvBackend) Destroy(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyed = append(f.destroyed, id)
	return nil
}

func (f *fakeEnvBackend) markDead(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dead[id] = true
}

func (f *fakeEnvBackend) createdIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.created...)
}

func (f *fakeEnvBackend) destroyedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.destroyed...)
}

func commandHasWrapper(cmd []string) bool {
	for _, a := range cmd {
		if strings.Contains(a, "wrapper.") {
			return true
		}
	}
	return false
}

func testSpec() envSpec {
	return envSpec{
		runtime:     isolation.RuntimeNodeJS20,
		memoryMB:    512,
		vcpus:       1,
		networkMode: sandbox.NetworkDeny,
		tenantID:    "t1",
		functionID:  "fn1",
	}
}

func testReq() isolation.ExecutionRequest {
	return isolation.ExecutionRequest{
		RequestID: "r1",
		Runtime:   isolation.RuntimeNodeJS20,
		Code:      "export default () => 'ok'",
		TimeoutMS: 5000,
	}
}

func TestInvoke_ReusesEnv(t *testing.T) {
	fake := newFakeEnvBackend()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, time.Minute)
	defer m.Stop()

	for i := 0; i < 3; i++ {
		res, cold, err := m.Invoke(context.Background(), "key-a", testSpec(), testReq())
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		if !res.Success {
			t.Fatalf("invoke %d: expected success, got %+v", i, res)
		}
		if i == 0 && !cold {
			t.Fatalf("first invoke should be a cold start")
		}
		if i > 0 && cold {
			t.Fatalf("invoke %d should reuse the warm env, got cold start", i)
		}
	}
	if got := fake.createdIDs(); len(got) != 1 {
		t.Fatalf("expected exactly 1 env created, got %d: %v", len(got), got)
	}
}

func TestInvoke_DistinctKeys(t *testing.T) {
	fake := newFakeEnvBackend()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, time.Minute)
	defer m.Stop()

	_, _, _ = m.Invoke(context.Background(), "key-a", testSpec(), testReq())
	_, _, _ = m.Invoke(context.Background(), "key-b", testSpec(), testReq())

	if got := fake.createdIDs(); len(got) != 2 {
		t.Fatalf("expected 2 envs for 2 keys, got %d: %v", len(got), got)
	}
}

func TestInvoke_HealthRecreate(t *testing.T) {
	fake := newFakeEnvBackend()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, time.Minute)
	defer m.Stop()

	if _, _, err := m.Invoke(context.Background(), "key-a", testSpec(), testReq()); err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	created := fake.createdIDs()
	if len(created) != 1 {
		t.Fatalf("expected 1 env, got %v", created)
	}
	// Kill the guest — the next invocation must detect it and recreate.
	fake.markDead(created[0])

	res, cold, err := m.Invoke(context.Background(), "key-a", testSpec(), testReq())
	if err != nil {
		t.Fatalf("recreate invoke: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success after recreate, got %+v", res)
	}
	if !cold {
		t.Fatalf("recreation should report a cold start")
	}
	if got := fake.createdIDs(); len(got) != 2 {
		t.Fatalf("expected 2 envs after recreate, got %v", got)
	}
	if d := fake.destroyedIDs(); len(d) != 1 || d[0] != created[0] {
		t.Fatalf("expected dead env %s destroyed, got %v", created[0], d)
	}
}

func TestReapIdle_DestroysAndForgets(t *testing.T) {
	fake := newFakeEnvBackend()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, 10*time.Millisecond)
	defer m.Stop()

	if _, _, err := m.Invoke(context.Background(), "key-a", testSpec(), testReq()); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	created := fake.createdIDs()
	time.Sleep(30 * time.Millisecond)
	m.reapIdle()

	if d := fake.destroyedIDs(); len(d) != 1 || d[0] != created[0] {
		t.Fatalf("expected idle env destroyed, got %v", d)
	}
	m.mu.Lock()
	n := len(m.envs)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected env map empty after reap, got %d", n)
	}
}

func TestInvoke_ConcurrentSameKeyCreatesOne(t *testing.T) {
	fake := newFakeEnvBackend()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, time.Minute)
	defer m.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = m.Invoke(context.Background(), "key-a", testSpec(), testReq())
		}()
	}
	wg.Wait()

	if got := fake.createdIDs(); len(got) != 1 {
		t.Fatalf("concurrent invokes must coalesce onto one env, got %d: %v", len(got), got)
	}
}

func TestSweepRecoveredFunctionEnvs(t *testing.T) {
	fake := newFakeEnvBackend()
	fake.listIDs = []string{"fn-abc-11111111", "sbx_manual_keepme", "fn-def-22222222", "wks_keepme2"}

	SweepRecoveredFunctionEnvs(context.Background(), fake)

	got := fake.destroyedIDs()
	if len(got) != 2 {
		t.Fatalf("expected 2 function envs reaped, got %d: %v", len(got), got)
	}
	for _, id := range got {
		if !strings.HasPrefix(id, functionEnvIDPrefix) {
			t.Fatalf("swept a non-function VM: %s", id)
		}
	}
}

func TestInvoke_TenantIsolatedDespiteCollidingKey(t *testing.T) {
	fake := newFakeEnvBackend()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, time.Minute)
	defer m.Stop()

	specA := testSpec()
	specA.tenantID = "tenant-a"
	specB := testSpec()
	specB.tenantID = "tenant-b"

	// Same (deliberately colliding) env_key from both tenants must NOT
	// share an environment — the composite registry key isolates them.
	if _, _, err := m.Invoke(context.Background(), "collide", specA, testReq()); err != nil {
		t.Fatalf("invoke A: %v", err)
	}
	if _, _, err := m.Invoke(context.Background(), "collide", specB, testReq()); err != nil {
		t.Fatalf("invoke B: %v", err)
	}
	if got := fake.createdIDs(); len(got) != 2 {
		t.Fatalf("cross-tenant env sharing! expected 2 envs, got %d: %v", len(got), got)
	}
}

func TestEnsureAlive_DoesNotDestroyBusyEnv(t *testing.T) {
	fake := newFakeEnvBackend()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, time.Minute)
	defer m.Stop()

	env := &funcEnv{key: "k", runtime: isolation.RuntimeNodeJS20, sandboxID: "fn-dead-1"}
	env.active.Store(2) // another invocation is concurrently using this VM
	fake.markDead("fn-dead-1")

	id, cold, err := m.ensureAlive(context.Background(), env, testSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cold {
		t.Fatalf("must not report a cold start while the env is busy")
	}
	if id != "fn-dead-1" {
		t.Fatalf("expected the existing VM id returned, got %s", id)
	}
	if d := fake.destroyedIDs(); len(d) != 0 {
		t.Fatalf("a busy env must never be destroyed by a health blip, got %v", d)
	}
	if c := fake.createdIDs(); len(c) != 0 {
		t.Fatalf("must not recreate while another invocation holds the VM, got %v", c)
	}
}

func TestInvoke_UnsupportedRuntime(t *testing.T) {
	fake := newFakeEnvBackend()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, time.Minute)
	defer m.Stop()

	spec := testSpec()
	spec.runtime = isolation.Runtime("rust")
	req := testReq()
	req.Runtime = isolation.Runtime("rust")

	_, _, err := m.Invoke(context.Background(), "key-a", spec, req)
	if err == nil {
		t.Fatalf("expected error for unsupported runtime")
	}
	if got := fake.createdIDs(); len(got) != 0 {
		t.Fatalf("no env should be created for unsupported runtime, got %v", got)
	}
}
