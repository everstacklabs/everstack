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
}

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
	if err != nil {
		http.Error(w, `{"error":"webhook not found"}`, http.StatusNotFound)
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
