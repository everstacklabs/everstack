package trigger

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/gorilla/mux"
	"golang.org/x/time/rate"
)

const (
	maxWebhookBodySize  = 1 << 20 // 1MB
	webhookTimeout      = 60 * time.Second
	defaultRateLimitRPM = 60
)

// WebhookHandler is an HTTP handler that routes webhook requests to agent trigger executions.
type WebhookHandler struct {
	store    Store
	executor *Executor
	limiters sync.Map // triggerID → *rate.Limiter

	// hostTenant resolves the request host to the tenant (managed instance)
	// it belongs to. Nil, or a host that does not resolve (platform host,
	// self-hosted), keeps path-only lookup.
	hostTenantMu sync.RWMutex
	hostTenant   func(ctx context.Context, host string) (instanceID string, found bool)
}

// SetHostTenantResolver installs the host → tenant resolver. When the request
// host resolves to a tenant, a webhook path owned by a different tenant is
// answered exactly like an unknown path (404), so paths never leak across
// tenants. nil disables the check.
func (wh *WebhookHandler) SetHostTenantResolver(fn func(ctx context.Context, host string) (instanceID string, found bool)) {
	wh.hostTenantMu.Lock()
	wh.hostTenant = fn
	wh.hostTenantMu.Unlock()
}

func (wh *WebhookHandler) resolveHostTenant(ctx context.Context, host string) (string, bool) {
	wh.hostTenantMu.RLock()
	fn := wh.hostTenant
	wh.hostTenantMu.RUnlock()
	if fn == nil || host == "" {
		return "", false
	}
	return fn(ctx, host)
}

// webhookPausedBody is the 403 body for a webhook whose tenant is paused.
const webhookPausedBody = `{"error":{"type":"instance_paused","code":"instance_paused","message":"This instance is paused."}}`

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(store Store, executor *Executor) *WebhookHandler {
	return &WebhookHandler{
		store:    store,
		executor: executor,
	}
}

// Handle processes an incoming webhook request.
func (wh *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]
	if path == "" {
		http.Error(w, `{"error":"invalid webhook path"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), webhookTimeout)
	defer cancel()

	// Look up trigger by webhook path
	t, err := wh.store.GetTriggerByWebhookPath(ctx, path)
	if err != nil || t == nil {
		http.Error(w, `{"error":"webhook not found"}`, http.StatusNotFound)
		return
	}

	// On a tenant host, the path must belong to that tenant. Answer a
	// mismatch exactly like an unknown path.
	if hostTenant, found := wh.resolveHostTenant(ctx, r.Host); found && hostTenant != t.TenantID {
		logger.WithFields("trigger_id", t.ID, "tenant_id", t.TenantID, "host_tenant_id", hostTenant).
			Info("trigger_webhook: path does not belong to the request host's tenant")
		http.Error(w, `{"error":"webhook not found"}`, http.StatusNotFound)
		return
	}

	// A paused tenant runs nothing. Answered synchronously (Execute runs in
	// the background) and before the rate limiter so no tokens are spent.
	if !wh.executor.tenantActive(ctx, t.TenantID) {
		logger.WithFields("trigger_id", t.ID, "tenant_id", t.TenantID).
			Info("trigger_webhook: rejected (tenant paused)")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, webhookPausedBody)
		return
	}

	// Check circuit breaker
	if !wh.executor.cb.ShouldExecute(t) {
		http.Error(w, `{"error":"trigger circuit open"}`, http.StatusServiceUnavailable)
		return
	}

	// Read body (1MB limit)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodySize))
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}

	// Verify HMAC signature if secret is set
	if t.WebhookSecretHash != "" {
		signature := r.Header.Get("X-Webhook-Signature")
		if signature == "" {
			http.Error(w, `{"error":"missing X-Webhook-Signature header"}`, http.StatusUnauthorized)
			return
		}
		if !verifyHMAC(t.WebhookSecretHash, body, signature) {
			http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
			return
		}
	}

	// Rate limit check
	limiter := wh.getLimiter(t.ID)
	if !limiter.Allow() {
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	// Build payload JSON
	payload := body
	if len(payload) == 0 {
		payload = []byte("{}")
	}

	// Fire executor asynchronously
	go wh.executor.Execute(context.Background(), t, payload)

	// Return accepted
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "accepted",
		"trigger_id": t.ID,
	})

	logger.WithFields("trigger_id", t.ID, "name", t.Name, "path", path).
		Info("trigger_webhook: request accepted")
}

// verifyHMAC checks the HMAC SHA256 signature against the stored secret.
// Signature format: "sha256=<hex>"
func verifyHMAC(secret string, body []byte, signature string) bool {
	sig := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}

func (wh *WebhookHandler) getLimiter(triggerID string) *rate.Limiter {
	if l, ok := wh.limiters.Load(triggerID); ok {
		return l.(*rate.Limiter)
	}
	rpm := defaultRateLimitRPM
	l := rate.NewLimiter(rate.Every(time.Minute/time.Duration(rpm)), rpm)
	actual, _ := wh.limiters.LoadOrStore(triggerID, l)
	return actual.(*rate.Limiter)
}

// GenerateWebhookSecret generates a random secret for webhook HMAC verification.
func GenerateWebhookSecret() (rawSecret string, secretHash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	raw := fmt.Sprintf("whsec_%s", hex.EncodeToString(b))
	h := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(h[:])
}
