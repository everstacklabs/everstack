package main

// HTTP handlers for the supervisor. Five endpoints, all tiny.
// Listening on a unix socket means fcagent (in its pod) is the only
// caller — no need for auth beyond socket file permissions.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// httpServer is the per-binary wiring for the HTTP handlers. Kept
// tiny: just the supervisor options + a mux. Built fresh in main.
type httpServer struct {
	opts supervisorOpts
	mux  *http.ServeMux
}

func newHTTPServer(opts supervisorOpts) *httpServer {
	s := &httpServer{opts: opts, mux: http.NewServeMux()}
	s.routes()
	return s
}

// ServeHTTP delegates to the mux. Implemented on the wrapper so
// main.go can pass the *httpServer directly to http.Server.
func (s *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *httpServer) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/vms", s.handleVMs)
	s.mux.HandleFunc("/vms/", s.handleVMByPath)
}

// handleHealth is the supervisor's liveness signal. Used by ops to
// confirm the service is running, and by fcagent's client to probe
// before its first real RPC after a connection failure.
func (s *httpServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleVMs dispatches GET (list) and POST (spawn). The trailing
// segment /vms/{id} and /vms/{id}/stop go through handleVMByPath
// instead — net/http's ServeMux doesn't path-match, so we split
// the routes by handler.
func (s *httpServer) handleVMs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleList(w, r)
	case http.MethodPost:
		if r.URL.Path != "/vms" && r.URL.Path != "/vms/spawn" {
			writeError(w, http.StatusNotFound, "use POST /vms/spawn")
			return
		}
		s.handleSpawn(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

// handleVMByPath handles /vms/{sandbox_id} and /vms/{sandbox_id}/stop.
func (s *httpServer) handleVMByPath(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/vms/")
	// Routes:
	//   /vms/spawn               → POST → handleSpawn (handled by /vms above; redirect-safe path here)
	//   /vms/{sandbox_id}        → GET  → handleGet
	//   /vms/{sandbox_id}/stop   → POST → handleStop
	if rest == "spawn" {
		s.handleVMs(w, r) // delegate to the POST /vms path
		return
	}
	if strings.HasSuffix(rest, "/stop") {
		sandboxID := strings.TrimSuffix(rest, "/stop")
		if sandboxID == "" {
			writeError(w, http.StatusBadRequest, "missing sandbox_id in path")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		s.handleStop(w, r, sandboxID)
		return
	}
	// /vms/{sandbox_id}
	if strings.Contains(rest, "/") {
		writeError(w, http.StatusNotFound, "unknown path under /vms/")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	s.handleGet(w, r, rest)
}

func (s *httpServer) handleList(w http.ResponseWriter, r *http.Request) {
	vms, err := listScopes(r.Context(), s.opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ListResponse{VMs: vms})
}

func (s *httpServer) handleGet(w http.ResponseWriter, r *http.Request, sandboxID string) {
	// Cheap lookup via the list path; for ~10s-of-VMs scale this
	// is fine. If it ever becomes a hot path, switch to a direct
	// `systemctl show` on the named unit.
	vms, err := listScopes(r.Context(), s.opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, vm := range vms {
		if vm.SandboxID == sandboxID {
			writeJSON(w, http.StatusOK, vm)
			return
		}
	}
	writeError(w, http.StatusNotFound, "no such sandbox")
}

func (s *httpServer) handleSpawn(w http.ResponseWriter, r *http.Request) {
	var req SpawnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	if err := validateSpawnRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 409 if a scope with this sandbox already exists. systemd-run
	// would otherwise fail with a cryptic "unit already loaded"
	// error; pre-flight gives the caller a useful HTTP code.
	if existing, err := scopeExists(r.Context(), s.opts, req.SandboxID); err == nil && existing {
		writeError(w, http.StatusConflict, "sandbox already has a running scope")
		return
	}

	resp, err := spawnVM(r.Context(), s.opts, req)
	if err != nil {
		log.Printf("supervisor: spawn %s failed: %v", req.SandboxID, err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("supervisor: spawned %s (scope=%s, pid=%d)", req.SandboxID, resp.ScopeName, resp.MainPID)
	writeJSON(w, http.StatusOK, resp)
}

func (s *httpServer) handleStop(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if sandboxID == "" {
		writeError(w, http.StatusBadRequest, "missing sandbox_id")
		return
	}
	unit := scopeNameForSandbox(sandboxID)
	if err := stopScope(r.Context(), s.opts, unit); err != nil {
		log.Printf("supervisor: stop %s failed: %v", sandboxID, err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("supervisor: stopped %s (scope=%s)", sandboxID, unit)
	w.WriteHeader(http.StatusNoContent)
}

func validateSpawnRequest(req SpawnRequest) error {
	missing := []string{}
	if req.SandboxID == "" {
		missing = append(missing, "sandbox_id")
	}
	if req.BinaryPath == "" {
		missing = append(missing, "binary_path")
	}
	if req.APISocket == "" {
		missing = append(missing, "api_sock")
	}
	if req.WorkDir == "" {
		missing = append(missing, "work_dir")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	if req.MemoryMB <= 0 {
		return errors.New("mem_mb must be > 0")
	}
	if req.VCPUs <= 0 {
		return errors.New("vcpus must be > 0")
	}
	if strings.ContainsAny(req.SandboxID, "/.") {
		// Defense in depth — sandbox_id flows into a systemd unit
		// name, which would reject these anyway, but we'd rather
		// return a clean 400 than a confusing systemd error.
		return errors.New("sandbox_id contains invalid characters")
	}
	return nil
}

// scopeExists is a cheap "is this sandbox already running" check
// used to pre-empt the 409 case for handleSpawn.
func scopeExists(ctx context.Context, opts supervisorOpts, sandboxID string) (bool, error) {
	unit := scopeNameForSandbox(sandboxID)
	// `systemctl is-active` returns 0 when active, 3 when inactive,
	// 4 when unit doesn't exist. We accept any non-zero as "not
	// running", and don't distinguish — the caller just needs the
	// bool.
	cmdCtx, cancel := context.WithTimeout(ctx, opts.queryTimeout)
	defer cancel()
	out, _ := runCommand(cmdCtx, opts.systemctlPath, "is-active", unit)
	state := strings.TrimSpace(out)
	return state == "active" || state == "activating", nil
}

// runCommand is a small wrapper that ignores exit codes — used
// only by scopeExists which interprets stdout, not the exit
// status.
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	out, err := execCombined(ctx, name, args...)
	return string(out), err
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}
