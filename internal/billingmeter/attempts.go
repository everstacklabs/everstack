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

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Billed inference attempts (billing.inference_attempts). A gateway request
// can make several provider attempts; every one becomes a settle unit and an
// attempt record, so the customer can see what each attempt cost and why.
//
// Charging rule (owner decision 2026-10-01): every platform-key attempt that
// generated tokens is charged, including parallel race losers and attempts
// that failed before a fallback took over, because racing and fallback are
// features the customer turned on. Two kinds of attempt are recorded but
// never charged: a retry the platform made for its own reasons
// (RolePlatformRetry) and any attempt on the customer's own key (BYOK).

// Attempt roles.
const (
	// RoleServed is an attempt whose result was returned to the caller.
	RoleServed = "served"
	// RoleRaceLoser is a leg of a customer-enabled parallel race that did not
	// win.
	RoleRaceLoser = "race_loser"
	// RoleFailedFallback is an attempt of a customer-enabled fallback chain
	// that failed, after which the next target was tried.
	RoleFailedFallback = "failed_fallback"
	// RoleRetry is a repeat of the same target the customer's routing asked
	// for.
	RoleRetry = "retry"
	// RolePlatformRetry is a repeat the platform made for its own reasons
	// (another platform key after a key-level failure). Never charged.
	RolePlatformRetry = "platform_retry"
	// RoleInterrupted is an attempt that broke off (a stream that failed
	// part way) with nothing configured to take over. Charged for the
	// tokens it generated.
	RoleInterrupted = "interrupted"
)

// RoleCharged reports whether an attempt in role is chargeable at all. The
// provenance (platform only) and the token count still apply.
func RoleCharged(role string) bool {
	switch role {
	case RoleServed, RoleRaceLoser, RoleFailedFallback, RoleRetry, RoleInterrupted:
		return true
	default:
		return false
	}
}

// ValidRole reports whether role is one of the recorded roles.
func ValidRole(role string) bool {
	return RoleCharged(role) || role == RolePlatformRetry
}

// AttemptRecord is the display record of one provider attempt, captured by
// value at settle time. It travels with its SettleUnit through the settle
// queue and the outbox.
type AttemptRecord struct {
	RequestID    string `json:"request_id"`
	Role         string `json:"role"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Provenance   string `json:"provenance"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Estimated    bool   `json:"estimated"`
	Internal     bool   `json:"internal"`
	// APIKeyID is the gateway API key (api_keys.id) the request
	// authenticated with; empty when it used none (session, agent trigger,
	// internal call). Not the provider key, which is MetaKeyID.
	APIKeyID string `json:"api_key_id,omitempty"`
	// EstCostMicros is a BYOK attempt's cost at catalog list price, with no
	// markup (docs/design/spend-limits-v2.md 4.1). Nil on platform attempts,
	// whose cost is the charged amount, so the two are never summed twice.
	// Zero when the model is unpriced or the attempt generated nothing.
	EstCostMicros *int64 `json:"est_cost_micros,omitempty"`
	// Unpriced marks a BYOK attempt that generated tokens on a model the
	// catalog cannot price: its estimate is 0 and the spend is not counted.
	Unpriced bool `json:"unpriced,omitempty"`
}

// chargeable reports whether u may debit the wallet: a positive amount on a
// platform attempt in a charged role. A unit without an attempt record is a
// legacy caller and keeps its old meaning (Micros decides).
func (u SettleUnit) chargeable() bool {
	if u.Micros <= 0 {
		return false
	}
	if u.Attempt == nil {
		return true
	}
	return u.Attempt.Provenance == ProvenancePlatform && RoleCharged(u.Attempt.Role)
}

func encodeAttempt(a *AttemptRecord) []byte {
	if a == nil {
		return nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil
	}
	return b
}

func decodeAttempt(raw []byte) *AttemptRecord {
	if len(raw) == 0 {
		return nil
	}
	var a AttemptRecord
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil
	}
	return &a
}

// insertAttempt writes u's attempt record. charged attempts must pass the
// ledger entry they debited. ON CONFLICT (id) DO NOTHING makes every replay
// a no-op.
func insertAttempt(ctx context.Context, ex sqlx.ExecerContext, u SettleUnit, charged bool, ledgerEntryID string) error {
	a := u.Attempt
	role := strings.TrimSpace(a.Role)
	if !ValidRole(role) {
		return fmt.Errorf("billingmeter: invalid attempt role %q", a.Role)
	}
	provenance := a.Provenance
	if provenance != ProvenancePlatform && provenance != ProvenanceBYOK && provenance != ProvenanceTrial {
		return fmt.Errorf("billingmeter: invalid attempt provenance %q", a.Provenance)
	}
	if charged && provenance != ProvenancePlatform {
		return fmt.Errorf("billingmeter: a %s attempt is never charged", provenance)
	}
	amount := int64(0)
	var ledger any
	if charged {
		amount = u.Micros
		ledger = ledgerEntryID
	}
	modality := strings.TrimSpace(u.Modality)
	if modality == "" {
		modality = MetricChat
	}
	at := u.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	// api_key_id is NULL, not '', when the request used no API key.
	var apiKeyID any
	if id := strings.TrimSpace(a.APIKeyID); id != "" {
		apiKeyID = id
	}
	var estCost any
	if a.EstCostMicros != nil {
		estCost = maxZero(*a.EstCostMicros)
	}
	_, err := ex.ExecContext(ctx, `
		INSERT INTO billing.inference_attempts
			(id, organization_id, request_id, role, modality, provider, model, provenance,
			 input_tokens, output_tokens, estimated, charged, amount_micros, ledger_entry_id, internal, occurred_at,
			 api_key_id, est_cost_micros, unpriced)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::uuid, $15, $16, $17, $18, $19)
		ON CONFLICT (id) DO NOTHING`,
		strings.TrimSpace(u.ID), strings.TrimSpace(u.OrgID), strings.TrimSpace(a.RequestID), role, modality,
		a.Provider, a.Model, provenance, maxZero(a.InputTokens), maxZero(a.OutputTokens), a.Estimated,
		charged, amount, ledger, a.Internal, at,
		apiKeyID, estCost, a.Unpriced)
	if err != nil {
		return fmt.Errorf("record inference attempt: %w", err)
	}
	return nil
}

func maxZero(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

// recordAttemptOnly writes the record of an attempt that debits nothing (a
// BYOK attempt, a platform retry, a failed attempt with no tokens). One
// statement, no wallet lock.
func recordAttemptOnly(ctx context.Context, db *sqlx.DB, u SettleUnit) error {
	if u.Attempt == nil {
		return nil
	}
	return insertAttempt(ctx, db, u, false, "")
}

// linkedLedgerEntry returns the ledger entry id this unit debited, inside the
// debit transaction. The lookup is scoped to the unit's organization: a unit
// id is globally unique, so a replay that names another organization finds
// nothing and records nothing rather than borrowing that organization's
// entry.
func linkedLedgerEntry(ctx context.Context, tx *sqlx.Tx, u SettleUnit) (string, bool, error) {
	var id string
	err := tx.GetContext(ctx, &id, `
		SELECT id::text FROM billing.credit_ledger
		WHERE idempotency_key = $1 AND organization_id = $2::uuid`, u.IdempotencyKey(), strings.TrimSpace(u.OrgID))
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// recordChargedAttempt writes a charged unit's attempt record in the debit
// transaction, linked to its ledger entry.
func recordChargedAttempt(ctx context.Context, tx *sqlx.Tx, u SettleUnit) error {
	if u.Attempt == nil {
		return nil
	}
	ledgerID, ok, err := linkedLedgerEntry(ctx, tx, u)
	if err != nil {
		return err
	}
	if !ok {
		logger.WithFields("org", u.OrgID, "settle_unit", u.ID).
			Error("billingmeter: settle unit key belongs to another organization; attempt not recorded")
		return nil
	}
	return insertAttempt(ctx, tx, u, true, ledgerID)
}
