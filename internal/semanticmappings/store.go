// Package semanticmappings persists tenant-scoped aliases from a tenant's own
// span-attribute names into our typed trace fields (model, provider, session,
// user, cost, tokens, input, output). At read time the traces list folds these
// extra keys into the coalesce so a non-standard SDK's attributes populate the
// built-in columns, without editing the hardcoded semconv key lists.
package semanticmappings

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

// Fields a mapping may target. Keep in sync with the coalesce builders in
// internal/query/handlers/traces/semconv.go and the frontend field picker.
var validFields = map[string]bool{
	"model":         true,
	"provider":      true,
	"session":       true,
	"user":          true,
	"cost":          true,
	"input":         true,
	"output":        true,
	"input_tokens":  true,
	"output_tokens": true,
	"total_tokens":  true,
}

// FieldList returns the allowed field names (for callers that need to enumerate).
func FieldList() []string {
	out := make([]string, 0, len(validFields))
	for f := range validFields {
		out = append(out, f)
	}
	return out
}

// attrKeyPattern bounds an attribute key to characters valid in an OTel
// attribute name. The key is inlined into the coalesce SQL, so anything that
// could escape a string literal is rejected.
var attrKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:/\-]{1,128}$`)

// ValidateField reports whether field is a known target field.
func ValidateField(field string) error {
	if !validFields[field] {
		return fmt.Errorf("unknown field %q", field)
	}
	return nil
}

// ValidateAttrKey reports whether attrKey is a safe attribute key.
func ValidateAttrKey(attrKey string) error {
	if !attrKeyPattern.MatchString(attrKey) {
		return fmt.Errorf("attribute key must match %s", attrKeyPattern.String())
	}
	return nil
}

// Mapping is one tenant alias: AttrKey populates Field.
type Mapping struct {
	Field     string
	AttrKey   string
	UpdatedAt time.Time
}

// Mappings is field -> extra attribute keys, ready to feed the coalesce builders.
type Mappings map[string][]string

// For returns the tenant's extra keys for a field, or nil.
func (m Mappings) For(field string) []string { return m[field] }

// Store persists semantic mappings in ClickHouse, append-only read-latest.
type Store struct {
	db *sql.DB
}

// NewStore creates a semantic-mapping store over the given ClickHouse handle.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func tenantIDFromContext(ctx context.Context) string {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid
	}
	return pkgdb.TenantSchemaFromContext(ctx)
}

// Add inserts a mapping (field <- attrKey).
func (r *Store) Add(ctx context.Context, field, attrKey string) error {
	if err := ValidateField(field); err != nil {
		return err
	}
	if err := ValidateAttrKey(attrKey); err != nil {
		return err
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_semantic_mappings (TenantId, Field, AttrKey, IsActive, UpdatedAt)
		VALUES (?, ?, ?, 1, ?)
	`, tenantID, field, attrKey, time.Now())
	if err != nil {
		logger.WithFields("field", field, "attr_key", attrKey, "error", err.Error()).Error("failed to write semantic mapping")
		return fmt.Errorf("failed to insert semantic mapping: %w", err)
	}
	return nil
}

// Delete soft-deletes a mapping by inserting a tombstone.
func (r *Store) Delete(ctx context.Context, field, attrKey string) error {
	if err := ValidateField(field); err != nil {
		return err
	}
	if err := ValidateAttrKey(attrKey); err != nil {
		return err
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_semantic_mappings (TenantId, Field, AttrKey, IsActive, UpdatedAt)
		VALUES (?, ?, ?, 0, ?)
	`, tenantID, field, attrKey, time.Now())
	if err != nil {
		logger.WithFields("field", field, "attr_key", attrKey, "error", err.Error()).Error("failed to delete semantic mapping")
		return fmt.Errorf("failed to delete semantic mapping: %w", err)
	}
	return nil
}

// List returns the tenant's active mappings, latest row per (Field, AttrKey).
func (r *Store) List(ctx context.Context) ([]Mapping, error) {
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return []Mapping{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT Field, AttrKey, IsActive, LatestUpdatedAt
		FROM (
			SELECT
				Field, AttrKey,
				argMax(IsActive, UpdatedAt) AS IsActive,
				max(UpdatedAt) AS LatestUpdatedAt
			FROM otel_trace_semantic_mappings
			WHERE TenantId = ?
			GROUP BY Field, AttrKey
		)
		WHERE IsActive = 1
		ORDER BY Field ASC, AttrKey ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query semantic mappings: %w", err)
	}
	defer rows.Close()

	var out []Mapping
	for rows.Next() {
		var (
			m        Mapping
			isActive uint8
		)
		if err := rows.Scan(&m.Field, &m.AttrKey, &isActive, &m.UpdatedAt); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan semantic mapping")
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AsMappings returns the tenant's active mappings grouped by field, with unsafe
// keys filtered (defensive; they should never have been stored).
func (r *Store) AsMappings(ctx context.Context) (Mappings, error) {
	list, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	m := Mappings{}
	for _, item := range list {
		if ValidateAttrKey(item.AttrKey) != nil || ValidateField(item.Field) != nil {
			continue
		}
		m[item.Field] = append(m[item.Field], item.AttrKey)
	}
	return m, nil
}
