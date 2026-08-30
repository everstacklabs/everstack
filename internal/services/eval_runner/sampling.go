package eval_runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// SamplingEvalRuleRecord is the Postgres row for sampling_eval_rules.
//
// A rule continuously samples production traces matching FilterPredicate
// at SampleRate and scores them with ScorerConfigIDs. Output rows land in
// otel_trace_scores via the existing score recorder, so they show up in
// the trace detail UI / dashboards / CI gate with zero extra plumbing.
//
// The polling runner is a follow-up — schema + CRUD + RunNow ship in this
// commit. Hand-run via RunSamplingEvalRuleNow until the scheduler lands.
type SamplingEvalRuleRecord struct {
	ID                   string         `db:"id" json:"id"`
	TenantID             string         `db:"tenant_id" json:"tenant_id"`
	Name                 string         `db:"name" json:"name"`
	Description          string         `db:"description" json:"description"`
	FilterPredicate      []byte         `db:"filter_predicate" json:"filter_predicate"`
	SampleRate           float64        `db:"sample_rate" json:"sample_rate"`
	ScorerConfigIDs      pq.StringArray `db:"scorer_config_ids" json:"scorer_config_ids"`
	LookbackSeconds      int            `db:"lookback_seconds" json:"lookback_seconds"`
	IntervalSeconds      int            `db:"interval_seconds" json:"interval_seconds"`
	Enabled              bool           `db:"enabled" json:"enabled"`
	LastRunAt            sql.NullTime   `db:"last_run_at" json:"last_run_at"`
	LastRunTraceCount    int            `db:"last_run_trace_count" json:"last_run_trace_count"`
	LastRunError         string         `db:"last_run_error" json:"last_run_error"`
	LastProcessedTraceAt sql.NullTime   `db:"last_processed_trace_at" json:"last_processed_trace_at"`
	CreatedAt            time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt            time.Time      `db:"updated_at" json:"updated_at"`
}

// CreateSamplingEvalRule inserts a new rule.
func CreateSamplingEvalRule(ctx context.Context, db *sqlx.DB, rec *SamplingEvalRuleRecord) error {
	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	if rec.SampleRate <= 0 {
		rec.SampleRate = 1.0
	}
	if rec.LookbackSeconds <= 0 {
		rec.LookbackSeconds = 300
	}
	if rec.IntervalSeconds < 0 {
		rec.IntervalSeconds = 60
	}
	var filter json.RawMessage = []byte("{}")
	if len(rec.FilterPredicate) > 0 {
		filter = rec.FilterPredicate
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO sampling_eval_rules (
			id, tenant_id, name, description,
			filter_predicate, sample_rate, scorer_config_ids,
			lookback_seconds, interval_seconds, enabled
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, rec.ID, rec.TenantID, rec.Name, rec.Description,
		filter, rec.SampleRate, rec.ScorerConfigIDs,
		rec.LookbackSeconds, rec.IntervalSeconds, rec.Enabled)
	return err
}

// GetSamplingEvalRule returns a rule by id, tenant-scoped.
func GetSamplingEvalRule(ctx context.Context, db *sqlx.DB, id, tenantID string) (*SamplingEvalRuleRecord, error) {
	var rec SamplingEvalRuleRecord
	err := db.GetContext(ctx, &rec, `
		SELECT * FROM sampling_eval_rules WHERE id = $1 AND tenant_id = $2
	`, id, tenantID)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// ListSamplingEvalRules returns rules for a tenant.
func ListSamplingEvalRules(ctx context.Context, db *sqlx.DB, tenantID string, enabledOnly bool, limit, offset int) ([]SamplingEvalRuleRecord, int, error) {
	if limit <= 0 {
		limit = 100
	}
	where := "tenant_id = $1"
	args := []interface{}{tenantID}
	if enabledOnly {
		where += " AND enabled = TRUE"
	}
	var rules []SamplingEvalRuleRecord
	err := db.SelectContext(ctx, &rules, fmt.Sprintf(`
		SELECT * FROM sampling_eval_rules
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, len(args)+1, len(args)+2), append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := db.GetContext(ctx, &total, fmt.Sprintf(`SELECT COUNT(*) FROM sampling_eval_rules WHERE %s`, where), args...); err != nil {
		return rules, 0, err
	}
	return rules, total, nil
}

// UpdateSamplingEvalRule applies a partial update.
func UpdateSamplingEvalRule(ctx context.Context, db *sqlx.DB, id, tenantID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	cols := make([]string, 0, len(updates))
	args := []interface{}{id, tenantID}
	i := 3
	for k, v := range updates {
		cols = append(cols, fmt.Sprintf("%s = $%d", k, i))
		args = append(args, v)
		i++
	}
	q := fmt.Sprintf(`UPDATE sampling_eval_rules SET %s WHERE id = $1 AND tenant_id = $2`,
		joinWith(cols, ", "))
	_, err := db.ExecContext(ctx, q, args...)
	return err
}

// DeleteSamplingEvalRule removes a rule.
func DeleteSamplingEvalRule(ctx context.Context, db *sqlx.DB, id, tenantID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sampling_eval_rules WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}

// MarkSamplingRuleRun records the outcome of a sampling-rule execution.
func MarkSamplingRuleRun(ctx context.Context, db *sqlx.DB, id, tenantID string, traceCount int, lastProcessedAt time.Time, runErr string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE sampling_eval_rules
		SET last_run_at = NOW(),
		    last_run_trace_count = $3,
		    last_run_error = $4,
		    last_processed_trace_at = $5
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID, traceCount, runErr, lastProcessedAt)
	return err
}

// joinWith is a tiny strings.Join shim to avoid importing strings just for
// this file when scheduler.go already pulls it in transitively.
func joinWith(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}
