package v1

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/query"
)

func TestIsOperationalTrace(t *testing.T) {
	// No model/tokens/cost/kind -> operational (tenant health check, wrapper).
	if !isOperationalTrace(&query.TraceReadModel{}) {
		t.Fatal("empty trace should be operational")
	}
	// Has a model (e.g. an external LLM call) -> not operational.
	if isOperationalTrace(&query.TraceReadModel{ServedModel: "claude-sonnet-4-6"}) {
		t.Fatal("trace with a model should not be operational")
	}
	// Has tokens -> not operational.
	if isOperationalTrace(&query.TraceReadModel{TotalTokens: 100}) {
		t.Fatal("trace with tokens should not be operational")
	}
	// Has cost -> not operational.
	if isOperationalTrace(&query.TraceReadModel{TotalCost: 0.01}) {
		t.Fatal("trace with cost should not be operational")
	}
	// Has an execution kind (agent/workflow/sandbox) -> not operational.
	if isOperationalTrace(&query.TraceReadModel{TraceKinds: []string{"sandbox"}}) {
		t.Fatal("trace with a kind should not be operational")
	}
}
