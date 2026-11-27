package interop

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	a2a "github.com/everstacklabs/everstack/internal/a2a/client"
	"github.com/gorilla/mux"
)

func NewRunService(store *Store) *RunService {
	return &RunService{Repository: store, Client: &a2a.Client{PrivateEndpoints: strings.Split(os.Getenv("EVS_A2A_PRIVATE_ENDPOINTS"), ",")}}
}
func interopError(w http.ResponseWriter, err error) {
	code, status := "operation_failed", http.StatusInternalServerError
	var protocolErr *a2a.Error
	switch {
	case errors.Is(err, ErrNoTenant):
		code, status = "tenant_required", 401
	case errors.Is(err, ErrNotFound):
		code, status = "not_found", 404
	case errors.Is(err, ErrInvalid):
		code, status = "invalid_request", 400
	case errors.Is(err, ErrConflict):
		code, status = "idempotency_conflict", 409
	case errors.Is(err, ErrBusy):
		code, status = "operation_in_progress", 409
	case errors.Is(err, ErrNoTask):
		code, status = "no_remote_task_handle", 409
	case errors.Is(err, ErrRemoteChanged):
		code, status = "remote_connection_changed", 409
	case errors.Is(err, ErrCredentialReplacement):
		code, status = "credential_replacement_required", 400
	case errors.Is(err, ErrCredentialUnavailable):
		code, status = "credential_encryption_unavailable", 503
	case errors.As(err, &protocolErr):
		code, status = protocolErr.Code, 400
	}
	writeJSON(w, status, map[string]string{"error": code})
}
func readInteropBody(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<10))
	if decoder.Decode(target) != nil {
		interopError(w, ErrInvalid)
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		interopError(w, ErrInvalid)
		return false
	}
	return true
}
func (h *Handlers) externalRuns(w http.ResponseWriter, r *http.Request) {
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	switch r.Method {
	case http.MethodGet:
		runs, err := h.runs.Repository.ListRuns(r.Context(), tenant)
		if err != nil {
			interopError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"runs": runs})
	case http.MethodPost:
		var body struct {
			Remote         string `json:"remote"`
			Message        string `json:"message"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if !readInteropBody(w, r, &body) {
			return
		}
		run, err := h.runs.Start(r.Context(), tenant, body.Remote, body.Message, body.IdempotencyKey)
		if err != nil {
			interopError(w, err)
			return
		}
		writeJSON(w, 200, run)
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(405)
	}
}
func (h *Handlers) externalRun(w http.ResponseWriter, r *http.Request) {
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	id := mux.Vars(r)["id"]
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		return
	}
	run, err := h.runs.Read(r.Context(), tenant, id)
	if err != nil {
		interopError(w, err)
		return
	}
	writeJSON(w, 200, run)
}
func (h *Handlers) externalRunAction(w http.ResponseWriter, r *http.Request) {
	tenant := requireTenant(w, r)
	if tenant == "" {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	action := mux.Vars(r)["action"]
	if action != "refresh" && action != "cancel" {
		w.WriteHeader(404)
		return
	}
	run, err := h.runs.Refresh(r.Context(), tenant, mux.Vars(r)["id"], action == "cancel")
	if err != nil {
		interopError(w, err)
		return
	}
	writeJSON(w, 200, run)
}
