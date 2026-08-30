package v1

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/classificationrules"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/customcolumns"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	traceshandler "github.com/everstacklabs/everstack/internal/query/handlers/traces"
	"github.com/everstacklabs/everstack/internal/query/handlers/traces/enhanced"
	"github.com/everstacklabs/everstack/internal/semanticmappings"
	"github.com/everstacklabs/everstack/internal/telemetry/scores"
	"github.com/everstacklabs/everstack/internal/telemetry/traceoverlays"
	"github.com/everstacklabs/everstack/internal/traceviews"
	tracespb "github.com/everstacklabs/everstack/pkg/grpc/everstack/traces/v1"
	tracesconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/traces/v1/tracesconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements the TracesService gRPC server
type Server struct {
	ctx                 context.Context
	scoreRecorder       *scores.Recorder
	overlayRecorder     *traceoverlays.Recorder
	customColumns       *customcolumns.Store
	traceViews          *traceviews.Store
	semanticMappings    *semanticmappings.Store
	classificationRules *classificationrules.Store
}

// SetCustomColumnStore wires the tenant-scoped custom-column registry so the
// traces list can resolve user-defined columns and the column-management RPCs
// can read/write definitions.
func (s *Server) SetCustomColumnStore(store *customcolumns.Store) {
	s.customColumns = store
}

// SetTraceViewStore wires the saved-view registry for the view-management RPCs.
func (s *Server) SetTraceViewStore(store *traceviews.Store) {
	s.traceViews = store
}

// SetSemanticMappingStore wires the tenant semantic-mapping registry so the
// list query can fold tenant attribute aliases into the typed-field coalesce.
func (s *Server) SetSemanticMappingStore(store *semanticmappings.Store) {
	s.semanticMappings = store
}

// SetClassificationRuleStore wires the tenant classification-rule registry so
// the list query can fold tenant SpanName-pattern -> kind rules into trace_kinds.
func (s *Server) SetClassificationRuleStore(store *classificationrules.Store) {
	s.classificationRules = store
}

// CreateServerWithContext creates a new traces gRPC server with context
func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

// SetScoreRecorder sets the score recorder for scoring RPCs
func (s *Server) SetScoreRecorder(recorder *scores.Recorder) {
	s.scoreRecorder = recorder
}

// SetOverlayRecorder sets the append-only recorder for trace overlays and
// custom observations. Raw OTEL spans remain immutable.
func (s *Server) SetOverlayRecorder(recorder *traceoverlays.Recorder) {
	s.overlayRecorder = recorder
}

// RegisterConnectServer registers the Connect RPC handler
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return tracesconnect.NewTracesServiceHandler(s, connect.WithInterceptors(interceptors...))
}

// FileDescriptor returns the file descriptor for this service
func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return tracespb.File_everstack_traces_v1_traces_service_proto
}

// AppName returns the service name
func (s *Server) AppName() string {
	return tracesconnect.TracesServiceName
}

// MethodPrefix returns the method prefix
func (s *Server) MethodPrefix() string {
	return tracesconnect.TracesServiceName
}

// RegisterGateway mounts the TracesService grpc-gateway REST handlers (the
// google.api.http routes, e.g. POST /v1/traces/observations/custom) onto the
// JSON gateway mux. Implementing grpcserver.GatewayRegistrable makes api.go
// register this automatically. Without it the REST routes 404 and the
// REST-based SDKs (the Python client) cannot reach the traces endpoints.
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return tracespb.RegisterTracesServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// ListTraces streams traces from ClickHouse OTEL traces
// Server-streaming: initial backfill + real-time tail
func (s *Server) ListTraces(
	ctx context.Context,
	req *connect.Request[tracespb.ListTracesRequest],
	stream *connect.ServerStream[tracespb.Trace],
) error {
	// Get CQRS system from context
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	// Parse time range
	from := time.Now().Add(-24 * time.Hour)
	if req.Msg.GetFrom() != nil {
		from = req.Msg.GetFrom().AsTime()
	}

	to := time.Time{}
	if req.Msg.GetTo() != nil {
		to = req.Msg.GetTo().AsTime()
	}

	// Build query
	limit := int(req.Msg.GetLimit())
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	offset := int(req.Msg.GetOffset())
	if offset < 0 {
		offset = 0
	}

	// Track trace_id -> fingerprint of the mutable aggregate, not a bare "seen"
	// flag. A trace is not immutable once observed: an in-flight one keeps
	// accumulating spans, tokens, cost and duration for as long as the work
	// runs. Sending it once and suppressing it forever froze every live row at
	// whatever it looked like a second after it started, so the list only ever
	// caught up on a full page reload. Re-send whenever the fingerprint moves.
	sent := make(map[string]uint64, 1024)

	// Load the tenant's custom-column definitions once; each streamed trace
	// resolves its values from already-fetched data (no extra per-trace query).
	var ccDefs []customcolumns.StoredColumn
	if s.customColumns != nil {
		if defs, derr := s.customColumns.List(ctx); derr != nil {
			logger.WithFields("error", derr.Error()).Warn("failed to load custom columns; continuing without")
		} else {
			ccDefs = defs
		}
	}

	// Operational/noise traces (health checks, external interaction wrappers)
	// are hidden unless the caller opts in.
	includeOperational := req.Msg.GetIncludeOperational()

	// Attribute-sourced custom columns are resolved inside the list query;
	// metadata-sourced ones are resolved in Go from the returned metadata.
	ccAttrCols := customAttrColumnsFromDefs(ccDefs)

	// Tenant semantic mappings: fold a tenant's own attribute names into the
	// typed-field coalesce for this query.
	var semMappings map[string][]string
	if s.semanticMappings != nil {
		if m, merr := s.semanticMappings.AsMappings(ctx); merr != nil {
			logger.WithFields("error", merr.Error()).Warn("failed to load semantic mappings; continuing without")
		} else if len(m) > 0 {
			semMappings = m
		}
	}

	// Tenant classification rules extend the trace_kinds derivation.
	var classRules []traceshandler.ClassificationRule
	if s.classificationRules != nil {
		if rules, rerr := s.classificationRules.List(ctx); rerr != nil {
			logger.WithFields("error", rerr.Error()).Warn("failed to load classification rules; continuing without")
		} else {
			for _, r := range rules {
				classRules = append(classRules, traceshandler.ClassificationRule{Pattern: r.Pattern, Kind: r.Kind})
			}
		}
	}

	// Send initial batch
	correlationID := req.Msg.GetCorrelationId()
	listQuery := traceshandler.NewListTracesQuery(
		contextkeys.GetTenantID(ctx),
		from,
		to,
		req.Msg.GetModel(),
		req.Msg.GetProvider(),
		req.Msg.GetStatusCode(),
		correlationID,
		"", // userID
		"", // traceID for tracking
	)
	listQuery.Limit = limit
	listQuery.Offset = offset
	listQuery.CustomAttrColumns = ccAttrCols
	listQuery.SemanticMappings = semMappings
	listQuery.ClassificationRules = classRules
	applyTraceFilterClauses(listQuery, req.Msg.GetFilters())

	// Apply multi-dimension filters (P0.3)
	if req.Msg.UserId != nil {
		listQuery.FilterUserID = *req.Msg.UserId
	}
	if req.Msg.SessionId != nil {
		listQuery.FilterSessionID = *req.Msg.SessionId
	}
	if req.Msg.ThreadId != nil {
		listQuery.FilterThreadID = *req.Msg.ThreadId
	}
	if req.Msg.Query != nil {
		listQuery.FullTextQuery = *req.Msg.Query
	}
	if len(req.Msg.Metadata) > 0 {
		listQuery.Metadata = req.Msg.Metadata
	}
	if req.Msg.Environment != nil {
		listQuery.Environment = *req.Msg.Environment
	}
	if len(req.Msg.Tags) > 0 {
		listQuery.Tags = req.Msg.Tags
	}
	if req.Msg.MinCost != nil {
		listQuery.MinCost = req.Msg.MinCost
	}
	if req.Msg.MaxCost != nil {
		listQuery.MaxCost = req.Msg.MaxCost
	}
	if req.Msg.MinDurationNs != nil {
		listQuery.MinDurationNs = req.Msg.MinDurationNs
	}
	if req.Msg.MaxDurationNs != nil {
		listQuery.MaxDurationNs = req.Msg.MaxDurationNs
	}

	result, err := sys.QueryBus.Execute(ctx, listQuery)
	if err == nil {
		response, ok := result.(*query.Response)
		if !ok {
			logger.WithFields("result_type", result).Error("invalid result type from ListTracesQuery")
		} else if traces, ok := response.Data.([]query.TraceReadModel); !ok {
			logger.WithFields("result_type", response.Data).Error("invalid data type in query response")
		} else {
			logger.WithFields("trace_count", len(traces)).Debug("sending initial traces batch to client")
			ccScores := s.scoresByTraceForPage(ctx, traces, ccDefs)
			for _, trace := range traces {
				if trace.TraceID == "" {
					continue
				}
				pbTrace := toProtoTrace(&trace)
				if pbTrace.IsOperational && !includeOperational {
					continue
				}
				resolveCustomColumns(ccDefs, pbTrace, ccScores[trace.TraceID])
				if err := stream.Send(pbTrace); err != nil {
					logger.WithError(err).Error("failed to send trace to client")
					return err
				}
				sent[trace.TraceID] = traceFingerprint(&trace)
			}
			logger.WithFields("sent_count", len(sent)).Debug("initial traces batch sent successfully")
		}
	} else {
		// Context canceled is expected when client disconnects
		if ctx.Err() != nil {
			logger.Debug("initial traces fetch canceled by client")
			return nil
		}
		if len(req.Msg.GetFilters()) > 0 {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		logger.WithError(err).Error("failed to fetch initial traces")
	}

	// Tail loop: poll every 1s for new traces (matching batch timeout)
	// Skip tail loop if 'to' is provided (historical/paused mode) OR if offset > 0 (pagination request)
	if !to.IsZero() || offset > 0 {
		logger.WithFields("to_provided", !to.IsZero(), "offset", offset).Debug("skipping tail loop (historical mode or pagination)")
		return nil
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Keep buffer size manageable
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

			// Aggregate over the SAME range as the initial batch, restricted to
			// traces touched in the last 60s. Passing the 60s mark as the range
			// start instead would re-aggregate long-running traces over only
			// their most recent spans, so a live row's span count and duration
			// would shrink on every tick. See ListTracesQuery.ActiveSince.
			correlationID := req.Msg.GetCorrelationId()
			tailQuery := traceshandler.NewListTracesQuery(
				contextkeys.GetTenantID(ctx),
				from,
				time.Time{}, // no 'to' for live mode
				req.Msg.GetModel(),
				req.Msg.GetProvider(),
				req.Msg.GetStatusCode(),
				correlationID,
				"",
				"",
			)
			tailQuery.Limit = 100
			tailQuery.ActiveSince = time.Now().Add(-60 * time.Second)
			tailQuery.CustomAttrColumns = ccAttrCols
			tailQuery.SemanticMappings = semMappings
			tailQuery.ClassificationRules = classRules
			applyTraceFilterClauses(tailQuery, req.Msg.GetFilters())

			result, err := sys.QueryBus.Execute(ctx, tailQuery)
			if err != nil {
				if len(req.Msg.GetFilters()) > 0 {
					return connect.NewError(connect.CodeInvalidArgument, err)
				}
				continue
			}

			response, ok := result.(*query.Response)
			if !ok {
				continue
			}

			traces, ok := response.Data.([]query.TraceReadModel)
			if !ok {
				continue
			}

			ccScores := s.scoresByTraceForPage(ctx, traces, ccDefs)

			for _, trace := range traces {
				if trace.TraceID == "" {
					continue
				}

				// Skip only if nothing about the trace has changed since we
				// last sent it. The client upserts by trace id, so a re-send is
				// an update of the existing row, not a duplicate.
				fp := traceFingerprint(&trace)
				if prev, ok := sent[trace.TraceID]; ok && prev == fp {
					continue
				}

				pbTrace := toProtoTrace(&trace)
				if pbTrace.IsOperational && !includeOperational {
					continue
				}
				resolveCustomColumns(ccDefs, pbTrace, ccScores[trace.TraceID])
				if err := stream.Send(pbTrace); err != nil {
					return err
				}
				sent[trace.TraceID] = fp
			}
		}
	}
}

func applyTraceFilterClauses(listQuery *traceshandler.ListTracesQuery, filters []*tracespb.TraceFilterClause) {
	for _, f := range filters {
		listQuery.Clauses = append(listQuery.Clauses, traceshandler.TraceFilterClause{
			Scope: traceshandler.ClauseScope(f.GetScope()),
			Field: f.GetField(),
			Op:    traceshandler.ClauseOp(f.GetOp()),
			Value: f.GetValue(),
		})
	}
}

// GetTraceByID retrieves all spans for a specific trace
func (s *Server) GetTraceByID(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceByIDRequest],
) (*connect.Response[tracespb.GetTraceByIDResponse], error) {
	// Get CQRS system from context
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	getByIDQuery := traceshandler.NewGetTraceByIDQuery(req.Msg.GetTraceId(), "", "")

	result, err := sys.QueryBus.Execute(ctx, getByIDQuery)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("trace not found: %s", req.Msg.GetTraceId()))
		}
		logger.WithError(err).Error("failed to get trace by ID")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	spans, ok := response.Data.([]query.SpanReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbSpans := make([]*tracespb.Span, len(spans))
	for i, span := range spans {
		pbSpans[i] = toProtoSpan(&span)
	}
	if s.overlayRecorder != nil {
		custom, err := s.overlayRecorder.ListObservations(ctx, req.Msg.GetTraceId())
		if err != nil {
			logger.WithFields("trace_id", req.Msg.GetTraceId(), "error", err.Error()).Warn("failed to load custom observations")
		} else {
			for _, obs := range custom {
				pbSpans = append(pbSpans, customObservationToSpan(obs))
			}
		}
	}

	return connect.NewResponse(&tracespb.GetTraceByIDResponse{
		Spans: pbSpans,
	}), nil
}

// GetTrace retrieves a single aggregated Trace by ID
func (s *Server) GetTrace(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceRequest],
) (*connect.Response[tracespb.GetTraceResponse], error) {
	// Get CQRS system from context
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	getTraceQuery := traceshandler.NewGetTraceQuery(req.Msg.GetTraceId(), "", "")

	result, err := sys.QueryBus.Execute(ctx, getTraceQuery)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if trace := s.customOnlyTrace(ctx, req.Msg.GetTraceId()); trace != nil {
				return connect.NewResponse(&tracespb.GetTraceResponse{Trace: trace}), nil
			}
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("trace not found: %s", req.Msg.GetTraceId()))
		}
		logger.WithError(err).Error("failed to get trace")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	trace, ok := response.Data.(query.TraceReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbTrace := toProtoTrace(&trace)
	s.applyOverlayToTrace(ctx, pbTrace)

	return connect.NewResponse(&tracespb.GetTraceResponse{
		Trace: pbTrace,
	}), nil
}

// GetTraceTree retrieves a trace in hierarchical tree structure
func (s *Server) GetTraceTree(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceTreeRequest],
) (*connect.Response[tracespb.GetTraceTreeResponse], error) {
	// Get CQRS system from context
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	treeQuery := traceshandler.NewGetTraceTreeQuery(req.Msg.GetTraceId(), "", "")

	result, err := sys.QueryBus.Execute(ctx, treeQuery)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if root := s.customOnlyTraceTree(ctx, req.Msg.GetTraceId()); root != nil {
				return connect.NewResponse(&tracespb.GetTraceTreeResponse{Root: root}), nil
			}
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("trace tree not found: %s", req.Msg.GetTraceId()))
		}
		logger.WithError(err).Error("failed to get trace tree")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	treeNode, ok := response.Data.(*query.SpanTreeNode)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbTreeNode := toProtoSpanTreeNode(treeNode)
	if s.overlayRecorder != nil {
		custom, err := s.overlayRecorder.ListObservations(ctx, req.Msg.GetTraceId())
		if err != nil {
			logger.WithFields("trace_id", req.Msg.GetTraceId(), "error", err.Error()).Warn("failed to load custom observations")
		} else {
			appendCustomObservationsToTree(pbTreeNode, custom)
		}
	}

	return connect.NewResponse(&tracespb.GetTraceTreeResponse{
		Root: pbTreeNode,
	}), nil
}

// GetTraceLogs returns the OTLP log records correlated to a trace.
func (s *Server) GetTraceLogs(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceLogsRequest],
) (*connect.Response[tracespb.GetTraceLogsResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logsQuery := traceshandler.NewGetTraceLogsQuery(req.Msg.GetTraceId(), req.Msg.GetSessionId())

	result, err := sys.QueryBus.Execute(ctx, logsQuery)
	if err != nil {
		logger.WithError(err).Error("failed to get trace logs")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}
	records, ok := response.Data.([]query.TraceLogReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbRecords := make([]*tracespb.TraceLogRecord, 0, len(records))
	for i := range records {
		r := &records[i]
		pbRecords = append(pbRecords, &tracespb.TraceLogRecord{
			Timestamp:      timestamppb.New(r.Timestamp),
			SeverityText:   r.SeverityText,
			SeverityNumber: r.SeverityNumber,
			Body:           r.Body,
			SpanId:         r.SpanID,
			ScopeName:      r.ScopeName,
			ServiceName:    r.ServiceName,
			Attributes:     r.Attributes,
		})
	}

	return connect.NewResponse(&tracespb.GetTraceLogsResponse{Records: pbRecords}), nil
}

// Helper functions to convert query models to proto messages

func toProtoTrace(trace *query.TraceReadModel) *tracespb.Trace {
	// Determine status based on root span's status (not error count)
	// This ensures non-critical errors (like cache misses) don't mark the trace as failed
	status := "success"
	if strings.EqualFold(trace.RootStatus, "error") {
		status = "error"
	}

	return &tracespb.Trace{
		TraceId:        trace.TraceID,
		StartTime:      timestamppb.New(trace.StartTime),
		EndTime:        timestamppb.New(trace.EndTime),
		TotalDuration:  trace.TotalDuration, // int64 matches proto definition
		ErrorCount:     int32(trace.ErrorCount),
		RequestedModel: trace.RequestedModel,
		ServedModel:    trace.ServedModel,
		LlmModel:       trace.LLMModel,
		Provider:       trace.Provider,
		TenantId:       trace.TenantID,
		SpanCount:      int32(trace.SpanCount),
		Status:         status,
		// Rich trace fields
		TraceInput:      trace.TraceInput,
		TraceOutput:     trace.TraceOutput,
		UserId:          trace.UserID,
		SessionId:       trace.SessionID,
		ThreadId:        trace.ThreadID,
		TotalCost:       trace.TotalCost,
		ModelParameters: trace.ModelParameters,
		TokenBreakdown:  buildListTokenBreakdown(trace),
		Metadata:        parseMetadataJSON(trace.Metadata),
		TraceKinds:      trace.TraceKinds,
		// Attribute-sourced custom columns resolved by the list query, plus the
		// reserved __everstack_* keys that drive the Trace Name column.
		// Metadata-sourced ones are merged in by resolveCustomColumns.
		CustomColumns: withDerivedColumns(trace),
		IsOperational: isOperationalTrace(trace),
	}
}

// traceFingerprint hashes the fields of a trace that change while it is still
// running, so the live tail can tell "this trace grew" from "this trace is
// exactly as I last sent it" without diffing whole messages.
//
// Only mutable aggregates go in. Identity (trace id) and immutable facts
// (start time) are deliberately excluded: including them would not change any
// comparison, since the fingerprint is only ever compared against the previous
// value for the same trace id. Root-derived fields are included because they go
// from empty to populated the moment the root span lands, which is a real
// change the client needs to see.
func traceFingerprint(t *query.TraceReadModel) uint64 {
	h := fnv.New64a()
	var scratch [8]byte
	put := func(v uint64) {
		binary.LittleEndian.PutUint64(scratch[:], v)
		_, _ = h.Write(scratch[:])
	}
	put(t.SpanCount)
	put(uint64(t.EndTime.UnixNano()))
	put(uint64(t.TotalDuration))
	put(t.ErrorCount)
	put(uint64(t.TotalTokens))
	put(math.Float64bits(t.TotalCost))
	_, _ = h.Write([]byte(t.RootStatus))
	_, _ = h.Write([]byte(t.ServedModel))
	_, _ = h.Write([]byte(t.SessionID))
	return h.Sum64()
}

// isOperationalTrace reports whether a trace is operational/noise: no model, no
// tokens, no cost, and no recognized execution kind. These are health checks
// (tenantcheck-*) or external interaction wrappers (e.g. a coding agent's
// top-level span) rather than real LLM/agent/workflow work, so the list hides
// them unless the caller opts in.
func isOperationalTrace(t *query.TraceReadModel) bool {
	hasModel := t.LLMModel != "" || t.ServedModel != "" || t.RequestedModel != ""
	hasTokens := t.TotalTokens > 0 || t.InputTokens > 0 || t.OutputTokens > 0
	return !hasModel && !hasTokens && t.TotalCost <= 0 && len(t.TraceKinds) == 0
}

const (
	reservedTraceNameKey = "__everstack_trace_name"
	reservedClientKey    = "__everstack_client"
)

// withDerivedColumns returns the trace's attribute-sourced custom columns with
// two reserved keys injected for the traces-table "Trace Name" column:
//
//	__everstack_client     — agent/client key for the logo (claude-code, codex, ...)
//	__everstack_trace_name — display name: the reserved agent name for a coding
//	                         agent, else the root span's trace.name attr / span name
//
// The "__everstack_" prefix keeps these out of the generic custom-column
// renderer, which only shows keys matching a user-defined column definition.
func withDerivedColumns(trace *query.TraceReadModel) map[string]string {
	out := trace.CustomAttrValues
	clientKey, clientName := deriveAgentClient(trace.ServiceName, trace.ScopeName)
	traceName := clientName
	if traceName == "" {
		traceName = firstNonEmptyStr(trace.TraceNameAttr, trace.RootSpanName)
	}
	if clientKey == "" && traceName == "" {
		return out
	}
	if out == nil {
		out = map[string]string{}
	}
	if traceName != "" {
		out[reservedTraceNameKey] = traceName
	}
	if clientKey != "" {
		out[reservedClientKey] = clientKey
	}
	return out
}

// deriveAgentClient maps a trace's emitter (service name + instrumentation
// scope) to a known coding agent: clientKey selects the logo, displayName is
// the reserved label. Returns ("","") for non-agent traces.
func deriveAgentClient(serviceName, scopeName string) (clientKey, displayName string) {
	s := strings.ToLower(serviceName)
	scope := strings.ToLower(scopeName)
	switch {
	case s == "claude-code" || strings.Contains(scope, "claude_code"):
		return "claude-code", "Claude Code"
	case strings.Contains(s, "codex") || strings.Contains(scope, "codex"):
		return "codex", "Codex"
	case strings.Contains(s, "gemini") || strings.Contains(scope, "gemini"):
		return "gemini-cli", "Gemini CLI"
	case strings.Contains(s, "cursor") || strings.Contains(scope, "cursor"):
		return "cursor", "Cursor"
	case strings.Contains(s, "copilot") || strings.Contains(scope, "copilot"):
		return "github-copilot", "GitHub Copilot"
	}
	return "", ""
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// buildListTokenBreakdown maps the list query's aggregated token counts into the
// proto TokenBreakdown so the traces table can show total/input/output/cached/
// reasoning tokens. Returns nil when no tokens were recorded.
func buildListTokenBreakdown(trace *query.TraceReadModel) *tracespb.TokenBreakdown {
	if trace.InputTokens == 0 && trace.OutputTokens == 0 && trace.TotalTokens == 0 &&
		trace.CachedTokens == 0 && trace.ReasoningTokens == 0 {
		return nil
	}
	total := trace.TotalTokens
	if total == 0 {
		total = trace.InputTokens + trace.OutputTokens
	}
	tb := &tracespb.TokenBreakdown{
		InputTokens:  trace.InputTokens,
		OutputTokens: trace.OutputTokens,
		TotalTokens:  total,
	}
	if trace.CachedTokens > 0 {
		c := trace.CachedTokens
		tb.PromptDetails = &tracespb.TokenDetails{CachedTokens: &c}
	}
	if trace.ReasoningTokens > 0 {
		r := trace.ReasoningTokens
		tb.CompletionDetails = &tracespb.TokenDetails{ReasoningTokens: &r}
	}
	return tb
}

// parseMetadataJSON parses the root span's trace.metadata JSON into a string map
// for the traces table Metadata column. Non-string values are stringified.
func parseMetadataJSON(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		return m
	}
	var generic map[string]interface{}
	if err := json.Unmarshal([]byte(s), &generic); err == nil {
		out := make(map[string]string, len(generic))
		for k, v := range generic {
			out[k] = fmt.Sprintf("%v", v)
		}
		return out
	}
	return nil
}

func toProtoSpan(span *query.SpanReadModel) *tracespb.Span {
	// Convert events
	var events []*tracespb.SpanEvent
	for _, e := range span.Events {
		events = append(events, &tracespb.SpanEvent{
			Timestamp:  timestamppb.New(e.Timestamp),
			Name:       e.Name,
			Attributes: e.Attributes,
		})
	}

	return &tracespb.Span{
		TraceId:            span.TraceID,
		SpanId:             span.SpanID,
		ParentSpanId:       span.ParentSpanID,
		SpanName:           span.SpanName,
		SpanKind:           span.SpanKind,
		Timestamp:          timestamppb.New(span.Timestamp),
		Duration:           span.Duration, // Already int64 from ClickHouse Int64
		StatusCode:         span.StatusCode,
		StatusMessage:      span.StatusMessage,
		SpanAttributes:     span.SpanAttributes,
		ResourceAttributes: span.ResourceAttributes,
		Events:             events,
	}
}

func toProtoSpanTreeNode(node *query.SpanTreeNode) *tracespb.SpanTreeNode {
	pbNode := &tracespb.SpanTreeNode{
		Span:     toProtoSpan(&node.Span),
		Children: make([]*tracespb.SpanTreeNode, len(node.Children)),
	}

	for i, child := range node.Children {
		pbNode.Children[i] = toProtoSpanTreeNode(child)
	}

	return pbNode
}

func (s *Server) applyOverlayToTrace(ctx context.Context, trace *tracespb.Trace) {
	if s.overlayRecorder == nil || trace == nil || trace.TraceId == "" {
		return
	}
	overlay, err := s.overlayRecorder.GetOverlay(ctx, trace.TraceId)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			logger.WithFields("trace_id", trace.TraceId, "error", err.Error()).Warn("failed to load trace overlay")
		}
		return
	}
	if trace.Metadata == nil {
		trace.Metadata = map[string]string{}
	}
	if overlay.DisplayName != nil && *overlay.DisplayName != "" {
		trace.Metadata["display_name"] = *overlay.DisplayName
	}
	if overlay.InputOverride != nil {
		trace.TraceInput = *overlay.InputOverride
	}
	if overlay.OutputOverride != nil {
		trace.TraceOutput = *overlay.OutputOverride
	}
	for k, v := range overlay.Metadata {
		trace.Metadata[k] = v
	}
	if len(overlay.Tags) > 0 {
		trace.Metadata["tags"] = strings.Join(overlay.Tags, ",")
	}
	if len(overlay.HiddenSpanIDs) > 0 {
		trace.Metadata["hidden_span_ids"] = strings.Join(overlay.HiddenSpanIDs, ",")
	}
}

type customTraceParts struct {
	overlay      *traceoverlays.Overlay
	observations []*traceoverlays.Observation
}

func (s *Server) loadCustomTraceParts(ctx context.Context, traceID string) *customTraceParts {
	if s.overlayRecorder == nil || traceID == "" {
		return nil
	}
	parts := &customTraceParts{}
	if overlay, err := s.overlayRecorder.GetOverlay(ctx, traceID); err == nil {
		parts.overlay = overlay
	} else if !errors.Is(err, sql.ErrNoRows) {
		logger.WithFields("trace_id", traceID, "error", err.Error()).Warn("failed to load trace overlay")
	}
	if observations, err := s.overlayRecorder.ListObservations(ctx, traceID); err == nil {
		parts.observations = observations
	} else {
		logger.WithFields("trace_id", traceID, "error", err.Error()).Warn("failed to load custom observations")
	}
	if parts.overlay == nil && len(parts.observations) == 0 {
		return nil
	}
	return parts
}

func (s *Server) customOnlyTrace(ctx context.Context, traceID string) *tracespb.Trace {
	parts := s.loadCustomTraceParts(ctx, traceID)
	if parts == nil {
		return nil
	}

	start, end := customTraceBounds(parts)
	trace := &tracespb.Trace{
		TraceId:       traceID,
		StartTime:     timestamppb.New(start),
		EndTime:       timestamppb.New(end),
		TotalDuration: end.Sub(start).Nanoseconds(),
		SpanCount:     int32(len(parts.observations)),
		Status:        "success",
		Metadata: map[string]string{
			"trace.source": "custom",
		},
	}
	for _, obs := range parts.observations {
		if strings.EqualFold(obs.Level, "ERROR") {
			trace.Status = "error"
			trace.ErrorCount++
		}
		if obs.InputTokens != nil && trace.TokenBreakdown == nil {
			trace.TokenBreakdown = &tracespb.TokenBreakdown{}
		}
		if trace.TokenBreakdown != nil {
			if obs.InputTokens != nil {
				trace.TokenBreakdown.InputTokens += *obs.InputTokens
			}
			if obs.OutputTokens != nil {
				trace.TokenBreakdown.OutputTokens += *obs.OutputTokens
			}
			if obs.TotalTokens != nil {
				trace.TokenBreakdown.TotalTokens += *obs.TotalTokens
			}
		}
		if obs.TotalCost != nil {
			trace.TotalCost += *obs.TotalCost
		}
	}
	if parts.overlay != nil {
		applyOverlayToTrace(trace, parts.overlay)
	}
	return trace
}

func (s *Server) customOnlyTraceTree(ctx context.Context, traceID string) *tracespb.SpanTreeNode {
	parts := s.loadCustomTraceParts(ctx, traceID)
	if parts == nil {
		return nil
	}
	start, end := customTraceBounds(parts)
	name := "custom_trace"
	if parts.overlay != nil && parts.overlay.DisplayName != nil && *parts.overlay.DisplayName != "" {
		name = *parts.overlay.DisplayName
	}
	root := &tracespb.SpanTreeNode{
		Span: &tracespb.Span{
			TraceId:    traceID,
			SpanId:     traceID + ":custom-root",
			SpanName:   name,
			SpanKind:   "SPAN_KIND_INTERNAL",
			Timestamp:  timestamppb.New(start),
			Duration:   end.Sub(start).Nanoseconds(),
			StatusCode: "OK",
			SpanAttributes: map[string]string{
				"trace.source":  "custom",
				"trace.overlay": "true",
			},
			ResourceAttributes: map[string]string{
				"service.name": "everstack-trace-overlay",
			},
		},
	}
	appendCustomObservationsToTree(root, parts.observations)
	return root
}

func (s *Server) customOnlyRichTrace(ctx context.Context, traceID string) *tracespb.RichTrace {
	parts := s.loadCustomTraceParts(ctx, traceID)
	if parts == nil {
		return nil
	}
	start, _ := customTraceBounds(parts)
	trace := &tracespb.RichTrace{
		Id:           traceID,
		Timestamp:    timestamppb.New(start),
		Observations: make([]*tracespb.RichObservation, 0, len(parts.observations)),
	}
	for _, obs := range parts.observations {
		richObs := customObservationToRichObservation(obs)
		trace.Observations = append(trace.Observations, richObs)
		if richObs.Latency != nil && *richObs.Latency > trace.Latency {
			trace.Latency = *richObs.Latency
		}
		if richObs.TotalCost != nil {
			trace.TotalCost += *richObs.TotalCost
		}
	}
	if parts.overlay != nil {
		applyOverlayToRichTrace(trace, parts.overlay)
	}
	return trace
}

func customTraceBounds(parts *customTraceParts) (time.Time, time.Time) {
	now := time.Now()
	start := now
	end := now
	if parts != nil && parts.overlay != nil && !parts.overlay.UpdatedAt.IsZero() {
		start = parts.overlay.UpdatedAt
		end = parts.overlay.UpdatedAt
	}
	if parts != nil {
		for _, obs := range parts.observations {
			if obs == nil {
				continue
			}
			if obs.StartTime.Before(start) {
				start = obs.StartTime
			}
			obsEnd := obs.StartTime.Add(time.Duration(obs.Duration))
			if obs.EndTime != nil {
				obsEnd = *obs.EndTime
			}
			if obsEnd.After(end) {
				end = obsEnd
			}
		}
	}
	return start, end
}

func applyOverlayToTrace(trace *tracespb.Trace, overlay *traceoverlays.Overlay) {
	if trace == nil || overlay == nil {
		return
	}
	if trace.Metadata == nil {
		trace.Metadata = map[string]string{}
	}
	if overlay.DisplayName != nil && *overlay.DisplayName != "" {
		trace.Metadata["display_name"] = *overlay.DisplayName
	}
	if overlay.InputOverride != nil {
		trace.TraceInput = *overlay.InputOverride
	}
	if overlay.OutputOverride != nil {
		trace.TraceOutput = *overlay.OutputOverride
	}
	for k, v := range overlay.Metadata {
		trace.Metadata[k] = v
	}
	if len(overlay.Tags) > 0 {
		trace.Metadata["tags"] = strings.Join(overlay.Tags, ",")
	}
	if len(overlay.HiddenSpanIDs) > 0 {
		trace.Metadata["hidden_span_ids"] = strings.Join(overlay.HiddenSpanIDs, ",")
	}
}

func customObservationToSpan(obs *traceoverlays.Observation) *tracespb.Span {
	attrs := map[string]string{
		"observation.type":   obs.Type,
		"observation.level":  obs.Level,
		"observation.source": obs.Source,
		"trace.overlay":      "true",
	}
	for k, v := range obs.Metadata {
		attrs[k] = v
	}
	if obs.Model != "" {
		attrs["llm.request.model"] = obs.Model
		attrs["llm.response.model"] = obs.Model
	}
	if obs.InputData != "" {
		attrs["input.value"] = obs.InputData
	}
	if obs.OutputData != "" {
		attrs["output.value"] = obs.OutputData
	}
	if obs.InputTokens != nil {
		attrs["llm.tokens.input"] = fmt.Sprintf("%d", *obs.InputTokens)
	}
	if obs.OutputTokens != nil {
		attrs["llm.tokens.output"] = fmt.Sprintf("%d", *obs.OutputTokens)
	}
	if obs.TotalTokens != nil {
		attrs["llm.tokens.total"] = fmt.Sprintf("%d", *obs.TotalTokens)
	}
	if obs.TotalCost != nil {
		attrs["llm.cost.total"] = fmt.Sprintf("%f", *obs.TotalCost)
	}
	return &tracespb.Span{
		TraceId:        obs.TraceID,
		SpanId:         obs.ID,
		ParentSpanId:   obs.ParentObservationID,
		SpanName:       obs.Name,
		SpanKind:       "SPAN_KIND_INTERNAL",
		Timestamp:      timestamppb.New(obs.StartTime),
		Duration:       obs.Duration,
		StatusCode:     statusCodeFromObservationLevel(obs.Level),
		StatusMessage:  obs.StatusMessage,
		SpanAttributes: attrs,
		ResourceAttributes: map[string]string{
			"service.name": "everstack-trace-overlay",
		},
	}
}

func appendCustomObservationsToTree(root *tracespb.SpanTreeNode, observations []*traceoverlays.Observation) {
	if root == nil || root.Span == nil {
		return
	}
	nodesByID := map[string]*tracespb.SpanTreeNode{}
	var index func(*tracespb.SpanTreeNode)
	index = func(node *tracespb.SpanTreeNode) {
		if node == nil || node.Span == nil {
			return
		}
		nodesByID[node.Span.SpanId] = node
		for _, child := range node.Children {
			index(child)
		}
	}
	index(root)

	for _, obs := range observations {
		node := &tracespb.SpanTreeNode{Span: customObservationToSpan(obs)}
		parent := root
		if obs.ParentObservationID != "" {
			if found := nodesByID[obs.ParentObservationID]; found != nil {
				parent = found
			}
		}
		parent.Children = append(parent.Children, node)
		nodesByID[obs.ID] = node
	}
}

func customObservationToRichObservation(obs *traceoverlays.Observation) *tracespb.RichObservation {
	pb := &tracespb.RichObservation{
		Id:        obs.ID,
		TraceId:   obs.TraceID,
		Type:      obs.Type,
		Name:      &obs.Name,
		StartTime: timestamppb.New(obs.StartTime),
	}
	if obs.EndTime != nil {
		pb.EndTime = timestamppb.New(*obs.EndTime)
	}
	if obs.Duration > 0 {
		latency := float64(obs.Duration) / 1e9
		pb.Latency = &latency
	}
	if obs.ParentObservationID != "" {
		pb.ParentObservationId = &obs.ParentObservationID
	}
	if obs.Model != "" {
		pb.Model = &obs.Model
	}
	if obs.Level != "" {
		pb.Level = &obs.Level
	}
	if obs.StatusMessage != "" {
		pb.StatusMessage = &obs.StatusMessage
	}
	pb.InputTokens = obs.InputTokens
	pb.OutputTokens = obs.OutputTokens
	pb.TotalTokens = obs.TotalTokens
	pb.InputCost = obs.InputCost
	pb.OutputCost = obs.OutputCost
	pb.TotalCost = obs.TotalCost
	return pb
}

func customObservationToEnhancedObservation(obs *traceoverlays.Observation) *tracespb.EnhancedObservation {
	pb := &tracespb.EnhancedObservation{
		Id:        obs.ID,
		TraceId:   obs.TraceID,
		Type:      obs.Type,
		Name:      &obs.Name,
		StartTime: timestamppb.New(obs.StartTime),
		Metadata:  obs.Metadata,
		Tags:      map[string]string{},
	}
	if obs.EndTime != nil {
		pb.EndTime = timestamppb.New(*obs.EndTime)
	}
	if obs.Duration > 0 {
		latency := float64(obs.Duration) / 1e9
		pb.Latency = &latency
	}
	if obs.ParentObservationID != "" {
		pb.ParentObservationId = &obs.ParentObservationID
	}
	if obs.Model != "" {
		pb.Model = &obs.Model
	}
	if obs.Level != "" {
		pb.Level = &obs.Level
	}
	if obs.StatusMessage != "" {
		pb.StatusMessage = &obs.StatusMessage
	}
	pb.InputTokens = obs.InputTokens
	pb.OutputTokens = obs.OutputTokens
	pb.TotalTokens = obs.TotalTokens
	pb.InputCost = obs.InputCost
	pb.OutputCost = obs.OutputCost
	pb.TotalCost = obs.TotalCost
	if obs.InputData != "" || obs.OutputData != "" {
		pb.Io = &tracespb.ObservationIO{
			InputTokens:    obs.InputTokens,
			OutputTokens:   obs.OutputTokens,
			TotalTokens:    obs.TotalTokens,
			InputMimeType:  &obs.InputMimeType,
			OutputMimeType: &obs.OutputMimeType,
		}
		if obs.InputData != "" {
			pb.Io.InputData = &obs.InputData
		}
		if obs.OutputData != "" {
			pb.Io.OutputData = &obs.OutputData
		}
	}
	for _, tag := range obs.Tags {
		pb.Tags[tag] = "true"
	}
	return pb
}

func spanToEnhancedObservation(span *query.SpanReadModel) *tracespb.EnhancedObservation {
	name := span.SpanName
	obsType := getObservationType(span.SpanAttributes)
	level := span.SpanAttributes["observation.level"]
	if level == "" {
		level = "DEFAULT"
	}
	statusMessage := span.StatusMessage
	pb := &tracespb.EnhancedObservation{
		Id:            span.SpanID,
		TraceId:       span.TraceID,
		Type:          obsType,
		Name:          &name,
		StartTime:     timestamppb.New(span.Timestamp),
		StatusMessage: &statusMessage,
		Level:         &level,
		Metadata:      span.SpanAttributes,
	}
	endTime := span.Timestamp.Add(time.Duration(span.Duration))
	pb.EndTime = timestamppb.New(endTime)
	if span.ParentSpanID != "" {
		pb.ParentObservationId = &span.ParentSpanID
	}
	if span.Duration > 0 {
		latency := float64(span.Duration) / 1e9
		pb.Latency = &latency
	}
	if stepRaw := firstNonEmpty(span.SpanAttributes["span.step"], span.SpanAttributes["step.number"]); stepRaw != "" {
		if step, err := parseInt64(stepRaw); err == nil && step >= 0 {
			stepUint := uint32(step)
			pb.Step = &stepUint
		}
	}
	if node := span.SpanAttributes["span.node"]; node != "" {
		pb.Node = &node
	}
	if model := firstNonEmpty(span.SpanAttributes["llm.response.model"], span.SpanAttributes["llm.request.model"], span.SpanAttributes["llm.model"]); model != "" {
		pb.Model = &model
	}
	if val, err := parseInt64(firstNonEmpty(span.SpanAttributes["llm.tokens.input"], span.SpanAttributes["agent.tokens.input"])); err == nil {
		pb.InputTokens = &val
	}
	if val, err := parseInt64(firstNonEmpty(span.SpanAttributes["llm.tokens.output"], span.SpanAttributes["agent.tokens.output"])); err == nil {
		pb.OutputTokens = &val
	}
	if val, err := parseInt64(firstNonEmpty(span.SpanAttributes["llm.tokens.total"], span.SpanAttributes["agent.tokens.total"])); err == nil {
		pb.TotalTokens = &val
	}
	if val, err := parseFloat64(span.SpanAttributes["llm.cost.input"]); err == nil {
		pb.InputCost = &val
	}
	if val, err := parseFloat64(span.SpanAttributes["llm.cost.output"]); err == nil {
		pb.OutputCost = &val
	}
	if val, err := parseFloat64(span.SpanAttributes["llm.cost.total"]); err == nil {
		pb.TotalCost = &val
	}
	inputData := firstNonEmpty(span.SpanAttributes["llm.request.messages"], span.SpanAttributes["trace.input"], span.SpanAttributes["input.value"])
	outputData := firstNonEmpty(span.SpanAttributes["llm.response.choices"], span.SpanAttributes["trace.output"], span.SpanAttributes["output.value"])
	if inputData != "" || outputData != "" || pb.InputTokens != nil || pb.OutputTokens != nil || pb.TotalTokens != nil {
		pb.Io = &tracespb.ObservationIO{
			InputTokens:  pb.InputTokens,
			OutputTokens: pb.OutputTokens,
			TotalTokens:  pb.TotalTokens,
		}
		if inputData != "" {
			pb.Io.InputData = &inputData
		}
		if outputData != "" {
			pb.Io.OutputData = &outputData
		}
	}
	return pb
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func applyOverlayToRichTrace(trace *tracespb.RichTrace, overlay *traceoverlays.Overlay) {
	if trace == nil || overlay == nil {
		return
	}
	if overlay.DisplayName != nil && *overlay.DisplayName != "" {
		trace.Name = overlay.DisplayName
	}
	if len(overlay.Tags) > 0 {
		trace.Tags = append(trace.Tags, overlay.Tags...)
	}
}

func toProtoOverlay(overlay *traceoverlays.Overlay) *tracespb.TraceOverlay {
	if overlay == nil {
		return nil
	}
	pb := &tracespb.TraceOverlay{
		TraceId:        overlay.TraceID,
		UpdatedAt:      timestamppb.New(overlay.UpdatedAt),
		DisplayName:    overlay.DisplayName,
		InputOverride:  overlay.InputOverride,
		OutputOverride: overlay.OutputOverride,
		Metadata:       overlay.Metadata,
		Tags:           overlay.Tags,
		HiddenSpanIds:  overlay.HiddenSpanIDs,
	}
	if overlay.AuthorUserID != "" {
		pb.AuthorUserId = &overlay.AuthorUserID
	}
	return pb
}

func customObservationFromRequest(req *tracespb.CreateCustomObservationRequest) *traceoverlays.Observation {
	obs := &traceoverlays.Observation{
		ID:                  req.GetId(),
		TraceID:             req.GetTraceId(),
		ParentObservationID: req.GetParentObservationId(),
		Name:                req.GetName(),
		Type:                req.GetType(),
		Source:              req.GetSource(),
		Duration:            req.GetDuration(),
		Level:               req.GetLevel(),
		StatusMessage:       req.GetStatusMessage(),
		Model:               req.GetModel(),
		InputData:           req.GetInputData(),
		OutputData:          req.GetOutputData(),
		InputMimeType:       req.GetInputMimeType(),
		OutputMimeType:      req.GetOutputMimeType(),
		Metadata:            req.GetMetadata(),
		Tags:                req.GetTags(),
		InputTokens:         req.InputTokens,
		OutputTokens:        req.OutputTokens,
		TotalTokens:         req.TotalTokens,
		InputCost:           req.InputCost,
		OutputCost:          req.OutputCost,
		TotalCost:           req.TotalCost,
	}
	if req.GetStartTime() != nil {
		obs.StartTime = req.GetStartTime().AsTime()
	}
	if req.GetEndTime() != nil {
		endTime := req.GetEndTime().AsTime()
		obs.EndTime = &endTime
	}
	return obs
}

func toProtoCustomObservation(obs *traceoverlays.Observation) *tracespb.CustomObservation {
	if obs == nil {
		return nil
	}
	pb := &tracespb.CustomObservation{
		Id:           obs.ID,
		TraceId:      obs.TraceID,
		Name:         obs.Name,
		Type:         obs.Type,
		Source:       obs.Source,
		StartTime:    timestamppb.New(obs.StartTime),
		Duration:     obs.Duration,
		Metadata:     obs.Metadata,
		Tags:         obs.Tags,
		CreatedAt:    timestamppb.New(obs.CreatedAt),
		UpdatedAt:    timestamppb.New(obs.UpdatedAt),
		InputTokens:  obs.InputTokens,
		OutputTokens: obs.OutputTokens,
		TotalTokens:  obs.TotalTokens,
		InputCost:    obs.InputCost,
		OutputCost:   obs.OutputCost,
		TotalCost:    obs.TotalCost,
	}
	if obs.ParentObservationID != "" {
		pb.ParentObservationId = &obs.ParentObservationID
	}
	if obs.EndTime != nil {
		pb.EndTime = timestamppb.New(*obs.EndTime)
	}
	if obs.Level != "" {
		pb.Level = &obs.Level
	}
	if obs.StatusMessage != "" {
		pb.StatusMessage = &obs.StatusMessage
	}
	if obs.Model != "" {
		pb.Model = &obs.Model
	}
	if obs.InputData != "" {
		pb.InputData = &obs.InputData
	}
	if obs.OutputData != "" {
		pb.OutputData = &obs.OutputData
	}
	if obs.InputMimeType != "" {
		pb.InputMimeType = &obs.InputMimeType
	}
	if obs.OutputMimeType != "" {
		pb.OutputMimeType = &obs.OutputMimeType
	}
	if obs.AuthorUserID != "" {
		pb.AuthorUserId = &obs.AuthorUserID
	}
	return pb
}

func toProtoAnnotation(annotation *traceoverlays.Annotation) *tracespb.TraceAnnotation {
	if annotation == nil {
		return nil
	}
	pb := &tracespb.TraceAnnotation{
		Id:        annotation.ID,
		TraceId:   annotation.TraceID,
		Body:      annotation.Body,
		Metadata:  annotation.Metadata,
		CreatedAt: timestamppb.New(annotation.CreatedAt),
	}
	if annotation.ObservationID != "" {
		pb.ObservationId = &annotation.ObservationID
	}
	if annotation.AuthorUserID != "" {
		pb.AuthorUserId = &annotation.AuthorUserID
	}
	return pb
}

func statusCodeFromObservationLevel(level string) string {
	if strings.EqualFold(level, "ERROR") {
		return "ERROR"
	}
	return "OK"
}

// GetRichTrace retrieves a Langfuse-compatible trace with full details
func (s *Server) GetRichTrace(
	ctx context.Context,
	req *connect.Request[tracespb.GetRichTraceRequest],
) (*connect.Response[tracespb.GetRichTraceResponse], error) {
	// Get CQRS system from context
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Get the rich trace handler from the query bus
	// Note: This requires the RichTraceQueryHandler to be registered
	// For now, we'll use the existing trace query and transform it
	richTraceQuery := traceshandler.NewGetTraceByIDQuery(req.Msg.GetTraceId(), "", "")

	result, err := sys.QueryBus.Execute(ctx, richTraceQuery)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if trace := s.customOnlyRichTrace(ctx, req.Msg.GetTraceId()); trace != nil {
				return connect.NewResponse(&tracespb.GetRichTraceResponse{Trace: trace}), nil
			}
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("trace not found: %s", req.Msg.GetTraceId()))
		}
		logger.WithError(err).Error("failed to get rich trace")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	spans, ok := response.Data.([]query.SpanReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	if len(spans) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("trace not found: %s", req.Msg.GetTraceId()))
	}

	// Transform spans to rich trace format
	richTrace := transformToRichTrace(req.Msg.GetTraceId(), spans)
	if s.overlayRecorder != nil {
		if overlay, err := s.overlayRecorder.GetOverlay(ctx, req.Msg.GetTraceId()); err == nil {
			applyOverlayToRichTrace(richTrace, overlay)
		} else if !errors.Is(err, sql.ErrNoRows) {
			logger.WithFields("trace_id", req.Msg.GetTraceId(), "error", err.Error()).Warn("failed to load trace overlay")
		}
		if custom, err := s.overlayRecorder.ListObservations(ctx, req.Msg.GetTraceId()); err == nil {
			for _, obs := range custom {
				richTrace.Observations = append(richTrace.Observations, customObservationToRichObservation(obs))
			}
		} else {
			logger.WithFields("trace_id", req.Msg.GetTraceId(), "error", err.Error()).Warn("failed to load custom observations")
		}
	}

	return connect.NewResponse(&tracespb.GetRichTraceResponse{
		Trace: richTrace,
	}), nil
}

// ListRichTraces retrieves Langfuse-compatible traces with filtering
func (s *Server) ListRichTraces(
	ctx context.Context,
	req *connect.Request[tracespb.ListRichTracesRequest],
) (*connect.Response[tracespb.ListRichTracesResponse], error) {
	// Get CQRS system from context
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Parse time range
	from := time.Now().Add(-24 * time.Hour)
	if req.Msg.GetFromTime() != nil {
		from = req.Msg.GetFromTime().AsTime()
	}

	to := time.Time{}
	if req.Msg.GetToTime() != nil {
		to = req.Msg.GetToTime().AsTime()
	}

	// Build query
	limit := int(req.Msg.GetLimit())
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	// Use existing ListTracesQuery
	listQuery := traceshandler.NewListTracesQuery(
		"", // tenant_id - extract from context if needed
		from,
		to,
		func() string {
			if req.Msg.Model != nil {
				return *req.Msg.Model
			}
			return ""
		}(),
		func() string {
			if req.Msg.Provider != nil {
				return *req.Msg.Provider
			}
			return ""
		}(),
		"", // status_code
		"", // correlation_id - not available in ListRichTracesRequest
		func() string {
			if req.Msg.UserId != nil {
				return *req.Msg.UserId
			}
			return ""
		}(),
		"", // trace_id
	)
	listQuery.Limit = limit
	listQuery.Offset = int(req.Msg.GetOffset())
	if req.Msg.SessionId != nil {
		listQuery.FilterSessionID = *req.Msg.SessionId
	}
	if req.Msg.ThreadId != nil {
		listQuery.FilterThreadID = *req.Msg.ThreadId
	}
	if req.Msg.Query != nil {
		listQuery.FullTextQuery = *req.Msg.Query
	}
	if len(req.Msg.Metadata) > 0 {
		listQuery.Metadata = req.Msg.Metadata
	}
	if req.Msg.Environment != nil {
		listQuery.Environment = *req.Msg.Environment
	}
	if len(req.Msg.Tags) > 0 {
		listQuery.Tags = req.Msg.Tags
	}

	result, err := sys.QueryBus.Execute(ctx, listQuery)
	if err != nil {
		logger.WithError(err).Error("failed to list rich traces")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	traces, ok := response.Data.([]query.TraceReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	// Transform to rich traces
	richTraces := make([]*tracespb.RichTrace, 0, len(traces))
	for _, trace := range traces {
		// For list view, we only include summary data
		// Full observations can be fetched via GetRichTrace
		richTrace := &tracespb.RichTrace{
			Id:        trace.TraceID,
			Timestamp: timestamppb.New(trace.StartTime),
			Latency:   float64(trace.TotalDuration) / 1e9, // Convert ns to seconds
			TotalCost: trace.TotalCost,
		}
		if trace.UserID != "" {
			userID := trace.UserID
			richTrace.UserId = &userID
		}
		if trace.SessionID != "" {
			sessionID := trace.SessionID
			richTrace.SessionId = &sessionID
		}
		if trace.ThreadID != "" {
			threadID := trace.ThreadID
			richTrace.ThreadId = &threadID
		}
		if s.overlayRecorder != nil {
			if overlay, err := s.overlayRecorder.GetOverlay(ctx, trace.TraceID); err == nil {
				applyOverlayToRichTrace(richTrace, overlay)
			}
		}
		richTraces = append(richTraces, richTrace)
	}

	return connect.NewResponse(&tracespb.ListRichTracesResponse{
		Traces:     richTraces,
		TotalCount: int32(len(richTraces)),
	}), nil
}

// transformToRichTrace converts SpanReadModels to RichTrace format
func transformToRichTrace(traceID string, spans []query.SpanReadModel) *tracespb.RichTrace {
	if len(spans) == 0 {
		return &tracespb.RichTrace{Id: traceID}
	}

	// Find root span
	var rootSpan *query.SpanReadModel
	for i := range spans {
		if spans[i].ParentSpanID == "" {
			rootSpan = &spans[i]
			break
		}
	}

	if rootSpan == nil {
		rootSpan = &spans[0]
	}

	richTrace := &tracespb.RichTrace{
		Id:        traceID,
		Timestamp: timestamppb.New(rootSpan.Timestamp),
	}

	// Extract trace-level attributes from root span
	if name, ok := rootSpan.SpanAttributes["trace.name"]; ok {
		richTrace.Name = &name
	}
	if userID, ok := rootSpan.SpanAttributes["trace.user_id"]; ok {
		richTrace.UserId = &userID
	}
	if sessionID, ok := rootSpan.SpanAttributes["trace.session_id"]; ok {
		richTrace.SessionId = &sessionID
	}
	if env, ok := rootSpan.ResourceAttributes["deployment.environment"]; ok {
		richTrace.Environment = &env
	}
	if release, ok := rootSpan.ResourceAttributes["service.version"]; ok {
		richTrace.Release = &release
	}

	// Calculate total latency and cost
	var maxDuration int64
	var totalCost float64

	observations := make([]*tracespb.RichObservation, 0, len(spans))
	for i := range spans {
		obs := transformToRichObservation(&spans[i])
		observations = append(observations, obs)

		if spans[i].Duration > maxDuration {
			maxDuration = spans[i].Duration
		}

		if obs.TotalCost != nil {
			totalCost += *obs.TotalCost
		}
	}

	richTrace.Latency = float64(maxDuration) / 1e9 // Convert ns to seconds
	richTrace.TotalCost = totalCost
	richTrace.Observations = observations

	return richTrace
}

// transformToRichObservation converts a SpanReadModel to RichObservation
func transformToRichObservation(span *query.SpanReadModel) *tracespb.RichObservation {
	obs := &tracespb.RichObservation{
		Id:        span.SpanID,
		TraceId:   span.TraceID,
		Type:      getObservationType(span.SpanAttributes),
		Name:      &span.SpanName,
		StartTime: timestamppb.New(span.Timestamp),
	}

	// Calculate end time
	endTime := span.Timestamp.Add(time.Duration(span.Duration))
	obs.EndTime = timestamppb.New(endTime)

	// Calculate latency in seconds
	latency := float64(span.Duration) / 1e9
	obs.Latency = &latency

	// Set parent if exists
	if span.ParentSpanID != "" {
		obs.ParentObservationId = &span.ParentSpanID
	}

	// Extract model
	if model, ok := span.SpanAttributes["llm.model"]; ok {
		obs.Model = &model
	}

	// Extract observation level
	level := span.SpanAttributes["observation.level"]
	if level == "" {
		level = "DEFAULT"
	}
	obs.Level = &level

	// Extract status message
	if span.StatusMessage != "" {
		obs.StatusMessage = &span.StatusMessage
	}

	// Extract token usage
	inputTokens, ok := span.SpanAttributes["llm.tokens.input"]
	if !ok {
		inputTokens, ok = span.SpanAttributes["agent.tokens.input"]
	}
	if ok {
		if val, err := parseInt64(inputTokens); err == nil {
			obs.InputTokens = &val
		}
	}
	outputTokens, ok := span.SpanAttributes["llm.tokens.output"]
	if !ok {
		outputTokens, ok = span.SpanAttributes["agent.tokens.output"]
	}
	if ok {
		if val, err := parseInt64(outputTokens); err == nil {
			obs.OutputTokens = &val
		}
	}
	totalTokens, ok := span.SpanAttributes["llm.tokens.total"]
	if !ok {
		totalTokens, ok = span.SpanAttributes["agent.tokens.total"]
	}
	if ok {
		if val, err := parseInt64(totalTokens); err == nil {
			obs.TotalTokens = &val
		}
	}

	// Extract costs
	if inputCost, ok := span.SpanAttributes["llm.cost.input"]; ok {
		if val, err := parseFloat64(inputCost); err == nil {
			obs.InputCost = &val
		}
	}
	if outputCost, ok := span.SpanAttributes["llm.cost.output"]; ok {
		if val, err := parseFloat64(outputCost); err == nil {
			obs.OutputCost = &val
		}
	}
	if totalCost, ok := span.SpanAttributes["llm.cost.total"]; ok {
		if val, err := parseFloat64(totalCost); err == nil {
			obs.TotalCost = &val
		}
	}

	return obs
}

// getObservationType extracts or infers the observation type
func getObservationType(attrs map[string]string) string {
	if obsType, ok := attrs["observation.type"]; ok {
		return obsType
	}
	return "SPAN" // Default
}

// Helper functions to parse string values
func parseInt64(s string) (int64, error) {
	var val int64
	_, err := fmt.Sscanf(s, "%d", &val)
	return val, err
}

func parseFloat64(s string) (float64, error) {
	var val float64
	_, err := fmt.Sscanf(s, "%f", &val)
	return val, err
}

// toProtoPerformanceMetrics converts internal PerformanceMetricsData to the proto type.
func toProtoPerformanceMetrics(m *enhanced.PerformanceMetricsData) *tracespb.PerformanceMetrics {
	if m == nil {
		return nil
	}
	pb := &tracespb.PerformanceMetrics{}
	if m.QueueTimeNs > 0 {
		v := m.QueueTimeNs
		pb.QueueTimeNs = &v
	}
	if m.ProcessingTimeNs > 0 {
		v := m.ProcessingTimeNs
		pb.ProcessingTimeNs = &v
	}
	if m.NetworkLatencyNs > 0 {
		v := m.NetworkLatencyNs
		pb.NetworkLatencyNs = &v
	}
	if m.SerializationTimeNs > 0 {
		v := m.SerializationTimeNs
		pb.SerializationTimeNs = &v
	}
	if m.DbQueryTimeNs > 0 {
		v := m.DbQueryTimeNs
		pb.DbQueryTimeNs = &v
	}
	if m.CacheLookupTimeNs > 0 {
		v := m.CacheLookupTimeNs
		pb.CacheLookupTimeNs = &v
	}
	if m.LlmTTFTNs > 0 {
		v := m.LlmTTFTNs
		pb.LlmTimeToFirstTokenNs = &v
	}
	if m.LlmTimePerTokenNs > 0 {
		v := m.LlmTimePerTokenNs
		pb.LlmTimePerTokenNs = &v
	}
	return pb
}

// toProtoPerformanceEntry converts a PerformanceEntry to its proto form.
func toProtoPerformanceEntry(e *enhanced.PerformanceEntry) *tracespb.PerformanceBreakdownEntry {
	if e == nil {
		return nil
	}
	pb := &tracespb.PerformanceBreakdownEntry{
		Metrics:           toProtoPerformanceMetrics(&e.Metrics),
		PercentageOfTotal: e.PercentageOfTotal,
	}
	if e.Node != nil {
		pb.Node = e.Node
	}
	if e.ObservationType != nil {
		pb.ObservationType = e.ObservationType
	}
	if e.ObservationID != nil {
		pb.ObservationId = e.ObservationID
	}
	return pb
}

// toProtoResourceMetrics converts internal ResourceMetricsData to the proto type.
func toProtoResourceMetrics(m *traceshandler.ResourceMetricsData) *tracespb.ResourceMetrics {
	if m == nil {
		return nil
	}
	pb := &tracespb.ResourceMetrics{}
	if m.MemoryUsedBytes != nil {
		pb.MemoryUsedBytes = m.MemoryUsedBytes
	}
	if m.MemoryAllocatedBytes != nil {
		pb.MemoryAllocatedBytes = m.MemoryAllocatedBytes
	}
	if m.CpuUsagePercent != nil {
		pb.CpuUsagePercent = m.CpuUsagePercent
	}
	if m.NetworkBytesSent != nil {
		pb.NetworkBytesSent = m.NetworkBytesSent
	}
	if m.NetworkBytesReceived != nil {
		pb.NetworkBytesReceived = m.NetworkBytesReceived
	}
	if m.DiskReadBytes != nil {
		pb.DiskReadBytes = m.DiskReadBytes
	}
	if m.DiskWriteBytes != nil {
		pb.DiskWriteBytes = m.DiskWriteBytes
	}
	if m.ThreadCount != nil {
		pb.ThreadCount = m.ThreadCount
	}
	return pb
}

// toProtoEnhancedObservation converts EnhancedObservationResult to protobuf message
func toProtoEnhancedObservation(obs *enhanced.EnhancedObservationResult) *tracespb.EnhancedObservation {
	pbObs := &tracespb.EnhancedObservation{
		Id:            obs.ID,
		TraceId:       obs.TraceID,
		Name:          &obs.Name,
		StartTime:     timestamppb.New(time.Unix(0, obs.StartTime)),
		StatusMessage: &obs.StatusMessage,
	}

	// Set optional fields
	if obs.EndTime != nil {
		pbObs.EndTime = timestamppb.New(time.Unix(0, *obs.EndTime))
	}
	if obs.ParentObservationID != "" {
		pbObs.ParentObservationId = &obs.ParentObservationID
	}
	if obs.Step != nil {
		pbObs.Step = obs.Step
	}
	if obs.Node != nil {
		pbObs.Node = obs.Node
	}
	if obs.ObservationType != nil {
		pbObs.Type = *obs.ObservationType
	}

	// Calculate latency
	if obs.Duration > 0 {
		latency := float64(obs.Duration) / 1e9
		pbObs.Latency = &latency
	}

	// Add I/O data if included
	if obs.InputData != nil || obs.OutputData != nil {
		pbObs.Io = &tracespb.ObservationIO{}
		if obs.InputData != nil {
			pbObs.Io.InputData = obs.InputData
		}
		if obs.OutputData != nil {
			pbObs.Io.OutputData = obs.OutputData
		}
		if obs.InputTokens != nil {
			pbObs.Io.InputTokens = obs.InputTokens
		}
		if obs.OutputTokens != nil {
			pbObs.Io.OutputTokens = obs.OutputTokens
		}
		if obs.TotalTokens != nil {
			pbObs.Io.TotalTokens = obs.TotalTokens
		}
	}

	// Add performance metrics if included
	if obs.QueueTimeNs != nil || obs.ProcessingTimeNs != nil {
		pbObs.Performance = &tracespb.PerformanceMetrics{}
		if obs.QueueTimeNs != nil {
			pbObs.Performance.QueueTimeNs = obs.QueueTimeNs
		}
		if obs.ProcessingTimeNs != nil {
			pbObs.Performance.ProcessingTimeNs = obs.ProcessingTimeNs
		}
		if obs.NetworkLatencyNs != nil {
			pbObs.Performance.NetworkLatencyNs = obs.NetworkLatencyNs
		}
		if obs.LlmTTFTNs != nil {
			pbObs.Performance.LlmTimeToFirstTokenNs = obs.LlmTTFTNs
		}
	}

	// Add resource metrics if included
	if obs.MemoryUsedBytes != nil || obs.CpuUsagePercent != nil {
		pbObs.Resources = &tracespb.ResourceMetrics{}
		if obs.MemoryUsedBytes != nil {
			pbObs.Resources.MemoryUsedBytes = obs.MemoryUsedBytes
		}
		if obs.CpuUsagePercent != nil {
			pbObs.Resources.CpuUsagePercent = obs.CpuUsagePercent
		}
	}

	// Add workflow metadata if available
	if obs.WorkflowID != nil {
		pbObs.Workflow = &tracespb.WorkflowMetadata{
			WorkflowId: obs.WorkflowID,
		}
		if obs.WorkflowType != nil {
			pbObs.Workflow.WorkflowType = obs.WorkflowType
		}
		if obs.WorkflowName != nil {
			pbObs.Workflow.WorkflowName = obs.WorkflowName
		}
	}

	return pbObs
}

// ============================================================================
// Enhanced Observability Endpoints
// ============================================================================

// StreamEnhancedObservations streams observations with workflow and performance data
func (s *Server) StreamEnhancedObservations(
	ctx context.Context,
	req *connect.Request[tracespb.StreamEnhancedObservationsRequest],
	stream *connect.ServerStream[tracespb.EnhancedObservation],
) error {
	// Get CQRS system from context
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	// Build enhanced observation query from request
	enhancedQuery := &enhanced.EnhancedObservationQuery{
		TraceID:          req.Msg.GetTraceId(),
		WorkflowID:       req.Msg.GetWorkflowId(),
		ObservationTypes: req.Msg.GetObservationTypes(),
		Nodes:            req.Msg.GetNodes(),
		IncludeIO:        req.Msg.GetIncludeIo(),
		IncludePerf:      req.Msg.GetIncludePerformance(),
		IncludeResources: req.Msg.GetIncludeResources(),
	}

	// Set min/max step filters if provided
	if req.Msg.MinStep != nil {
		minStep := *req.Msg.MinStep
		enhancedQuery.MinStep = &minStep
	}
	if req.Msg.MaxStep != nil {
		maxStep := *req.Msg.MaxStep
		enhancedQuery.MaxStep = &maxStep
	}

	// Execute query via query bus
	result, err := sys.QueryBus.Execute(ctx, enhancedQuery)
	if err != nil {
		logger.WithError(err).Error("failed to execute enhanced observation query")
		return connect.NewError(connect.CodeInternal, err)
	}

	// Type assert result
	response, ok := result.(*query.Response)
	if !ok {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	observations, ok := response.Data.([]enhanced.EnhancedObservationResult)
	if !ok {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	// Stream observations to client
	for _, obs := range observations {
		pbObs := toProtoEnhancedObservation(&obs)
		if err := stream.Send(pbObs); err != nil {
			logger.WithError(err).Error("failed to send enhanced observation to client")
			return err
		}
	}
	if s.overlayRecorder != nil {
		custom, err := s.overlayRecorder.ListObservations(ctx, req.Msg.GetTraceId())
		if err != nil {
			logger.WithFields("trace_id", req.Msg.GetTraceId(), "error", err.Error()).Warn("failed to load custom observations")
		} else {
			for _, obs := range custom {
				if len(req.Msg.GetObservationTypes()) > 0 && !containsString(req.Msg.GetObservationTypes(), obs.Type) {
					continue
				}
				if err := stream.Send(customObservationToEnhancedObservation(obs)); err != nil {
					logger.WithError(err).Error("failed to send custom enhanced observation to client")
					return err
				}
			}
		}
	}

	return nil
}

// GetWorkflowMetrics retrieves aggregated workflow performance metrics
func (s *Server) GetWorkflowMetrics(
	ctx context.Context,
	req *connect.Request[tracespb.GetWorkflowMetricsRequest],
) (*connect.Response[tracespb.GetWorkflowMetricsResponse], error) {
	// TODO: Implement WorkflowMetricsQuery and execute via query bus
	logger.Warn("GetWorkflowMetrics not yet fully implemented")
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("GetWorkflowMetrics not yet implemented"))
}

// ListObservationsByStep retrieves observations ordered by execution step
func (s *Server) ListObservationsByStep(
	ctx context.Context,
	req *connect.Request[tracespb.ListObservationsByStepRequest],
) (*connect.Response[tracespb.ListObservationsByStepResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}

	result, err := sys.QueryBus.Execute(ctx, traceshandler.NewGetTraceByIDQuery(traceID, "", ""))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}
	spans, ok := response.Data.([]query.SpanReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	observations := make([]*tracespb.EnhancedObservation, 0, len(spans))
	for i := range spans {
		observations = append(observations, spanToEnhancedObservation(&spans[i]))
	}
	if s.overlayRecorder != nil {
		custom, err := s.overlayRecorder.ListObservations(ctx, traceID)
		if err != nil {
			logger.WithFields("trace_id", traceID, "error", err.Error()).Warn("failed to load custom observations")
		} else {
			for _, obs := range custom {
				observations = append(observations, customObservationToEnhancedObservation(obs))
			}
		}
	}
	sort.SliceStable(observations, func(i, j int) bool {
		leftStep := uint32(0)
		rightStep := uint32(0)
		if observations[i].Step != nil {
			leftStep = *observations[i].Step
		}
		if observations[j].Step != nil {
			rightStep = *observations[j].Step
		}
		if leftStep != rightStep {
			if leftStep == 0 {
				return false
			}
			if rightStep == 0 {
				return true
			}
			return leftStep < rightStep
		}
		return observations[i].GetStartTime().AsTime().Before(observations[j].GetStartTime().AsTime())
	})
	if !req.Msg.GetAscending() {
		for i, j := 0, len(observations)-1; i < j; i, j = i+1, j-1 {
			observations[i], observations[j] = observations[j], observations[i]
		}
	}
	total := len(observations)
	offset := int(req.Msg.GetOffset())
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return connect.NewResponse(&tracespb.ListObservationsByStepResponse{
		Observations: observations[offset:end],
		TotalCount:   int32(total),
	}), nil
}

// GetObservationIO retrieves input/output data for specific observations
func (s *Server) GetObservationIO(
	ctx context.Context,
	req *connect.Request[tracespb.GetObservationIORequest],
) (*connect.Response[tracespb.GetObservationIOResponse], error) {
	result := map[string]*tracespb.ObservationIO{}
	if s.overlayRecorder == nil {
		return connect.NewResponse(&tracespb.GetObservationIOResponse{ObservationIo: result}), nil
	}
	ids := map[string]struct{}{}
	for _, id := range req.Msg.GetObservationIds() {
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return connect.NewResponse(&tracespb.GetObservationIOResponse{ObservationIo: result}), nil
	}
	idList := make([]string, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	custom, err := s.overlayRecorder.ListObservationsByID(ctx, idList)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get custom observation IO: %w", err))
	}
	for _, obs := range custom {
		io := &tracespb.ObservationIO{
			InputTokens:  obs.InputTokens,
			OutputTokens: obs.OutputTokens,
			TotalTokens:  obs.TotalTokens,
		}
		if obs.InputData != "" {
			io.InputData = &obs.InputData
		}
		if obs.OutputData != "" {
			io.OutputData = &obs.OutputData
		}
		if obs.InputMimeType != "" {
			io.InputMimeType = &obs.InputMimeType
		}
		if obs.OutputMimeType != "" {
			io.OutputMimeType = &obs.OutputMimeType
		}
		result[obs.ID] = io
	}
	return connect.NewResponse(&tracespb.GetObservationIOResponse{ObservationIo: result}), nil
}

// GetTraceAnalytics retrieves pre-aggregated trace statistics
func (s *Server) GetTraceAnalytics(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceAnalyticsRequest],
) (*connect.Response[tracespb.GetTraceAnalyticsResponse], error) {
	// TODO: Implement BatchAnalyticsHandler query
	logger.Warn("GetTraceAnalytics not yet fully implemented")
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("GetTraceAnalytics not yet implemented"))
}

// GetPerformanceBreakdown retrieves detailed performance analysis
func (s *Server) GetPerformanceBreakdown(
	ctx context.Context,
	req *connect.Request[tracespb.GetPerformanceBreakdownRequest],
) (*connect.Response[tracespb.GetPerformanceBreakdownResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}

	q := &enhanced.PerformanceBreakdownQuery{
		TraceID:       traceID,
		ObservationID: req.Msg.GetObservationId(),
		GroupByNode:   req.Msg.GetGroupByNode(),
		GroupByType:   req.Msg.GetGroupByType(),
	}

	result, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		logger.WithError(err).Error("failed to execute performance breakdown query")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}
	br, ok := response.Data.(*enhanced.PerformanceBreakdownResult)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbResp := &tracespb.GetPerformanceBreakdownResponse{
		TotalMetrics: toProtoPerformanceMetrics(&br.TotalMetrics),
		Entries:      make([]*tracespb.PerformanceBreakdownEntry, 0, len(br.Entries)),
	}
	for _, e := range br.Entries {
		pbResp.Entries = append(pbResp.Entries, toProtoPerformanceEntry(&e))
	}
	return connect.NewResponse(pbResp), nil
}

// GetResourceUtilization retrieves resource usage over time
func (s *Server) GetResourceUtilization(
	ctx context.Context,
	req *connect.Request[tracespb.GetResourceUtilizationRequest],
) (*connect.Response[tracespb.GetResourceUtilizationResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}

	q := &traceshandler.ResourceUtilizationQuery{
		TraceID:       traceID,
		GranularityMs: req.Msg.GetGranularityMs(),
	}
	if req.Msg.FromTime != nil {
		from := req.Msg.FromTime.AsTime()
		q.FromTime = &from
	}
	if req.Msg.ToTime != nil {
		to := req.Msg.ToTime.AsTime()
		q.ToTime = &to
	}

	result, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		logger.WithError(err).Error("failed to execute resource utilization query")
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}
	ru, ok := response.Data.(*traceshandler.ResourceUtilizationResult)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbResp := &tracespb.GetResourceUtilizationResponse{
		PeakUtilization:    toProtoResourceMetrics(&ru.Peak),
		AverageUtilization: toProtoResourceMetrics(&ru.Average),
		Points:             make([]*tracespb.ResourceUtilizationPoint, 0, len(ru.Points)),
	}
	for _, p := range ru.Points {
		pbResp.Points = append(pbResp.Points, &tracespb.ResourceUtilizationPoint{
			Timestamp:          timestamppb.New(p.Timestamp),
			Metrics:            toProtoResourceMetrics(&p.Metrics),
			ActiveObservations: p.ActiveObservations,
		})
	}
	return connect.NewResponse(pbResp), nil
}

// ============================================================================
// Score Management Endpoints (P0.5)
// ============================================================================

// GetTraceScores retrieves all scores for a trace
func (s *Server) GetTraceScores(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceScoresRequest],
) (*connect.Response[tracespb.GetTraceScoresResponse], error) {
	if s.scoreRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("score recorder not configured"))
	}

	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}

	scoresList, err := s.scoreRecorder.GetScoresByTrace(ctx, traceID)
	if err != nil {
		logger.WithFields("trace_id", traceID, "error", err.Error()).Error("failed to get scores")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get scores: %w", err))
	}

	pbScores := make([]*tracespb.Score, 0, len(scoresList))
	for _, score := range scoresList {
		pbScores = append(pbScores, toProtoScore(score))
	}

	return connect.NewResponse(&tracespb.GetTraceScoresResponse{
		Scores: pbScores,
	}), nil
}

// CreateScore creates a new score for a trace
func (s *Server) CreateScore(
	ctx context.Context,
	req *connect.Request[tracespb.CreateScoreRequest],
) (*connect.Response[tracespb.CreateScoreResponse], error) {
	if s.scoreRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("score recorder not configured"))
	}

	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}
	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}

	// Build score from request
	score := &scores.Score{
		TraceID:  traceID,
		Name:     req.Msg.GetName(),
		Source:   scores.ScoreSource(req.Msg.GetSource()),
		DataType: scores.ScoreDataType(req.Msg.GetDataType()),
	}

	if req.Msg.ObservationId != nil {
		score.ObservationID = *req.Msg.ObservationId
	}
	if req.Msg.NumericValue != nil {
		score.NumericValue = req.Msg.NumericValue
	}
	if req.Msg.StringValue != nil {
		score.StringValue = req.Msg.StringValue
	}
	if req.Msg.BooleanValue != nil {
		score.BooleanValue = req.Msg.BooleanValue
		// Also set numeric value for boolean scores
		numVal := 0.0
		if *req.Msg.BooleanValue {
			numVal = 1.0
		}
		score.NumericValue = &numVal
	}
	if req.Msg.Comment != nil {
		score.Comment = *req.Msg.Comment
	}

	if err := scores.ValidateFixAttemptVerdict(score); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.scoreRecorder.Record(ctx, score); err != nil {
		logger.WithFields("trace_id", traceID, "error", err.Error()).Error("failed to create score")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create score: %w", err))
	}

	return connect.NewResponse(&tracespb.CreateScoreResponse{
		Score: toProtoScore(score),
	}), nil
}

// DeleteScore deletes a score
func (s *Server) DeleteScore(
	ctx context.Context,
	req *connect.Request[tracespb.DeleteScoreRequest],
) (*connect.Response[tracespb.DeleteScoreResponse], error) {
	if s.scoreRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("score recorder not configured"))
	}

	scoreID := req.Msg.GetScoreId()
	traceID := req.Msg.GetTraceId()
	if scoreID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("score_id is required"))
	}
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}

	if err := s.scoreRecorder.Delete(ctx, scoreID, traceID); err != nil {
		logger.WithFields("score_id", scoreID, "trace_id", traceID, "error", err.Error()).Error("failed to delete score")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete score: %w", err))
	}

	return connect.NewResponse(&tracespb.DeleteScoreResponse{}), nil
}

// GetTraceOverlay retrieves the latest presentation overlay for a trace.
func (s *Server) GetTraceOverlay(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceOverlayRequest],
) (*connect.Response[tracespb.GetTraceOverlayResponse], error) {
	if s.overlayRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("trace overlay recorder not configured"))
	}
	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}
	overlay, err := s.overlayRecorder.GetOverlay(ctx, traceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return connect.NewResponse(&tracespb.GetTraceOverlayResponse{}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get trace overlay: %w", err))
	}
	return connect.NewResponse(&tracespb.GetTraceOverlayResponse{
		Overlay: toProtoOverlay(overlay),
	}), nil
}

// UpdateTraceOverlay appends a new presentation overlay version for a trace.
func (s *Server) UpdateTraceOverlay(
	ctx context.Context,
	req *connect.Request[tracespb.UpdateTraceOverlayRequest],
) (*connect.Response[tracespb.UpdateTraceOverlayResponse], error) {
	if s.overlayRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("trace overlay recorder not configured"))
	}
	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}
	overlay := &traceoverlays.Overlay{
		TraceID:        traceID,
		DisplayName:    req.Msg.DisplayName,
		InputOverride:  req.Msg.InputOverride,
		OutputOverride: req.Msg.OutputOverride,
		Metadata:       req.Msg.GetMetadata(),
		Tags:           req.Msg.GetTags(),
		HiddenSpanIDs:  req.Msg.GetHiddenSpanIds(),
	}
	if err := s.overlayRecorder.PutOverlay(ctx, overlay); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update trace overlay: %w", err))
	}
	return connect.NewResponse(&tracespb.UpdateTraceOverlayResponse{
		Overlay: toProtoOverlay(overlay),
	}), nil
}

// CreateCustomObservation appends a user/API observation to a trace.
func (s *Server) CreateCustomObservation(
	ctx context.Context,
	req *connect.Request[tracespb.CreateCustomObservationRequest],
) (*connect.Response[tracespb.CreateCustomObservationResponse], error) {
	if s.overlayRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("trace overlay recorder not configured"))
	}
	obs := customObservationFromRequest(req.Msg)
	if err := s.overlayRecorder.CreateObservation(ctx, obs); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create custom observation: %w", err))
	}
	return connect.NewResponse(&tracespb.CreateCustomObservationResponse{
		Observation: toProtoCustomObservation(obs),
	}), nil
}

// BatchCreateCustomObservations appends several user/API observations.
func (s *Server) BatchCreateCustomObservations(
	ctx context.Context,
	req *connect.Request[tracespb.BatchCreateCustomObservationsRequest],
) (*connect.Response[tracespb.BatchCreateCustomObservationsResponse], error) {
	if s.overlayRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("trace overlay recorder not configured"))
	}
	requests := req.Msg.GetObservations()
	observations := make([]*traceoverlays.Observation, 0, len(requests))
	for _, item := range requests {
		observations = append(observations, customObservationFromRequest(item))
	}
	if err := s.overlayRecorder.CreateObservations(ctx, observations); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create custom observations: %w", err))
	}
	pb := make([]*tracespb.CustomObservation, 0, len(observations))
	for _, obs := range observations {
		pb = append(pb, toProtoCustomObservation(obs))
	}
	return connect.NewResponse(&tracespb.BatchCreateCustomObservationsResponse{
		Observations: pb,
	}), nil
}

// ListCustomObservations lists user/API-authored observations for a trace.
func (s *Server) ListCustomObservations(
	ctx context.Context,
	req *connect.Request[tracespb.ListCustomObservationsRequest],
) (*connect.Response[tracespb.ListCustomObservationsResponse], error) {
	if s.overlayRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("trace overlay recorder not configured"))
	}
	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}
	observations, err := s.overlayRecorder.ListObservations(ctx, traceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list custom observations: %w", err))
	}
	pb := make([]*tracespb.CustomObservation, 0, len(observations))
	for _, obs := range observations {
		pb = append(pb, toProtoCustomObservation(obs))
	}
	return connect.NewResponse(&tracespb.ListCustomObservationsResponse{
		Observations: pb,
	}), nil
}

// CreateTraceAnnotation appends an annotation to a trace or observation.
func (s *Server) CreateTraceAnnotation(
	ctx context.Context,
	req *connect.Request[tracespb.CreateTraceAnnotationRequest],
) (*connect.Response[tracespb.CreateTraceAnnotationResponse], error) {
	if s.overlayRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("trace overlay recorder not configured"))
	}
	annotation := &traceoverlays.Annotation{
		TraceID:       req.Msg.GetTraceId(),
		ObservationID: req.Msg.GetObservationId(),
		Body:          req.Msg.GetBody(),
		Metadata:      req.Msg.GetMetadata(),
	}
	if err := s.overlayRecorder.CreateAnnotation(ctx, annotation); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create trace annotation: %w", err))
	}
	return connect.NewResponse(&tracespb.CreateTraceAnnotationResponse{
		Annotation: toProtoAnnotation(annotation),
	}), nil
}

// ListTraceAnnotations lists annotations for a trace.
func (s *Server) ListTraceAnnotations(
	ctx context.Context,
	req *connect.Request[tracespb.ListTraceAnnotationsRequest],
) (*connect.Response[tracespb.ListTraceAnnotationsResponse], error) {
	if s.overlayRecorder == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("trace overlay recorder not configured"))
	}
	traceID := req.Msg.GetTraceId()
	if traceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("trace_id is required"))
	}
	annotations, err := s.overlayRecorder.ListAnnotations(ctx, traceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list trace annotations: %w", err))
	}
	pb := make([]*tracespb.TraceAnnotation, 0, len(annotations))
	for _, annotation := range annotations {
		pb = append(pb, toProtoAnnotation(annotation))
	}
	return connect.NewResponse(&tracespb.ListTraceAnnotationsResponse{
		Annotations: pb,
	}), nil
}

// toProtoScore converts a score to protobuf Score
func toProtoScore(score *scores.Score) *tracespb.Score {
	pbScore := &tracespb.Score{
		Id:        score.ID,
		TraceId:   score.TraceID,
		Timestamp: timestamppb.New(score.Timestamp),
		CreatedAt: timestamppb.New(score.CreatedAt),
		UpdatedAt: timestamppb.New(score.UpdatedAt),
		Name:      score.Name,
		Source:    string(score.Source),
		DataType:  string(score.DataType),
	}
	if score.ObservationID != "" {
		pbScore.ObservationId = &score.ObservationID
	}
	if score.NumericValue != nil {
		pbScore.NumericValue = score.NumericValue
	}
	if score.StringValue != nil {
		pbScore.StringValue = score.StringValue
	}
	if score.BooleanValue != nil {
		pbScore.BooleanValue = score.BooleanValue
	}
	if score.Comment != "" {
		pbScore.Comment = &score.Comment
	}
	if score.AuthorUserID != "" {
		pbScore.AuthorUserId = &score.AuthorUserID
	}
	return pbScore
}

// ============================================================================
// Custom columns (user-defined trace columns)
// ============================================================================

// resolveCustomColumns fills pbTrace.CustomColumns from the tenant's column
// definitions. v1 resolves metadata-source columns from the already-parsed
// trace metadata; attribute- and score-source columns are defined but resolved
// in a later pass (the list query does not carry arbitrary span attributes or
// scores), so they are skipped here rather than guessed.
func resolveCustomColumns(defs []customcolumns.StoredColumn, pbTrace *tracespb.Trace, scores map[string]float64) {
	if len(defs) == 0 || pbTrace == nil {
		return
	}
	var meta map[string]interface{}
	if len(pbTrace.Metadata) > 0 {
		meta = make(map[string]interface{}, len(pbTrace.Metadata))
		for k, v := range pbTrace.Metadata {
			meta[k] = v
		}
	}
	for _, d := range defs {
		var val customcolumns.Value
		switch d.Source {
		case customcolumns.SourceMetadata:
			val = d.Definition().Resolve(nil, meta, nil)
		case customcolumns.SourceScore:
			if scores == nil {
				continue
			}
			val = d.Definition().Resolve(nil, nil, scores)
		default:
			continue // attribute-sourced columns are resolved by the list query
		}
		if !val.Set {
			continue
		}
		// Merge into the attribute-sourced values already set by toProtoTrace.
		if pbTrace.CustomColumns == nil {
			pbTrace.CustomColumns = map[string]string{}
		}
		pbTrace.CustomColumns[d.Key] = customColumnValueString(val, d.ValueType)
	}
}

// hasScoreSourceDefs reports whether any definition is score-sourced.
func hasScoreSourceDefs(defs []customcolumns.StoredColumn) bool {
	for _, d := range defs {
		if d.Source == customcolumns.SourceScore {
			return true
		}
	}
	return false
}

// scoresByTraceForPage batch-fetches scores for a page of traces (one query)
// and returns, per trace id, the latest numeric value per score name, ready for
// score-sourced custom column resolution. Returns nil when no score columns are
// configured or the recorder is absent.
func (s *Server) scoresByTraceForPage(ctx context.Context, traces []query.TraceReadModel, defs []customcolumns.StoredColumn) map[string]map[string]float64 {
	if s.scoreRecorder == nil || !hasScoreSourceDefs(defs) || len(traces) == 0 {
		return nil
	}
	ids := make([]string, 0, len(traces))
	for _, t := range traces {
		if t.TraceID != "" {
			ids = append(ids, t.TraceID)
		}
	}
	raw, err := s.scoreRecorder.GetScoresByTraces(ctx, ids)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to batch-fetch scores for custom columns")
		return nil
	}
	out := make(map[string]map[string]float64, len(raw))
	for traceID, scoreList := range raw {
		m := map[string]float64{}
		// scoreList is ordered Timestamp DESC, so the first per name is latest.
		for _, sc := range scoreList {
			if sc.Name == "" {
				continue
			}
			if _, seen := m[sc.Name]; seen {
				continue
			}
			if sc.NumericValue != nil {
				m[sc.Name] = *sc.NumericValue
			} else if sc.BooleanValue != nil {
				if *sc.BooleanValue {
					m[sc.Name] = 1
				} else {
					m[sc.Name] = 0
				}
			}
		}
		out[traceID] = m
	}
	return out
}

// customAttrColumnsFromDefs selects the attribute-sourced columns and maps them
// to the query's CustomAttrColumn shape so the list query can project them.
func customAttrColumnsFromDefs(defs []customcolumns.StoredColumn) []traceshandler.CustomAttrColumn {
	var cols []traceshandler.CustomAttrColumn
	for _, d := range defs {
		if d.Source != customcolumns.SourceAttribute || d.SourceRef == "" {
			continue
		}
		if customcolumns.ValidateKey(d.Key) != nil {
			continue // defensive: never inline an unvalidated key
		}
		cols = append(cols, traceshandler.CustomAttrColumn{Key: d.Key, Ref: d.SourceRef})
	}
	return cols
}

// customColumnValueString renders a resolved typed value as the string the
// proto map carries; the frontend formats per the column's declared type.
func customColumnValueString(val customcolumns.Value, vt customcolumns.ValueType) string {
	switch vt {
	case customcolumns.TypeNumber:
		return strconv.FormatFloat(val.Number, 'f', -1, 64)
	case customcolumns.TypeBool:
		return strconv.FormatBool(val.Bool)
	case customcolumns.TypeDate:
		return val.Date.Format(time.RFC3339)
	default:
		return val.String
	}
}

func customColumnToProto(c customcolumns.StoredColumn) *tracespb.CustomColumnDef {
	return &tracespb.CustomColumnDef{
		Key:       c.Key,
		Label:     c.Label,
		ValueType: string(c.ValueType),
		Source:    string(c.Source),
		SourceRef: c.SourceRef,
		Position:  c.Position,
	}
}

// ListCustomColumns returns the tenant's registered custom columns.
func (s *Server) ListCustomColumns(
	ctx context.Context,
	_ *connect.Request[tracespb.ListCustomColumnsRequest],
) (*connect.Response[tracespb.ListCustomColumnsResponse], error) {
	if s.customColumns == nil {
		return connect.NewResponse(&tracespb.ListCustomColumnsResponse{}), nil
	}
	defs, err := s.customColumns.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	cols := make([]*tracespb.CustomColumnDef, 0, len(defs))
	for _, d := range defs {
		cols = append(cols, customColumnToProto(d))
	}
	return connect.NewResponse(&tracespb.ListCustomColumnsResponse{Columns: cols}), nil
}

// UpsertCustomColumn creates or updates a custom column definition.
func (s *Server) UpsertCustomColumn(
	ctx context.Context,
	req *connect.Request[tracespb.UpsertCustomColumnRequest],
) (*connect.Response[tracespb.UpsertCustomColumnResponse], error) {
	if s.customColumns == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("custom columns not configured"))
	}
	in := req.Msg.GetColumn()
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("column is required"))
	}
	col := &customcolumns.StoredColumn{
		Key:       strings.TrimSpace(in.GetKey()),
		Label:     in.GetLabel(),
		ValueType: customcolumns.ValueType(in.GetValueType()),
		Source:    customcolumns.Source(in.GetSource()),
		SourceRef: in.GetSourceRef(),
		Position:  in.GetPosition(),
	}
	if err := s.customColumns.Put(ctx, col); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tracespb.UpsertCustomColumnResponse{Column: customColumnToProto(*col)}), nil
}

// DeleteCustomColumn removes a custom column definition by key.
func (s *Server) DeleteCustomColumn(
	ctx context.Context,
	req *connect.Request[tracespb.DeleteCustomColumnRequest],
) (*connect.Response[tracespb.DeleteCustomColumnResponse], error) {
	if s.customColumns == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("custom columns not configured"))
	}
	if err := s.customColumns.Delete(ctx, strings.TrimSpace(req.Msg.GetKey())); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tracespb.DeleteCustomColumnResponse{}), nil
}

// ============================================================================
// Saved views
// ============================================================================

func traceViewToProto(v traceviews.View) *tracespb.TraceView {
	return &tracespb.TraceView{
		Id:           v.ID,
		Name:         v.Name,
		ConfigJson:   v.ConfigJSON,
		AuthorUserId: v.AuthorUserID,
	}
}

// ListTraceViews returns the tenant's saved views.
func (s *Server) ListTraceViews(
	ctx context.Context,
	_ *connect.Request[tracespb.ListTraceViewsRequest],
) (*connect.Response[tracespb.ListTraceViewsResponse], error) {
	if s.traceViews == nil {
		return connect.NewResponse(&tracespb.ListTraceViewsResponse{}), nil
	}
	views, err := s.traceViews.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*tracespb.TraceView, 0, len(views))
	for _, v := range views {
		out = append(out, traceViewToProto(v))
	}
	return connect.NewResponse(&tracespb.ListTraceViewsResponse{Views: out}), nil
}

// UpsertTraceView creates or updates a saved view.
func (s *Server) UpsertTraceView(
	ctx context.Context,
	req *connect.Request[tracespb.UpsertTraceViewRequest],
) (*connect.Response[tracespb.UpsertTraceViewResponse], error) {
	if s.traceViews == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("saved views not configured"))
	}
	in := req.Msg.GetView()
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("view is required"))
	}
	v := &traceviews.View{
		ID:         strings.TrimSpace(in.GetId()),
		Name:       strings.TrimSpace(in.GetName()),
		ConfigJSON: in.GetConfigJson(),
	}
	if err := s.traceViews.Put(ctx, v); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tracespb.UpsertTraceViewResponse{View: traceViewToProto(*v)}), nil
}

// DeleteTraceView removes a saved view by id.
func (s *Server) DeleteTraceView(
	ctx context.Context,
	req *connect.Request[tracespb.DeleteTraceViewRequest],
) (*connect.Response[tracespb.DeleteTraceViewResponse], error) {
	if s.traceViews == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("saved views not configured"))
	}
	if err := s.traceViews.Delete(ctx, strings.TrimSpace(req.Msg.GetId())); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tracespb.DeleteTraceViewResponse{}), nil
}

// ============================================================================
// Semantic mappings
// ============================================================================

// ListSemanticMappings returns the tenant's field aliases.
func (s *Server) ListSemanticMappings(
	ctx context.Context,
	_ *connect.Request[tracespb.ListSemanticMappingsRequest],
) (*connect.Response[tracespb.ListSemanticMappingsResponse], error) {
	if s.semanticMappings == nil {
		return connect.NewResponse(&tracespb.ListSemanticMappingsResponse{}), nil
	}
	list, err := s.semanticMappings.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*tracespb.SemanticMapping, 0, len(list))
	for _, m := range list {
		out = append(out, &tracespb.SemanticMapping{Field: m.Field, AttrKey: m.AttrKey})
	}
	return connect.NewResponse(&tracespb.ListSemanticMappingsResponse{Mappings: out}), nil
}

// AddSemanticMapping adds an attribute-key alias for a field.
func (s *Server) AddSemanticMapping(
	ctx context.Context,
	req *connect.Request[tracespb.AddSemanticMappingRequest],
) (*connect.Response[tracespb.AddSemanticMappingResponse], error) {
	if s.semanticMappings == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("semantic mappings not configured"))
	}
	field := strings.TrimSpace(req.Msg.GetField())
	attrKey := strings.TrimSpace(req.Msg.GetAttrKey())
	if err := s.semanticMappings.Add(ctx, field, attrKey); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tracespb.AddSemanticMappingResponse{
		Mapping: &tracespb.SemanticMapping{Field: field, AttrKey: attrKey},
	}), nil
}

// DeleteSemanticMapping removes an attribute-key alias for a field.
func (s *Server) DeleteSemanticMapping(
	ctx context.Context,
	req *connect.Request[tracespb.DeleteSemanticMappingRequest],
) (*connect.Response[tracespb.DeleteSemanticMappingResponse], error) {
	if s.semanticMappings == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("semantic mappings not configured"))
	}
	if err := s.semanticMappings.Delete(ctx, strings.TrimSpace(req.Msg.GetField()), strings.TrimSpace(req.Msg.GetAttrKey())); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tracespb.DeleteSemanticMappingResponse{}), nil
}

// ============================================================================
// Classification rules
// ============================================================================

// ListClassificationRules returns the tenant's classification rules.
func (s *Server) ListClassificationRules(
	ctx context.Context,
	_ *connect.Request[tracespb.ListClassificationRulesRequest],
) (*connect.Response[tracespb.ListClassificationRulesResponse], error) {
	if s.classificationRules == nil {
		return connect.NewResponse(&tracespb.ListClassificationRulesResponse{}), nil
	}
	rules, err := s.classificationRules.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*tracespb.ClassificationRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, &tracespb.ClassificationRule{Pattern: r.Pattern, Kind: r.Kind})
	}
	return connect.NewResponse(&tracespb.ListClassificationRulesResponse{Rules: out}), nil
}

// AddClassificationRule adds a SpanName-pattern -> kind rule.
func (s *Server) AddClassificationRule(
	ctx context.Context,
	req *connect.Request[tracespb.AddClassificationRuleRequest],
) (*connect.Response[tracespb.AddClassificationRuleResponse], error) {
	if s.classificationRules == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("classification rules not configured"))
	}
	pattern := strings.TrimSpace(req.Msg.GetPattern())
	kind := strings.TrimSpace(req.Msg.GetKind())
	if err := s.classificationRules.Add(ctx, pattern, kind); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tracespb.AddClassificationRuleResponse{
		Rule: &tracespb.ClassificationRule{Pattern: pattern, Kind: kind},
	}), nil
}

// DeleteClassificationRule removes a classification rule.
func (s *Server) DeleteClassificationRule(
	ctx context.Context,
	req *connect.Request[tracespb.DeleteClassificationRuleRequest],
) (*connect.Response[tracespb.DeleteClassificationRuleResponse], error) {
	if s.classificationRules == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("classification rules not configured"))
	}
	if err := s.classificationRules.Delete(ctx, strings.TrimSpace(req.Msg.GetPattern()), strings.TrimSpace(req.Msg.GetKind())); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tracespb.DeleteClassificationRuleResponse{}), nil
}
