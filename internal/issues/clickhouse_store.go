package issues

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

// CHStore aggregates error spans in `otel_traces` into issue groups.
type CHStore struct {
	conn clickhouse.Conn
}

func NewCHStore(conn clickhouse.Conn) *CHStore {
	return &CHStore{conn: conn}
}

// IssueAgg is one issue group as computed from the trace store (no triage
// state; that is overlaid from Postgres by the service layer).
type IssueAgg struct {
	Fingerprint    string
	Title          string
	Signature      string
	Category       string
	Count          int64
	FirstSeen      time.Time
	LastSeen       time.Time
	Provider       string
	Model          string
	SampleTraceIDs []string
	// Spark is a fixed-width occurrence histogram across the window (oldest to
	// newest) for the inline list sparkline.
	Spark []int32
}

// sparkBuckets is the number of columns in a list-row sparkline.
const sparkBuckets = 24

// sparkExpr builds a ClickHouse aggregate that produces a fixed-width
// occurrence histogram for the [from,to] window. Each row contributes a 1 to
// its time bucket; sumForEach sums those vectors element-wise across the group.
// The numeric bounds are server-computed (not user input), so inlining them is
// safe and keeps the bucket arithmetic out of the bound-arg list.
func sparkExpr(from, to time.Time) string {
	width := int64(to.Sub(from).Seconds()) / int64(sparkBuckets)
	if width < 1 {
		width = 1
	}
	fromUnix := from.Unix()
	return fmt.Sprintf(
		`sumForEach(arrayMap(i -> toUInt32(least(greatest(intDiv(toInt64(toUnixTimestamp(Timestamp)) - %d, %d), 0), %d) = i), range(%d)))`,
		fromUnix, width, sparkBuckets-1, sparkBuckets,
	)
}

// Occurrence is a single failure instance within an issue.
type Occurrence struct {
	TraceID   string
	SpanID    string
	SpanName  string
	Timestamp time.Time
	Message   string
}

// TrendPoint is one bucket of an issue's occurrence sparkline.
type TrendPoint struct {
	Timestamp time.Time
	Count     int64
}

// errorStatusClause matches an error span regardless of which OTel-status
// spelling the writer used. The predicate itself now lives in
// internal/telemetry/otelstatus so every reader of otel_traces shares one
// definition; this alias is kept because the SQL below reads better with a
// short local name.
var errorStatusClause = otelstatus.IsError(otelstatus.Column)

// issueSource is the unioned, tenant- and time-scoped set of error events that
// feed the issue aggregation. It has two branches:
//
//   1. Native OTEL error spans in otel_traces (gateway-proxied LLM failures,
//      or any customer OTLP export).
//   2. ERROR-level custom observations in otel_custom_observations — the spans
//      our SDKs record via traces.span()/withSpan() (auto-captured on a thrown
//      exception) and client.capture_exception(). This is what lets a customer
//      "create" an issue straight from their app code without proxying through
//      the gateway.
//
// Both branches project the SAME column set so the outer aggregation treats
// them uniformly. For branch 2 we synthesize a SpanAttributes map from the
// observation's JSON Metadata plus its Model column, so the LLM-aware
// classifier (chCategory), provider/model resolution and tag distributions all
// work without special-casing. Tenant and time filters are pushed INTO each
// branch (each references its own tenant column), so the outer query must not
// re-scope — that also keeps the per-branch tenant isolation intact.
func issueSource(tenantID string, from, to time.Time) (string, []interface{}) {
	tenantClause, tenantArgs := tenantBridgeFilter(tenantID)

	b1 := `SELECT
			Timestamp, TraceId, SpanId, ParentSpanId, SpanName,
			toString(StatusCode) AS StatusCode, StatusMessage,
			toInt64(Duration) AS Duration, SpanAttributes
		FROM otel_traces
		WHERE ` + errorStatusClause + ` AND ` + tenantClause + ` AND Timestamp >= ? AND Timestamp <= ?`

	b2 := `SELECT
			StartTime AS Timestamp, TraceId, ObservationId AS SpanId,
			ParentObservationId AS ParentSpanId, Name AS SpanName,
			'STATUS_CODE_ERROR' AS StatusCode, StatusMessage,
			toInt64(Duration) AS Duration,
			mapUpdate(
				CAST(JSONExtractKeysAndValues(Metadata, 'String') AS Map(String, String)),
				map('llm.model', Model)
			) AS SpanAttributes
		FROM otel_custom_observations
		WHERE Level = 'ERROR' AND TenantId = ? AND StartTime >= ? AND StartTime <= ?`

	src := "(" + b1 + " UNION ALL " + b2 + ")"
	args := make([]interface{}, 0, len(tenantArgs)+5)
	args = append(args, tenantArgs...) // branch 1 tenant
	args = append(args, from, to)      // branch 1 window
	args = append(args, tenantID)      // branch 2 tenant
	args = append(args, from, to)      // branch 2 window
	return src, args
}

// ListIssues returns issue groups ranked by occurrence count for a window.
func (s *CHStore) ListIssues(ctx context.Context, tenantID string, from, to time.Time, queryText string, limit, offset int) ([]IssueAgg, error) {
	src, args := issueSource(tenantID, from, to)
	where := "1"
	if queryText != "" {
		where = "positionCaseInsensitive(" + chErrorMessage + ", ?) > 0"
		args = append(args, queryText)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	sql := fmt.Sprintf(`
		SELECT
			%s AS fingerprint,
			any(%s) AS title,
			any(%s) AS signature,
			any(%s) AS category,
			toInt64(count()) AS cnt,
			min(Timestamp) AS first_seen,
			max(Timestamp) AS last_seen,
			any(%s) AS provider,
			any(%s) AS model,
			arraySlice(groupUniqArray(TraceId), 1, 5) AS samples,
			%s AS spark
		FROM %s
		WHERE %s
		GROUP BY fingerprint
		ORDER BY cnt DESC
		LIMIT ? OFFSET ?`,
		chFingerprint, chErrorMessage, chSignature, chCategory, chProvider, chModel, sparkExpr(from, to), src, where)
	args = append(args, limit, offset)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("issues list query: %w", err)
	}
	defer rows.Close()

	var out []IssueAgg
	for rows.Next() {
		var a IssueAgg
		var spark []uint32
		if err := rows.Scan(&a.Fingerprint, &a.Title, &a.Signature, &a.Category, &a.Count,
			&a.FirstSeen, &a.LastSeen, &a.Provider, &a.Model, &a.SampleTraceIDs, &spark); err != nil {
			return nil, fmt.Errorf("issues list scan: %w", err)
		}
		a.Spark = make([]int32, len(spark))
		for i, v := range spark {
			a.Spark[i] = int32(v)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetIssue returns a single issue group by fingerprint for a window.
func (s *CHStore) GetIssue(ctx context.Context, tenantID, fingerprint string, from, to time.Time) (*IssueAgg, error) {
	src, args := issueSource(tenantID, from, to)
	args = append(args, fingerprint)
	sql := fmt.Sprintf(`
		SELECT
			%s AS fingerprint,
			any(%s) AS title,
			any(%s) AS signature,
			any(%s) AS category,
			toInt64(count()) AS cnt,
			min(Timestamp) AS first_seen,
			max(Timestamp) AS last_seen,
			any(%s) AS provider,
			any(%s) AS model,
			arraySlice(groupUniqArray(TraceId), 1, 5) AS samples
		FROM %s
		WHERE %s = ?
		GROUP BY fingerprint
		LIMIT 1`,
		chFingerprint, chErrorMessage, chSignature, chCategory, chProvider, chModel, src, chFingerprint)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("issues get query: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var a IssueAgg
	if err := rows.Scan(&a.Fingerprint, &a.Title, &a.Signature, &a.Category, &a.Count,
		&a.FirstSeen, &a.LastSeen, &a.Provider, &a.Model, &a.SampleTraceIDs); err != nil {
		return nil, fmt.Errorf("issues get scan: %w", err)
	}
	return &a, nil
}

// Occurrences returns the most recent failure instances for an issue.
func (s *CHStore) Occurrences(ctx context.Context, tenantID, fingerprint string, from, to time.Time, limit int) ([]Occurrence, error) {
	src, args := issueSource(tenantID, from, to)
	args = append(args, fingerprint)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	sql := fmt.Sprintf(`
		SELECT TraceId, SpanId, SpanName, Timestamp, %s AS message
		FROM %s
		WHERE %s = ?
		ORDER BY Timestamp DESC
		LIMIT ?`, chErrorMessage, src, chFingerprint)
	args = append(args, limit)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("issues occurrences query: %w", err)
	}
	defer rows.Close()
	var out []Occurrence
	for rows.Next() {
		var o Occurrence
		if err := rows.Scan(&o.TraceID, &o.SpanID, &o.SpanName, &o.Timestamp, &o.Message); err != nil {
			return nil, fmt.Errorf("issues occurrences scan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Trend returns occurrence counts bucketed over the window for an issue.
func (s *CHStore) Trend(ctx context.Context, tenantID, fingerprint string, from, to time.Time, interval string) ([]TrendPoint, error) {
	bucket := "toStartOfHour(Timestamp)"
	switch strings.ToLower(interval) {
	case "day":
		bucket = "toStartOfDay(Timestamp)"
	case "minute":
		bucket = "toStartOfMinute(Timestamp)"
	case "", "hour":
		bucket = "toStartOfHour(Timestamp)"
	}
	src, args := issueSource(tenantID, from, to)
	args = append(args, fingerprint)
	sql := fmt.Sprintf(`
		SELECT %s AS bucket, toInt64(count()) AS cnt
		FROM %s
		WHERE %s = ?
		GROUP BY bucket
		ORDER BY bucket ASC`, bucket, src, chFingerprint)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("issues trend query: %w", err)
	}
	defer rows.Close()
	var out []TrendPoint
	for rows.Next() {
		var p TrendPoint
		if err := rows.Scan(&p.Timestamp, &p.Count); err != nil {
			return nil, fmt.Errorf("issues trend scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─── Detail enrichment (event-centric, Sentry-style) ─────────────────

// EventDetail is one representative event with its full captured attributes.
type EventDetail struct {
	TraceID    string
	SpanID     string
	Timestamp  time.Time
	Message    string
	Attributes map[string]string
}

// SpanCrumb is one span in the failing occurrence's trace (breadcrumb).
type SpanCrumb struct {
	SpanID        string
	ParentSpanID  string
	Name          string
	StatusCode    string
	StatusMessage string
	Timestamp     time.Time
	DurationMs    float64
}

// TagValue is one observed value of an attribute and its event count.
type TagValue struct {
	Value string
	Count int64
}

// TagDistribution is one attribute's value breakdown across an issue's events.
type TagDistribution struct {
	Key    string
	Total  int64
	Values []TagValue
}

// distTagKeys are the attributes we surface as distributions on an issue.
var distTagKeys = []string{
	"llm.provider", "llm.model", "http.status_code", "error.type", "http.method",
}

// LatestEvent returns the most recent error occurrence for an issue with its
// full attribute map, for the highlights panel.
func (s *CHStore) LatestEvent(ctx context.Context, tenantID, fingerprint string, from, to time.Time) (*EventDetail, error) {
	src, args := issueSource(tenantID, from, to)
	args = append(args, fingerprint)
	sql := fmt.Sprintf(`
		SELECT TraceId, SpanId, Timestamp, %s AS message, SpanAttributes
		FROM %s
		WHERE %s = ?
		ORDER BY Timestamp DESC
		LIMIT 1`, chErrorMessage, src, chFingerprint)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("issues latest-event query: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var e EventDetail
	e.Attributes = map[string]string{}
	if err := rows.Scan(&e.TraceID, &e.SpanID, &e.Timestamp, &e.Message, &e.Attributes); err != nil {
		return nil, fmt.Errorf("issues latest-event scan: %w", err)
	}
	return &e, nil
}

// Breadcrumbs returns every span of a trace (ordered oldest-first) as the
// breadcrumb chain into the failure. Scoped to the tenant for isolation.
func (s *CHStore) Breadcrumbs(ctx context.Context, tenantID, traceID string) ([]SpanCrumb, error) {
	if traceID == "" {
		return nil, nil
	}
	tenantClause, tenantArgs := tenantBridgeFilter(tenantID)
	sql := `
		SELECT SpanId, ParentSpanId, SpanName, StatusCode, StatusMessage, Timestamp, Duration
		FROM otel_traces
		WHERE TraceId = ? AND ` + tenantClause + `
		ORDER BY Timestamp ASC
		LIMIT 100`
	args := []interface{}{traceID}
	args = append(args, tenantArgs...)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("issues breadcrumbs query: %w", err)
	}
	defer rows.Close()
	var out []SpanCrumb
	for rows.Next() {
		var c SpanCrumb
		var durNs uint64
		if err := rows.Scan(&c.SpanID, &c.ParentSpanID, &c.Name, &c.StatusCode, &c.StatusMessage, &c.Timestamp, &durNs); err != nil {
			return nil, fmt.Errorf("issues breadcrumbs scan: %w", err)
		}
		c.DurationMs = float64(durNs) / 1e6
		out = append(out, c)
	}
	return out, rows.Err()
}

// TagDistributions returns value breakdowns for a fixed set of attributes
// across an issue's events.
func (s *CHStore) TagDistributions(ctx context.Context, tenantID, fingerprint string, from, to time.Time) ([]TagDistribution, error) {
	var out []TagDistribution
	for _, key := range distTagKeys {
		src, srcArgs := issueSource(tenantID, from, to)
		sql := `
			SELECT SpanAttributes[?] AS v, toInt64(count()) AS c
			FROM ` + src + `
			WHERE ` + chFingerprint + ` = ? AND SpanAttributes[?] != ''
			GROUP BY v
			ORDER BY c DESC
			LIMIT 8`
		// Arg order follows the SQL text: the SELECT SpanAttributes[?] key,
		// then the union source args, then the outer WHERE (fingerprint, key).
		qargs := append([]interface{}{key}, srcArgs...)
		qargs = append(qargs, fingerprint, key)

		rows, err := s.conn.Query(ctx, sql, qargs...)
		if err != nil {
			return nil, fmt.Errorf("issues tag-dist query (%s): %w", key, err)
		}
		var dist TagDistribution
		dist.Key = key
		for rows.Next() {
			var tv TagValue
			if err := rows.Scan(&tv.Value, &tv.Count); err != nil {
				rows.Close()
				return nil, fmt.Errorf("issues tag-dist scan (%s): %w", key, err)
			}
			dist.Total += tv.Count
			dist.Values = append(dist.Values, tv)
		}
		rows.Close()
		if len(dist.Values) > 0 {
			out = append(out, dist)
		}
	}
	return out, nil
}

// DistinctUsers counts distinct agent sessions affected by an issue (our
// "users affected" proxy).
func (s *CHStore) DistinctUsers(ctx context.Context, tenantID, fingerprint string, from, to time.Time) (int64, error) {
	src, args := issueSource(tenantID, from, to)
	args = append(args, fingerprint)
	sql := `
		SELECT toInt64(uniqExactIf(SpanAttributes['agent.session.id'], SpanAttributes['agent.session.id'] != ''))
		FROM ` + src + `
		WHERE ` + chFingerprint + ` = ?`
	var n int64
	if err := s.conn.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("issues distinct-users query: %w", err)
	}
	return n, nil
}
