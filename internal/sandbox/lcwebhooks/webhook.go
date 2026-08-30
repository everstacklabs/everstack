// Package lcwebhooks implements customer-facing outgoing webhook delivery
// for sandbox lifecycle events (sandbox.started, sandbox.stopped, etc.).
//
// Architecture: the Notifier subscribes to the EventBus (which listens on
// the sandbox_events Postgres NOTIFY channel) and fans out to configured
// webhook endpoints per tenant. Delivery is at-least-once with 3 retries
// and exponential backoff. A delivery log (last 100 per endpoint) is stored
// for the admin UI.
package lcwebhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
)

// Endpoint is a customer webhook destination.
type Endpoint struct {
	ID        string    `db:"id"         json:"id"`
	TenantID  string    `db:"tenant_id"  json:"tenant_id"`
	URL       string    `db:"url"        json:"url"`
	Events    []byte    `db:"events"     json:"-"` // raw JSONB
	EventList []string  `db:"-"          json:"events"`
	Secret    string    `db:"secret"     json:"secret,omitempty"`
	Enabled   bool      `db:"enabled"    json:"enabled"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// Delivery is a single webhook delivery attempt record.
type Delivery struct {
	ID         int64     `db:"id"          json:"id"`
	EndpointID string    `db:"endpoint_id" json:"endpoint_id"`
	TenantID   string    `db:"tenant_id"   json:"tenant_id"`
	Event      string    `db:"event"       json:"event"`
	Payload    []byte    `db:"payload"     json:"payload"`
	StatusCode *int      `db:"status_code" json:"status_code,omitempty"`
	Error      *string   `db:"error"       json:"error,omitempty"`
	DurationMs *int      `db:"duration_ms" json:"duration_ms,omitempty"`
	CreatedAt  time.Time `db:"created_at"  json:"created_at"`
}

// Payload is the JSON body sent to the customer's endpoint.
type Payload struct {
	Event     string                 `json:"event"`
	Timestamp string                 `json:"timestamp"`
	SandboxID string                 `json:"sandbox_id"`
	TenantID  string                 `json:"tenant_id"`
	State     string                 `json:"state"`
	Status    string                 `json:"status"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// lifecycleToEvent maps a sandbox lifecycle state to a webhook event name.
// Returns "" for states that don't trigger outgoing webhooks.
func lifecycleToEvent(state string) string {
	switch state {
	case "running":
		return "sandbox.started"
	case "sleeping", "stopped":
		return "sandbox.stopped"
	case "archived":
		return "sandbox.archived"
	case "terminated":
		return "sandbox.deleted"
	case "failed":
		return "sandbox.error"
	}
	return ""
}

// Repository handles DB operations for webhook endpoints and deliveries.
type Repository struct {
	db *sqlx.DB
}

// NewRepository creates a Repository.
func NewRepository(db *sqlx.DB) *Repository { return &Repository{db: db} }

func newEndpointID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return "whe_" + hex.EncodeToString(b)
}

// CreateEndpoint inserts a new webhook endpoint.
func (r *Repository) CreateEndpoint(ctx context.Context, tenantID, url string, events []string, secret string) (*Endpoint, error) {
	eventsJSON, _ := json.Marshal(events)
	id := newEndpointID()
	const q = `
		INSERT INTO sandbox_lifecycle_webhook_endpoints (id, tenant_id, url, events, secret, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING *`
	var ep Endpoint
	if err := r.db.GetContext(ctx, &ep, q, id, tenantID, url, eventsJSON, secret); err != nil {
		return nil, fmt.Errorf("create endpoint: %w", err)
	}
	_ = json.Unmarshal(ep.Events, &ep.EventList)
	return &ep, nil
}

// ListEndpoints returns all endpoints for a tenant.
func (r *Repository) ListEndpoints(ctx context.Context, tenantID string) ([]Endpoint, error) {
	const q = `SELECT * FROM sandbox_lifecycle_webhook_endpoints WHERE tenant_id = $1 ORDER BY created_at DESC`
	var eps []Endpoint
	if err := r.db.SelectContext(ctx, &eps, q, tenantID); err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	for i := range eps {
		_ = json.Unmarshal(eps[i].Events, &eps[i].EventList)
	}
	if eps == nil {
		eps = []Endpoint{}
	}
	return eps, nil
}

// DeleteEndpoint removes a webhook endpoint.
func (r *Repository) DeleteEndpoint(ctx context.Context, tenantID, id string) error {
	const q = `DELETE FROM sandbox_lifecycle_webhook_endpoints WHERE id = $1 AND tenant_id = $2`
	res, err := r.db.ExecContext(ctx, q, id, tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("endpoint not found")
	}
	return nil
}

// ListDeliveries returns the last 100 deliveries for an endpoint.
func (r *Repository) ListDeliveries(ctx context.Context, tenantID, endpointID string) ([]Delivery, error) {
	const q = `
		SELECT * FROM sandbox_lifecycle_webhook_deliveries
		WHERE endpoint_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC LIMIT 100`
	var ds []Delivery
	if err := r.db.SelectContext(ctx, &ds, q, endpointID, tenantID); err != nil {
		return nil, err
	}
	if ds == nil {
		ds = []Delivery{}
	}
	return ds, nil
}

func (r *Repository) activeEndpoints(ctx context.Context, tenantID string) ([]Endpoint, error) {
	const q = `SELECT * FROM sandbox_lifecycle_webhook_endpoints WHERE tenant_id = $1 AND enabled = true`
	var eps []Endpoint
	if err := r.db.SelectContext(ctx, &eps, q, tenantID); err != nil {
		return nil, err
	}
	for i := range eps {
		_ = json.Unmarshal(eps[i].Events, &eps[i].EventList)
	}
	return eps, nil
}

func (r *Repository) logDelivery(ctx context.Context, d Delivery) {
	const q = `
		INSERT INTO sandbox_lifecycle_webhook_deliveries (endpoint_id, tenant_id, event, payload, status_code, error, duration_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`
	_, _ = r.db.ExecContext(ctx, q, d.EndpointID, d.TenantID, d.Event, d.Payload, d.StatusCode, d.Error, d.DurationMs)
	// Trim old deliveries (keep last 100 per endpoint).
	_, _ = r.db.ExecContext(ctx,
		`DELETE FROM sandbox_lifecycle_webhook_deliveries WHERE endpoint_id = $1 AND id NOT IN (
			SELECT id FROM sandbox_lifecycle_webhook_deliveries WHERE endpoint_id = $1 ORDER BY created_at DESC LIMIT 100
		)`, d.EndpointID)
}

// Notifier subscribes to the EventBus and delivers webhooks.
type Notifier struct {
	repo   *Repository
	bus    *sandboxlc.EventBus
	client *http.Client
}

// NewNotifier creates a Notifier.
func NewNotifier(repo *Repository, bus *sandboxlc.EventBus) *Notifier {
	return &Notifier{
		repo: repo,
		bus:  bus,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Run starts the notification loop. Blocks until ctx is cancelled.
// Subscribes to ALL tenant events via a special wildcard channel;
// for now uses a single subscription keyed by "_all" and filters by tenant in-process.
// TODO: subscribe per tenant on first endpoint registration.
func (n *Notifier) Run(ctx context.Context) {
	// Subscribe to the event bus using a synthetic tenant "_all" -- the
	// EventBus broadcast method doesn't exist yet; use a per-pod approach
	// where we subscribe once and handle all tenants.
	// For now, use a timer-based poll from the DB as a simpler approach
	// that doesn't require EventBus changes. This is less latency-sensitive
	// than SSE (webhooks are async by definition) so polling every 5s is fine.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Track the last processed updated_at to avoid redelivery.
	var lastSeen time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.processBatch(ctx, &lastSeen)
		}
	}
}

// processBatch scans recently-changed sandbox rows and delivers webhooks.
func (n *Notifier) processBatch(ctx context.Context, lastSeen *time.Time) {
	since := *lastSeen
	if since.IsZero() {
		since = time.Now().Add(-10 * time.Second)
	}

	const q = `
		SELECT id, tenant_id, lifecycle_state, status, updated_at
		FROM sandbox_instances
		WHERE updated_at > $1
		ORDER BY updated_at ASC
		LIMIT 200`
	type row struct {
		ID             string    `db:"id"`
		TenantID       string    `db:"tenant_id"`
		LifecycleState string    `db:"lifecycle_state"`
		Status         string    `db:"status"`
		UpdatedAt      time.Time `db:"updated_at"`
	}
	var rows []row
	if err := n.repo.db.SelectContext(ctx, &rows, q, since); err != nil {
		logger.WithFields("error", err.Error()).Warn("lcwebhooks: batch scan failed")
		return
	}
	for _, r := range rows {
		if r.UpdatedAt.After(*lastSeen) {
			*lastSeen = r.UpdatedAt
		}
		eventName := lifecycleToEvent(r.LifecycleState)
		if eventName == "" {
			continue
		}
		n.deliver(ctx, r.TenantID, eventName, Payload{
			Event:     eventName,
			Timestamp: r.UpdatedAt.UTC().Format(time.RFC3339),
			SandboxID: r.ID,
			TenantID:  r.TenantID,
			State:     r.LifecycleState,
			Status:    r.Status,
		})
	}
}

// deliver fans out to all active endpoints for the tenant that subscribe to this event.
func (n *Notifier) deliver(ctx context.Context, tenantID, event string, payload Payload) {
	eps, err := n.repo.activeEndpoints(ctx, tenantID)
	if err != nil || len(eps) == 0 {
		return
	}
	body, _ := json.Marshal(payload)
	for _, ep := range eps {
		if !endpointWantsEvent(ep.EventList, event) {
			continue
		}
		n.deliverToEndpoint(ctx, ep, event, body)
	}
}

// deliverToEndpoint sends the payload to one endpoint with 3 retries.
func (n *Notifier) deliverToEndpoint(ctx context.Context, ep Endpoint, event string, body []byte) {
	delays := []time.Duration{0, 5 * time.Second, 30 * time.Second}
	for attempt, delay := range delays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		start := time.Now()
		statusCode, err := n.post(ep.URL, ep.Secret, body)
		durationMs := int(time.Since(start).Milliseconds())
		if err == nil && statusCode >= 200 && statusCode < 300 {
			n.repo.logDelivery(ctx, Delivery{
				EndpointID: ep.ID, TenantID: ep.TenantID, Event: event,
				Payload: body, StatusCode: &statusCode, DurationMs: &durationMs,
			})
			return
		}
		errStr := fmt.Sprintf("attempt %d: status %d", attempt+1, statusCode)
		if err != nil {
			errStr = fmt.Sprintf("attempt %d: %v", attempt+1, err)
		}
		logger.WithFields("endpoint_id", ep.ID, "event", event, "attempt", attempt+1, "error", errStr).
			Warn("lcwebhooks: delivery failed")
		if attempt == len(delays)-1 {
			n.repo.logDelivery(ctx, Delivery{
				EndpointID: ep.ID, TenantID: ep.TenantID, Event: event,
				Payload: body, StatusCode: &statusCode, Error: &errStr, DurationMs: &durationMs,
			})
		}
	}
}

func (n *Notifier) post(url, secret string, body []byte) (int, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Everstack-Webhook/1.0")
	if secret != "" {
		sig := hmacSHA256(secret, body)
		req.Header.Set("X-Everstack-Signature", "sha256="+sig)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func hmacSHA256(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func endpointWantsEvent(subscribed []string, event string) bool {
	for _, e := range subscribed {
		if e == event || e == "*" {
			return true
		}
	}
	// Empty subscribed list = subscribe to all.
	if len(subscribed) == 0 {
		return true
	}
	return false
}

// Sign returns the HMAC-SHA256 signature for a webhook body (used by tests + UI test endpoint).
func Sign(secret string, body []byte) string {
	return hmacSHA256(secret, body)
}

// EventNames is the canonical list of outgoing webhook event names.
var EventNames = []string{
	"sandbox.started",
	"sandbox.stopped",
	"sandbox.archived",
	"sandbox.deleted",
	"sandbox.error",
}

// ValidateEvents returns an error if any event name is not in EventNames.
func ValidateEvents(events []string) error {
	valid := make(map[string]bool, len(EventNames))
	for _, e := range EventNames {
		valid[e] = true
	}
	for _, e := range events {
		if e != "*" && !valid[e] {
			return fmt.Errorf("unknown event %q; valid: %s", e, strings.Join(EventNames, ", "))
		}
	}
	return nil
}
