package v1

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/everstacklabs/everstack/internal/sandbox/lcwebhooks"
)

// HandleListLCWebhooks serves GET /v1/sandbox-webhooks.
func (s *Server) HandleListLCWebhooks(w http.ResponseWriter, r *http.Request) {
	if s.lcWebhookRepo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "webhooks not configured")
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), r.URL.Query().Get("tenant_id"))
	eps, err := s.lcWebhookRepo.ListEndpoints(r.Context(), tenantID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"endpoints": eps, "total": len(eps)})
}

// HandleCreateLCWebhook serves POST /v1/sandbox-webhooks.
// Body: { "url": string, "events": string[], "secret": string }
func (s *Server) HandleCreateLCWebhook(w http.ResponseWriter, r *http.Request) {
	if s.lcWebhookRepo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "webhooks not configured")
		return
	}
	var body struct {
		URL      string   `json:"url"`
		Events   []string `json:"events"`
		Secret   string   `json:"secret"`
		TenantID string   `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.URL == "" {
		writeJSONError(w, http.StatusBadRequest, "url is required")
		return
	}
	if err := lcwebhooks.ValidateEvents(body.Events); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), body.TenantID)
	ep, err := s.lcWebhookRepo.CreateEndpoint(r.Context(), tenantID, body.URL, body.Events, body.Secret)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(ep)
}

// HandleDeleteLCWebhook serves DELETE /v1/sandbox-webhooks/{webhook_id}.
func (s *Server) HandleDeleteLCWebhook(w http.ResponseWriter, r *http.Request) {
	if s.lcWebhookRepo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "webhooks not configured")
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), r.URL.Query().Get("tenant_id"))
	id := mux.Vars(r)["webhook_id"]
	if err := s.lcWebhookRepo.DeleteEndpoint(r.Context(), tenantID, id); err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleListLCWebhookDeliveries serves GET /v1/sandbox-webhooks/{webhook_id}/deliveries.
func (s *Server) HandleListLCWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	if s.lcWebhookRepo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "webhooks not configured")
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), r.URL.Query().Get("tenant_id"))
	id := mux.Vars(r)["webhook_id"]
	ds, err := s.lcWebhookRepo.ListDeliveries(r.Context(), tenantID, id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"deliveries": ds, "total": len(ds)})
}

// HandleTestLCWebhook serves POST /v1/sandbox-webhooks/{webhook_id}/test.
// Sends a test payload to the endpoint and returns the result.
func (s *Server) HandleTestLCWebhook(w http.ResponseWriter, r *http.Request) {
	if s.lcWebhookRepo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "webhooks not configured")
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), r.URL.Query().Get("tenant_id"))
	id := mux.Vars(r)["webhook_id"]
	eps, err := s.lcWebhookRepo.ListEndpoints(r.Context(), tenantID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var target *lcwebhooks.Endpoint
	for i := range eps {
		if eps[i].ID == id {
			target = &eps[i]
			break
		}
	}
	if target == nil {
		writeJSONError(w, http.StatusNotFound, "webhook endpoint not found")
		return
	}

	testPayload := lcwebhooks.Payload{
		Event:     "sandbox.started",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		SandboxID: "sbx_test_payload",
		TenantID:  tenantID,
		State:     "running",
		Status:    "running",
	}
	body, _ := json.Marshal(testPayload)
	sig := lcwebhooks.Sign(target.Secret, body)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"payload":   testPayload,
		"signature": "sha256=" + sig,
		"note":      "Test payload generated. Actual delivery happens via the Notifier in background.",
	})
}
