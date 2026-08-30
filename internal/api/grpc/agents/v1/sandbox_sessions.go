package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// shellSessionAPI is the JSON shape returned by the list endpoint and
// rendered by the admin UI. We don't reuse the proto type here so the
// API surface can evolve independently of the wire format between
// gateway and fcagent.
type shellSessionAPI struct {
	ID              string `json:"id"`
	AttachedClients int    `json:"attached_clients"`
	CreatedUnix     int64  `json:"created_unix"`
	// IdleSeconds is how long the session has been idle, computed
	// against the guest's wall clock (not the gateway's). -1 means
	// "the guest didn't report enough info to know" — UIs should
	// render this as "unknown" rather than e.g. "1969-12-31".
	IdleSeconds int64 `json:"idle_seconds"`
}

type listShellSessionsResponse struct {
	Sessions []shellSessionAPI `json:"sessions"`
}

// HandleListSandboxShellSessions returns the list of persistent shell
// sessions inside a sandbox. It supports both the legacy session_id route
// and the concrete sandbox instance route:
//
// GET /v1/sandbox/{session_id}/shell-sessions
// GET /v1/sandbox/instances/{sandbox_id}/shell-sessions
func (s *Server) HandleListSandboxShellSessions(w http.ResponseWriter, r *http.Request) {
	if s.sandboxMgr == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "sandbox feature is not enabled")
		return
	}

	inst, err := s.resolveShellSandboxInstance(r.Context(), r)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "sandbox not found")
		return
	}

	// Same auth path the shell WebSocket uses — same-origin or signed
	// SSH key. Keeps the admin UI's cookie-auth working while still
	// allowing the CLI to inspect sessions over signed requests.
	if err := s.authenticateShellRequest(r, inst.ID, inst.TenantID); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized: "+err.Error())
		return
	}

	// 10s timeout: gives the fcagent backend headroom for its
	// gRPC-client retries (5 attempts spanning ~3s on UNAVAILABLE) +
	// withRoute route-rediscovery (3 attempts spanning ~3.5s). The
	// admin UI polls this every 15s, so even at the cap we still have
	// 5s of breathing room before the next poll arrives.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	infos, err := s.sandboxMgr.ListShellSessions(ctx, inst.ID)
	if err != nil {
		// Transient gRPC Unavailable from the fcagent (rolling restart,
		// DNS still propagating, network blip) shouldn't 502 the admin
		// UI — the sessions endpoint is polled every few seconds, so
		// the next call will succeed. Degrade gracefully: return an
		// empty list with a header telling the client the result is
		// stale. The Logs tab + Shell sessions panel both already
		// handle empty responses without flagging anything red, so
		// the UX is "sessions list briefly blank during restart"
		// rather than "Error: failed to list sessions."
		if isTransientUnavailable(err) {
			logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
				Info("list_shell_sessions: transient backend unavailable, returning empty list")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Sessions-Stale", "1")
			_ = json.NewEncoder(w).Encode(listShellSessionsResponse{Sessions: []shellSessionAPI{}})
			return
		}
		// "VM not found" + route-recovery exhausted across every
		// discovered fcagent means the sandbox is genuinely gone —
		// the firecracker process is dead and the in-memory state
		// can't be rebuilt. This is terminal, not transient: no
		// amount of retrying will surface a VM that no longer
		// exists. Return 410 Gone with a typed error so the admin
		// UI can render a clean "sandbox no longer running" state
		// instead of a raw gRPC error wall.
		if isSandboxGoneError(err) {
			logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
				Warn("list_shell_sessions: sandbox is gone on all fcagents")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":   "sandbox_gone",
				"message": "This sandbox is no longer running. Reprovision to continue.",
			})
			return
		}
		logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
			Warn("list_shell_sessions: backend error")
		writeJSONError(w, http.StatusBadGateway, "failed to list sessions: "+err.Error())
		return
	}

	out := listShellSessionsResponse{Sessions: make([]shellSessionAPI, 0, len(infos))}
	for _, i := range infos {
		out.Sessions = append(out.Sessions, shellSessionAPI{
			ID:              i.ID,
			AttachedClients: i.AttachedClients,
			CreatedUnix:     i.CreatedUnix,
			IdleSeconds:     i.IdleSeconds,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// HandleKillSandboxShellSession terminates a persistent shell session.
// Idempotent — killing a missing session is treated as success so the
// admin UI doesn't have to special-case a race with the reaper.
//
// DELETE /v1/sandbox/{session_id}/shell-sessions/{shell_session_id}
// DELETE /v1/sandbox/instances/{sandbox_id}/shell-sessions/{shell_session_id}
func (s *Server) HandleKillSandboxShellSession(w http.ResponseWriter, r *http.Request) {
	shellSessionID := mux.Vars(r)["shell_session_id"]
	if shellSessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "shell_session_id is required")
		return
	}
	if s.sandboxMgr == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "sandbox feature is not enabled")
		return
	}

	inst, err := s.resolveShellSandboxInstance(r.Context(), r)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "sandbox not found")
		return
	}

	if err := s.authenticateShellRequest(r, inst.ID, inst.TenantID); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := s.sandboxMgr.KillShellSession(ctx, inst.ID, shellSessionID); err != nil {
		logger.WithFields("sandbox_id", inst.ID, "shell_session_id", shellSessionID, "error", err.Error()).
			Warn("kill_shell_session: backend error")
		writeJSONError(w, http.StatusBadGateway, "failed to kill session: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// resolveShellSandboxInstance resolves the sandbox addressed by either
// /v1/sandbox/{session_id}/... or /v1/sandbox/instances/{sandbox_id}/...
// routes. Prefer the concrete sandbox ID route when present because
// multiple sandbox rows can share a session_id.
func (s *Server) resolveShellSandboxInstance(ctx context.Context, r *http.Request) (instanceRef, error) {
	if sandboxID := mux.Vars(r)["sandbox_id"]; sandboxID != "" {
		return s.resolveSandboxInstanceByIDOrName(ctx, sandboxID)
	}
	sessionID := sandboxSessionIDFromRequest(r)
	if sessionID == "" {
		return instanceRef{}, errMissingSandboxIdentifier
	}
	return s.resolveSandboxInstance(ctx, sessionID)
}

// resolveSandboxInstance mirrors the lookup path used by the shell
// WebSocket handler: in-memory cache first, fall back to the DB so
// the endpoints survive a gateway pod restart even when this pod
// didn't directly create the sandbox.
func (s *Server) resolveSandboxInstance(ctx context.Context, sessionID string) (instanceRef, error) {
	if inst, ok := s.sandboxMgr.GetInstance(sessionID); ok {
		return instanceRef{ID: inst.ID, TenantID: inst.Config.TenantID, ShortCode: inst.ShortCode}, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	dbInst, err := s.sandboxMgr.LookupInstanceBySession(dbCtx, sessionID)
	if err != nil {
		return instanceRef{}, err
	}
	return instanceRef{ID: dbInst.ID, TenantID: dbInst.Config.TenantID, ShortCode: dbInst.ShortCode}, nil
}

// resolveSandboxInstanceByIDOrName mirrors HandleSandboxShellByIDOrName.
func (s *Server) resolveSandboxInstanceByIDOrName(ctx context.Context, sandboxID string) (instanceRef, error) {
	if inst, ok := s.sandboxMgr.GetBySandboxIDOrName(sandboxID); ok {
		return instanceRef{ID: inst.ID, TenantID: inst.Config.TenantID, ShortCode: inst.ShortCode}, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	dbInst, err := s.sandboxMgr.LookupInstanceByIDFromDB(dbCtx, sandboxID)
	if err != nil {
		return instanceRef{}, err
	}
	return instanceRef{ID: dbInst.ID, TenantID: dbInst.Config.TenantID, ShortCode: dbInst.ShortCode}, nil
}

// instanceRef is the small slice of sandbox.Instance that the session
// endpoints actually need — keeps the function signature obvious and
// avoids passing a pointer that might tempt callers to mutate manager
// state through it.
type instanceRef struct {
	ID        string
	TenantID  string
	ShortCode string // may be empty for legacy sandboxes
}

var errMissingSandboxIdentifier = &missingSandboxIdentifierError{}

type missingSandboxIdentifierError struct{}

func (*missingSandboxIdentifierError) Error() string {
	return "session_id or sandbox_id is required"
}

// sandboxSessionIDFromRequest pulls the sandbox session_id from the
// URL. Mirrors the dual-lookup approach already in
// HandleSandboxShell — gorilla mux vars first, fall back to manual
// path parsing for the grpc-gateway path.
func sandboxSessionIDFromRequest(r *http.Request) string {
	if v := mux.Vars(r)["session_id"]; v != "" {
		return v
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for i, p := range parts {
		if p == "sandbox" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// writeJSONError emits a {"error": "..."} body matching the rest of
// the gateway's REST surface so admin UI error handlers can rely on
// a single response shape.
func writeJSONError(w http.ResponseWriter, statusCode int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// isSandboxGoneError returns true when the fcagent backend reports
// that no fcagent in the pool has this sandbox's VM, AND the reason
// in every probe was "VM not found" (not a transport error). That
// combination is terminal: the firecracker process is gone and there's
// nothing to recover.
//
// The message must contain "VM not found" (a clean "I don't have this
// sandbox" answer from an fcagent, not a transport error), combined
// with either of:
//
//  1. The outer error is wrapped in sandbox.ErrSandboxRouteMissing,
//     meaning withRoute exhausted discoverRoute across every target
//     and every probe reported the sandbox missing.
//  2. The error carries gRPC codes.NotFound, meaning the routed agent
//     answered authoritatively that the VM does not exist.
//
// We accept the substring match because the fcagent backend formats
// the wrapped errors with a "%v" verb that loses the typed-error
// chain by the time it reaches this layer. A more disciplined
// solution would propagate a typed ErrVMGone — queued for the
// reconciler-integration follow-up.
func isSandboxGoneError(err error) bool {
	if err == nil {
		return false
	}
	if !strings.Contains(err.Error(), "VM not found") {
		return false
	}
	if errors.Is(err, sandbox.ErrSandboxRouteMissing) {
		return true
	}
	// Newer fcagents return a typed NotFound for a missing VM. When
	// the status survives to this layer unwrapped (single-agent
	// deployments where the route was valid and the agent answered
	// authoritatively), that is just as terminal as the route-missing
	// wrap above.
	return status.Code(err) == codes.NotFound
}

// isTransientUnavailable returns true for gRPC errors that mean
// "this call would have succeeded if we'd retried." Maps to
// Unavailable / DeadlineExceeded / Canceled — the same set
// withRoute already treats as recoverable in the fcagent backend.
//
// Used by read-only endpoints (e.g. list shell sessions) to degrade
// gracefully on a transient backend hiccup instead of surfacing a
// 502 to the admin UI on every restart of the fcagent DaemonSet.
func isTransientUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
			return true
		}
	}
	// Some error paths wrap the gRPC error with fmt.Errorf so the
	// status extraction above misses them. Substring-match as a
	// fallback — the gRPC client always emits these phrases verbatim.
	msg := err.Error()
	return strings.Contains(msg, "Unavailable") ||
		strings.Contains(msg, "connection error") ||
		strings.Contains(msg, "context deadline exceeded") ||
		errors.Is(err, context.DeadlineExceeded)
}
