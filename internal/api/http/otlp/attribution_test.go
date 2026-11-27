package otlp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/lib/authratelimit"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type captureConn struct {
	driver.Conn
	batch *captureBatch
}

func (c captureConn) PrepareBatch(context.Context, string, ...driver.PrepareBatchOption) (driver.Batch, error) {
	return c.batch, nil
}

type captureBatch struct {
	driver.Batch
	rows [][]interface{}
}

func (b *captureBatch) Append(v ...interface{}) error { b.rows = append(b.rows, v); return nil }
func (*captureBatch) Send() error                     { return nil }
func (*captureBatch) Abort() error                    { return nil }

type attributedResolver struct{ key string }

func (a attributedResolver) AuthenticateKeyContext(r *http.Request) (context.Context, string, string, bool) {
	return r.Context(), "tenant", a.key, a.key != ""
}
func (a attributedResolver) PresentsCredential(*http.Request) bool { return a.key != "" }
func TestIngestResourceKeyAttribution(t *testing.T) {
	for _, signal := range []struct {
		name, body string
		index      int
	}{
		{"traces", `{"resourceSpans":[{"resource":{"attributes":ATTRS},"scopeSpans":[{"scope":{"attributes":ATTRS},"spans":[{"traceId":"11111111111111111111111111111111","spanId":"2222222222222222","name":"test","attributes":ATTRS,"events":[{"name":"event","attributes":ATTRS}],"links":[{"traceId":"11111111111111111111111111111111","spanId":"2222222222222222","attributes":ATTRS}]}]}]}]}`, 8},
		{"logs", `{"resourceLogs":[{"resource":{"attributes":ATTRS},"scopeLogs":[{"scope":{"attributes":ATTRS},"logRecords":[{"attributes":ATTRS,"body":{"stringValue":"hello"}}]}]}]}`, 9},
		{"metrics", `{"resourceMetrics":[{"resource":{"attributes":ATTRS},"scopeMetrics":[{"scope":{"attributes":ATTRS},"metrics":[{"name":"test","gauge":{"dataPoints":[{"asInt":"1","attributes":ATTRS,"exemplars":[{"asInt":"1","filteredAttributes":ATTRS}]}]}}]}]}]}`, 0},
	} {
		for _, tc := range []struct{ name, key, claim string }{{"stamped", "verified", ""}, {"overwritten", "verified", "forged"}, {"removed", "", "forged"}} {
			t.Run(signal.name+"/"+tc.name, func(t *testing.T) {
				batch := &captureBatch{}
				conn := captureConn{batch: batch}
				var handler http.Handler
				switch signal.name {
				case "traces":
					handler = NewTracesHandler(conn)
				case "logs":
					handler = NewLogsHandler(conn)
				default:
					handler = NewMetricsHandler(conn)
				}
				attrs := `[]`
				if tc.claim != "" {
					attrs = `[{"key":"everstack.api_key_id","value":{"stringValue":"` + tc.claim + `"}}]`
				}
				body := strings.ReplaceAll(signal.body, "ATTRS", attrs)
				assertDecodedAttributesClean(t, signal.name, body, tc.key)
				r := httptest.NewRequest("POST", "/v1/"+signal.name, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r = r.WithContext(contextkeys.WithTenantID(contextkeys.WithAPIKeyID(r.Context(), "stale-upstream-id"), "tenant"))
				w := httptest.NewRecorder()
				WithTenantAuth(handler, attributedResolver{tc.key}).ServeHTTP(w, r)
				if w.Code != 200 || len(batch.rows) != 1 {
					t.Fatalf("ingest: %d %s rows=%d", w.Code, w.Body, len(batch.rows))
				}
				stored, _ := json.Marshal(batch.rows)
				if strings.Contains(string(stored), "forged") {
					t.Fatalf("forged identity persisted: %s", stored)
				}
				resource := batch.rows[0][signal.index].(map[string]string)
				if got := resource["everstack.api_key_id"]; got != tc.key {
					t.Fatalf("attributed to %q, want %q", got, tc.key)
				}
				if tc.key == "" {
					if _, ok := resource["everstack.api_key_id"]; ok {
						t.Fatal("keyless resource retained claim")
					}
				}
			})
		}
	}
}
func TestKeylessOTLPDoesNotConsumeLimits(t *testing.T) {
	t.Setenv("EVS_OTLP_RATE_LIMIT_RPM", "1")
	store := &keylessStoreSpy{}
	old := apikey.SetRuntime(apikey.NewRuntime(store, nil))
	defer apikey.SetRuntime(old)
	h := WithTenantAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), nil)
	for range 10 {
		r := httptest.NewRequest("POST", "/v1/traces", nil)
		r = r.WithContext(contextkeys.WithTenantID(r.Context(), "local-tenant"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 || w.Header().Get("Retry-After") != "" {
			t.Fatalf("anonymous traffic refused: %d", w.Code)
		}
	}
	if store.calls != 0 {
		t.Fatalf("keyless requests consumed %d shared counters", store.calls)
	}
}

type keylessStoreSpy struct{ calls int }

func (s *keylessStoreSpy) Consume(context.Context, string, authratelimit.Rule) (authratelimit.Decision, error) {
	s.calls++
	return authratelimit.Decision{}, nil
}
func (*keylessStoreSpy) Peek(context.Context, string) (authratelimit.Window, error) {
	return authratelimit.Window{}, nil
}

func assertDecodedAttributesClean(t *testing.T, signal, body, key string) {
	t.Helper()
	var req proto.Message
	switch signal {
	case "traces":
		req = &collectortrace.ExportTraceServiceRequest{}
	case "logs":
		req = &collectorlogs.ExportLogsServiceRequest{}
	default:
		req = &collectormetrics.ExportMetricsServiceRequest{}
	}
	if err := protojson.Unmarshal([]byte(body), req); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), ingestKey{}, key)
	conn := captureConn{batch: &captureBatch{}}
	var err error
	switch v := req.(type) {
	case *collectortrace.ExportTraceServiceRequest:
		_, _, err = NewTracesHandler(conn).insertSpans(ctx, "tenant", v.ResourceSpans)
	case *collectorlogs.ExportLogsServiceRequest:
		_, _, err = NewLogsHandler(conn).insertLogs(ctx, "tenant", v.ResourceLogs)
	case *collectormetrics.ExportMetricsServiceRequest:
		_, _, err = NewMetricsHandler(conn).insertMetrics(ctx, "tenant", v.ResourceMetrics)
	}
	if err != nil {
		t.Fatal(err)
	}
	out, err := protojson.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "everstack.api_key_id") {
		t.Fatalf("%s retained a caller identity in decoded attributes: %s", signal, out)
	}
}
