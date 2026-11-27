package billingmeter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Settle outbox (docs/design/prepaid-credits-v2.md section 5, "Settle
// durability"). When the platform DB cannot take a debit after the in-process
// retries, the settle unit is written to everstack.billing_settle_outbox in the
// gateway's own primary DB, which is on the request path already, and a loop
// replays it with the same idempotency key until it lands. The only accepted
// loss is the crash window: settle units held in memory when the process dies.

const (
	outboxReplayInterval = 30 * time.Second
	outboxReplayBatch    = 200
	// outboxBackoffBase is the delay before the first replay of a row; each
	// failed replay doubles it up to outboxBackoffMax.
	outboxBackoffBase = 30 * time.Second
	outboxBackoffMax  = 15 * time.Minute
)

// outboxRow mirrors one everstack.billing_settle_outbox row.
type outboxRow struct {
	ID             int64     `db:"id"`
	IdempotencyKey string    `db:"idempotency_key"`
	SettleUnitID   string    `db:"settle_unit_id"`
	OrganizationID string    `db:"organization_id"`
	Modality       string    `db:"modality"`
	AmountMicros   int64     `db:"amount_micros"`
	OccurredAt     time.Time `db:"occurred_at"`
	Metadata       []byte    `db:"metadata"`
	Attempt        []byte    `db:"attempt"`
	Attempts       int       `db:"attempts"`
}

func (r outboxRow) unit() SettleUnit {
	return SettleUnit{
		ID:       r.SettleUnitID,
		OrgID:    r.OrganizationID,
		Modality: r.Modality,
		Micros:   r.AmountMicros,
		At:       r.OccurredAt,
		Metadata: decodeMetadata(r.Metadata),
		Attempt:  decodeAttempt(r.Attempt),
	}
}

// OutboxInsert durably parks a settle unit whose debit failed. Idempotent on
// the settle unit's key, so a unit that is both retried in-process and parked
// can never produce two outbox rows. A nil gateway DB is an error: the caller
// logs the unit as lost.
func OutboxInsert(ctx context.Context, gatewayDB *sqlx.DB, u SettleUnit, lastErr error) error {
	if gatewayDB == nil {
		return fmt.Errorf("billingmeter: nil gateway database for settle outbox")
	}
	if strings.TrimSpace(u.ID) == "" || strings.TrimSpace(u.OrgID) == "" {
		return fmt.Errorf("billingmeter: settle unit id and organization are required")
	}
	errText := ""
	if lastErr != nil {
		errText = lastErr.Error()
	}
	at := u.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	// A record-only unit parks with a zero amount; the constraint admits it
	// only alongside its attempt record.
	amount := u.Micros
	if amount < 0 {
		amount = 0
	}
	var attempt any
	if b := encodeAttempt(u.Attempt); b != nil {
		attempt = string(b)
	}
	_, err := gatewayDB.ExecContext(ctx, `
		INSERT INTO everstack.billing_settle_outbox
			(idempotency_key, settle_unit_id, organization_id, modality, amount_micros, occurred_at, metadata, attempt, attempts, last_error, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, 0, $9, NOW() + $10::interval)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		u.IdempotencyKey(), strings.TrimSpace(u.ID), strings.TrimSpace(u.OrgID), u.Modality, amount, at,
		encodeMetadata(u.Metadata), attempt, errText, outboxBackoffBase.String())
	if err != nil {
		return fmt.Errorf("billingmeter: outbox insert: %w", err)
	}
	return nil
}

// OutboxDepth counts parked settle units. Exported for the metrics loop and
// for operators.
func OutboxDepth(ctx context.Context, gatewayDB *sqlx.DB) (int64, error) {
	if gatewayDB == nil {
		return 0, nil
	}
	var n int64
	err := gatewayDB.GetContext(ctx, &n, `SELECT COUNT(*) FROM everstack.billing_settle_outbox`)
	return n, err
}

// outboxReplayOnce replays one batch of due rows: each is debited with the
// same idempotency key it was parked with and deleted on success. Rows are
// claimed FOR UPDATE SKIP LOCKED so several gateway replicas can replay
// concurrently without contending, and because DebitUnit is idempotent a row
// that is debited but whose delete is lost is simply replayed again later.
// Returns the number of rows that landed.
func outboxReplayOnce(ctx context.Context, gatewayDB, billingDB *sqlx.DB, debit debitFunc) (landed int, err error) {
	if gatewayDB == nil || billingDB == nil {
		return 0, nil
	}
	tx, err := gatewayDB.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var rows []outboxRow
	if err := tx.SelectContext(ctx, &rows, `
		SELECT id, idempotency_key, settle_unit_id, organization_id, modality, amount_micros, occurred_at, metadata, attempt, attempts
		FROM everstack.billing_settle_outbox
		WHERE next_attempt_at <= NOW()
		ORDER BY next_attempt_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, outboxReplayBatch); err != nil {
		return 0, err
	}
	for _, row := range rows {
		u := row.unit()
		attemptCtx, cancel := context.WithTimeout(ctx, settleAttemptTimeout)
		debitErr := debit(attemptCtx, billingDB, u)
		cancel()
		if debitErr == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM everstack.billing_settle_outbox WHERE id = $1`, row.ID); err != nil {
				return landed, err
			}
			landed++
			outboxReplayed.Inc()
			continue
		}
		backoff := outboxBackoffBase << uint(minInt(row.Attempts+1, 6))
		if backoff > outboxBackoffMax {
			backoff = outboxBackoffMax
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE everstack.billing_settle_outbox
			SET attempts = attempts + 1, last_error = $2, next_attempt_at = NOW() + $3::interval
			WHERE id = $1`, row.ID, debitErr.Error(), backoff.String()); err != nil {
			return landed, err
		}
		logger.WithFields("org", u.OrgID, "settle_unit", u.ID, "micros", u.Micros, "attempts", row.Attempts+1, "error", debitErr.Error()).
			Warn("billingmeter: outbox replay failed; will retry")
	}
	if err := tx.Commit(); err != nil {
		return landed, err
	}
	return landed, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// runOutboxReplay ticks until ctx is done, replaying due rows and publishing
// the outbox depth. One replay error never stops the loop.
func runOutboxReplay(ctx context.Context, gatewayDB, billingDB *sqlx.DB, debit debitFunc, interval time.Duration) {
	if interval <= 0 {
		interval = outboxReplayInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if n, err := outboxReplayOnce(ctx, gatewayDB, billingDB, debit); err != nil {
			logger.WithFields("error", err.Error()).Warn("billingmeter: outbox replay pass failed")
		} else if n > 0 {
			logger.WithFields("landed", n).Info("billingmeter: outbox replay landed parked debits")
		}
		if depth, err := OutboxDepth(ctx, gatewayDB); err == nil {
			outboxDepth.Set(float64(depth))
		}
	}
}
