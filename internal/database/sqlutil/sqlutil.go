package sqlutil

import (
	"strings"

	"github.com/jmoiron/sqlx"
)

// BindNamed expands :named params and rebinds placeholders for the given dialect.
// dialect: "postgres" -> $1.., "clickhouse" -> ?
func BindNamed(dialect string, q string, args map[string]any) (string, []any, error) {
	named, vals, err := sqlx.Named(q, args)
	if err != nil {
		return "", nil, err
	}
	switch strings.ToLower(dialect) {
	case "postgres":
		return sqlx.Rebind(sqlx.DOLLAR, named), vals, nil
	case "clickhouse":
		return sqlx.Rebind(sqlx.QUESTION, named), vals, nil
	default:
		// leave as-is
		return named, vals, nil
	}
}

// ExpandMacros replaces simple SQL macros with dialect-specific expressions.
// Keep this minimal and explicit to avoid unsafe template expansion.
func ExpandMacros(dialect, sql string) string {
	d := strings.ToLower(dialect)
	out := sql
	switch d {
	case "postgres":
		out = strings.ReplaceAll(out, "{{period_hour_from_created_at}}", "DATE_TRUNC('hour', TO_TIMESTAMP(created_at))")
		out = strings.ReplaceAll(out, "{{json_provider}}", "CAST(payload->>'provider' AS TEXT)")
		out = strings.ReplaceAll(out, "{{json_model}}", "CAST(payload->>'model' AS TEXT)")
	case "clickhouse":
		out = strings.ReplaceAll(out, "{{period_hour_from_created_at}}", "toStartOfHour(toDateTime(created_at))")
		out = strings.ReplaceAll(out, "{{json_provider}}", "JSONExtractString(payload, 'provider')")
		out = strings.ReplaceAll(out, "{{json_model}}", "JSONExtractString(payload, 'model')")
	default:
		// no-op for unknown dialects
	}
	return out
}
