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
