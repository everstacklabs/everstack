package v1

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/logcolumns"
	"github.com/everstacklabs/everstack/internal/query"
	logshandler "github.com/everstacklabs/everstack/internal/query/handlers/logs"
	logspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/logs/v1"
	logsconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/logs/v1/logsconnect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements the LogsService gRPC server
type Server struct {
	ctx        context.Context
	logColumns *logcolumns.Store
}

// CreateServerWithContext creates a new logs gRPC server with context
func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

// SetLogColumnStore wires the tenant-scoped custom-log-column registry so the
// logs list projects the LogAttributes fields a tenant cares about and the
// column-management RPCs are served.
func (s *Server) SetLogColumnStore(store *logcolumns.Store) {
	s.logColumns = store
}

// RegisterConnectServer registers the Connect RPC handler
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return logsconnect.NewLogsServiceHandler(s, connect.WithInterceptors(interceptors...))
}

// FileDescriptor returns the file descriptor for this service
func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return logspb.File_everstack_logs_v1_logs_service_proto
}

// AppName returns the service name
func (s *Server) AppName() string {
	return logsconnect.LogsServiceName
}

// MethodPrefix returns the method prefix
func (s *Server) MethodPrefix() string {
	return logsconnect.LogsServiceName
}

// ListLogs streams request logs from ClickHouse OTEL logs
// Server-streaming: initial backfill + real-time tail
func (s *Server) ListLogs(
	ctx context.Context,
	req *connect.Request[logspb.ListLogsRequest],
	stream *connect.ServerStream[logspb.ListLogsResponse],
) error {
	// Get CQRS system from context
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	// Initial backfill
	from := time.Now().Add(-24 * time.Hour)
	if req.Msg.GetFrom() != "" {
		if t, err := time.Parse(time.RFC3339, req.Msg.GetFrom()); err == nil {
			from = t
		}
	}

	// Handle 'to' parameter for historical queries
	to := time.Time{}
	if req.Msg.GetTo() != "" {
		if t, err := time.Parse(time.RFC3339, req.Msg.GetTo()); err == nil {
			to = t
		}
	}

	logger.WithFields(
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"has_to", !to.IsZero(),
		"page_size", req.Msg.GetPageSize(),
		"offset", req.Msg.GetOffset(),
	).Debug("ListLogs request received")

	sent := make(map[string]string, 1024) // Track correlation_id -> status

	// Resolve the tenant's custom log columns once; project them into both the
	// backfill and tail queries so the list surfaces the LogAttributes fields
	// the tenant registered. Failure is non-fatal: logs still stream without
	// the extra columns.
	var customCols []logshandler.CustomAttrColumn
	if s.logColumns != nil {
		if defs, derr := s.logColumns.List(ctx); derr != nil {
			logger.WithError(derr).Warn("failed to list log custom columns")
		} else {
			for _, d := range defs {
				customCols = append(customCols, logshandler.CustomAttrColumn{Key: d.Key, Ref: d.AttrKey})
			}
		}
	}

	// Get pagination parameters
	pageSize := int(req.Msg.GetPageSize())
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 100
	}
	offset := int(req.Msg.GetOffset())
	if offset < 0 {
		offset = 0
	}

	// Send initial batch
	logs, err := logshandler.ListLogs(ctx, sys.QueryBus, from, to, "", "", pageSize, offset, customCols...)
	if err == nil {
		logger.WithFields("log_count", len(logs)).Debug("sending initial logs batch to client")
		for _, log := range logs {
			if log.CorrelationID == "" {
				continue
			}
			pbLog := toProtoLog(&log)
			logger.WithFields(
				"correlation_id", log.CorrelationID,
				"timestamp", log.Timestamp,
				"has_proto_timestamp", pbLog.Timestamp != nil,
			).Debug("sending log to client")
			if err := stream.Send(&logspb.ListLogsResponse{Logs: []*logspb.RequestLog{pbLog}}); err != nil {
				logger.WithError(err).Error("failed to send log to client")
				return err
			}
			sent[log.CorrelationID] = log.Status
		}
		logger.WithFields("sent_count", len(sent)).Debug("initial logs batch sent successfully")
	} else {
		// Context canceled is expected when client disconnects (e.g., historical mode)
		if ctx.Err() != nil {
			logger.Debug("initial logs fetch canceled by client (expected in historical mode)")
			return nil
		}
		// Check if logs handler is not registered (single mode without ClickHouse)
		if err.Error() == "no handler registered for query type: ListLogs" {
			return connect.NewError(connect.CodeUnimplemented,
				fmt.Errorf("logs functionality requires ClickHouse in hybrid mode. Please configure database.mode=hybrid and provide a ClickHouse DSN"))
		}
		logger.WithError(err).Error("failed to fetch initial logs")
	}

	// Tail loop: poll every 500ms for new logs and status updates (max 500 buffer like events)
	// Skip tail loop if 'to' is provided (historical/paused mode) OR if offset > 0 (pagination request)
	if !to.IsZero() || offset > 0 {
		logger.WithFields("to_provided", !to.IsZero(), "offset", offset).Debug("skipping tail loop (historical mode or pagination)")
		return nil
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Keep buffer size manageable (max 500 like events page)
			if len(sent) > 500 {
				// Clear half the oldest entries
				count := 0
				for k := range sent {
					delete(sent, k)
					count++
					if count >= 250 {
						break
					}
				}
			}

			// In tail loop, don't use 'to' parameter to get latest logs
			// Use smaller page size for tail loop, no offset needed (always get latest)
			lookbackFrom := time.Now().Add(-60 * time.Second)
			logs, err := logshandler.ListLogs(ctx, sys.QueryBus, lookbackFrom, time.Time{}, "", "", 100, 0, customCols...)
			if err != nil {
				continue
			}

			for _, log := range logs {
				if log.CorrelationID == "" {
					continue
				}

				// Check if we've sent this log before
				if prevStatus, ok := sent[log.CorrelationID]; ok {
					// Re-send if status changed (e.g., processing → success/error)
					if prevStatus == log.Status {
						continue // Same status, skip
					}
					// Status changed, send update
					logger.WithFields(
						"correlation_id", log.CorrelationID,
						"old_status", prevStatus,
						"new_status", log.Status,
					).Debug("log status changed, sending update")
				}

				pbLog := toProtoLog(&log)
				if err := stream.Send(&logspb.ListLogsResponse{Logs: []*logspb.RequestLog{pbLog}}); err != nil {
					return err
				}
				sent[log.CorrelationID] = log.Status
			}
		}
	}
}

func toProtoLog(log *query.RequestLogReadModel) *logspb.RequestLog {
	pbLog := &logspb.RequestLog{
		CorrelationId:    log.CorrelationID,
		FirstTimestamp:   log.FirstTimestamp,
		CommandType:      log.CommandType,
		Provider:         log.Provider,
		Model:            log.Model, // Kept for backward compatibility
		LatencyMs:        log.LatencyMs,
		PromptTokens:     log.PromptTokens,
		CompletionTokens: log.CompletionTokens,
		TotalTokens:      log.TotalTokens,
		Cost:             log.Cost,
		Status:           log.Status,
		Severity:         log.Severity,
		LogEvent:         log.LogEvent,
		Stream:           log.Stream,
		Payload:          log.Payload,
		RequestText:      log.RequestText,
		ResponseText:     log.ResponseText,
		TraceId:          log.TraceID,
		SpanId:           log.SpanID,
		TenantId:         log.TenantID,
		TenantType:       log.TenantType,
		// Multi-model fallback tracking
		RequestedModel:   log.RequestedModel,
		ServedModel:      log.ServedModel,
		AttemptedModels:  log.AllAttemptedModels,
		FallbackOccurred: log.FallbackOccurred,
		AttemptCount:     int32(log.AttemptCount),
	}

	// Resolved user-defined custom column values (LogAttributes-sourced).
	if len(log.CustomAttrValues) > 0 {
		pbLog.CustomColumns = log.CustomAttrValues
	}

	// Parse timestamp - try multiple formats
	if log.Timestamp != "" {
		formats := []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02 15:04:05",           // ClickHouse DateTime format
			"2006-01-02T15:04:05",           // ISO 8601 without timezone
			"2006-01-02 15:04:05.999999999", // ClickHouse DateTime64 format
		}
		for _, format := range formats {
			if t, err := time.Parse(format, log.Timestamp); err == nil {
				pbLog.Timestamp = timestamppb.New(t)
				break
			}
		}
	}

	// Map streaming metrics if available
	if log.StreamingMetrics != nil {
		pbLog.StreamingMetrics = &logspb.StreamingMetrics{
			TtftMs:                 log.StreamingMetrics.TtftMs,
			ChunkCount:             int32(log.StreamingMetrics.ChunkCount),
			AvgChunkLatencyMs:      log.StreamingMetrics.AvgChunkLatencyMs,
			MaxChunkLatencyMs:      log.StreamingMetrics.MaxChunkLatencyMs,
			TokensPerSecond:        log.StreamingMetrics.TokensPerSecond,
			StreamDurationMs:       log.StreamingMetrics.StreamDurationMs,
			PartialResponseOnError: log.StreamingMetrics.PartialResponseOnError,
		}

		// Map chunk timeline if available
		if len(log.StreamingMetrics.ChunkTimeline) > 0 {
			pbLog.StreamingMetrics.ChunkTimeline = make([]*logspb.ChunkMetadata, len(log.StreamingMetrics.ChunkTimeline))
			for i, chunk := range log.StreamingMetrics.ChunkTimeline {
				pbLog.StreamingMetrics.ChunkTimeline[i] = &logspb.ChunkMetadata{
					Index:            int32(chunk.Index),
					TimestampMs:      chunk.TimestampMs,
					LatencyMs:        chunk.LatencyMs,
					TokenCount:       int32(chunk.TokenCount),
					CumulativeTokens: int32(chunk.CumulativeTokens),
				}
			}
		}
	}

	// Map function executions if available
	if len(log.FunctionExecutions) > 0 {
		logger.WithFields(
			"correlation_id", log.CorrelationID,
			"function_executions_count", len(log.FunctionExecutions),
		).Debug("mapping function executions to proto")

		pbLog.FunctionExecutions = make([]*logspb.FunctionExecution, len(log.FunctionExecutions))
		for i, exec := range log.FunctionExecutions {
			pbLog.FunctionExecutions[i] = &logspb.FunctionExecution{
				FunctionId:    exec.FunctionID,
				FunctionName:  exec.FunctionName,
				Runtime:       exec.Runtime,
				Backend:       exec.Backend,
				ExecutionMode: exec.ExecutionMode,
				DurationMs:    exec.DurationMs,
				Success:       exec.Success,
				Error:         exec.Error,
				ErrorType:     exec.ErrorType,
				Stdout:        exec.Stdout,
				Stderr:        exec.Stderr,
			}
		}
	} else {
		logger.WithFields(
			"correlation_id", log.CorrelationID,
		).Debug("no function executions for log")
	}

	return pbLog
}

// ============================================================================
// Custom log columns
// ============================================================================

func logColumnToProto(c logcolumns.Column) *logspb.LogCustomColumnDef {
	return &logspb.LogCustomColumnDef{
		Key:      c.Key,
		Label:    c.Label,
		AttrKey:  c.AttrKey,
		Position: c.Position,
	}
}

// ListLogCustomColumns returns the tenant's registered log columns.
func (s *Server) ListLogCustomColumns(
	ctx context.Context,
	_ *connect.Request[logspb.ListLogCustomColumnsRequest],
) (*connect.Response[logspb.ListLogCustomColumnsResponse], error) {
	if s.logColumns == nil {
		return connect.NewResponse(&logspb.ListLogCustomColumnsResponse{}), nil
	}
	defs, err := s.logColumns.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	cols := make([]*logspb.LogCustomColumnDef, 0, len(defs))
	for _, d := range defs {
		cols = append(cols, logColumnToProto(d))
	}
	return connect.NewResponse(&logspb.ListLogCustomColumnsResponse{Columns: cols}), nil
}

// UpsertLogCustomColumn creates or updates a log column definition.
func (s *Server) UpsertLogCustomColumn(
	ctx context.Context,
	req *connect.Request[logspb.UpsertLogCustomColumnRequest],
) (*connect.Response[logspb.UpsertLogCustomColumnResponse], error) {
	if s.logColumns == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("log custom columns not configured"))
	}
	in := req.Msg.GetColumn()
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("column is required"))
	}
	col := &logcolumns.Column{
		Key:      strings.TrimSpace(in.GetKey()),
		Label:    in.GetLabel(),
		AttrKey:  strings.TrimSpace(in.GetAttrKey()),
		Position: in.GetPosition(),
	}
	if err := s.logColumns.Put(ctx, col); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&logspb.UpsertLogCustomColumnResponse{Column: logColumnToProto(*col)}), nil
}

// DeleteLogCustomColumn removes a log column definition by key.
func (s *Server) DeleteLogCustomColumn(
	ctx context.Context,
	req *connect.Request[logspb.DeleteLogCustomColumnRequest],
) (*connect.Response[logspb.DeleteLogCustomColumnResponse], error) {
	if s.logColumns == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("log custom columns not configured"))
	}
	if err := s.logColumns.Delete(ctx, strings.TrimSpace(req.Msg.GetKey())); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&logspb.DeleteLogCustomColumnResponse{}), nil
}
