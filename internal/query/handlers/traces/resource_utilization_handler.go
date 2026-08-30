package traces

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// ResourceUtilizationHandler queries time-bucketed resource utilization for a trace
// from the otel_resource_metrics table.
type ResourceUtilizationHandler struct {
	conn clickhouse.Conn
}

func NewResourceUtilizationHandler(conn clickhouse.Conn) *ResourceUtilizationHandler {
	return &ResourceUtilizationHandler{conn: conn}
}

func (h *ResourceUtilizationHandler) QueryType() string {
	return "GetResourceUtilization"
}

// ResourceUtilizationQuery filters resource metrics for a single trace.
type ResourceUtilizationQuery struct {
	TraceID       string
	FromTime      *time.Time
	ToTime        *time.Time
	GranularityMs int32
}

func (q *ResourceUtilizationQuery) QueryType() string  { return "GetResourceUtilization" }
func (q *ResourceUtilizationQuery) Validate() error    { return nil }

// ResourceMetricsData holds a single resource snapshot. Pointers so we can leave
// fields unset when the underlying value is null.
type ResourceMetricsData struct {
	MemoryUsedBytes      *int64
	MemoryAllocatedBytes *int64
	CpuUsagePercent      *float64
	NetworkBytesSent     *int64
	NetworkBytesReceived *int64
	DiskReadBytes        *int64
	DiskWriteBytes       *int64
	ThreadCount          *int32
}

// ResourceUtilizationPoint is one time bucket of utilization data.
type ResourceUtilizationPoint struct {
	Timestamp          time.Time
	Metrics            ResourceMetricsData
	ActiveObservations []string
}

// ResourceUtilizationResult is the aggregated response.
type ResourceUtilizationResult struct {
	Points  []ResourceUtilizationPoint
	Peak    ResourceMetricsData
	Average ResourceMetricsData
}

func (h *ResourceUtilizationHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	ruQuery, ok := q.(*ResourceUtilizationQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for ResourceUtilizationHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return &ResourceUtilizationResult{}, nil
	}

	granularityMs := ruQuery.GranularityMs
	if granularityMs <= 0 {
		granularityMs = 1000 // 1s default
	}
	bucketSeconds := float64(granularityMs) / 1000.0

	conditions := []string{"rm.TraceId = ?", "ot.TraceId = ?", "ot.ResourceAttributes['tenant.id'] = ?"}
	args := []interface{}{ruQuery.TraceID, ruQuery.TraceID, tenantID}
	if ruQuery.FromTime != nil {
		conditions = append(conditions, "rm.Timestamp >= ?")
		args = append(args, *ruQuery.FromTime)
	}
	if ruQuery.ToTime != nil {
		conditions = append(conditions, "rm.Timestamp <= ?")
		args = append(args, *ruQuery.ToTime)
	}
	where := joinConditions(conditions)

	// Bucket by floor(unixTimestamp / bucket_seconds) so callers can choose granularity.
	// Join otel_traces (ot) only to enforce tenant isolation — otel_resource_metrics has
	// no tenant column.
	pointsSQL := fmt.Sprintf(`
		SELECT
			toDateTime(toUInt64(floor(toUnixTimestamp64Nano(rm.Timestamp) / 1e9 / %f) * %f)) AS bucket,
			max(rm.MemoryUsedBytes)        AS peak_mem,
			max(rm.MemoryAllocatedBytes)   AS peak_mem_alloc,
			max(rm.CpuUsagePercent)        AS peak_cpu,
			max(rm.NetworkBytesSent)       AS peak_net_sent,
			max(rm.NetworkBytesReceived)   AS peak_net_recv,
			max(rm.DiskReadBytes)          AS peak_disk_read,
			max(rm.DiskWriteBytes)         AS peak_disk_write,
			max(rm.ThreadCount)            AS peak_threads,
			arrayDistinct(groupArray(rm.ObservationId)) AS observation_ids
		FROM otel_resource_metrics rm
		INNER JOIN (SELECT DISTINCT TraceId, ResourceAttributes FROM otel_traces WHERE TraceId = ?) ot
			ON ot.TraceId = rm.TraceId
		WHERE %s
		GROUP BY bucket
		ORDER BY bucket
		LIMIT 10000
	`, bucketSeconds, bucketSeconds, where)

	// One extra arg at the start of the prepared query for the otel_traces subquery.
	bucketArgs := make([]interface{}, 0, len(args)+1)
	bucketArgs = append(bucketArgs, ruQuery.TraceID)
	bucketArgs = append(bucketArgs, args...)

	rows, err := h.conn.Query(ctx, pointsSQL, bucketArgs...)
	if err != nil {
		logger.WithFields("error", err.Error(), "trace_id", ruQuery.TraceID).Error("failed to query resource utilization")
		return nil, fmt.Errorf("failed to query resource utilization: %w", err)
	}
	defer rows.Close()

	result := &ResourceUtilizationResult{}
	var (
		sumMem, sumNetSent, sumNetRecv, sumDiskR, sumDiskW int64
		sumCpu                                              float64
		sumThreads                                          int64
		count                                               int64
	)
	for rows.Next() {
		var (
			bucket           time.Time
			mem, memAlloc    *int64
			cpu              *float64
			netSent, netRecv *int64
			diskR, diskW     *int64
			threads          *int32
			observationIDs   []string
		)
		if err := rows.Scan(&bucket, &mem, &memAlloc, &cpu, &netSent, &netRecv, &diskR, &diskW, &threads, &observationIDs); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan resource utilization row")
			continue
		}
		point := ResourceUtilizationPoint{
			Timestamp:          bucket,
			ActiveObservations: observationIDs,
			Metrics: ResourceMetricsData{
				MemoryUsedBytes:      mem,
				MemoryAllocatedBytes: memAlloc,
				CpuUsagePercent:      cpu,
				NetworkBytesSent:     netSent,
				NetworkBytesReceived: netRecv,
				DiskReadBytes:        diskR,
				DiskWriteBytes:       diskW,
				ThreadCount:          threads,
			},
		}
		result.Points = append(result.Points, point)
		updatePeak(&result.Peak, &point.Metrics)
		// Sums for averages
		if mem != nil {
			sumMem += *mem
		}
		if cpu != nil {
			sumCpu += *cpu
		}
		if netSent != nil {
			sumNetSent += *netSent
		}
		if netRecv != nil {
			sumNetRecv += *netRecv
		}
		if diskR != nil {
			sumDiskR += *diskR
		}
		if diskW != nil {
			sumDiskW += *diskW
		}
		if threads != nil {
			sumThreads += int64(*threads)
		}
		count++
	}

	if count > 0 {
		avgMem := sumMem / count
		avgCpu := sumCpu / float64(count)
		avgNetS := sumNetSent / count
		avgNetR := sumNetRecv / count
		avgDR := sumDiskR / count
		avgDW := sumDiskW / count
		avgT := int32(sumThreads / count)
		result.Average = ResourceMetricsData{
			MemoryUsedBytes:      &avgMem,
			CpuUsagePercent:      &avgCpu,
			NetworkBytesSent:     &avgNetS,
			NetworkBytesReceived: &avgNetR,
			DiskReadBytes:        &avgDR,
			DiskWriteBytes:       &avgDW,
			ThreadCount:          &avgT,
		}
	}

	return result, nil
}

func updatePeak(peak, next *ResourceMetricsData) {
	if next.MemoryUsedBytes != nil && (peak.MemoryUsedBytes == nil || *next.MemoryUsedBytes > *peak.MemoryUsedBytes) {
		v := *next.MemoryUsedBytes
		peak.MemoryUsedBytes = &v
	}
	if next.MemoryAllocatedBytes != nil && (peak.MemoryAllocatedBytes == nil || *next.MemoryAllocatedBytes > *peak.MemoryAllocatedBytes) {
		v := *next.MemoryAllocatedBytes
		peak.MemoryAllocatedBytes = &v
	}
	if next.CpuUsagePercent != nil && (peak.CpuUsagePercent == nil || *next.CpuUsagePercent > *peak.CpuUsagePercent) {
		v := *next.CpuUsagePercent
		peak.CpuUsagePercent = &v
	}
	if next.NetworkBytesSent != nil && (peak.NetworkBytesSent == nil || *next.NetworkBytesSent > *peak.NetworkBytesSent) {
		v := *next.NetworkBytesSent
		peak.NetworkBytesSent = &v
	}
	if next.NetworkBytesReceived != nil && (peak.NetworkBytesReceived == nil || *next.NetworkBytesReceived > *peak.NetworkBytesReceived) {
		v := *next.NetworkBytesReceived
		peak.NetworkBytesReceived = &v
	}
	if next.DiskReadBytes != nil && (peak.DiskReadBytes == nil || *next.DiskReadBytes > *peak.DiskReadBytes) {
		v := *next.DiskReadBytes
		peak.DiskReadBytes = &v
	}
	if next.DiskWriteBytes != nil && (peak.DiskWriteBytes == nil || *next.DiskWriteBytes > *peak.DiskWriteBytes) {
		v := *next.DiskWriteBytes
		peak.DiskWriteBytes = &v
	}
	if next.ThreadCount != nil && (peak.ThreadCount == nil || *next.ThreadCount > *peak.ThreadCount) {
		v := *next.ThreadCount
		peak.ThreadCount = &v
	}
}
