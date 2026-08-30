package v1

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
	memorypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/memory/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var errBackendNotConfigured = errors.New("memory backend not configured — set features.enable_memory: true in your gateway config with a valid memory backend and embedding model")

func (s *Server) requireBackend() error {
	if s.store == nil || s.embedder == nil {
		return connect.NewError(connect.CodeUnavailable, errBackendNotConfigured)
	}
	return nil
}

// requireTenantID returns the tenant ID set by the auth middleware. It never
// consults request fields, because those are client-controlled and would let
// any caller read another tenant's data. An empty result is treated as an
// unauthenticated request.
func requireTenantID(ctx context.Context) (string, error) {
	tid := contextkeys.GetTenantID(ctx)
	if tid != "" {
		return tid, nil
	}
	return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
}

// CreateCollection implements the ConnectRPC handler.
func (s *Server) CreateCollection(ctx context.Context, req *connect.Request[memorypb.CreateCollectionRequest]) (*connect.Response[memorypb.CreateCollectionResponse], error) {
	start := time.Now()
	if err := s.requireBackend(); err != nil {
		return nil, err
	}
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}

	model := req.Msg.GetEmbeddingModel()
	if model == "" {
		model = s.defaultModel
	}
	dim := int(req.Msg.GetEmbeddingDimension())
	if dim <= 0 {
		dim = s.defaultDim
	}
	metric := memory.DistanceMetric(req.Msg.GetDistanceMetric())
	if metric == "" {
		metric = memory.DistanceCosine
	}

	coll, err := s.store.CreateCollection(ctx, tenantID, memory.CollectionOptions{
		Name:               req.Msg.GetName(),
		Description:        req.Msg.GetDescription(),
		EmbeddingModel:     model,
		EmbeddingDimension: dim,
		DistanceMetric:     metric,
		Metadata:           req.Msg.GetMetadata(),
	})
	if err != nil {
		s.logRequest(tenantID, "", req.Msg.GetName(), "create_collection", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logRequest(tenantID, coll.ID, coll.Name, "create_collection", "admin", start, nil, nil, "")
	return connect.NewResponse(&memorypb.CreateCollectionResponse{
		Collection: collectionToProto(coll),
	}), nil
}

// GetCollection implements the ConnectRPC handler.
func (s *Server) GetCollection(ctx context.Context, req *connect.Request[memorypb.GetCollectionRequest]) (*connect.Response[memorypb.GetCollectionResponse], error) {
	start := time.Now()
	if err := s.requireBackend(); err != nil {
		return nil, err
	}
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}

	coll, err := s.store.GetCollection(ctx, tenantID, req.Msg.GetName())
	if err != nil {
		s.logRequest(tenantID, "", req.Msg.GetName(), "get_collection", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	s.logRequest(tenantID, coll.ID, coll.Name, "get_collection", "admin", start, nil, nil, "")
	return connect.NewResponse(&memorypb.GetCollectionResponse{
		Collection: collectionToProto(coll),
	}), nil
}

// ListCollections implements the ConnectRPC handler.
func (s *Server) ListCollections(ctx context.Context, req *connect.Request[memorypb.ListCollectionsRequest]) (*connect.Response[memorypb.ListCollectionsResponse], error) {
	start := time.Now()
	if err := s.requireBackend(); err != nil {
		return nil, err
	}
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}

	colls, err := s.store.ListCollections(ctx, tenantID)
	if err != nil {
		s.logRequest(tenantID, "", "", "list_collections", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	rc := len(colls)
	s.logRequest(tenantID, "", "", "list_collections", "admin", start, &rc, nil, "")

	protos := make([]*memorypb.Collection, len(colls))
	for i, c := range colls {
		protos[i] = collectionToProto(c)
	}

	return connect.NewResponse(&memorypb.ListCollectionsResponse{
		Collections: protos,
	}), nil
}

// DeleteCollection implements the ConnectRPC handler.
func (s *Server) DeleteCollection(ctx context.Context, req *connect.Request[memorypb.DeleteCollectionRequest]) (*connect.Response[memorypb.DeleteCollectionResponse], error) {
	start := time.Now()
	if err := s.requireBackend(); err != nil {
		return nil, err
	}
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.store.DeleteCollection(ctx, tenantID, req.Msg.GetName()); err != nil {
		s.logRequest(tenantID, "", req.Msg.GetName(), "delete_collection", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	s.logRequest(tenantID, "", req.Msg.GetName(), "delete_collection", "admin", start, nil, nil, "")
	return connect.NewResponse(&memorypb.DeleteCollectionResponse{
		Success: true,
	}), nil
}

// AddDocuments implements the ConnectRPC handler.
func (s *Server) AddDocuments(ctx context.Context, req *connect.Request[memorypb.AddDocumentsRequest]) (*connect.Response[memorypb.AddDocumentsResponse], error) {
	start := time.Now()
	if err := s.requireBackend(); err != nil {
		return nil, err
	}
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}

	collName := req.Msg.GetCollectionName()
	coll, err := s.store.GetCollection(ctx, tenantID, collName)
	if err != nil {
		s.logRequest(tenantID, "", collName, "add_documents", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("collection %q not found: %w", collName, err))
	}

	chunkSize := int(req.Msg.GetChunkSize())
	if chunkSize <= 0 {
		chunkSize = 512
	}

	embModel := coll.EmbeddingModel
	if embModel == "" {
		embModel = s.defaultModel
	}

	var allDocIDs []string
	totalChunks := 0

	for _, doc := range req.Msg.GetDocuments() {
		docs := []memory.Document{{
			Content:  doc.GetContent(),
			Metadata: doc.GetMetadata(),
			Source:   doc.GetSource(),
		}}
		docIDs, err := s.store.AddDocuments(ctx, coll.ID, docs)
		if err != nil {
			s.logRequest(tenantID, coll.ID, collName, "add_documents", "admin", start, nil, nil, err.Error())
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("add document: %w", err))
		}
		allDocIDs = append(allDocIDs, docIDs...)

		chunks := memory.ChunkText(doc.GetContent(), chunkSize)
		embeddings, err := s.embedder.EmbedBatch(ctx, embModel, chunks)
		if err != nil {
			s.logRequest(tenantID, coll.ID, collName, "add_documents", "admin", start, nil, nil, err.Error())
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("embedding: %w", err))
		}

		memChunks := make([]memory.Chunk, len(chunks))
		for i, chunkText := range chunks {
			meta := map[string]string{}
			if doc.GetSource() != "" {
				meta["source"] = doc.GetSource()
			}
			for k, v := range doc.GetMetadata() {
				meta[k] = v
			}
			memChunks[i] = memory.Chunk{
				DocumentID: docIDs[0],
				Text:       chunkText,
				ChunkIndex: i,
				Embedding:  embeddings[i],
				Metadata:   meta,
			}
		}

		if err := s.store.Store(ctx, coll.ID, memChunks); err != nil {
			s.logRequest(tenantID, coll.ID, collName, "add_documents", "admin", start, nil, nil, err.Error())
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("store chunks: %w", err))
		}
		totalChunks += len(memChunks)
	}

	logger.WithFields("collection", collName, "documents", len(allDocIDs), "chunks", totalChunks).
		Debug("memory: documents added via ConnectRPC")

	dc := len(allDocIDs)
	s.logRequest(tenantID, coll.ID, collName, "add_documents", "admin", start, &dc, &totalChunks, "")
	return connect.NewResponse(&memorypb.AddDocumentsResponse{
		DocumentIds:   allDocIDs,
		ChunksCreated: int32(totalChunks),
	}), nil
}

// DeleteDocument implements the ConnectRPC handler.
func (s *Server) DeleteDocument(ctx context.Context, req *connect.Request[memorypb.DeleteDocumentRequest]) (*connect.Response[memorypb.DeleteDocumentResponse], error) {
	start := time.Now()
	if err := s.requireBackend(); err != nil {
		return nil, err
	}
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteDocument(ctx, tenantID, req.Msg.GetDocumentId()); err != nil {
		s.logRequest(tenantID, "", req.Msg.GetCollectionName(), "delete_document", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	s.logRequest(tenantID, "", req.Msg.GetCollectionName(), "delete_document", "admin", start, nil, nil, "")
	return connect.NewResponse(&memorypb.DeleteDocumentResponse{
		Success: true,
	}), nil
}

// QueryCollection implements the ConnectRPC handler.
func (s *Server) QueryCollection(ctx context.Context, req *connect.Request[memorypb.QueryCollectionRequest]) (*connect.Response[memorypb.QueryCollectionResponse], error) {
	start := time.Now()
	if err := s.requireBackend(); err != nil {
		return nil, err
	}
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}

	collName := req.Msg.GetCollectionName()
	coll, err := s.store.GetCollection(ctx, tenantID, collName)
	if err != nil {
		s.logRequest(tenantID, "", collName, "query", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("collection %q not found: %w", collName, err))
	}

	embModel := coll.EmbeddingModel
	if embModel == "" {
		embModel = s.defaultModel
	}

	embedding, err := s.embedder.Embed(ctx, embModel, req.Msg.GetQuery())
	if err != nil {
		s.logRequest(tenantID, coll.ID, collName, "query", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("embedding: %w", err))
	}

	topK := int(req.Msg.GetTopK())
	if topK <= 0 {
		topK = 5
	}

	results, err := s.store.Query(ctx, coll.ID, embedding, memory.QueryOptions{
		TopK:           topK,
		MinScore:       req.Msg.GetMinScore(),
		MetadataFilter: req.Msg.GetMetadataFilter(),
	})
	if err != nil {
		s.logRequest(tenantID, coll.ID, collName, "query", "admin", start, nil, nil, err.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query: %w", err))
	}

	rc := len(results)
	s.logRequest(tenantID, coll.ID, collName, "query", "admin", start, &rc, nil, "")

	protos := make([]*memorypb.SearchResult, len(results))
	for i, r := range results {
		protos[i] = &memorypb.SearchResult{
			DocumentId: r.DocumentID,
			ChunkText:  r.ChunkText,
			ChunkIndex: int32(r.ChunkIndex),
			Score:      r.Score,
			Metadata:   r.Metadata,
		}
	}

	return connect.NewResponse(&memorypb.QueryCollectionResponse{
		Results: protos,
	}), nil
}

// logRequest is a convenience wrapper around the request logger.
func (s *Server) logRequest(tenantID, collectionID, collectionName, operation, callerType string, start time.Time, resultCount, chunkCount *int, errMsg string) {
	if s.reqLogger == nil {
		return
	}
	s.reqLogger.Log(memory.RequestLog{
		TenantID:       tenantID,
		CollectionID:   collectionID,
		CollectionName: collectionName,
		Operation:      operation,
		CallerType:     callerType,
		LatencyMs:      int(time.Since(start).Milliseconds()),
		ResultCount:    resultCount,
		ChunkCount:     chunkCount,
		ErrorMessage:   errMsg,
	})
}

// GetMemoryAnalytics implements the ConnectRPC handler for analytics.
func (s *Server) GetMemoryAnalytics(ctx context.Context, req *connect.Request[memorypb.GetMemoryAnalyticsRequest]) (*connect.Response[memorypb.GetMemoryAnalyticsResponse], error) {
	if s.DB == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("analytics not available"))
	}
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	from, _ := time.Parse(time.RFC3339, req.Msg.GetFrom())
	to, _ := time.Parse(time.RFC3339, req.Msg.GetTo())
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}

	summary, err := memory.GetAnalytics(ctx, s.DB, tenantID, from, to)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("analytics: %w", err))
	}

	buckets := make([]*memorypb.AnalyticsBucket, len(summary.Buckets))
	for i, b := range summary.Buckets {
		buckets[i] = &memorypb.AnalyticsBucket{
			Timestamp:    b.Timestamp.Format(time.RFC3339),
			QueryCount:   int32(b.QueryCount),
			StoreCount:   int32(b.StoreCount),
			DeleteCount:  int32(b.DeleteCount),
			ErrorCount:   int32(b.ErrorCount),
			AvgLatencyMs: float32(b.AvgLatencyMs),
		}
	}

	topColls := make([]*memorypb.CollectionStat, len(summary.TopCollections))
	for i, c := range summary.TopCollections {
		topColls[i] = &memorypb.CollectionStat{
			CollectionName: c.CollectionName,
			RequestCount:   int32(c.RequestCount),
		}
	}

	return connect.NewResponse(&memorypb.GetMemoryAnalyticsResponse{
		Buckets:        buckets,
		TotalRequests:  int32(summary.TotalRequests),
		TotalErrors:    int32(summary.TotalErrors),
		AvgLatencyMs:   float32(summary.AvgLatencyMs),
		TopCollections: topColls,
	}), nil
}

// SetupPgVector implements the ConnectRPC handler for on-demand pgvector setup.
func (s *Server) SetupPgVector(ctx context.Context, _ *connect.Request[memorypb.SetupPgVectorRequest]) (*connect.Response[memorypb.SetupPgVectorResponse], error) {
	if s.DB == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("database not configured"))
	}

	if err := memory.EnsurePgVector(ctx, s.DB); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("pgvector setup failed: %w", err))
	}

	return connect.NewResponse(&memorypb.SetupPgVectorResponse{
		Success: true,
		Message: "pgvector extension and embedding column configured successfully",
	}), nil
}

// collectionToProto converts a memory.Collection to a proto Collection.
func collectionToProto(c *memory.Collection) *memorypb.Collection {
	return &memorypb.Collection{
		Id:                 c.ID,
		TenantId:           c.TenantID,
		Name:               c.Name,
		Description:        c.Description,
		EmbeddingModel:     c.EmbeddingModel,
		EmbeddingDimension: int32(c.EmbeddingDimension),
		DistanceMetric:     string(c.DistanceMetric),
		DocumentCount:      int32(c.DocumentCount),
		Metadata:           c.Metadata,
		CreatedAt:          timestamppb.New(c.CreatedAt),
		UpdatedAt:          timestamppb.New(c.UpdatedAt),
	}
}
