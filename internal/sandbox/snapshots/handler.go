package snapshots

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Handler serves the /v1/snapshots REST API.
type Handler struct {
	repo          *Repository
	resolveTenant func(r *http.Request) (string, error)
}

// NewHandler creates a new snapshot Handler.
func NewHandler(repo *Repository, resolveTenant func(r *http.Request) (string, error)) *Handler {
	return &Handler{repo: repo, resolveTenant: resolveTenant}
}

func (h *Handler) tenantID(r *http.Request) (string, bool) {
	tid, err := h.resolveTenant(r)
	if err != nil || tid == "" {
		return "", false
	}
	return tid, true
}

// HandleList serves GET /v1/snapshots.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tid, ok := h.tenantID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	snaps, err := h.repo.ListByTenant(r.Context(), tid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"snapshots": snaps, "total": len(snaps)})
}

// HandleCreate serves POST /v1/snapshots.
// Body: { "name": string, "image"?: string, "from_sandbox_id"?: string }
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tid, ok := h.tenantID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Name          string `json:"name"`
		Image         string `json:"image"`
		FromSandboxID string `json:"from_sandbox_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.Image == "" && body.FromSandboxID == "" {
		writeErr(w, http.StatusBadRequest, "image or from_sandbox_id is required")
		return
	}

	snap, err := h.repo.Create(r.Context(), CreateParams{
		TenantID:      tid,
		Name:          body.Name,
		Image:         body.Image,
		FromSandboxID: body.FromSandboxID,
	})
	if err != nil {
		if errors.Is(err, ErrDuplicateName) {
			writeErr(w, http.StatusConflict, "a snapshot with this name already exists")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, snap)
}

// HandleGet serves GET /v1/snapshots/{snapshot_id}.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	tid, ok := h.tenantID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := mux.Vars(r)["snapshot_id"]
	snap, err := h.repo.GetByID(r.Context(), tid, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "snapshot not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, snap)
}

// HandleDelete serves DELETE /v1/snapshots/{snapshot_id}.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	tid, ok := h.tenantID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := mux.Vars(r)["snapshot_id"]
	if err := h.repo.Delete(r.Context(), tid, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "snapshot not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RegisterRoutes mounts snapshot routes on the given mux.
// Prefix should be "/v1/snapshots".
func (h *Handler) RegisterRoutes(r *mux.Router) {
	r.Handle("/v1/snapshots", http.HandlerFunc(h.HandleList)).Methods("GET")
	r.Handle("/v1/snapshots", http.HandlerFunc(h.HandleCreate)).Methods("POST")
	r.Handle("/v1/snapshots/{snapshot_id}", http.HandlerFunc(h.HandleGet)).Methods("GET")
	r.Handle("/v1/snapshots/{snapshot_id}", http.HandlerFunc(h.HandleDelete)).Methods("DELETE")
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":     msg,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}
