package openaiapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) http.Handler { return &HTTPHandler{service: service} }

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o, err := ownershipFromContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/openai-agents"), "/")
	parts := []string{}
	if path != "" {
		parts = strings.Split(path, "/")
	}
	if len(parts) == 0 {
		writeError(w, ErrNotFound)
		return
	}
	switch parts[0] {
	case "capabilities":
		if r.Method != http.MethodGet {
			writeMethod(w)
			return
		}
		v, e := h.service.Capabilities(r.Context(), o)
		writeResult(w, v, e, http.StatusOK)
	case "connections":
		h.connections(w, r, o, parts[1:])
	case "sessions":
		h.sessions(w, r, o, parts[1:])
	case "approvals":
		h.approvals(w, r, o, parts[1:])
	default:
		writeError(w, ErrNotFound)
	}
}
func (h *HTTPHandler) approvals(w http.ResponseWriter, r *http.Request, o Ownership, p []string) {
	if len(p) == 0 && r.Method == http.MethodGet {
		v, e := h.service.ListApprovals(r.Context(), o)
		writeResult(w, map[string]any{"data": v}, e, http.StatusOK)
		return
	}
	if len(p) == 2 && p[1] == "resolve" && r.Method == http.MethodPost {
		var in struct {
			Decision string `json:"decision"`
		}
		if !decode(w, r, &in) {
			return
		}
		e := h.service.ResolveApproval(r.Context(), o, p[0], in.Decision)
		writeResult(w, map[string]bool{"resolved": e == nil}, e, http.StatusOK)
		return
	}
	writeMethod(w)
}
func (h *HTTPHandler) connections(w http.ResponseWriter, r *http.Request, o Ownership, p []string) {
	if len(p) == 0 && r.Method == http.MethodPost {
		var in CreateConnectionRequest
		if !decode(w, r, &in) {
			return
		}
		v, e := h.service.CreateConnection(r.Context(), o, in)
		writeResult(w, v, e, http.StatusCreated)
		return
	}
	if len(p) == 0 && r.Method == http.MethodGet {
		v, e := h.service.ListConnections(r.Context(), o)
		writeResult(w, map[string]any{"data": v}, e, http.StatusOK)
		return
	}
	if len(p) == 1 && r.Method == http.MethodGet {
		v, e := h.service.GetConnection(r.Context(), o, p[0])
		writeResult(w, v, e, http.StatusOK)
		return
	}
	if len(p) == 1 && r.Method == http.MethodPatch {
		var in UpdateConnectionRequest
		if !decode(w, r, &in) {
			return
		}
		v, e := h.service.UpdateConnection(r.Context(), o, p[0], in)
		writeResult(w, v, e, http.StatusOK)
		return
	}
	if len(p) == 1 && r.Method == http.MethodDelete {
		e := h.service.DeleteConnection(r.Context(), o, p[0])
		writeResult(w, map[string]bool{"deleted": e == nil}, e, http.StatusOK)
		return
	}
	writeMethod(w)
}
func (h *HTTPHandler) sessions(w http.ResponseWriter, r *http.Request, o Ownership, p []string) {
	if len(p) == 0 && r.Method == http.MethodPost {
		var in CreateSessionRequest
		if !decode(w, r, &in) {
			return
		}
		v, e := h.service.CreateSession(r.Context(), o, in)
		writeResult(w, v, e, http.StatusCreated)
		return
	}
	if len(p) == 0 && r.Method == http.MethodGet {
		v, e := h.service.ListSessions(r.Context(), o)
		writeResult(w, map[string]any{"data": v}, e, http.StatusOK)
		return
	}
	if len(p) == 1 && r.Method == http.MethodGet {
		v, e := h.service.GetSession(r.Context(), o, p[0])
		writeResult(w, v, e, http.StatusOK)
		return
	}
	if len(p) == 1 && r.Method == http.MethodDelete {
		e := h.service.DeleteSession(r.Context(), o, p[0])
		writeResult(w, map[string]bool{"deleted": e == nil}, e, http.StatusOK)
		return
	}
	if len(p) == 2 && p[1] == "input" && r.Method == http.MethodPost {
		var in InputRequest
		if !decode(w, r, &in) {
			return
		}
		e := h.service.SendInput(r.Context(), o, p[0], in)
		writeResult(w, map[string]bool{"accepted": e == nil}, e, http.StatusAccepted)
		return
	}
	if len(p) == 2 && p[1] == "cancel" && r.Method == http.MethodPost {
		e := h.service.Cancel(r.Context(), o, p[0])
		writeResult(w, map[string]bool{"accepted": e == nil}, e, http.StatusAccepted)
		return
	}
	if len(p) == 2 && p[1] == "events" && r.Method == http.MethodGet {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		v, e := h.service.ListEvents(r.Context(), o, p[0], after, limit)
		next := after
		if len(v) > 0 {
			next = v[len(v)-1].Cursor
		}
		writeResult(w, map[string]any{"data": v, "next_cursor": next, "has_more": limit > 0 && len(v) == limit}, e, http.StatusOK)
		return
	}
	if len(p) == 2 && p[1] == "usage" && r.Method == http.MethodGet {
		v, e := h.service.Usage(r.Context(), o, p[0])
		writeResult(w, v, e, http.StatusOK)
		return
	}
	if len(p) == 2 && p[1] == "inputs" && r.Method == http.MethodPost {
		if e := authorize(o, "session:write"); e != nil {
			writeError(w, e)
			return
		}
		var in struct {
			Filename      string `json:"filename"`
			ContentType   string `json:"content_type"`
			ContentBase64 string `json:"content_base64"`
			Destination   string `json:"destination"`
		}
		if !decode(w, r, &in) {
			return
		}
		b, e := base64.StdEncoding.DecodeString(in.ContentBase64)
		if e != nil {
			writeError(w, ErrInvalid)
			return
		}
		if h.service.artifacts == nil {
			writeError(w, ErrUnavailable)
			return
		}
		v, e := h.service.UploadSessionInput(r.Context(), o, p[0], in.Filename, in.ContentType, b, in.Destination)
		writeResult(w, v, e, http.StatusCreated)
		return
	}
	if len(p) == 2 && p[1] == "artifacts" && r.Method == http.MethodGet {
		if e := authorize(o, "session:read"); e != nil {
			writeError(w, e)
			return
		}
		if h.service.artifacts == nil {
			writeError(w, ErrUnavailable)
			return
		}
		v, e := h.service.ListSessionArtifacts(r.Context(), o, p[0])
		writeResult(w, map[string]any{"data": v}, e, http.StatusOK)
		return
	}
	if len(p) == 3 && p[1] == "artifacts" && p[2] == "export" && r.Method == http.MethodPost {
		if e := authorize(o, "session:write"); e != nil {
			writeError(w, e)
			return
		}
		var in struct {
			Path string `json:"path"`
		}
		if !decode(w, r, &in) {
			return
		}
		if h.service.artifacts == nil {
			writeError(w, ErrUnavailable)
			return
		}
		v, e := h.service.ExportSessionArtifact(r.Context(), o, p[0], in.Path)
		writeResult(w, v, e, http.StatusCreated)
		return
	}
	if len(p) == 4 && p[1] == "artifacts" && p[3] == "download" && r.Method == http.MethodGet {
		if e := authorize(o, "session:read"); e != nil {
			writeError(w, e)
			return
		}
		if h.service.artifacts == nil {
			writeError(w, ErrUnavailable)
			return
		}
		v, e := h.service.GetArtifactDownload(r.Context(), o, p[0], p[2])
		writeResult(w, v, e, http.StatusOK)
		return
	}
	if len(p) == 3 && p[1] == "artifacts" && r.Method == http.MethodDelete {
		if e := authorize(o, "session:write"); e != nil {
			writeError(w, e)
			return
		}
		if h.service.artifacts == nil {
			writeError(w, ErrUnavailable)
			return
		}
		e := h.service.DeleteSessionArtifact(r.Context(), o, p[0], p[2])
		writeResult(w, map[string]bool{"deleted": e == nil}, e, http.StatusOK)
		return
	}
	writeMethod(w)
}
func ownershipFromContext(r *http.Request) (Ownership, error) {
	return OwnershipFromContext(r.Context())
}

// OwnershipFromContext derives OpenAI Agents scope exclusively from verified
// authentication middleware state. Transports must never accept these values
// from request payloads.
func OwnershipFromContext(ctx context.Context) (Ownership, error) {
	if !contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return Ownership{}, ErrForbidden
	}
	tenant := contextkeys.GetTenantID(ctx)
	o := Ownership{OrganizationID: tenant, InstanceID: tenant, Role: contextkeys.GetUserRole(ctx)}
	if scope, ok := contextkeys.RequestInstanceScopeFromContext(ctx); ok {
		if tenant != scope.InstanceID && tenant != scope.OrganizationID {
			return Ownership{}, ErrForbidden
		}
		o.OrganizationID = scope.OrganizationID
		o.InstanceID = scope.InstanceID
	}
	if u := contextkeys.GetAuthenticatedUserID(ctx); u != "" {
		o.PrincipalID = u
	} else if k := contextkeys.GetAPIKeyHash(ctx); k != "" {
		o.PrincipalID = "apikey:" + k
	}
	return o, nil
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		writeError(w, ErrInvalid)
		return false
	}
	return true
}
func writeResult(w http.ResponseWriter, v any, err error, status int) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal"
	switch {
	case errors.Is(err, ErrForbidden):
		status = http.StatusForbidden
		code = "permission_denied"
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
		code = "not_found"
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
		code = "invalid_argument"
	case errors.Is(err, ErrUnavailable):
		status = http.StatusServiceUnavailable
		code = "unavailable"
	case errors.Is(err, ErrCreatePending):
		status = http.StatusConflict
		code = "recovery_pending"
	case errors.Is(err, ErrConnectionInUse):
		status = http.StatusConflict
		code = "connection_in_use"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": sanitizeError(err.Error())}})
}
func writeMethod(w http.ResponseWriter) { writeError(w, ErrNotFound) }

// SanitizeError removes credentials and sensitive URLs before a transport
// exposes an internal error message to a caller.
func SanitizeError(message string) string { return sanitizeError(message) }
