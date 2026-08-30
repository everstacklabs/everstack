package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// AnalyticsBucket represents one time-bucketed row of analytics data.
type AnalyticsBucket struct {
	Timestamp    time.Time
	QueryCount   int
	StoreCount   int
	DeleteCount  int
	ErrorCount   int
	AvgLatencyMs float64
}

// CollectionStat represents request count for a single collection.
type CollectionStat struct {
	CollectionName string
	RequestCount   int
}

// AnalyticsSummary is the full analytics response.
type AnalyticsSummary struct {
	Buckets        []AnalyticsBucket
	TotalRequests  int
	TotalErrors    int
	AvgLatencyMs   float64
	TopCollections []CollectionStat
}

// GetAnalytics aggregates memory request metrics into time-bucketed data
// for a single tenant. tenantID is required: an empty value would have run
// the underlying queries unscoped and returned every tenant's request
// volume / top collections to the caller, which is the leak this function
// previously had.
func GetAnalytics(ctx context.Context, db *sqlx.DB, tenantID string, from, to time.Time) (*AnalyticsSummary, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("memory analytics: tenant id is required")
	}
	interval := selectInterval(to.Sub(from))

	// Bucketed operation counts
	type bucketRow struct {
		Bucket       time.Time `db:"bucket"`
		Operation    string    `db:"operation"`
		Count        int       `db:"cnt"`
		AvgLatencyMs float64   `db:"avg_lat"`
		ErrorCount   int       `db:"err_cnt"`
	}

	bucketQuery := fmt.Sprintf(`
		SELECT
			date_trunc('%s', created_at) AS bucket,
			operation,
			COUNT(*)::int AS cnt,
			COALESCE(AVG(latency_ms), 0) AS avg_lat,
			COUNT(*) FILTER (WHERE error_message IS NOT NULL)::int AS err_cnt
		FROM memory_request_log
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at <= $3
		GROUP BY bucket, operation
		ORDER BY bucket
	`, interval)

	var rows []bucketRow
	if err := db.SelectContext(ctx, &rows, bucketQuery, tenantID, from, to); err != nil {
		return nil, fmt.Errorf("analytics bucket query: %w", err)
	}

	// Aggregate into buckets map
	bucketMap := make(map[time.Time]*AnalyticsBucket)
	for _, r := range rows {
		b, ok := bucketMap[r.Bucket]
		if !ok {
			b = &AnalyticsBucket{Timestamp: r.Bucket}
			bucketMap[r.Bucket] = b
		}
		switch r.Operation {
		case "query":
			b.QueryCount += r.Count
		case "add_documents":
			b.StoreCount += r.Count
		case "delete_document", "delete_collection":
			b.DeleteCount += r.Count
		default:
			// create_collection, get_collection, list_collections count as queries
			b.QueryCount += r.Count
		}
		b.ErrorCount += r.ErrorCount
		// Running weighted average
		total := b.QueryCount + b.StoreCount + b.DeleteCount
		if total > 0 {
			b.AvgLatencyMs = (b.AvgLatencyMs*float64(total-r.Count) + r.AvgLatencyMs*float64(r.Count)) / float64(total)
		}
	}

	// Sort buckets
	buckets := make([]AnalyticsBucket, 0, len(bucketMap))
	for _, b := range bucketMap {
		buckets = append(buckets, *b)
	}
	sortBuckets(buckets)

	// Summary totals
	type summaryRow struct {
		TotalRequests int     `db:"total_requests"`
		TotalErrors   int     `db:"total_errors"`
		AvgLatencyMs  float64 `db:"avg_latency_ms"`
	}
	var summary summaryRow
	err := db.GetContext(ctx, &summary, `
		SELECT
			COUNT(*)::int AS total_requests,
			COUNT(*) FILTER (WHERE error_message IS NOT NULL)::int AS total_errors,
			COALESCE(AVG(latency_ms), 0) AS avg_latency_ms
		FROM memory_request_log
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at <= $3
	`, tenantID, from, to)
	if err != nil {
		return nil, fmt.Errorf("analytics summary query: %w", err)
	}

	// Top collections
	type collRow struct {
		CollectionName string `db:"collection_name"`
		RequestCount   int    `db:"request_count"`
	}
	var topColls []collRow
	err = db.SelectContext(ctx, &topColls, `
		SELECT collection_name, COUNT(*)::int AS request_count
		FROM memory_request_log
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at <= $3
			AND collection_name != ''
		GROUP BY collection_name
		ORDER BY request_count DESC
		LIMIT 5
	`, tenantID, from, to)
	if err != nil {
		return nil, fmt.Errorf("analytics top collections query: %w", err)
	}

	topCollections := make([]CollectionStat, len(topColls))
	for i, c := range topColls {
		topCollections[i] = CollectionStat{
			CollectionName: c.CollectionName,
			RequestCount:   c.RequestCount,
		}
	}

	return &AnalyticsSummary{
		Buckets:        buckets,
		TotalRequests:  summary.TotalRequests,
		TotalErrors:    summary.TotalErrors,
		AvgLatencyMs:   summary.AvgLatencyMs,
		TopCollections: topCollections,
	}, nil
}

func selectInterval(d time.Duration) string {
	switch {
	case d <= 2*time.Hour:
		return "minute"
	case d <= 48*time.Hour:
		return "hour"
	default:
		return "day"
	}
}

func sortBuckets(buckets []AnalyticsBucket) {
	for i := 1; i < len(buckets); i++ {
		for j := i; j > 0 && buckets[j].Timestamp.Before(buckets[j-1].Timestamp); j-- {
			buckets[j], buckets[j-1] = buckets[j-1], buckets[j]
		}
	}
}
