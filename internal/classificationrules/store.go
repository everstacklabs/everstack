// Package classificationrules persists tenant-scoped rules that extend the
// built-in trace-kind classifier: a SpanName LIKE pattern -> a kind label. At
// read time the traces list folds these into the trace_kinds derivation so the
// Type column shows kinds the tenant cares about (e.g. "retriever.% ->
// retrieval") without editing the hardcoded classifier.
package classificationrules

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"

	pkgdb "github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// kindPattern bounds a kind label. It is inlined into the SQL as the array
// element value, so quotes and other escape chars are rejected.
var kindPattern = regexp.MustCompile(`^[a-zA-Z0-9_ \-]{1,40}$`)

// likePattern bounds a SpanName LIKE pattern. It is bound as a query parameter
// (not inlined), so this is a sanity bound, not an injection guard: allow
// attribute/span-name chars plus the LIKE wildcards % and _.
var likePattern = regexp.MustCompile(`^[a-zA-Z0-9_.:/%\-]{1,128}$`)

// ValidateKind reports whether kind is a safe, inlineable label.
func ValidateKind(kind string) error {
	if !kindPattern.MatchString(kind) {
		return fmt.Errorf("kind must match %s", kindPattern.String())
	}
	return nil
}

// ValidatePattern reports whether pattern is a reasonable SpanName LIKE pattern.
func ValidatePattern(pattern string) error {
	if !likePattern.MatchString(pattern) {
		return fmt.Errorf("pattern must match %s", likePattern.String())
	}
	return nil
}

// Rule is one classification rule: spans whose name is LIKE Pattern get Kind.
type Rule struct {
	Pattern   string
	Kind      string
	UpdatedAt time.Time
}

// Store persists classification rules in ClickHouse, append-only read-latest.
type Store struct {
	db *sql.DB
}

// NewStore creates a classification-rule store over the given ClickHouse handle.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func tenantIDFromContext(ctx context.Context) string {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid
	}
	return pkgdb.TenantSchemaFromContext(ctx)
}

// Add inserts a rule.
func (r *Store) Add(ctx context.Context, pattern, kind string) error {
	if err := ValidatePattern(pattern); err != nil {
		return err
	}
	if err := ValidateKind(kind); err != nil {
		return err
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_classification_rules (TenantId, Pattern, Kind, IsActive, UpdatedAt)
		VALUES (?, ?, ?, 1, ?)
	`, tenantID, pattern, kind, time.Now())
	if err != nil {
		logger.WithFields("pattern", pattern, "kind", kind, "error", err.Error()).Error("failed to write classification rule")
		return fmt.Errorf("failed to insert classification rule: %w", err)
	}
	return nil
}

// Delete soft-deletes a rule by inserting a tombstone.
func (r *Store) Delete(ctx context.Context, pattern, kind string) error {
	if err := ValidatePattern(pattern); err != nil {
		return err
	}
	if err := ValidateKind(kind); err != nil {
		return err
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_classification_rules (TenantId, Pattern, Kind, IsActive, UpdatedAt)
		VALUES (?, ?, ?, 0, ?)
	`, tenantID, pattern, kind, time.Now())
	if err != nil {
		logger.WithFields("pattern", pattern, "kind", kind, "error", err.Error()).Error("failed to delete classification rule")
		return fmt.Errorf("failed to delete classification rule: %w", err)
	}
	return nil
}

// List returns the tenant's active rules, latest row per (Pattern, Kind), with
// invalid rows filtered defensively (the pattern is bound but the kind is
// inlined, so a bad kind must never reach the query builder).
func (r *Store) List(ctx context.Context) ([]Rule, error) {
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return []Rule{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT Pattern, Kind, IsActive, LatestUpdatedAt
		FROM (
			SELECT
				Pattern, Kind,
				argMax(IsActive, UpdatedAt) AS IsActive,
				max(UpdatedAt) AS LatestUpdatedAt
			FROM otel_trace_classification_rules
			WHERE TenantId = ?
			GROUP BY Pattern, Kind
		)
		WHERE IsActive = 1
		ORDER BY Kind ASC, Pattern ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query classification rules: %w", err)
	}
	defer rows.Close()

	var out []Rule
	for rows.Next() {
		var (
			rule     Rule
			isActive uint8
		)
		if err := rows.Scan(&rule.Pattern, &rule.Kind, &isActive, &rule.UpdatedAt); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan classification rule")
			continue
		}
		if ValidatePattern(rule.Pattern) != nil || ValidateKind(rule.Kind) != nil {
			continue
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}
