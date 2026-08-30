package deployment

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/utils"
)

// Handler exposes HTTP endpoints for invoking agent deployments.
type Handler struct {
	store       Store
	invoker     *Invoker
	rateLimiter *DeploymentRateLimiter
	concLimiter *ConcurrencyLimiter
}

// NewHandler creates a new deployment HTTP handler.
func NewHandler(store Store, invoker *Invoker) *Handler {
	return &Handler{
		store:       store,
		invoker:     invoker,
		rateLimiter: NewDeploymentRateLimiter(),
		concLimiter: NewConcurrencyLimiter(),
	}
}

// HandleInvoke handles POST /v1/deploy/{agent_id}/invoke (sync).
func (h *Handler) HandleInvoke(w http.ResponseWriter, r *http.Request) {
	dep, keyID, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	var req InvokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Message == "" {
		writeJSONError(w, http.StatusBadRequest, "message is required")
		return
	}

	// Check rate limit
	rpm := 0
	burst := 0
	if dep.RateLimitRPM != nil {
		rpm = *dep.RateLimitRPM
	}
	if dep.RateLimitBurst != nil {
		burst = *dep.RateLimitBurst
	}
	if !h.rateLimiter.Allow(dep.ID, rpm, burst) {
		writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// Check concurrency limit
	if !h.concLimiter.Acquire(dep.ID, dep.MaxConcurrentSessions) {
		writeJSONError(w, http.StatusTooManyRequests, "max concurrent sessions exceeded")
		return
	}
	defer h.concLimiter.Release(dep.ID)

	// Record invocation. Use a context detached from the request so a client
	// disconnect does not abort the DB write — without this the row never
	// lands and the Invocations tab shows "No invocations yet" even after
	// successful runs.
	inputPreview := req.Message
	if len(inputPreview) > 500 {
		inputPreview = inputPreview[:500]
	}
	inv := &Invocation{
		TenantID:     dep.TenantID,
		DeploymentID: dep.ID,
		KeyID:        &keyID,
		Status:       string(InvocationRunning),
		InputPreview: inputPreview,
		ClientIP:     clientIP(r),
		UserAgent:    truncate(r.UserAgent(), 500),
	}
	recordCtx := context.WithoutCancel(r.Context())
	if err := h.store.RecordInvocation(recordCtx, inv); err != nil {
		logger.WithFields(
			"deployment_id", dep.ID,
			"tenant_id", dep.TenantID,
			"error", err.Error(),
		).Error("deployment: failed to record invocation")
	}

	// Invoke
	resp, err := h.invoker.InvokeSync(r.Context(), dep, &req)
	if err != nil {
		_ = h.store.CompleteInvocation(recordCtx, inv.ID, "", string(InvocationFailed), "", 0, 0, 0, 0)
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Complete invocation
	outputPreview := resp.Output
	if len(outputPreview) > 500 {
		outputPreview = outputPreview[:500]
	}
	if err := h.store.CompleteInvocation(recordCtx, inv.ID, resp.SessionID, resp.Status, outputPreview,
		resp.Turns, resp.Tokens.Prompt, resp.Tokens.Completion, resp.DurationMs); err != nil {
		logger.WithFields(
			"deployment_id", dep.ID,
			"invocation_id", inv.ID,
			"error", err.Error(),
		).Error("deployment: failed to complete invocation")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleStream handles POST /v1/deploy/{agent_id}/stream (SSE).
func (h *Handler) HandleStream(w http.ResponseWriter, r *http.Request) {
	dep, keyID, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	var req InvokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Message == "" {
		writeJSONError(w, http.StatusBadRequest, "message is required")
		return
	}

	// Check rate limit
	rpm := 0
	burst := 0
	if dep.RateLimitRPM != nil {
		rpm = *dep.RateLimitRPM
	}
	if dep.RateLimitBurst != nil {
		burst = *dep.RateLimitBurst
	}
	if !h.rateLimiter.Allow(dep.ID, rpm, burst) {
		writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// Check concurrency limit
	if !h.concLimiter.Acquire(dep.ID, dep.MaxConcurrentSessions) {
		writeJSONError(w, http.StatusTooManyRequests, "max concurrent sessions exceeded")
		return
	}
	defer h.concLimiter.Release(dep.ID)

	// Record invocation (detached from request ctx — see HandleInvoke).
	inputPreview := req.Message
	if len(inputPreview) > 500 {
		inputPreview = inputPreview[:500]
	}
	inv := &Invocation{
		TenantID:     dep.TenantID,
		DeploymentID: dep.ID,
		KeyID:        &keyID,
		Status:       string(InvocationRunning),
		InputPreview: inputPreview,
		ClientIP:     clientIP(r),
		UserAgent:    truncate(r.UserAgent(), 500),
	}
	recordCtx := context.WithoutCancel(r.Context())
	if err := h.store.RecordInvocation(recordCtx, inv); err != nil {
		logger.WithFields(
			"deployment_id", dep.ID,
			"tenant_id", dep.TenantID,
			"error", err.Error(),
		).Error("deployment: failed to record invocation")
	}

	// Stream — captures summary stats so we can complete the invocation
	// record (previously stayed stuck in 'running' on stream success).
	resp, err := h.invoker.InvokeStream(r.Context(), dep, &req, w)
	if err != nil {
		_ = h.store.CompleteInvocation(recordCtx, inv.ID, "", string(InvocationFailed), "", 0, 0, 0, 0)
		logger.WithFields("error", err.Error()).Warn("deployment: stream invocation failed")
		return
	}
	if resp != nil {
		outputPreview := resp.Output
		if len(outputPreview) > 500 {
			outputPreview = outputPreview[:500]
		}
		if err := h.store.CompleteInvocation(recordCtx, inv.ID, resp.SessionID, resp.Status, outputPreview,
			resp.Turns, resp.Tokens.Prompt, resp.Tokens.Completion, resp.DurationMs); err != nil {
			logger.WithFields(
				"deployment_id", dep.ID,
				"invocation_id", inv.ID,
				"error", err.Error(),
			).Error("deployment: failed to complete stream invocation")
		}
	}
}

// authenticate extracts the deployment key from the Authorization header,
// resolves the deployment, and validates the deployment status.
// Returns the deployment, the key ID, and whether auth succeeded.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (*Deployment, string, bool) {
	// Extract bearer token
	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
		writeJSONError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
		return nil, "", false
	}
	rawKey := strings.TrimPrefix(auth, "Bearer ")
	rawKey = strings.TrimSpace(rawKey)

	// Hash the key
	keyHash, ok := apikeylib.HashFromContext(r.Context(), rawKey)
	if !ok {
		// Fallback: use SHA256 directly if HMAC secret not configured
		keyHash = utils.GenerateRandomString(0) // won't match anything
		writeJSONError(w, http.StatusUnauthorized, "invalid API key")
		return nil, "", false
	}

	// Look up the key
	key, err := h.store.GetKeyByHash(r.Context(), keyHash)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid API key")
		return nil, "", false
	}

	// Check expiration
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		writeJSONError(w, http.StatusUnauthorized, "API key expired")
		return nil, "", false
	}

	// Touch last used (fire-and-forget)
	go func() {
		_ = h.store.TouchKeyLastUsed(r.Context(), key.ID)
	}()

	// Get agent_id from URL
	vars := mux.Vars(r)
	agentID := vars["agent_id"]
	if agentID == "" {
		writeJSONError(w, http.StatusBadRequest, "agent_id is required")
		return nil, "", false
	}

	// Resolve version from query param
	var version *int
	if v := r.URL.Query().Get("version"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			version = &n
		}
	}

	// Load deployment
	dep, err := h.store.GetDeployment(r.Context(), key.DeploymentID, key.TenantID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "deployment not found")
		return nil, "", false
	}

	// If a specific version was requested and it differs, try to load that version
	if version != nil && *version != dep.Version {
		dep, err = h.store.GetActiveDeployment(r.Context(), agentID, key.TenantID, version)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "deployment version not found")
			return nil, "", false
		}
	}

	// Check deployment status
	switch dep.Status {
	case StatusPaused:
		writeJSONError(w, http.StatusServiceUnavailable, "deployment is paused")
		return nil, "", false
	case StatusRetired:
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]string{"error": "deployment is retired"})
		return nil, "", false
	}

	return dep, key.ID, true
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// clientIP extracts the client IP from the request.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// truncate truncates a string to the given max length.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// GenerateDeploymentKey generates a new deployment API key.
// Returns the raw key (shown once to user) and the hash (stored).
func GenerateDeploymentKey(ctx context.Context) (rawKey, keyHash, keyPrefix string, ok bool) {
	raw := "evs_dk_" + utils.GenerateRandomString(24)
	hash, hashOk := apikeylib.HashFromContext(ctx, raw)
	if !hashOk {
		return "", "", "", false
	}
	prefix := raw[:12]
	return raw, hash, prefix, true
}
