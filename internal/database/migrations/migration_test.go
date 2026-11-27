package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestSplitStatementsRespectsQuotedSemicolons(t *testing.T) {
	sql := `
COMMENT ON COLUMN sandbox_instances.billing_started_at IS
    'Start of the currently open billing window; NULL when inactive.';
SELECT 'it''s still one; statement';
SELECT "identifier;with;semicolons";
DO $body$
BEGIN
    PERFORM 'dollar-quoted; body';
END
$body$;
SELECT 1;
`

	parts := splitStatements(sql)
	if len(parts) != 5 {
		t.Fatalf("splitStatements() returned %d statements, want 5:\n%q", len(parts), parts)
	}

	if !strings.Contains(parts[0], "billing window; NULL") {
		t.Fatalf("first statement lost its quoted semicolon: %q", parts[0])
	}
	if !strings.Contains(parts[1], "it''s still one; statement") {
		t.Fatalf("second statement lost its escaped quote or semicolon: %q", parts[1])
	}
	if !strings.Contains(parts[2], `"identifier;with;semicolons"`) {
		t.Fatalf("third statement lost its quoted identifier: %q", parts[2])
	}
}

func TestStripSQLCommentsPreservesQuotedCommentMarkers(t *testing.T) {
	sql := `
SELECT '-- literal', '/* also literal */'; -- remove this comment
SELECT "identifier--literal"; /* remove this block */
SELECT 1;
`

	stripped := stripSQLComments(sql)
	for _, literal := range []string{
		"'-- literal'",
		"'/* also literal */'",
		`"identifier--literal"`,
	} {
		if !strings.Contains(stripped, literal) {
			t.Fatalf("stripSQLComments() removed quoted content %q:\n%s", literal, stripped)
		}
	}
	for _, comment := range []string{"remove this comment", "remove this block"} {
		if strings.Contains(stripped, comment) {
			t.Fatalf("stripSQLComments() retained comment %q:\n%s", comment, stripped)
		}
	}

	if parts := splitStatements(stripped); len(parts) != 3 {
		t.Fatalf("stripped SQL split into %d statements, want 3:\n%q", len(parts), parts)
	}
}

func TestLegacyModelMetricsBackfillKeepsManagedCloudBoundary(t *testing.T) {
	t.Parallel()

	sqlBytes, err := os.ReadFile(
		"sql/clickhouse/model_metrics_legacy_environment_backfill_20260730110000/up.sql",
	)
	if err != nil {
		t.Fatalf("read legacy model metrics backfill: %v", err)
	}
	sql := string(sqlBytes)

	for _, required := range []string{
		"ResourceAttributes['deployment.environment'] = ''",
		"ResourceAttributes['tenant.type'] = 'cloud'",
		"ResourceAttributes['instance.owner'] = 'everstack'",
		"SpanAttributes['everstack.traffic.kind'] != 'internal'",
		"SpanAttributes['tenant.id'] != ''",
		"Timestamp >= now() - INTERVAL 30 DAY",
		"stamped_canonical != ''",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("legacy backfill lost required boundary %q", required)
		}
	}

	if strings.Contains(sql, "ResourceAttributes['deployment.environment'] != 'production'") {
		t.Fatal("legacy backfill accepts explicit non-production environments")
	}

	if statements := splitStatements(stripSQLComments(sql)); len(statements) != 1 {
		t.Fatalf("legacy backfill split into %d statements, want 1", len(statements))
	}
}

func TestModelMetricsTokenAliasesStayManagedAndBackfillOnlyTokenDeltas(t *testing.T) {
	t.Parallel()

	sqlBytes, err := os.ReadFile(
		"sql/clickhouse/model_metrics_token_aliases_20260730230000/up.sql",
	)
	if err != nil {
		t.Fatalf("read model metrics token alias migration: %v", err)
	}
	sql := string(sqlBytes)

	for _, required := range []string{
		"SpanAttributes['llm.tokens.input']",
		"SpanAttributes['gen_ai.usage.input_tokens']",
		"SpanAttributes['gen_ai.usage.prompt_tokens']",
		"SpanAttributes['response.prompt_tokens']",
		"SpanAttributes['llm.tokens.output']",
		"SpanAttributes['gen_ai.usage.completion_tokens']",
		"SpanAttributes['response.completion_tokens']",
		"SpanAttributes['llm.tokens.reasoning']",
		"ResourceAttributes['tenant.type'] = 'cloud'",
		"ResourceAttributes['instance.owner'] = 'everstack'",
		"SpanAttributes['tenant.id'] != ''",
		"Timestamp >= now() - INTERVAL 30 DAY",
		"toUInt64(0) AS request_count",
		"raw.input_tokens - coalesce(existing.input_tokens, 0)",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("token alias migration lost required clause %q", required)
		}
	}
	if strings.Contains(
		sql,
		"ResourceAttributes['deployment.environment'] != 'production'",
	) {
		t.Fatal("token alias migration accepts explicit non-production environments")
	}
	if statements := splitStatements(stripSQLComments(sql)); len(statements) != 3 {
		t.Fatalf("token alias migration split into %d statements, want 3", len(statements))
	}
}

// TestNoMaterializedViewComparesTheEnumStatusSpellingAlone guards the whole
// class of bug fixed by model_metrics_error_status_20260826075905 and
// metrics_error_status_20260901223737: otel_traces stores the span status
// under the collector's short spelling ("Ok" / "Error" / "Unset") as well as
// the enum name written by internal/api/http/otlp/handler.go, and ClickHouse
// "=" is case-sensitive. A materialized view that compares against the enum
// name alone counts zero errors forever.
//
// The check is scoped to the CURRENT definition of each view. A migration is
// free to contain the old spelling in a comment or in a down.sql that restores
// a superseded definition; what must never happen is a live CREATE
// MATERIALIZED VIEW whose only error test is the enum literal.
func TestNoMaterializedViewComparesTheEnumStatusSpellingAlone(t *testing.T) {
	t.Parallel()

	// view name -> migration whose up.sql holds the definition in force.
	currentDefinitions := map[string]string{
		"mv_model_metrics_hourly": "sql/clickhouse/model_metrics_error_status_20260826075905/up.sql",
		"mv_trace_metrics":        "sql/clickhouse/metrics_error_status_20260901223737/up.sql",
		"mv_provider_key_metrics": "sql/clickhouse/metrics_error_status_20260901223737/up.sql",
	}

	for view, path := range currentDefinitions {
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read current definition of %s: %v", view, err)
		}
		sql := stripSQLComments(string(sqlBytes))

		if !strings.Contains(sql, "CREATE MATERIALIZED VIEW") ||
			!strings.Contains(sql, view) {
			t.Fatalf("%s does not redefine %s", path, view)
		}
		if !strings.Contains(sql, "lower(StatusCode) IN ('error', 'status_code_error')") {
			t.Fatalf(
				"%s must compare the span status case-insensitively against both "+
					"spellings (see internal/telemetry/otelstatus)", view,
			)
		}
		if strings.Contains(sql, "StatusCode = 'STATUS_CODE_ERROR'") {
			t.Fatalf(
				"%s still compares against the enum spelling alone, which no "+
					"collector-written span carries", view,
			)
		}
	}
}

// TestMetricsErrorStatusBackfillsOnlyErrorDeltas pins the shape of the
// correcting backfill: it must stay inside the raw retention window, add
// nothing but errors, and be safe to run twice.
func TestMetricsErrorStatusBackfillsOnlyErrorDeltas(t *testing.T) {
	t.Parallel()

	sqlBytes, err := os.ReadFile(
		"sql/clickhouse/metrics_error_status_20260901223737/up.sql",
	)
	if err != nil {
		t.Fatalf("read metrics error status migration: %v", err)
	}
	sql := string(sqlBytes)

	for _, required := range []string{
		// Rollup rows whose source spans have aged out cannot be corrected,
		// so the delta is scoped to the spans that still exist.
		"Timestamp >= now() - INTERVAL 30 DAY",
		"INSERT INTO trace_metrics_hourly",
		"INSERT INTO provider_key_metrics_hourly",
		// Only the missing errors are inserted; every other measure is zero.
		"toUInt64(0) AS request_count",
		"raw.error_count - coalesce(existing.error_count, 0)",
		// Re-running the migration must insert nothing.
		"WHERE raw.error_count > coalesce(existing.error_count, 0)",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("metrics error status migration lost required clause %q", required)
		}
	}

	// 2 DROP VIEW + 2 CREATE MATERIALIZED VIEW + 2 INSERT.
	if statements := splitStatements(stripSQLComments(sql)); len(statements) != 6 {
		t.Fatalf(
			"metrics error status migration split into %d statements, want 6",
			len(statements),
		)
	}
}
