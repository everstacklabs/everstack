package modelmetrics

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type ClickHouseRepository struct {
	conn clickhouse.Conn
}

func NewClickHouseRepository(conn clickhouse.Conn) *ClickHouseRepository {
	return &ClickHouseRepository{conn: conn}
}

func (r *ClickHouseRepository) LoadReport(ctx context.Context, query Query) (RawReport, error) {
	if r == nil || r.conn == nil {
		return RawReport{}, fmt.Errorf("clickhouse connection is required")
	}
	sqlQuery, args, err := buildReportSQL(query)
	if err != nil {
		return RawReport{}, err
	}

	rows, err := r.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		return RawReport{}, fmt.Errorf("query model metrics: %w", err)
	}
	defer rows.Close()

	result := RawReport{Buckets: []Bucket{}}
	for rows.Next() {
		var bucket Bucket
		if err := rows.Scan(
			&bucket.Period,
			&bucket.TenantCount,
			&bucket.ExternalTenantCount,
			&bucket.Metrics.Requests,
			&bucket.Metrics.Errors,
			&bucket.Metrics.InputTokens,
			&bucket.Metrics.OutputTokens,
			&bucket.Metrics.ReasoningTokens,
			&bucket.Metrics.CacheReadTokens,
			&bucket.Metrics.CacheWriteTokens,
			&bucket.Metrics.CostUSD,
			&bucket.Metrics.LatencyTotalMS,
			&bucket.Metrics.LatencySamples,
			&bucket.Metrics.TTFTTotalMS,
			&bucket.Metrics.TTFTSamples,
			&bucket.Metrics.StreamOutputTokens,
			&bucket.Metrics.GenerationDurationMS,
		); err != nil {
			return RawReport{}, fmt.Errorf("scan model metrics: %w", err)
		}
		if result.DataSince.IsZero() {
			result.DataSince = bucket.Period
		}
		result.Buckets = append(result.Buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return RawReport{}, fmt.Errorf("iterate model metrics: %w", err)
	}
	return result, nil
}

func (r *ClickHouseRepository) LoadBreakdown(
	ctx context.Context,
	query BreakdownQuery,
) (RawBreakdown, error) {
	if r == nil || r.conn == nil {
		return RawBreakdown{}, fmt.Errorf("clickhouse connection is required")
	}
	sqlQuery, args, err := buildBreakdownSQL(query)
	if err != nil {
		return RawBreakdown{}, err
	}

	rows, err := r.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		return RawBreakdown{}, fmt.Errorf("query provider model breakdown: %w", err)
	}
	defer rows.Close()

	result := RawBreakdown{Buckets: []BreakdownBucket{}}
	for rows.Next() {
		var bucket BreakdownBucket
		if err := rows.Scan(
			&bucket.Period,
			&bucket.Key,
			&bucket.TenantCount,
			&bucket.ExternalTenantCount,
			&bucket.Metrics.Requests,
			&bucket.Metrics.Errors,
			&bucket.Metrics.InputTokens,
			&bucket.Metrics.OutputTokens,
			&bucket.Metrics.ReasoningTokens,
			&bucket.Metrics.CacheReadTokens,
			&bucket.Metrics.CacheWriteTokens,
			&bucket.Metrics.CostUSD,
			&bucket.Metrics.LatencyTotalMS,
			&bucket.Metrics.LatencySamples,
			&bucket.Metrics.TTFTTotalMS,
			&bucket.Metrics.TTFTSamples,
			&bucket.Metrics.StreamOutputTokens,
			&bucket.Metrics.GenerationDurationMS,
		); err != nil {
			return RawBreakdown{}, fmt.Errorf(
				"scan provider model breakdown: %w",
				err,
			)
		}
		if result.DataSince.IsZero() {
			result.DataSince = bucket.Period
		}
		result.Buckets = append(result.Buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return RawBreakdown{}, fmt.Errorf(
			"iterate provider model breakdown: %w",
			err,
		)
	}
	return result, nil
}

func buildReportSQL(query Query) (string, []any, error) {
	dimension, err := reportDimension(query.Kind)
	if err != nil {
		return "", nil, err
	}
	bucket, err := reportBucket(query.Interval)
	if err != nil {
		return "", nil, err
	}
	if query.Key == "" || query.EndTime.IsZero() {
		return "", nil, fmt.Errorf("resolved report query is required")
	}

	conditions := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if !query.StartTime.IsZero() {
		conditions = append(conditions, "period >= ?")
		args = append(args, query.StartTime)
	}
	conditions = append(conditions, "period < ?")
	args = append(args, query.EndTime)
	if query.Kind == KindModel {
		conditions = append(
			conditions,
			"(canonical_model_id = ? OR (startsWith(canonical_model_id, ?) AND match(canonical_model_id, '-[0-9]{4}-[0-9]{2}-[0-9]{2}$')))",
		)
		args = append(args, query.Key, query.Key+"-")
	} else {
		conditions = append(conditions, dimension+" = ?")
		args = append(args, query.Key)
	}

	externalExpr, externalArgs := externalTenantExpr(query.FirstPartyTenants)
	// The expression sits in the SELECT list, so its bind parameters must come
	// before the WHERE clause's in the positional argument slice.
	args = append(externalArgs, args...)

	sqlQuery := fmt.Sprintf(`
		SELECT
			%s AS bucket,
			uniqExact(tenant_id) AS tenant_count,
			%s AS external_tenant_count,
			sum(request_count) AS requests,
			sum(error_count) AS errors,
			sum(input_tokens) AS input_tokens,
			sum(output_tokens) AS output_tokens,
			sum(reasoning_tokens) AS reasoning_tokens,
			sum(cache_read_tokens) AS cache_read_tokens,
			sum(cache_write_tokens) AS cache_write_tokens,
			sum(total_cost_usd) AS total_cost_usd,
			sum(latency_total_ms) AS latency_total_ms,
			sum(latency_samples) AS latency_samples,
			sum(ttft_total_ms) AS ttft_total_ms,
			sum(ttft_samples) AS ttft_samples,
			sum(stream_output_tokens) AS stream_output_tokens,
			sum(generation_duration_ms) AS generation_duration_ms
		FROM model_metrics_hourly
		WHERE %s
		GROUP BY bucket
		ORDER BY bucket ASC
	`, bucket, externalExpr, strings.Join(conditions, " AND "))
	return sqlQuery, args, nil
}

func buildBreakdownSQL(query BreakdownQuery) (string, []any, error) {
	bucket, err := reportBucket(query.Interval)
	if err != nil {
		return "", nil, err
	}
	if query.Provider == "" || query.EndTime.IsZero() {
		return "", nil, fmt.Errorf("resolved provider model breakdown is required")
	}

	conditions := make([]string, 0, 4)
	args := make([]any, 0, 3)
	if !query.StartTime.IsZero() {
		conditions = append(conditions, "period >= ?")
		args = append(args, query.StartTime)
	}
	conditions = append(conditions, "period < ?", "provider = ?")
	args = append(args, query.EndTime, query.Provider)
	conditions = append(conditions, "canonical_model_id != ''")

	externalExpr, externalArgs := externalTenantExpr(query.FirstPartyTenants)
	// SELECT-list binds precede WHERE binds in the positional slice.
	args = append(externalArgs, args...)

	sqlQuery := fmt.Sprintf(`
		SELECT
			%s AS bucket,
			replaceRegexpOne(
				canonical_model_id,
				'-[0-9]{4}-[0-9]{2}-[0-9]{2}$',
				''
			) AS series_key,
			uniqExact(tenant_id) AS tenant_count,
			%s AS external_tenant_count,
			sum(request_count) AS requests,
			sum(error_count) AS errors,
			sum(input_tokens) AS input_tokens,
			sum(output_tokens) AS output_tokens,
			sum(reasoning_tokens) AS reasoning_tokens,
			sum(cache_read_tokens) AS cache_read_tokens,
			sum(cache_write_tokens) AS cache_write_tokens,
			sum(total_cost_usd) AS total_cost_usd,
			sum(latency_total_ms) AS latency_total_ms,
			sum(latency_samples) AS latency_samples,
			sum(ttft_total_ms) AS ttft_total_ms,
			sum(ttft_samples) AS ttft_samples,
			sum(stream_output_tokens) AS stream_output_tokens,
			sum(generation_duration_ms) AS generation_duration_ms
		FROM model_metrics_hourly
		WHERE %s
		GROUP BY bucket, series_key
		ORDER BY bucket ASC, series_key ASC
	`, bucket, externalExpr, strings.Join(conditions, " AND "))
	return sqlQuery, args, nil
}

func reportDimension(kind Kind) (string, error) {
	switch kind {
	case KindModel:
		return "canonical_model_id", nil
	case KindProvider:
		return "provider", nil
	case KindPublisher:
		return "publisher", nil
	default:
		return "", fmt.Errorf("unsupported metrics kind %q", kind)
	}
}

func reportBucket(interval Interval) (string, error) {
	switch interval {
	case IntervalHour:
		return "toStartOfHour(period)", nil
	case IntervalDay:
		return "toStartOfDay(period)", nil
	case IntervalMonth:
		return "toStartOfMonth(period)", nil
	default:
		return "", fmt.Errorf("unsupported metrics interval %q", interval)
	}
}

// externalTenantExpr renders the count of contributing tenants that are NOT
// operated by Everstack. When no first-party tenants are configured the
// carve-out is off, so it renders a constant rather than binding an empty
// array: ClickHouse never has to reason about an Array(Nothing) literal, and
// the value is unused by the caller in that case.
func externalTenantExpr(firstParty []string) (string, []any) {
	if len(firstParty) == 0 {
		return "uniqExact(tenant_id)", nil
	}
	// The list is operator config, never caller input, and is bound as a
	// single array parameter rather than interpolated into the statement.
	return "uniqExactIf(tenant_id, NOT has(?, lowerUTF8(tenant_id)))", []any{firstParty}
}
