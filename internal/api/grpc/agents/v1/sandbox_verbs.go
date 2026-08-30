package v1

// Daytona-style lifecycle verb endpoints. These are thin aliases over
// the desired-state machinery with the verb vocabulary external users
// expect:
//
//	POST   /v1/sandbox/instances/{sandbox_id}/start    sleeping|archived -> running
//	POST   /v1/sandbox/instances/{sandbox_id}/stop     running -> stopped (filesystem persists)
//	POST   /v1/sandbox/instances/{sandbox_id}/archive  stopped -> archived (filesystem to object storage)
//	DELETE /v1/sandbox/instances/{sandbox_id}           any -> destroyed
//
// The pre-existing routes (/revive, /terminate, /restore) keep working;
// new clients and the SDK use these. Responses return the row's public
// state so callers can poll or subscribe to SSE for convergence.
//
// Unlike the legacy sandbox REST routes (ownership enforcement gated on
// the deterministic-instance-resolution fix), these handlers verify
// tenant ownership inline and fail closed.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
)

// requireSandboxOwnershipHTTP resolves the caller's tenant and verifies
// it owns sandboxID. Writes the error response and returns false when
// the check fails. 404 (not 403) on mismatch so the response does not
// confirm the existence of another tenant's sandbox.
func (s *Server) requireSandboxOwnershipHTTP(w http.ResponseWriter, r *http.Request, sandboxID string) bool {
	tenantID, err := s.resolveTenantID(r.Context(), "")
	if err != nil || tenantID == "" {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	if !s.sandboxOwnedByTenant(r.Context(), sandboxID, tenantID) {
		writeJSONError(w, http.StatusNotFound, "sandbox not found")
		return false
	}
	return true
}

// requireSandboxBillingHTTP checks the authenticated tenant before accepting
// a desired-state transition that can allocate compute. The executor checks
// again immediately before backend.Create; this preflight keeps an unpaid
// request from being accepted and left retrying in the reconciler.
func (s *Server) requireSandboxBillingHTTP(w http.ResponseWriter, r *http.Request) bool {
	if s.sandboxMgr == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "sandbox feature is not enabled")
		return false
	}
	tenantID, err := s.resolveTenantID(r.Context(), "")
	if err != nil || tenantID == "" {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	if err := s.sandboxMgr.RequireSandboxBilling(tenantID); err != nil {
		writeJSONError(w, http.StatusPaymentRequired, err.Error())
		return false
	}
	return true
}

// writeSandboxStateResponse emits the row's current public state after
// a verb was accepted.
func (s *Server) writeSandboxStateResponse(w http.ResponseWriter, r *http.Request, sandboxID, message string) {
	resp := map[string]interface{}{
		"id":      sandboxID,
		"message": message,
	}
	if s.lifecycleRepo != nil {
		if row, err := s.lifecycleRepo.GetByID(r.Context(), sandboxID); err == nil {
			resp["state"] = sandboxlc.PublicState(row.LifecycleState)
			resp["lifecycle_state"] = row.LifecycleState
			resp["desired_state"] = row.DesiredState
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleSandboxVerb factors the shared shape of the four verbs: resolve
// id, verify ownership, write the desired state, return current state.
func (s *Server) handleSandboxVerb(w http.ResponseWriter, r *http.Request, desired, message string) {
	sandboxID := mux.Vars(r)["sandbox_id"]
	if sandboxID == "" {
		writeJSONError(w, http.StatusBadRequest, "sandbox_id is required")
		return
	}
	if !s.requireSandboxOwnershipHTTP(w, r, sandboxID) {
		return
	}
	if desired == sandboxlc.DesireRunning && !s.requireSandboxBillingHTTP(w, r) {
		return
	}

	if s.lifecycleRepo != nil {
		if err := s.lifecycleRepo.SetDesiredState(r.Context(), sandboxID, desired); err != nil {
			if errors.Is(err, sandboxlc.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "sandbox not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeSandboxStateResponse(w, r, sandboxID, message)
		return
	}

	// Legacy synchronous fallback (reconciler off). Archive has no
	// legacy implementation; everything else maps onto the manager.
	if s.sandboxMgr == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "sandbox feature is not enabled")
		return
	}
	var err error
	switch desired {
	case sandboxlc.DesireRunning:
		_, err = s.sandboxMgr.ReviveSandbox(r.Context(), sandboxID)
	case sandboxlc.DesireSleeping:
		err = s.sandboxMgr.StopSandbox(r.Context(), sandboxID)
	case sandboxlc.DesireTerminated:
		err = s.sandboxMgr.TerminateSandbox(r.Context(), sandboxID)
	default:
		writeJSONError(w, http.StatusNotImplemented, "archive requires the lifecycle reconciler")
		return
	}
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "verb", desired, "error", err.Error()).
			Warn("sandbox_verbs: legacy fallback failed")
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeSandboxStateResponse(w, r, sandboxID, message)
}

// HandleStartSandbox starts a stopped or archived sandbox.
// POST /v1/sandbox/instances/{sandbox_id}/start
func (s *Server) HandleStartSandbox(w http.ResponseWriter, r *http.Request) {
	s.handleSandboxVerb(w, r, sandboxlc.DesireRunning, "Start requested")
}

// HandleStopSandboxVerb stops a running sandbox; the filesystem
// persists and the sandbox can be started again.
// POST /v1/sandbox/instances/{sandbox_id}/stop
func (s *Server) HandleStopSandboxVerb(w http.ResponseWriter, r *http.Request) {
	s.handleSandboxVerb(w, r, sandboxlc.DesireSleeping, "Stop requested")
}

// HandleArchiveSandbox archives a stopped sandbox: its workspace moves
// to object storage and the host-disk copy is freed.
// POST /v1/sandbox/instances/{sandbox_id}/archive
func (s *Server) HandleArchiveSandbox(w http.ResponseWriter, r *http.Request) {
	s.handleSandboxVerb(w, r, sandboxlc.DesireArchived, "Archive requested")
}

// HandleDeleteSandboxVerb destroys a sandbox (Daytona's delete).
// DELETE /v1/sandbox/instances/{sandbox_id}
func (s *Server) HandleDeleteSandboxVerb(w http.ResponseWriter, r *http.Request) {
	s.handleSandboxVerb(w, r, sandboxlc.DesireTerminated, "Delete requested")
}
