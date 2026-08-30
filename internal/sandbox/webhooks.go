package sandbox

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"golang.org/x/time/rate"
)

// SandboxWebhook represents a webhook trigger for sandbox execution.
type SandboxWebhook struct {
	ID              int64           `json:"id" db:"id"`
	TenantID        string          `json:"tenant_id" db:"tenant_id"`
	SandboxID       string          `json:"sandbox_id" db:"sandbox_id"`
	SessionID       string          `json:"session_id" db:"session_id"`
	Name            string          `json:"name" db:"name"`
	Path            string          `json:"path" db:"path"`
	Secret          string          `json:"secret" db:"secret"`
	Command         string          `json:"command" db:"command"`
	WorkDir         string          `json:"work_dir" db:"work_dir"`
	TimeoutSeconds  int             `json:"timeout_seconds" db:"timeout_seconds"`
	Enabled         bool            `json:"enabled" db:"enabled"`
	RateLimitRPM    int             `json:"rate_limit_rpm" db:"rate_limit_rpm"`
	LastTriggeredAt *time.Time      `json:"last_triggered_at,omitempty" db:"last_triggered_at"`
	TriggerCount    int             `json:"trigger_count" db:"trigger_count"`
	ErrorCount      int             `json:"error_count" db:"error_count"`
	LastError       sql.NullString  `json:"last_error,omitempty" db:"last_error"`
	AutoRecreate    bool            `json:"auto_recreate" db:"auto_recreate"`
	SandboxConfig   json.RawMessage `json:"sandbox_config" db:"sandbox_config"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
}

// WebhookRouter is an HTTP handler that routes webhook requests to sandbox executions.
// Route by URL path (e.g., /wh/{webhook_id}), verifies HMAC signature, and executes.
type WebhookRouter struct {
	manager  *SandboxManager
	limiters sync.Map // webhook_id → *rate.Limiter
}

// NewWebhookRouter creates a new webhook router.
func NewWebhookRouter(manager *SandboxManager) *WebhookRouter {
	return &WebhookRouter{
		manager: manager,
	}
}

// ServeHTTP implements the http.Handler interface.
func (wr *WebhookRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract webhook path from URL: /wh/{path}
	path := strings.TrimPrefix(r.URL.Path, "/wh/")
	if path == "" || path == r.URL.Path {
		http.Error(w, `{"error":"invalid webhook path"}`, http.StatusBadRequest)
		return
	}

	db := wr.manager.DB()
	if db == nil {
		http.Error(w, `{"error":"database not available"}`, http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Look up webhook by path
	var webhook SandboxWebhook
	const q = `SELECT * FROM sandbox_webhooks WHERE path = $1 AND enabled = true`
	if err := db.GetContext(ctx, &webhook, q, path); err != nil {
		http.Error(w, `{"error":"webhook not found"}`, http.StatusNotFound)
		return
	}

	// Read body (1MB limit)
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}

	// Verify HMAC signature
	signature := r.Header.Get("X-Webhook-Signature")
	if signature == "" {
		http.Error(w, `{"error":"missing X-Webhook-Signature header"}`, http.StatusUnauthorized)
		return
	}

	if !wr.verifySignature(webhook.Secret, body, signature) {
		http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
		return
	}

	// Rate limit check
	limiter := wr.getLimiter(webhook.ID, webhook.RateLimitRPM)
	if !limiter.Allow() {
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	start := time.Now()

	// Get or recreate sandbox
	var inst *Instance
	if webhook.AutoRecreate && len(webhook.SandboxConfig) > 0 {
		inst, err = wr.manager.GetOrRecreate(ctx, webhook.SessionID, webhook.TenantID, webhook.SandboxConfig)
	} else {
		inst, _ = wr.manager.GetInstance(webhook.SessionID)
		if inst == nil {
			err = fmt.Errorf("sandbox not running for session %s", webhook.SessionID)
		}
	}

	if err != nil {
		wr.recordTrigger(ctx, webhook, "", "failed", err.Error(), 0, r, body)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusServiceUnavailable)
		return
	}

	// Execute command. Pass webhook body as WEBHOOK_BODY env var (safe, no shell injection).
	cmd := strings.Fields(webhook.Command)
	if len(cmd) == 0 {
		cmd = []string{"sh", "-c", webhook.Command}
	}
	result, execErr := wr.manager.Exec(ctx, webhook.SessionID, ExecRequest{
		Command: cmd,
		WorkDir: webhook.WorkDir,
		Timeout: time.Duration(webhook.TimeoutSeconds) * time.Second,
		Env:     map[string]string{"WEBHOOK_BODY": string(body), "WEBHOOK_METHOD": r.Method},
	})

	durationMs := time.Since(start).Milliseconds()
	status := "completed"
	errStr := ""
	if execErr != nil {
		status = "failed"
		errStr = execErr.Error()
	} else if result != nil && result.ExitCode != 0 {
		status = "failed"
		errStr = fmt.Sprintf("exit code %d", result.ExitCode)
	}

	wr.recordTrigger(ctx, webhook, inst.ID, status, errStr, durationMs, r, body)

	// Reset keep-warm clock to end-of-execution so the idle timeout starts now.
	wr.manager.touchLastUsed(webhook.SessionID)

	// Record event
	go wr.manager.recordEvent(webhook.SandboxID, webhook.SessionID, webhook.TenantID, EventWebhookTrigger, fmt.Sprintf("Webhook '%s' triggered", webhook.Name), map[string]interface{}{
		"webhook_id": webhook.ID,
		"method":     r.Method,
		"status":     status,
	}, &durationMs, errStr)

	// Return result
	w.Header().Set("Content-Type", "application/json")
	if execErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      status,
			"error":       errStr,
			"duration_ms": durationMs,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      status,
		"exit_code":   result.ExitCode,
		"stdout":      result.Stdout,
		"stderr":      result.Stderr,
		"duration_ms": durationMs,
	})
}

// verifySignature checks the HMAC SHA256 signature.
// Signature format: "sha256=<hex>"
func (wr *WebhookRouter) verifySignature(secret string, body []byte, signature string) bool {
	sig := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}

// getLimiter returns a rate limiter for a webhook, creating one if needed.
func (wr *WebhookRouter) getLimiter(webhookID int64, rpm int) *rate.Limiter {
	key := fmt.Sprintf("%d", webhookID)
	if l, ok := wr.limiters.Load(key); ok {
		return l.(*rate.Limiter)
	}
	if rpm <= 0 {
		rpm = 60
	}
	l := rate.NewLimiter(rate.Every(time.Minute/time.Duration(rpm)), rpm)
	actual, _ := wr.limiters.LoadOrStore(key, l)
	return actual.(*rate.Limiter)
}

// recordTrigger records a webhook trigger execution in the sandbox_triggers table.
func (wr *WebhookRouter) recordTrigger(ctx context.Context, webhook SandboxWebhook, sandboxID, status, errStr string, durationMs int64, r *http.Request, body []byte) {
	db := wr.manager.DB()
	if db == nil {
		return
	}

	if sandboxID == "" {
		sandboxID = webhook.SandboxID
	}

	headers, _ := json.Marshal(r.Header)

	const triggerQ = `
		INSERT INTO sandbox_triggers
			(trigger_type, trigger_id, sandbox_id, status, error, duration_ms, webhook_method, webhook_headers, webhook_body)
		VALUES ('webhook', $1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8)`

	bodyStr := string(body)
	if len(bodyStr) > 10000 {
		bodyStr = bodyStr[:10000] + "...(truncated)"
	}

	db.ExecContext(ctx, triggerQ, webhook.ID, sandboxID, status, errStr, durationMs, r.Method, headers, bodyStr)

	// Update webhook stats
	const updateQ = `
		UPDATE sandbox_webhooks SET
			last_triggered_at = NOW(),
			trigger_count = trigger_count + 1,
			error_count = CASE WHEN $1 = '' THEN error_count ELSE error_count + 1 END,
			last_error = NULLIF($1, ''),
			updated_at = NOW()
		WHERE id = $2`
	db.ExecContext(ctx, updateQ, errStr, webhook.ID)

	logger.WithFields("webhook_id", webhook.ID, "name", webhook.Name, "status", status).
		Info("webhook_router: trigger recorded")
}
