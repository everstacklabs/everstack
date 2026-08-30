package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
)

// resolveTenantID extracts the tenant ID from the request context. The auth
// middleware is the only trusted source of the tenant identity — the query
// string and request body are never consulted, because both are
// client-controlled and would let any caller read another tenant's data.
// Callers should treat an empty return as "request is unauthenticated" and
// reject with 401/403.
func resolveTenantID(r *http.Request) string {
	if tid := contextkeys.GetTenantID(r.Context()); tid != "" {
		return tid
	}
	return contextkeys.ExtractTenantID(r.Context())
}

// requireTenantIDREST returns the request's tenant ID or writes a 403 and
// returns false. Use at the top of every memory REST handler so a missing
// tenant context can never silently fall through to a global query.
func requireTenantIDREST(w http.ResponseWriter, r *http.Request) (string, bool) {
	tid := resolveTenantID(r)
	if tid == "" {
		writeError(w, http.StatusForbidden, "tenant context missing")
		return "", false
	}
	return tid, true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleCreateCollection handles POST /v1/memory/collections.
func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	start := time.Now()
	tenantID, ok := requireTenantIDREST(w, r)
	if !ok {
		return
	}

	var body struct {
		Name               string            `json:"name"`
		Description        string            `json:"description"`
		EmbeddingModel     string            `json:"embedding_model"`
		EmbeddingDimension int               `json:"embedding_dimension"`
		DistanceMetric     string            `json:"distance_metric"`
		Metadata           map[string]string `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	model := body.EmbeddingModel
	if model == "" {
		model = s.defaultModel
	}
	dim := body.EmbeddingDimension
	if dim <= 0 {
		dim = s.defaultDim
	}
	metric := memory.DistanceMetric(body.DistanceMetric)
	if metric == "" {
		metric = memory.DistanceCosine
	}

	coll, err := s.store.CreateCollection(r.Context(), tenantID, memory.CollectionOptions{
		Name:               body.Name,
		Description:        body.Description,
		EmbeddingModel:     model,
		EmbeddingDimension: dim,
		DistanceMetric:     metric,
		Metadata:           body.Metadata,
	})
	if err != nil {
		logger.WithFields("error", err.Error()).Error("memory: failed to create collection")
		s.logRequest(tenantID, "", body.Name, "create_collection", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.logRequest(tenantID, coll.ID, coll.Name, "create_collection", "api", start, nil, nil, "")
	writeJSON(w, http.StatusCreated, map[string]interface{}{"collection": collectionToJSON(coll)})
}

// handleListCollections handles GET /v1/memory/collections.
func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	start := time.Now()
	tenantID, ok := requireTenantIDREST(w, r)
	if !ok {
		return
	}

	colls, err := s.store.ListCollections(r.Context(), tenantID)
	if err != nil {
		s.logRequest(tenantID, "", "", "list_collections", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rc := len(colls)
	s.logRequest(tenantID, "", "", "list_collections", "api", start, &rc, nil, "")

	out := make([]map[string]interface{}, 0, len(colls))
	for _, c := range colls {
		out = append(out, collectionToJSON(c))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"collections": out})
}

// handleGetCollection handles GET /v1/memory/collections/{name}.
func (s *Server) handleGetCollection(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	start := time.Now()
	tenantID, ok := requireTenantIDREST(w, r)
	if !ok {
		return
	}
	name := pathParams["name"]

	coll, err := s.store.GetCollection(r.Context(), tenantID, name)
	if err != nil {
		s.logRequest(tenantID, "", name, "get_collection", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.logRequest(tenantID, coll.ID, coll.Name, "get_collection", "api", start, nil, nil, "")
	writeJSON(w, http.StatusOK, map[string]interface{}{"collection": collectionToJSON(coll)})
}

// handleDeleteCollection handles DELETE /v1/memory/collections/{name}.
func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	start := time.Now()
	tenantID, ok := requireTenantIDREST(w, r)
	if !ok {
		return
	}
	name := pathParams["name"]

	if err := s.store.DeleteCollection(r.Context(), tenantID, name); err != nil {
		s.logRequest(tenantID, "", name, "delete_collection", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.logRequest(tenantID, "", name, "delete_collection", "api", start, nil, nil, "")
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleAddDocuments handles POST /v1/memory/collections/{name}/documents.
func (s *Server) handleAddDocuments(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	start := time.Now()
	tenantID, ok := requireTenantIDREST(w, r)
	if !ok {
		return
	}
	collectionName := pathParams["name"]

	var body struct {
		Documents []struct {
			Content  string            `json:"content"`
			Metadata map[string]string `json:"metadata"`
			Source   string            `json:"source"`
		} `json:"documents"`
		ChunkSize int `json:"chunk_size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Resolve collection
	coll, err := s.store.GetCollection(r.Context(), tenantID, collectionName)
	if err != nil {
		s.logRequest(tenantID, "", collectionName, "add_documents", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusNotFound, "collection not found: "+err.Error())
		return
	}

	chunkSize := body.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 512
	}

	embModel := coll.EmbeddingModel
	if embModel == "" {
		embModel = s.defaultModel
	}

	var allDocIDs []string
	totalChunks := 0

	for _, doc := range body.Documents {
		// Add document
		docs := []memory.Document{{
			Content:  doc.Content,
			Metadata: doc.Metadata,
			Source:   doc.Source,
		}}
		docIDs, err := s.store.AddDocuments(r.Context(), coll.ID, docs)
		if err != nil {
			s.logRequest(tenantID, coll.ID, collectionName, "add_documents", "api", start, nil, nil, err.Error())
			writeError(w, http.StatusInternalServerError, "failed to add document: "+err.Error())
			return
		}
		allDocIDs = append(allDocIDs, docIDs...)

		// Chunk and embed
		chunks := memory.ChunkText(doc.Content, chunkSize)
		embeddings, err := s.embedder.EmbedBatch(r.Context(), embModel, chunks)
		if err != nil {
			s.logRequest(tenantID, coll.ID, collectionName, "add_documents", "api", start, nil, nil, err.Error())
			writeError(w, http.StatusInternalServerError, "embedding failed: "+err.Error())
			return
		}

		memChunks := make([]memory.Chunk, len(chunks))
		for i, chunkText := range chunks {
			meta := map[string]string{}
			if doc.Source != "" {
				meta["source"] = doc.Source
			}
			for k, v := range doc.Metadata {
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

		if err := s.store.Store(r.Context(), coll.ID, memChunks); err != nil {
			s.logRequest(tenantID, coll.ID, collectionName, "add_documents", "api", start, nil, nil, err.Error())
			writeError(w, http.StatusInternalServerError, "failed to store embeddings: "+err.Error())
			return
		}
		totalChunks += len(memChunks)
	}

	dc := len(allDocIDs)
	s.logRequest(tenantID, coll.ID, collectionName, "add_documents", "api", start, &dc, &totalChunks, "")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"document_ids":   allDocIDs,
		"chunks_created": totalChunks,
	})
}

// handleDeleteDocument handles DELETE /v1/memory/collections/{name}/documents/{document_id}.
func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	start := time.Now()
	tenantID, ok := requireTenantIDREST(w, r)
	if !ok {
		return
	}
	collectionName := pathParams["name"]
	documentID := pathParams["document_id"]

	if err := s.store.DeleteDocument(r.Context(), tenantID, documentID); err != nil {
		s.logRequest(tenantID, "", collectionName, "delete_document", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.logRequest(tenantID, "", collectionName, "delete_document", "api", start, nil, nil, "")
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleQueryCollection handles POST /v1/memory/collections/{name}/query.
func (s *Server) handleQueryCollection(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	start := time.Now()
	tenantID, ok := requireTenantIDREST(w, r)
	if !ok {
		return
	}
	collectionName := pathParams["name"]

	var body struct {
		Query          string            `json:"query"`
		TopK           int               `json:"top_k"`
		MinScore       float32           `json:"min_score"`
		MetadataFilter map[string]string `json:"metadata_filter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	// Resolve collection
	coll, err := s.store.GetCollection(r.Context(), tenantID, collectionName)
	if err != nil {
		s.logRequest(tenantID, "", collectionName, "query", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusNotFound, "collection not found: "+err.Error())
		return
	}

	embModel := coll.EmbeddingModel
	if embModel == "" {
		embModel = s.defaultModel
	}

	// Embed query
	embedding, err := s.embedder.Embed(r.Context(), embModel, body.Query)
	if err != nil {
		s.logRequest(tenantID, coll.ID, collectionName, "query", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusInternalServerError, "embedding failed: "+err.Error())
		return
	}

	topK := body.TopK
	if topK <= 0 {
		topK = 5
	}

	results, err := s.store.Query(r.Context(), coll.ID, embedding, memory.QueryOptions{
		TopK:           topK,
		MinScore:       body.MinScore,
		MetadataFilter: body.MetadataFilter,
	})
	if err != nil {
		s.logRequest(tenantID, coll.ID, collectionName, "query", "api", start, nil, nil, err.Error())
		writeError(w, http.StatusInternalServerError, "query failed: "+err.Error())
		return
	}

	rc := len(results)
	s.logRequest(tenantID, coll.ID, collectionName, "query", "api", start, &rc, nil, "")

	out := make([]map[string]interface{}, len(results))
	for i, r := range results {
		out[i] = map[string]interface{}{
			"document_id": r.DocumentID,
			"chunk_text":  r.ChunkText,
			"chunk_index": r.ChunkIndex,
			"score":       r.Score,
			"metadata":    r.Metadata,
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"results": out})
}

// handleGetMemoryAnalytics handles POST /v1/memory/analytics.
func (s *Server) handleGetMemoryAnalytics(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if s.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "analytics not available")
		return
	}

	tenantID, ok := requireTenantIDREST(w, r)
	if !ok {
		return
	}

	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	to, _ := time.Parse(time.RFC3339, body.To)
	from, _ := time.Parse(time.RFC3339, body.From)
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}

	summary, err := memory.GetAnalytics(r.Context(), s.DB, tenantID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("analytics: %v", err))
		return
	}

	buckets := make([]map[string]interface{}, len(summary.Buckets))
	for i, b := range summary.Buckets {
		buckets[i] = map[string]interface{}{
			"timestamp":      b.Timestamp.Format(time.RFC3339),
			"query_count":    b.QueryCount,
			"store_count":    b.StoreCount,
			"delete_count":   b.DeleteCount,
			"error_count":    b.ErrorCount,
			"avg_latency_ms": b.AvgLatencyMs,
		}
	}

	topColls := make([]map[string]interface{}, len(summary.TopCollections))
	for i, c := range summary.TopCollections {
		topColls[i] = map[string]interface{}{
			"collection_name": c.CollectionName,
			"request_count":   c.RequestCount,
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"buckets":         buckets,
		"total_requests":  summary.TotalRequests,
		"total_errors":    summary.TotalErrors,
		"avg_latency_ms":  summary.AvgLatencyMs,
		"top_collections": topColls,
	})
}

// handleSetupPgVector handles POST /v1/memory/setup.
func (s *Server) handleSetupPgVector(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if s.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}

	if err := memory.EnsurePgVector(r.Context(), s.DB); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("pgvector setup failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "pgvector extension and embedding column configured successfully",
	})
}

// collectionToJSON converts a memory.Collection to a JSON-friendly map.
func collectionToJSON(c *memory.Collection) map[string]interface{} {
	m := map[string]interface{}{
		"id":                  c.ID,
		"tenant_id":           c.TenantID,
		"name":                c.Name,
		"description":         c.Description,
		"embedding_model":     c.EmbeddingModel,
		"embedding_dimension": c.EmbeddingDimension,
		"distance_metric":     string(c.DistanceMetric),
		"document_count":      c.DocumentCount,
		"created_at":          c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"updated_at":          c.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if c.Metadata != nil {
		m["metadata"] = c.Metadata
	}
	return m
}
