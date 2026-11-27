// Package billingmeter records platform-key inference cost against the fungible
// usage-credit wallet from the gateway. It is the gateway-side counterpart to the
// billing service's debit loop: the gateway can reach the billing.* schema (via
// the BillingDB pool) but cannot import services/billing internals, so the wallet
// writes are self-contained raw SQL under the same per-org advisory lock the
// billing service uses. Metering phase only: this records spend (the wallet may
// go negative); it never holds or blocks. Enforcement is a later phase.
//
// The unit of metering is the settle unit: one provider attempt (one Chat,
// ChatStream, Embed or Responses call at dispatch). Every settle unit is keyed
// `inference:<settle_unit_id>` where the id is a UUID minted at dispatch, so a
// gateway request, an agent turn or a tool loop that makes N provider attempts
// produces N ledger rows sharing one correlation id in metadata. See
// docs/design/prepaid-credits-v2.md section 5.
package billingmeter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// inferenceResource is the ledger resource_type for every inference modality, so
// the inference-period ledger CHECK and (later) the inference accounting apply
// uniformly; the modality is carried in metric_type.
const inferenceResource = "inference"

// Modality metric_type values, pinned so the ledger vocabulary cannot drift
// across call sites.
const (
	MetricChat        = "chat"
	MetricEmbeddings  = "embeddings"
	MetricResponses   = "responses"
	MetricImage       = "image"
	MetricAudioInput  = "audio_input"
	MetricAudioOutput = "audio_output"
	MetricModeration  = "moderation"
	MetricRerank      = "rerank"
)

// Provenance of the key that served a settle unit. Only platform provenance is
// ever metered; it is derived from ownership (a provider configuration with
// organization_id IS NULL on a shared gateway), never from a key's label.
const (
	ProvenancePlatform = "platform"
	ProvenanceBYOK     = "byok"
)

// SettleUnit is one provider attempt's wallet debit, captured by value at
// dispatch. Everything is plain data so no request state outlives the request
// through the settle queue or the outbox.
type SettleUnit struct {
	// ID is the settle unit id minted at dispatch; the idempotency key is
	// "inference:" + ID.
	ID string
	// OrgID is the billable organization (resolved from the request tenant).
	OrgID string
	// Modality is the metric_type (MetricChat, MetricEmbeddings, ...).
	Modality string
	// Micros is the wallet charge in micro-dollars, already marked up.
	Micros int64
	// At is the time the attempt completed; it selects the ledger period.
	At time.Time
	// Metadata is stored on the ledger row: correlation id, provider, model,
	// key id, provenance, internal flag. Keys are strings and values are JSON
	// scalars.
	Metadata map[string]any
	// Attempt is the unit's billed-attempt record (billing.inference_attempts).
	// With it, a unit is written even when it charges nothing (a BYOK
	// attempt, a platform retry), and its role and provenance decide whether
	// Micros may be debited at all. Nil for legacy callers.
	Attempt *AttemptRecord
}

// IdempotencyKey is the ledger key for this settle unit.
func (u SettleUnit) IdempotencyKey() string {
	return "inference:" + strings.TrimSpace(u.ID)
}

// Metadata keys written on every inference debit row. Downstream tooling
// (the byok-correction job, attribution in S11) reads these.
const (
	MetaCorrelationID = "correlation_id"
	MetaProvider      = "provider"
	MetaModel         = "model"
	MetaKeyID         = "key_id"
	MetaProvenance    = "provenance"
	MetaInternal      = "internal"
	MetaInternalKind  = "internal_kind"
	MetaCatalogUSD    = "catalog_usd"
	MetaMarkup        = "markup"
	// MetaUsageEstimated is true when the provider reported no usage and
	// the unit was settled on the gateway's character estimate.
	MetaUsageEstimated = "usage_estimated"
	// MetaAdmissionEstimate is the G1 admission estimate in micros (the
	// reservation S5 would have placed), so the estimator's error can be
	// measured per ledger row against amount_micros.
	MetaAdmissionEstimate = "admission_estimate_micros"
	// MetaAbortedInThinking is true when the stream broke off while the
	// provider still held reasoning it had not emitted (an Anthropic
	// thinking block mid-generation). The estimate includes that reasoning.
	MetaAbortedInThinking = "aborted_in_thinking"
)

// MetaAPIKeyID is the gateway API key the request authenticated with
// (api_keys.id), written on every inference debit row that has one. MetaKeyID
// is the upstream provider key; the two are never the same thing.
const MetaAPIKeyID = "api_key_id"

// ResolveOrg maps a request tenant id to its billable organization id. The tenant
// is either an organization itself or a managed instance whose owning org lives
// in everstack.managed_instances. Returns ("", false) when the tenant maps to no
// org (usage is then not billed). Mirrors the billing service's resolver so both
// sides bill the same organization.
func ResolveOrg(ctx context.Context, db *sqlx.DB, tenantID string) (string, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	if db == nil || tenantID == "" {
		return "", false, nil
	}
	var orgID string
	err := db.GetContext(ctx, &orgID, `SELECT id::text FROM everstack.organizations WHERE id::text = $1`, tenantID)
	if err == nil {
		return orgID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	err = db.GetContext(ctx, &orgID, `SELECT organization_id::text FROM everstack.managed_instances WHERE id::text = $1`, tenantID)
	if err == nil {
		return orgID, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return "", false, err
}

// DebitInference records the cost of one platform-key inference request against
// the org's fungible wallet, keyed on requestID. Kept for callers that still
// think in request ids; new code settles a SettleUnit through DebitUnit.
func DebitInference(ctx context.Context, db *sqlx.DB, orgID, requestID, modality string, costMicros int64, now time.Time) error {
	return DebitUnit(ctx, db, SettleUnit{ID: requestID, OrgID: orgID, Modality: modality, Micros: costMicros, At: now})
}

// DebitUnit records one settle unit against the org's fungible wallet, and
// its attempt record when it carries one. It is idempotent on the settle unit
// id, so a durable/retried settle debits and records exactly once. A unit
// that is not chargeable (Micros <= 0, or an attempt whose role or provenance
// is never charged) debits nothing and only writes its attempt record, if
// any. The debit carries the current calendar-month
// period (the inference-period ledger CHECK requires one; exact
// subscription-period alignment is an enforcement-phase concern). Runs in a
// single transaction under the per-org advisory lock the billing service also
// takes, and recomputes the balance cache from the ledger under that lock.
//
// The caller owns durability: settle on a detached context (context.WithoutCancel)
// and retry on error, since this is a one-shot transaction.
func DebitUnit(ctx context.Context, db *sqlx.DB, u SettleUnit) error {
	orgID := strings.TrimSpace(u.OrgID)
	unitID := strings.TrimSpace(u.ID)
	if orgID == "" || unitID == "" {
		return fmt.Errorf("billingmeter: organization and settle unit id are required")
	}
	chargeable := u.chargeable()
	if !chargeable && u.Attempt == nil {
		return nil
	}
	if db == nil {
		return fmt.Errorf("billingmeter: nil billing database")
	}
	if !chargeable {
		return recordAttemptOnly(ctx, db, u)
	}
	modality := strings.TrimSpace(u.Modality)
	if modality == "" {
		modality = MetricChat
	}
	now := u.At
	if now.IsZero() {
		now = time.Now()
	}
	periodStart := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)
	metadata := encodeMetadata(withOccurredAt(u.Metadata, now))

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, orgID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, metric_type, idempotency_key, period_start, period_end, metadata)
		VALUES ($1::uuid, 'debit', $2, $3, $4, $5, $6, $7, $8::jsonb)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		orgID, -u.Micros, inferenceResource, modality, u.IdempotencyKey(), periodStart, periodEnd, metadata); err != nil {
		return fmt.Errorf("debit inference: %w", err)
	}
	if err := recordChargedAttempt(ctx, tx, u); err != nil {
		return err
	}

	// Recompute the balance cache from the ledger (absolute, under the lock) so it
	// stays consistent with the billing service's writes, which take the same lock.
	var balance int64
	if err := tx.GetContext(ctx, &balance, `
		SELECT COALESCE(SUM(amount_micros), 0)::BIGINT FROM billing.credit_ledger WHERE organization_id = $1::uuid`, orgID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing.credit_balances (organization_id, balance_micros)
		VALUES ($1::uuid, $2)
		ON CONFLICT (organization_id) DO UPDATE SET balance_micros = EXCLUDED.balance_micros, updated_at = NOW()`,
		orgID, balance); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// encodeMetadata serialises a settle unit's metadata for the JSONB column. A
// nil or unencodable map becomes "{}" so a debit is never lost to metadata.
// OccurredAtMetadataKey is the ledger metadata key holding when the charged
// usage happened (RFC 3339, UTC). A replayed settle unit carries its original
// time, so it is the usage time even when the debit lands late. The signup
// credit expiry counts a debit as signup spend by this time when present
// (internal/billingcredit).
const OccurredAtMetadataKey = "occurred_at"

// withOccurredAt returns a copy of m with the usage time added. m itself is
// left unchanged (the outbox stores it as given).
func withOccurredAt(m map[string]any, at time.Time) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out[OccurredAtMetadataKey] = at.UTC().Format(time.RFC3339Nano)
	return out
}

func encodeMetadata(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// decodeMetadata is the inverse of encodeMetadata for outbox replay.
func decodeMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// MicrosFor converts a catalog USD cost into a marked-up wallet charge in
// micro-dollars, rounding half up. Zero or negative cost yields zero.
func MicrosFor(catalogUSD, markup float64) int64 {
	if catalogUSD <= 0 {
		return 0
	}
	if markup <= 0 {
		markup = 1
	}
	return int64(catalogUSD*markup*1_000_000 + 0.5)
}

// ListPriceMicros converts a catalog USD cost into micro-dollars at list
// price, with no markup: the estimate of what a customer's own provider key
// (BYOK) was billed. Markup is Everstack's rate card and the customer's
// provider does not charge it (docs/design/spend-limits-v2.md 4.1).
func ListPriceMicros(catalogUSD float64) int64 {
	return MicrosFor(catalogUSD, 1)
}
