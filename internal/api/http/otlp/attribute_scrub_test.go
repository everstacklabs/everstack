package otlp

import (
	"context"
	"strings"
	"testing"

	collector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Even currently rejected metric types must be scrubbed before flattening, so
// future persistence cannot accidentally reintroduce caller-supplied identities.
func TestEveryMetricTypeAndExemplarDropsClientIdentity(t *testing.T) {
	attrs := `[{"key":"everstack.api_key_id","value":{"stringValue":"forged"}},{"key":"safe","value":{"stringValue":"kept"}}]`
	number := `{"attributes":ATTRS,"asInt":"1","exemplars":[{"asInt":"1","filteredAttributes":ATTRS}]}`
	histogram := `{"attributes":ATTRS,"count":"1","bucketCounts":["1"],"exemplars":[{"asInt":"1","filteredAttributes":ATTRS}]}`
	exponential := `{"attributes":ATTRS,"count":"1","exemplars":[{"asInt":"1","filteredAttributes":ATTRS}]}`
	summary := `{"attributes":ATTRS,"count":"1"}`
	body := `{"resourceMetrics":[{"resource":{"attributes":ATTRS},"scopeMetrics":[{"scope":{"attributes":ATTRS},"metrics":[
 {"name":"gauge","gauge":{"dataPoints":[` + number + `]}},
 {"name":"sum","sum":{"dataPoints":[` + number + `]}},
 {"name":"histogram","histogram":{"dataPoints":[` + histogram + `]}},
 {"name":"exponential","exponentialHistogram":{"dataPoints":[` + exponential + `]}},
 {"name":"summary","summary":{"dataPoints":[` + summary + `]}}
 ]}]}]}`
	for _, key := range []string{"", "verified"} {
		req := &collector.ExportMetricsServiceRequest{}
		if err := protojson.Unmarshal([]byte(strings.ReplaceAll(body, "ATTRS", attrs)), req); err != nil {
			t.Fatal(err)
		}
		batch := &captureBatch{}
		h := NewMetricsHandler(captureConn{batch: batch})
		rejected, accepted, err := h.insertMetrics(context.WithValue(context.Background(), ingestKey{}, key), "tenant", req.ResourceMetrics)
		if err != nil || rejected != 3 || accepted != 2 {
			t.Fatalf("ingest rejected=%d accepted=%d err=%v", rejected, accepted, err)
		}
		out, err := protojson.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "everstack.api_key_id") || strings.Contains(string(out), "forged") {
			t.Fatalf("metric attribute survived: %s", out)
		}
		if !strings.Contains(string(out), "kept") {
			t.Fatal("unrelated metric attributes lost")
		}
		for _, row := range batch.rows {
			if row[0].(map[string]string)["everstack.api_key_id"] != key {
				t.Fatal("resource identity lost")
			}
		}
	}
}
