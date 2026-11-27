package traces

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/everstacklabs/everstack/internal/database"
)

// The p95 / TTFT / trace-name reads filtered raw spans on
// ResourceAttributes['tenant.id'] alone. In prod that attribute is blank on
// most tenant spans (the populated one is SpanAttributes['tenant.id']), so
// p95 and TTFT read 0 for cloud tenants. Every raw otel_traces read in these
// handlers must use tenantBridgeFilter.

type recordingConn struct {
	driver.Conn
	queries []string
	args    [][]any
}

func (c *recordingConn) Query(_ context.Context, q string, args ...any) (driver.Rows, error) {
	c.queries = append(c.queries, q)
	c.args = append(c.args, args)
	return emptyRows{}, nil
}

type emptyRows struct{ driver.Rows }

func (emptyRows) Next() bool   { return false }
func (emptyRows) Close() error { return nil }
func (emptyRows) Err() error   { return nil }

const bridgeClause = "(SpanAttributes['tenant.id'] = ? OR (SpanAttributes['tenant.id'] = '' AND ResourceAttributes['tenant.id'] = ?))"

func assertRawReadsUseBridge(t *testing.T, c *recordingConn, tenant string) {
	t.Helper()
	raw := 0
	for i, q := range c.queries {
		if !strings.Contains(q, "FROM otel_traces") {
			continue
		}
		raw++
		if !strings.Contains(q, bridgeClause) {
			t.Fatalf("raw span query %d is not tenant-bridged:\n%s", i, q)
		}
		if n := strings.Count(q, "?"); n != len(c.args[i]) {
			t.Fatalf("raw span query %d has %d placeholders but %d args", i, n, len(c.args[i]))
		}
		seen := 0
		for _, a := range c.args[i] {
			if a == tenant {
				seen++
			}
		}
		if seen != 2 {
			t.Fatalf("raw span query %d binds the tenant %d times, want 2 (span + resource fallback)", i, seen)
		}
	}
	if raw == 0 {
		t.Fatal("no raw otel_traces query ran; the test is not exercising the fix")
	}
}

func tenantCtx(tenant string) context.Context {
	return database.WithTenantSchema(context.Background(), tenant)
}

func TestMetricsDashboard_PercentilesUseSpanTenant(t *testing.T) {
	c := &recordingConn{}
	h := NewMetricsDashboardHandler(c)
	end := time.Now()
	q := &MetricsDashboardQuery{StartTime: end.Add(-time.Hour), EndTime: end, Models: []string{"m"}, Environments: []string{"production"}}
	if _, err := h.queryWindow(tenantCtx("inst-1"), q, q.StartTime, q.EndTime); err != nil {
		t.Fatal(err)
	}
	assertRawReadsUseBridge(t, c, "inst-1")
}

func TestMetricsTimeSeries_TTFTUsesSpanTenant(t *testing.T) {
	for _, metric := range []string{"ttft_p50", "ttft_p95"} {
		c := &recordingConn{}
		h := NewMetricsTimeSeriesHandler(c)
		end := time.Now()
		q := &MetricsTimeSeriesQuery{StartTime: end.Add(-time.Hour), EndTime: end, Metric: metric, Granularity: "hour", Providers: []string{"openai"}}
		if _, err := h.queryWindow(tenantCtx("inst-1"), q, q.StartTime, q.EndTime); err != nil {
			t.Fatal(err)
		}
		assertRawReadsUseBridge(t, c, "inst-1")
	}
}

func TestMetricsBreakdown_TraceNameUsesSpanTenant(t *testing.T) {
	c := &recordingConn{}
	h := NewMetricsBreakdownHandler(c)
	end := time.Now()
	q := &MetricsBreakdownQuery{StartTime: end.Add(-time.Hour), EndTime: end, Metric: "requests", GroupBy: "trace_name", Limit: 5, Environments: []string{"production"}}
	if _, err := h.queryWindow(tenantCtx("inst-1"), q, q.StartTime, q.EndTime, "inst-1", []string{"chat"}); err != nil {
		t.Fatal(err)
	}
	assertRawReadsUseBridge(t, c, "inst-1")
}
