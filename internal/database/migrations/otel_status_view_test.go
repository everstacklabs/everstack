package migrations

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// otel_traces stores a span status as 'Ok' / 'Error' / 'Unset' when the
// OpenTelemetry collector exporter writes it, and as 'STATUS_CODE_OK' /
// 'STATUS_CODE_ERROR' / 'STATUS_CODE_UNSET' when the Everstack OTLP HTTP
// handler does. ClickHouse '=' is case-sensitive, so any of these literals in
// a view definition scores exactly zero forever.
var unmatchableStatusLiterals = []string{
	"StatusCode = 'OK'",
	"StatusCode = 'ERROR'",
	"StatusCode = 'UNSET'",
	"StatusCode = 'Ok'",
	"StatusCode = 'Error'",
	"StatusCode = 'Unset'",
	"StatusCode = 'STATUS_CODE_OK'",
	"StatusCode = 'STATUS_CODE_ERROR'",
	"StatusCode = 'STATUS_CODE_UNSET'",
}

// otelStatusGuardedViews are the read-time views over otel_traces whose
// definitions compare a span status. Materialized views are deliberately out of
// scope: they aggregate at INSERT time, so repairing one is a backfill decision
// rather than a redefinition.
var otelStatusGuardedViews = []string{
	"trace_summary",
	"trace_errors",
	"llm_operation_metrics",
}

var (
	migrationDirRegex = regexp.MustCompile(`^(?P<name>.+)_(?P<ts>\d{14})$`)
	lineCommentRegex  = regexp.MustCompile(`(?m)--.*$`)
)

// effectiveViewDefinition returns the LAST definition of view across every
// ClickHouse migration in version order -- i.e. the definition a freshly
// migrated database actually ends up with. Migrations are append-only, so
// checking one file is not enough: a later migration is exactly how a view like
// this gets repaired, and also exactly how it could get re-broken.
func effectiveViewDefinition(t *testing.T, view string) (statement string, migration string) {
	t.Helper()

	root := filepath.Join("sql", "clickhouse")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}

	type versioned struct {
		version string
		dir     string
	}
	var dirs []versioned
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := migrationDirRegex.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		dirs = append(dirs, versioned{version: m[migrationDirRegex.SubexpIndex("ts")], dir: e.Name()})
	}
	// The version is a fixed-width timestamp, so lexical order is time order.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].version < dirs[j].version })

	// "CREATE VIEW x", "CREATE VIEW IF NOT EXISTS x", "CREATE OR REPLACE VIEW x".
	create := regexp.MustCompile(`(?i)CREATE\s+(OR\s+REPLACE\s+)?VIEW\s+(IF\s+NOT\s+EXISTS\s+)?` + regexp.QuoteMeta(view) + `\s`)

	for _, d := range dirs {
		raw, readErr := os.ReadFile(filepath.Join(root, d.dir, "up.sql"))
		if readErr != nil {
			continue
		}
		body := lineCommentRegex.ReplaceAllString(string(raw), "")
		for _, stmt := range strings.Split(body, ";") {
			if create.MatchString(stmt) {
				statement, migration = stmt, d.dir
			}
		}
	}
	if statement == "" {
		t.Fatalf("no CREATE VIEW statement found for %q in any ClickHouse migration", view)
	}
	return statement, migration
}

// Before this guard existed, trace_summary.ErrorCount was
// countIf(StatusCode = 'ERROR') and therefore exactly 0 for every trace ever
// recorded. TraceStatsHandler reads that column as both error_count and
// error_rate, so both were structurally zero. Verified read-only against
// production: trace_summary held 9192 rows summing to 0 errors, while the
// case-insensitive membership predicate scores 32 over the same spans.
func TestClickHouseViewsCompareOtelStatusMatchably(t *testing.T) {
	for _, view := range otelStatusGuardedViews {
		t.Run(view, func(t *testing.T) {
			stmt, migration := effectiveViewDefinition(t, view)

			for _, literal := range unmatchableStatusLiterals {
				if strings.Contains(stmt, literal) {
					t.Errorf("view %s (last defined by migration %s) compares a span status with %s, which matches no value otel_traces stores; use the membership predicate from internal/telemetry/otelstatus instead", view, migration, literal)
				}
			}

			if !strings.Contains(stmt, "lower(StatusCode) IN (") {
				t.Errorf("view %s (last defined by migration %s) must select errors with a case-insensitive membership test over both stored spellings; got:\n%s", view, migration, strings.TrimSpace(stmt))
			}
		})
	}
}
