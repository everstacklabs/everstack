package v1

// Per-tenant OTLP telemetry export configuration (POR-90).
//
// When configured, sandbox metrics (from sandbox_metrics_history) are
// forwarded to the tenant's OTLP backend using HTTP/JSON OTLP.
// Supported providers: New Relic, Grafana Cloud, Datadog, Honeycomb, Jaeger.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
)

// OTLPConfig is a tenant's OTLP export configuration.
type OTLPConfig struct {
	TenantID    string            `db:"tenant_id"    json:"tenant_id"`
	Endpoint    string            `db:"endpoint"     json:"endpoint"`
	Headers     map[string]string `db:"-"            json:"headers"`
	HeadersRaw  []byte            `db:"headers"      json:"-"`
	ExtraLabels map[string]string `db:"-"            json:"extra_labels"`
	LabelsRaw   []byte            `db:"extra_labels" json:"-"`
	Enabled     bool              `db:"enabled"      json:"enabled"`
	CreatedAt   time.Time         `db:"created_at"   json:"created_at"`
	UpdatedAt   time.Time         `db:"updated_at"   json:"updated_at"`
}

// otlpRepo handles OTLP config DB operations.
type otlpRepo struct {
	db *sqlx.DB
}

func (r *otlpRepo) get(tenantID string) (*OTLPConfig, error) {
	var cfg OTLPConfig
	if err := r.db.Get(&cfg, `SELECT * FROM sandbox_otlp_configs WHERE tenant_id = $1`, tenantID); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(cfg.HeadersRaw, &cfg.Headers)
	_ = json.Unmarshal(cfg.LabelsRaw, &cfg.ExtraLabels)
	return &cfg, nil
}

func (r *otlpRepo) upsert(cfg OTLPConfig) (*OTLPConfig, error) {
	headersJSON, _ := json.Marshal(cfg.Headers)
	labelsJSON, _ := json.Marshal(cfg.ExtraLabels)
	const q = `
		INSERT INTO sandbox_otlp_configs (tenant_id, endpoint, headers, extra_labels, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		ON CONFLICT (tenant_id) DO UPDATE
		  SET endpoint     = EXCLUDED.endpoint,
		      headers      = EXCLUDED.headers,
		      extra_labels = EXCLUDED.extra_labels,
		      enabled      = EXCLUDED.enabled,
		      updated_at   = NOW()
		RETURNING *`
	var out OTLPConfig
	if err := r.db.Get(&out, q, cfg.TenantID, cfg.Endpoint, headersJSON, labelsJSON, cfg.Enabled); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(out.HeadersRaw, &out.Headers)
	_ = json.Unmarshal(out.LabelsRaw, &out.ExtraLabels)
	return &out, nil
}

// HandleGetOTLPConfig serves GET /v1/settings/otlp.
func (s *Server) HandleGetOTLPConfig(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), r.URL.Query().Get("tenant_id"))
	repo := &otlpRepo{db: s.db}
	cfg, err := repo.get(tenantID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tenant_id": tenantID,
			"endpoint":  "",
			"headers":   map[string]string{},
			"enabled":   false,
			"note":      "No OTLP config found. Use PUT /v1/settings/otlp to configure.",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

// HandleUpsertOTLPConfig serves PUT /v1/settings/otlp.
// Body: { "endpoint": "https://...", "headers": {"api-key":"..."}, "extra_labels": {}, "enabled": true }
func (s *Server) HandleUpsertOTLPConfig(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	var body struct {
		Endpoint    string            `json:"endpoint"`
		Headers     map[string]string `json:"headers"`
		ExtraLabels map[string]string `json:"extra_labels"`
		Enabled     bool              `json:"enabled"`
		TenantID    string            `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), body.TenantID)
	if body.Endpoint == "" {
		writeJSONError(w, http.StatusBadRequest, "endpoint is required")
		return
	}
	repo := &otlpRepo{db: s.db}
	cfg, err := repo.upsert(OTLPConfig{
		TenantID:    tenantID,
		Endpoint:    body.Endpoint,
		Headers:     body.Headers,
		ExtraLabels: body.ExtraLabels,
		Enabled:     body.Enabled,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

// HandleTestOTLPConfig sends a test span to the configured OTLP endpoint.
// POST /v1/settings/otlp/test
func (s *Server) HandleTestOTLPConfig(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), r.URL.Query().Get("tenant_id"))
	repo := &otlpRepo{db: s.db}
	cfg, err := repo.get(tenantID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "OTLP config not found")
		return
	}
	if !cfg.Enabled || cfg.Endpoint == "" {
		writeJSONError(w, http.StatusBadRequest, "OTLP config is disabled or has no endpoint")
		return
	}

	// Send a minimal OTLP JSON test payload.
	testPayload := buildTestOTLPPayload(tenantID)
	body, _ := json.Marshal(testPayload)

	req, err := http.NewRequestWithContext(r.Context(), "POST", cfg.Endpoint+"/v1/traces", bytes.NewReader(body))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to build request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "OTLP test failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     resp.StatusCode >= 200 && resp.StatusCode < 300,
		"status_code": resp.StatusCode,
		"endpoint":    cfg.Endpoint,
	})
}

func buildTestOTLPPayload(tenantID string) map[string]interface{} {
	return map[string]interface{}{
		"resourceSpans": []interface{}{
			map[string]interface{}{
				"resource": map[string]interface{}{
					"attributes": []interface{}{
						map[string]string{"key": "service.name", "value": "everstack-sandbox"},
						map[string]string{"key": "everstack.tenant_id", "value": tenantID},
					},
				},
				"scopeSpans": []interface{}{
					map[string]interface{}{
						"scope": map[string]string{"name": "everstack.sandbox"},
						"spans": []interface{}{
							map[string]interface{}{
								"name":              "test_span",
								"startTimeUnixNano": time.Now().UnixNano(),
								"endTimeUnixNano":   time.Now().UnixNano() + 1e6,
								"kind":              1,
								"status":            map[string]int{"code": 1},
							},
						},
					},
				},
			},
		},
	}
}
