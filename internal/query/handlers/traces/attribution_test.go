package traces

import (
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

// TestLatestSuccessfulGenExpr pins the shape of the multi-provider attribution
// fix: provider and model must be read from the latest successful generation
// span (tiered fallback to the latest generation, then any span), gated to
// generation spans, with a total (Timestamp, SpanId) ordering so provider and
// model always resolve to the same span. It also logs the generated SQL so the
// expression can be exercised against a real ClickHouse.
func TestLatestSuccessfulGenExpr(t *testing.T) {
	provExpr := latestSuccessfulGenExpr(providerSQL(), providerSQL(), modelSQL())
	modelExpr := latestSuccessfulGenExpr(modelSQL(), providerSQL(), modelSQL())

	for _, tc := range []struct{ name, expr string }{
		{"provider", provExpr},
		{"model", modelExpr},
	} {
		if got := strings.Count(tc.expr, "argMaxIf("); got != 2 {
			t.Errorf("%s: want 2 argMaxIf tiers, got %d in %q", tc.name, got, tc.expr)
		}
		if !strings.Contains(tc.expr, "anyIf(") {
			t.Errorf("%s: missing anyIf fallback tier", tc.name)
		}
		// The success tier must exclude errored spans under EITHER stored
		// status spelling. Pinning the enum name alone is what let the
		// short "Error" form -- the only one gateway spans carry -- slip
		// through the "successful generation" tier unnoticed.
		if !strings.Contains(tc.expr, otelstatus.IsNotError(otelstatus.Column)) {
			t.Errorf("%s: success tier must exclude errored spans, got %q", tc.name, tc.expr)
		}
		if !strings.Contains(tc.expr, "(Timestamp, SpanId)") {
			t.Errorf("%s: must order by (Timestamp, SpanId) for a total order", tc.name)
		}
		if !strings.Contains(tc.expr, "observation.type") || !strings.Contains(tc.expr, "provider.%") {
			t.Errorf("%s: must gate to generation spans", tc.name)
		}
	}

	t.Logf("PROVIDER_EXPR=%s", provExpr)
	t.Logf("MODEL_EXPR=%s", modelExpr)
}
