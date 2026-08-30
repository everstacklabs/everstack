package v1

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"

	sandboxcp "github.com/everstacklabs/everstack/internal/sandbox/controlplane"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshots"
)

// HandleListSnapshots serves GET /v1/snapshots.
func (s *Server) HandleListSnapshots(w http.ResponseWriter, r *http.Request) {
	scope, err := s.resolveSandboxTenantInstanceScope(r.Context(), r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	}
	snaps, err := sandboxcp.NewSnapshotService(s.snapshotRepo).ListSnapshots(r.Context(), scope)
	if err != nil {
		writeSnapshotError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"snapshots": snaps,
		"total":     len(snaps),
	})
}

// HandleCreateSnapshot serves POST /v1/snapshots.
// Body: { "name": string, "image"?: string, "from_sandbox_id"?: string, "tenant_id"?: string }
func (s *Server) HandleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string `json:"name"`
		Image         string `json:"image"`
		FromSandboxID string `json:"from_sandbox_id"`
		TenantID      string `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	scope, err := s.resolveSandboxTenantInstanceScope(r.Context(), body.TenantID)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	}
	snap, err := sandboxcp.NewSnapshotService(s.snapshotRepo).CreateSnapshot(r.Context(), sandboxcp.CreateSnapshotRequest{
		Scope:         scope,
		Name:          body.Name,
		Image:         body.Image,
		FromSandboxID: body.FromSandboxID,
	})
	if err != nil {
		writeSnapshotError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(snap)
}

// HandleGetSnapshot serves GET /v1/snapshots/{snapshot_id}.
func (s *Server) HandleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	scope, err := s.resolveSandboxTenantInstanceScope(r.Context(), r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	}
	id := mux.Vars(r)["snapshot_id"]
	snap, err := sandboxcp.NewSnapshotService(s.snapshotRepo).GetSnapshot(r.Context(), scope, id)
	if err != nil {
		writeSnapshotError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

// HandleDeleteSnapshot serves DELETE /v1/snapshots/{snapshot_id}.
func (s *Server) HandleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	scope, err := s.resolveSandboxTenantInstanceScope(r.Context(), r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	}
	id := mux.Vars(r)["snapshot_id"]
	if err := sandboxcp.NewSnapshotService(s.snapshotRepo).DeleteSnapshot(r.Context(), scope, id); err != nil {
		writeSnapshotError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeSnapshotError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sandboxcp.ErrSnapshotsNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "snapshots not configured")
	case errors.Is(err, sandboxcp.ErrSnapshotNameRequired):
		writeJSONError(w, http.StatusBadRequest, "name is required")
	case errors.Is(err, sandboxcp.ErrSnapshotImageRequired):
		writeJSONError(w, http.StatusBadRequest, "image or from_sandbox_id is required")
	case errors.Is(err, snapshots.ErrDuplicateName):
		writeJSONError(w, http.StatusConflict, "a snapshot with this name already exists")
	case errors.Is(err, snapshots.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "snapshot not found")
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}
