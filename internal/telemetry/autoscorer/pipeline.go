package autoscorer

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/scores"
	"github.com/everstacklabs/everstack/internal/telemetry/scoringstate"
	"go.opentelemetry.io/otel/attribute"
)

// Pipeline orchestrates all registered scorers and persists their output.
// It runs asynchronously after each turn to avoid blocking the agent loop.
type Pipeline struct {
	scorers  []Scorer
	recorder ScoreRecorder
}

// NewPipeline creates a scoring pipeline with the given scorers and recorder.
func NewPipeline(recorder ScoreRecorder, scorers ...Scorer) *Pipeline {
	return &Pipeline{
		scorers:  scorers,
		recorder: recorder,
	}
}

// DefaultPipeline creates a pipeline with all built-in heuristic scorers.
// The PolicyComplianceScorer uses built-in PII patterns unless a custom config is provided.
func DefaultPipeline(recorder ScoreRecorder, policyConfig *PolicyConfig) *Pipeline {
	scorerList := []Scorer{
		&ToolQualityScorer{},
		&TaskCompletionScorer{},
		&LoopHealthScorer{},
		&SandboxHygieneScorer{},
	}

	if policyConfig != nil {
		scorerList = append(scorerList, NewPolicyComplianceScorer(*policyConfig))
	} else {
		// Use built-in PII patterns as default
		scorerList = append(scorerList, NewPolicyComplianceScorer(PolicyConfig{
			BlockedPatterns: BuiltInPolicyPatterns(),
		}))
	}

	return NewPipeline(recorder, scorerList...)
}

// ScoreTurn runs all scorers against the given turn context and persists results.
// This method is safe to call from a goroutine — it creates its own timeout context.
func (p *Pipeline) ScoreTurn(parentCtx context.Context, tc *TurnContext) {
	if p == nil || p.recorder == nil {
		return
	}

	// Use a dedicated context with timeout so scoring doesn't hang
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	// Join the original trace and open a scorer-pipeline span (M3-T2). Scorer
	// runs become inspectable spans under the scored trace, flagged purpose=scorer
	// so they are excluded from the host trace's cost/latency rollups.
	ctx = telemetry.ScorerTraceContext(ctx, tc.TraceID)
	ctx, runSpan := telemetry.StartScorerSpan(ctx, "pipeline")
	defer runSpan.End()

	// Async-scoring state (M3-T1): track which scorers ran, their idempotency
	// keys, and status. Surfaced on the spans now; a Store persists it later.
	state := scoringstate.NewState(tc.TraceID, "turn")

	var allScores []*scores.Score
	for _, scorer := range p.scorers {
		tf := state.Trigger(scorer.Name(), int64(tc.TurnNumber))
		sctx, span := telemetry.StartScorerSpan(ctx, scorer.Name())
		span.SetAttributes(attribute.String(attrs.ScoringIdempotencyKey, tf.IdempotencyKey))
		results := scorer.Score(sctx, tc)
		state.Complete(scorer.Name(), len(results))
		span.SetAttributes(attribute.Int(attrs.ScoreCount, len(results)))
		span.End()
		allScores = append(allScores, results...)
	}
	runSpan.SetAttributes(
		attribute.Int(attrs.ScoreCount, len(allScores)),
		attribute.String(attrs.ScoringState, state.Summary().String()),
	)

	if len(allScores) == 0 {
		return
	}

	// Enrich all scores with canonical context fields:
	// - ObservationID = AgentID (dashboard filters by agent; overrides scorer defaults)
	// - Environment = TenantID (tenant isolation in queries)
	// - Metadata with session_id (unique session counting)
	for _, s := range allScores {
		if tc.AgentID != "" {
			s.ObservationID = tc.AgentID
		}
		if tc.TenantID != "" {
			s.Environment = tc.TenantID
		}
		if tc.SessionID != "" {
			if s.Metadata == nil {
				s.Metadata = make(map[string]interface{})
			}
			s.Metadata["session_id"] = tc.SessionID
		}
	}

	if err := p.recorder.RecordBatch(ctx, allScores); err != nil {
		logger.WithFields(
			"trace_id", tc.TraceID,
			"session_id", tc.SessionID,
			"turn_number", tc.TurnNumber,
			"score_count", len(allScores),
			"error", err.Error(),
		).Warn("autoscorer: failed to persist scores")
		return
	}

	logger.WithFields(
		"trace_id", tc.TraceID,
		"session_id", tc.SessionID,
		"turn_number", tc.TurnNumber,
		"score_count", len(allScores),
	).Debug("autoscorer: scores persisted")
}

// ScoreTurnAsync runs ScoreTurn in a background goroutine.
// This is the primary integration point — called from loop.go at turn end.
func (p *Pipeline) ScoreTurnAsync(tc *TurnContext) {
	if p == nil {
		return
	}
	go func() {
		// Use a detached context so scoring survives turn context cancellation
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		p.ScoreTurn(ctx, tc)
	}()
}
