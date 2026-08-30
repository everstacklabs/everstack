package adk

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeSandbox struct {
	created   bool
	tenant    string
	files     map[string]string
	execs     [][]string
	destroyed bool
	createErr error
	// execFn lets a test control exec results per invocation.
	execFn func(cmd []string) (stdout, stderr string, code int, err error)
}

func newFake() *fakeSandbox {
	return &fakeSandbox{files: map[string]string{}}
}

func (f *fakeSandbox) Create(_ context.Context, tenantID string) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.created = true
	f.tenant = tenantID
	return "h1", nil
}

func (f *fakeSandbox) WriteFile(_ context.Context, _, path, content string) error {
	f.files[path] = content
	return nil
}

func (f *fakeSandbox) Exec(_ context.Context, _ string, cmd []string) (string, string, int, error) {
	f.execs = append(f.execs, cmd)
	if f.execFn != nil {
		return f.execFn(cmd)
	}
	return "", "", 0, nil
}

func (f *fakeSandbox) Destroy(_ context.Context, _ string) error {
	f.destroyed = true
	return nil
}

func TestRunValidation(t *testing.T) {
	rt := New(newFake())
	if _, err := rt.Run(context.Background(), RunRequest{AgentCode: "x"}); err == nil {
		t.Error("expected error for missing tenant")
	}
	if _, err := rt.Run(context.Background(), RunRequest{TenantID: "t"}); err == nil {
		t.Error("expected error for missing agent code")
	}
}

func TestRunHappyPath(t *testing.T) {
	f := newFake()
	f.execFn = func(cmd []string) (string, string, int, error) {
		if len(cmd) > 0 && cmd[0] == "python" {
			return "the agent answer", "", 0, nil
		}
		return "", "", 0, nil // pip install
	}
	rt := New(f)
	res, err := rt.Run(context.Background(), RunRequest{TenantID: "tenant-A", AgentCode: "root_agent = ...", Input: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Output != "the agent answer" {
		t.Errorf("output = %q", res.Output)
	}
	if !f.created || f.tenant != "tenant-A" {
		t.Errorf("sandbox not created for tenant: %+v", f)
	}
	// agent.py, harness, input all written.
	if _, ok := f.files[agentPath]; !ok {
		t.Error("agent.py not written")
	}
	if _, ok := f.files[harnessPath]; !ok {
		t.Error("harness not written")
	}
	// install then run.
	if len(f.execs) < 2 {
		t.Fatalf("expected install + run execs, got %d", len(f.execs))
	}
	if f.execs[0][0] != "pip" {
		t.Errorf("first exec = %v, want pip install", f.execs[0])
	}
	if !f.destroyed {
		t.Error("sandbox not destroyed")
	}
}

func TestRunExtraPackages(t *testing.T) {
	f := newFake()
	rt := New(f)
	_, _ = rt.Run(context.Background(), RunRequest{TenantID: "t", AgentCode: "x", Packages: []string{"requests"}})
	install := strings.Join(f.execs[0], " ")
	if !strings.Contains(install, "google-adk") || !strings.Contains(install, "requests") {
		t.Errorf("install cmd = %q, want google-adk + requests", install)
	}
}

func TestRunDestroysOnInstallFailure(t *testing.T) {
	f := newFake()
	f.execFn = func(cmd []string) (string, string, int, error) {
		if cmd[0] == "pip" {
			return "", "could not resolve google-adk", 1, nil
		}
		return "", "", 0, nil
	}
	rt := New(f)
	if _, err := rt.Run(context.Background(), RunRequest{TenantID: "t", AgentCode: "x"}); err == nil {
		t.Error("expected install failure error")
	}
	if !f.destroyed {
		t.Error("sandbox must be destroyed even when install fails")
	}
}

func TestRunDestroysOnCreateFailureSkipped(t *testing.T) {
	f := newFake()
	f.createErr = errors.New("no capacity")
	rt := New(f)
	if _, err := rt.Run(context.Background(), RunRequest{TenantID: "t", AgentCode: "x"}); err == nil {
		t.Error("expected create failure")
	}
	// Nothing to destroy if create failed.
	if f.destroyed {
		t.Error("should not destroy a sandbox that was never created")
	}
}

func TestRunNonZeroExitReturnsLogs(t *testing.T) {
	f := newFake()
	f.execFn = func(cmd []string) (string, string, int, error) {
		if cmd[0] == "python" {
			return "partial", "Traceback...", 1, nil
		}
		return "", "", 0, nil
	}
	rt := New(f)
	res, err := rt.Run(context.Background(), RunRequest{TenantID: "t", AgentCode: "x"})
	if err == nil {
		t.Error("expected error for non-zero exit")
	}
	if res == nil || !strings.Contains(res.Logs, "Traceback") {
		t.Errorf("expected logs surfaced, got %+v", res)
	}
}
