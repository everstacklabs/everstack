package main

// HTTP-level tests for the supervisor. Use a fake systemctl/systemd-run
// (a shell script the test writes into a temp dir + injects via the
// opts paths) so we exercise the full handler chain without a real
// systemd. The host-side integration test (systemd actually present)
// runs in a separate file gated on a tag.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeFakeTools writes two tiny shell scripts that mimic
// systemctl/systemd-run for the cases we care about. Returns the
// supervisorOpts pointing at them.
//
// The fakes look at $1..$N to decide what to print and what exit
// code to return. We deliberately don't try to simulate scope
// state across calls (would require a shared file); each test sets
// up the right fake behavior for the specific scenario.
func makeFakeTools(t *testing.T, systemdRunScript, systemctlScript string) supervisorOpts {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tools rely on /bin/sh; skip on windows")
	}
	dir := t.TempDir()

	srPath := filepath.Join(dir, "systemd-run")
	if err := os.WriteFile(srPath, []byte("#!/bin/sh\n"+systemdRunScript), 0o755); err != nil {
		t.Fatalf("write systemd-run fake: %v", err)
	}
	scPath := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(scPath, []byte("#!/bin/sh\n"+systemctlScript), 0o755); err != nil {
		t.Fatalf("write systemctl fake: %v", err)
	}

	opts := defaultSupervisorOpts()
	opts.systemdRunPath = srPath
	opts.systemctlPath = scPath
	// Keep the production command budgets. These tests cover HTTP behavior,
	// not timeout handling, and short process deadlines make scheduler latency
	// part of otherwise deterministic assertions on loaded CI runners.
	return opts
}

func TestHealthz(t *testing.T) {
	srv := newHTTPServer(defaultSupervisorOpts())
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/healthz", nil)
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("healthz: want 204, got %d", w.Code)
	}
}

func TestSpawnRejectsMissingFields(t *testing.T) {
	srv := newHTTPServer(defaultSupervisorOpts())
	body, _ := json.Marshal(SpawnRequest{
		SandboxID: "sbx_test",
		// everything else missing
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/vms/spawn", bytes.NewReader(body))
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("spawn with missing fields: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing required fields") {
		t.Fatalf("expected validation error in body, got %q", w.Body.String())
	}
}

func TestSpawnRejectsBadSandboxID(t *testing.T) {
	srv := newHTTPServer(defaultSupervisorOpts())
	body, _ := json.Marshal(SpawnRequest{
		SandboxID: "sbx/../etc/passwd",
		BinaryPath: "/usr/bin/firecracker",
		APISocket: "/var/lib/everstack/vms/x/firecracker.sock",
		WorkDir:   "/var/lib/everstack/vms/x",
		MemoryMB:  512,
		VCPUs:     1,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/vms/spawn", bytes.NewReader(body))
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("spawn with bad sandbox_id: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestSpawnSuccess(t *testing.T) {
	// Fake systemd-run exits 0 with no output (matches reality —
	// `systemd-run --scope` is silent on success).
	// Fake systemctl handles `show -p MainPID <unit>` for the
	// post-spawn lookup.
	opts := makeFakeTools(t,
		`exit 0`,
		`case "$1" in
		  show)
		    case "$3" in
		      *MainPID*) echo "MainPID=4242" ;;
		      *) echo "" ;;
		    esac
		    ;;
		  is-active)
		    echo "inactive"
		    exit 3
		    ;;
		  *) echo "unhandled: $@" >&2; exit 1 ;;
		esac`,
	)
	srv := newHTTPServer(opts)

	body, _ := json.Marshal(SpawnRequest{
		SandboxID:  "sbx_unit_test",
		BinaryPath: "/usr/bin/firecracker",
		APISocket:  "/tmp/x.sock",
		WorkDir:    "/tmp/work",
		MemoryMB:   512,
		VCPUs:      1,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/vms/spawn", bytes.NewReader(body))
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("spawn success: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var got SpawnResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	if got.ScopeName != "everstack-fc-sbx_unit_test.scope" {
		t.Errorf("scope_name: want everstack-fc-sbx_unit_test.scope, got %q", got.ScopeName)
	}
	if got.MainPID != 4242 {
		t.Errorf("main_pid: want 4242, got %d", got.MainPID)
	}
}

func TestSpawn409WhenAlreadyRunning(t *testing.T) {
	// is-active returns "active" → handler should return 409.
	opts := makeFakeTools(t,
		`exit 0`,
		`case "$1" in
		  is-active) echo "active"; exit 0 ;;
		  *) exit 1 ;;
		esac`,
	)
	srv := newHTTPServer(opts)
	body, _ := json.Marshal(SpawnRequest{
		SandboxID:  "sbx_dup",
		BinaryPath: "/usr/bin/firecracker",
		APISocket:  "/tmp/x.sock",
		WorkDir:    "/tmp/work",
		MemoryMB:   512,
		VCPUs:      1,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/vms/spawn", bytes.NewReader(body))
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("spawn dup: want 409, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestStopIdempotentWhenAlreadyGone(t *testing.T) {
	// systemctl stop returns non-zero with "Unit not loaded"; the
	// handler should treat this as success per the idempotency
	// contract.
	opts := makeFakeTools(t,
		`exit 0`,
		`case "$1" in
		  stop) echo "Failed to stop xyz.scope: Unit xyz.scope not loaded." >&2; exit 5 ;;
		  *) exit 0 ;;
		esac`,
	)
	srv := newHTTPServer(opts)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/vms/sbx_gone/stop", nil)
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("stop already-gone: want 204, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestListEmpty(t *testing.T) {
	opts := makeFakeTools(t, `exit 0`, `echo ""`) // empty output from list-units
	srv := newHTTPServer(opts)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/vms", nil)
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list empty: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var got ListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.VMs) != 0 {
		t.Errorf("expected empty list, got %+v", got.VMs)
	}
}
