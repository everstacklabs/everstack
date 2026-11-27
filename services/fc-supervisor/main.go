package main

// fc-supervisor: a tiny host-side daemon that owns the lifecycle of
// every Everstack firecracker VM on this node.
//
// Architecture rationale at docs/design/fc-supervisor.md. Short
// version: VMs run in host-side systemd transient scopes instead of
// inside the fcagent pod's cgroup, so pod replacement, container
// restart, OOM, and helm rollouts no longer destroy them.
//
// This is a separate binary because host systemd interaction requires
// privileged namespace access and can block while holding locks. Keeping that
// work outside fcagent isolates it from the process serving gRPC.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	socketPath := flag.String("socket", "/var/run/everstack/fc-supervisor.sock", "unix socket to listen on")
	slice := flag.String("slice", defaultScopeSlice, "systemd slice to host all VM scopes under")
	flag.Parse()

	opts := defaultSupervisorOpts()
	opts.slice = *slice

	// Pre-flight: make sure the tools we shell out to are actually
	// installed. Fail loud at startup rather than producing
	// confusing errors on every spawn.
	for _, tool := range []string{opts.systemdRunPath, opts.systemctlPath} {
		if _, err := exec.LookPath(tool); err != nil {
			log.Fatalf("supervisor: required tool %q not found in PATH — install systemd / systemd-utils", tool)
		}
	}

	// Make sure the parent dir for the socket exists. We can't
	// rely on tmpfs being mounted somewhere convenient; the
	// systemd unit's ExecStartPre normally handles this, but it's
	// cheap to be defensive.
	if err := os.MkdirAll(filepath.Dir(*socketPath), 0o750); err != nil {
		log.Fatalf("supervisor: create socket dir %q: %v", filepath.Dir(*socketPath), err)
	}
	// Clean up a leftover socket from a prior run. The listen
	// would otherwise fail with "address in use" even though no
	// process is bound — happens after an ungraceful exit.
	_ = os.Remove(*socketPath)

	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Fatalf("supervisor: listen on %s: %v", *socketPath, err)
	}
	// Group-readable so fcagent (running as root in its pod, but
	// also visible from the host via hostPath mount) can connect.
	// In practice the socket is bind-mounted into the pod and the
	// pod's processes are uid=0 — root can always read/write.
	if err := os.Chmod(*socketPath, 0o660); err != nil {
		log.Printf("supervisor: chmod socket %s: %v (proceeding anyway)", *socketPath, err)
	}

	srv := &http.Server{
		Handler:      newHTTPServer(opts),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 20 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("supervisor: listening on %s (slice=%s)", *socketPath, opts.slice)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("supervisor: serve: %v", err)
		}
	}()

	// Wait for shutdown signal, then graceful-stop the HTTP
	// server. We deliberately do NOT touch running scopes on
	// shutdown — the whole point is that they outlive us.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("supervisor: shutdown signal received, draining HTTP")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = os.Remove(*socketPath)
	log.Printf("supervisor: stopped (VM scopes left running)")
}

// execCombined is the package-internal exec-and-collect helper used
// by scopeExists. Wrapped here so tests can swap it out via a
// build-time injection if we ever need to.
func execCombined(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("exec %s: %w", name, err)
	}
	return out, nil
}
