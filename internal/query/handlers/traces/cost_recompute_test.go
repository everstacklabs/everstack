package traces

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/query"
)

func TestRecomputeTraceCostIfMissing(t *testing.T) {
	orig := costFn
	defer func() { costFn = orig }()
	// Stub catalog pricing: gpt-4o under openai, glm-4.7 under zhipu.
	costFn = func(provider, model string, in, out, cached int) float64 {
		switch {
		case provider == "openai" && model == "gpt-4o":
			return 0.01
		case provider == "zhipu" && model == "glm-4.7":
			return 0.02
		}
		return 0
	}

	t.Run("skips when cost already present", func(t *testing.T) {
		tr := &query.TraceReadModel{TotalCost: 0.5, LLMModel: "gpt-4o", Provider: "openai", InputTokens: 100}
		recomputeTraceCostIfMissing(tr)
		if tr.TotalCost != 0.5 {
			t.Fatalf("reported cost was overwritten: %v", tr.TotalCost)
		}
	})
	t.Run("skips when no model", func(t *testing.T) {
		tr := &query.TraceReadModel{InputTokens: 100}
		recomputeTraceCostIfMissing(tr)
		if tr.TotalCost != 0 {
			t.Fatalf("got %v, want 0", tr.TotalCost)
		}
	})
	t.Run("skips when no tokens", func(t *testing.T) {
		tr := &query.TraceReadModel{LLMModel: "gpt-4o", Provider: "openai"}
		recomputeTraceCostIfMissing(tr)
		if tr.TotalCost != 0 {
			t.Fatalf("got %v, want 0", tr.TotalCost)
		}
	})
	t.Run("computes from provider attr", func(t *testing.T) {
		tr := &query.TraceReadModel{LLMModel: "gpt-4o", Provider: "openai", InputTokens: 100, OutputTokens: 50}
		recomputeTraceCostIfMissing(tr)
		if tr.TotalCost != 0.01 {
			t.Fatalf("got %v, want 0.01", tr.TotalCost)
		}
	})
	t.Run("infers provider from model when attr provider is wrong", func(t *testing.T) {
		// glm-* model but provider attr says anthropic (Claude Code via Z.ai).
		tr := &query.TraceReadModel{LLMModel: "glm-4.7", Provider: "anthropic", InputTokens: 100, OutputTokens: 50}
		recomputeTraceCostIfMissing(tr)
		if tr.TotalCost != 0.02 {
			t.Fatalf("got %v, want 0.02 (should infer zhipu)", tr.TotalCost)
		}
	})
	t.Run("infers provider when attr provider empty", func(t *testing.T) {
		tr := &query.TraceReadModel{LLMModel: "glm-4.7", InputTokens: 100}
		recomputeTraceCostIfMissing(tr)
		if tr.TotalCost != 0.02 {
			t.Fatalf("got %v, want 0.02", tr.TotalCost)
		}
	})
}
