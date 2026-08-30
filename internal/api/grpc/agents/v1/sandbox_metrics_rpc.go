package v1

// Sandbox resource metrics time-series (#292) — ConnectRPC.
//
// Migrated from raw mux handlers. The point-in-time /metrics and the SSE
// /metrics/watch stream stay raw (streaming); only the JSON history + batch
// endpoints move to ConnectRPC. Auth + ownership via the interceptor chain.

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	sandboxmetrics "github.com/everstacklabs/everstack/internal/sandbox/metrics"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

func toMetricSnapshot(s sandboxmetrics.Snapshot) *agentspb.MetricSnapshot {
	return &agentspb.MetricSnapshot{
		SandboxId:   s.SandboxID,
		CpuPercent:  s.CPUPercent,
		MemoryUsage: s.MemoryUsage,
		MemoryLimit: s.MemoryLimit,
		DiskUsedMb:  s.DiskUsedMB,
		CollectedAt: s.CollectedAt.UTC().Format(time.RFC3339),
	}
}

// SandboxMetricsHistory returns stored time-series snapshots for a sandbox.
func (s *Server) SandboxMetricsHistory(
	ctx context.Context,
	req *connect.Request[agentspb.SandboxMetricsHistoryRequest],
) (*connect.Response[agentspb.SandboxMetricsHistoryResponse], error) {
	if s.metricsRepo == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("metrics history not configured"))
	}
	sandboxID := req.Msg.GetSandboxId()
	if _, _, err := s.resolveSandbox(sandboxID); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 120
	}
	snaps, err := s.metricsRepo.History(ctx, sandboxID, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*agentspb.MetricSnapshot, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, toMetricSnapshot(sn))
	}
	return connect.NewResponse(&agentspb.SandboxMetricsHistoryResponse{
		Snapshots: out,
		Total:     int32(len(out)),
	}), nil
}

// SandboxMetricsBatch returns the latest snapshot for multiple sandboxes,
// keyed by sandbox_id. Capped at 50 ids.
func (s *Server) SandboxMetricsBatch(
	ctx context.Context,
	req *connect.Request[agentspb.SandboxMetricsBatchRequest],
) (*connect.Response[agentspb.SandboxMetricsBatchResponse], error) {
	if s.metricsRepo == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("metrics history not configured"))
	}
	ids := req.Msg.GetIds()
	if len(ids) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("ids is required"))
	}
	if len(ids) > 50 {
		ids = ids[:50]
	}
	snaps, err := s.metricsRepo.LatestBatch(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	byID := make(map[string]*agentspb.MetricSnapshot, len(snaps))
	for _, sn := range snaps {
		byID[sn.SandboxID] = toMetricSnapshot(sn)
	}
	return connect.NewResponse(&agentspb.SandboxMetricsBatchResponse{Metrics: byID}), nil
}
