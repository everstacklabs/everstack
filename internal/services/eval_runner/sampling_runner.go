package eval_runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// Sampling runner — the loop that makes online eval actually work.
//
// Polls sampling_eval_rules, finds rules due to run (last_run_at + interval
// has elapsed), pulls recent traces from otel_traces matching the rule's
// filter_predicate, applies sample_rate via stable hash so the same trace is
// either consistently sampled or consistently skipped across replays, runs
// each scorer config against each sampled trace, and writes the resulting
// scores via the existing scores.Recorder so they show up in
// /observability/traces, sampling dashboards, and the CI gate.
//
// Multi-replica safe: holds a pg advisory lock so only one replica drives
// the loop at a time. Failed runs are recorded on the rule (last_run_error)
// and retried on the next tick.

// SamplingRunSummary is what a single rule execution returns. Surfaced via
// the RunSamplingEvalRuleNow gRPC handler.
type SamplingRunSummary struct {
	TracesMatched  int
	TracesSampled  int
	ScoresRecorded int
	ErrorMessage   string
}

// SetScoreRecorder lets the runner write to otel_trace_scores. Optional
// because the existing dataset-driven eval runs write to eval_run_items
// instead; sampling rules specifically need the trace-score sink. Call
// from start_api after the recorder is built.
func (r *Runner) SetScoreRecorder(rec *scores.Recorder) {
	r.scoreRecorder = rec
}

// ExecuteSamplingRule runs the rule one tick: loads the rule, queries
// matching traces since last_processed_trace_at (or now - lookback),
// samples by stable hash, scores each, persists scores, updates rule
// telemetry.
//
// Idempotent on partial failure: the row-by-row scorer + record path
// writes per trace, so a mid-run crash leaves the rule's
// last_processed_trace_at pointing at the latest successful trace and the
// next tick picks up from there. The rule's last_run_error captures the
// first error message; subsequent traces in the same tick still process.
func (r *Runner) ExecuteSamplingRule(ctx context.Context, tenantID, ruleID string) (*SamplingRunSummary, error) {
	summary := &SamplingRunSummary{}
	if r.scoreRecorder == nil {
		summary.ErrorMessage = "no score recorder configured — start_api must call runner.SetScoreRecorder"
		return summary, fmt.Errorf("%s", summary.ErrorMessage)
	}
	if r.chConn == nil {
		summary.ErrorMessage = "no clickhouse connection — runner cannot pull traces"
		return summary, fmt.Errorf("%s", summary.ErrorMessage)
	}

	rule, err := GetSamplingEvalRule(ctx, r.db, ruleID, tenantID)
	if err != nil {
		summary.ErrorMessage = "load rule: " + err.Error()
		return summary, err
	}
	if !rule.Enabled {
		return summary, nil
	}
	if len(rule.ScorerConfigIDs) == 0 {
		summary.ErrorMessage = "rule has no scorer_config_ids — nothing to do"
		return summary, nil
	}

	// Tenant context — every CH query relies on tenant.id in attributes.
	if tenantID == "" {
		tenantID = rule.TenantID
	}

	configs, err := r.loadScoreConfigs(ctx, tenantID, []string(rule.ScorerConfigIDs))
	if err != nil {
		summary.ErrorMessage = "load configs: " + err.Error()
		_ = MarkSamplingRuleRun(ctx, r.db, ruleID, tenantID, 0, time.Time{}, summary.ErrorMessage)
		return summary, err
	}
	if len(configs) == 0 {
		summary.ErrorMessage = "all scorer configs are missing or archived"
		_ = MarkSamplingRuleRun(ctx, r.db, ruleID, tenantID, 0, time.Time{}, summary.ErrorMessage)
		return summary, nil
	}

	since := time.Now().Add(-time.Duration(rule.LookbackSeconds) * time.Second).UTC()
	if rule.LastProcessedTraceAt.Valid && rule.LastProcessedTraceAt.Time.After(since) {
		since = rule.LastProcessedTraceAt.Time.UTC()
	}

	traces, latestTraceAt, err := r.pullSampledTraces(ctx, tenantID, rule, since)
	if err != nil {
		summary.ErrorMessage = "pull traces: " + err.Error()
		_ = MarkSamplingRuleRun(ctx, r.db, ruleID, tenantID, 0, time.Time{}, summary.ErrorMessage)
		return summary, err
	}
	summary.TracesMatched = len(traces)

	// Apply sample rate via stable hash (FNV-32). Same trace either
	// consistently in or out across re-runs, so replaying a tick doesn't
	// double-score and idempotency holds.
	sampled := make([]traceForScoring, 0, len(traces))
	for _, t := range traces {
		if stableSample(rule.ID, t.TraceID, rule.SampleRate) {
			sampled = append(sampled, t)
		}
	}
	summary.TracesSampled = len(sampled)

	var firstErr string
	scoresWritten := 0
	for _, tr := range sampled {
		for _, cfg := range configs {
			var (
				result    *ScoreResult
				scorerErr error
			)
			switch {
			case IsBuiltinScorer(cfg.DataType):
				var matched bool
				result, matched, scorerErr = runBuiltinScorer(ctx, cfg, tr.Input, tr.Output, nil, nil, tr.Context)
				if !matched {
					continue
				}
			case cfg.EvalPrompt != "" || len(cfg.Messages) > 0:
				result, scorerErr = r.runScorer(ctx, tenantID, cfg, tr.Input, tr.Output, nil, nil, tr.Context)
			default:
				continue
			}
			if scorerErr != nil {
				logger.WithError(scorerErr).WithFields(map[string]interface{}{
					"rule_id":  rule.ID,
					"trace_id": tr.TraceID,
					"scorer":   cfg.Name,
				}).Warn("sampling runner: scorer failed")
				if firstErr == "" {
					firstErr = cfg.Name + ": " + scorerErr.Error()
				}
				continue
			}
			if result == nil {
				continue
			}

			score := scoreFromResult(tr.TraceID, cfg, result, rule.ID, tr.Environment)
			if score == nil {
				continue
			}
			if err := r.scoreRecorder.Record(ctx, score); err != nil {
				logger.WithError(err).WithFields(map[string]interface{}{
					"rule_id":  rule.ID,
					"trace_id": tr.TraceID,
					"scorer":   cfg.Name,
				}).Warn("sampling runner: record score")
				if firstErr == "" {
					firstErr = "record: " + err.Error()
				}
				continue
			}
			scoresWritten++
		}
	}
	summary.ScoresRecorded = scoresWritten

	finalLastProcessed := latestTraceAt
	if finalLastProcessed.IsZero() {
		finalLastProcessed = time.Now().UTC()
	}
	summary.ErrorMessage = firstErr
	if err := MarkSamplingRuleRun(ctx, r.db, ruleID, tenantID, summary.TracesSampled, finalLastProcessed, firstErr); err != nil {
		logger.WithError(err).Warn("sampling runner: mark run")
	}

	return summary, nil
}

// traceForScoring is one trace's distilled IO + tenant context for the
// scorer call. Pulled with a single GROUP BY query per tick.
type traceForScoring struct {
	TraceID     string
	Timestamp   time.Time
	Input       interface{}
	Output      interface{}
	Context     string
	Environment string
}

// pullSampledTraces runs the filter against otel_traces and returns the
// candidate trace cohort (still pre-sampling). One row per trace with the
// root-span input/output already extracted.
//
// Filter predicate keys honoured (subset of ListRichTracesRequest):
//
//	environment, user_id, session_id, thread_id, model, provider, tags[].
//
// Anything else is ignored for now (and is logged in a follow-up).
func (r *Runner) pullSampledTraces(ctx context.Context, tenantID string, rule *SamplingEvalRuleRecord, since time.Time) ([]traceForScoring, time.Time, error) {
	filter := map[string]interface{}{}
	if len(rule.FilterPredicate) > 0 {
		_ = json.Unmarshal(rule.FilterPredicate, &filter)
	}

	conds := []string{
		"SpanAttributes['tenant.id'] = ?",
		"Timestamp > ?",
	}
	args := []interface{}{tenantID, since}

	getStr := func(k string) string {
		if v, ok := filter[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	if v := getStr("environment"); v != "" {
		conds = append(conds, "ResourceAttributes['deployment.environment'] = ?")
		args = append(args, v)
	}
	if v := getStr("user_id"); v != "" {
		conds = append(conds, "SpanAttributes['trace.user_id'] = ?")
		args = append(args, v)
	}
	if v := getStr("session_id"); v != "" {
		conds = append(conds, "SpanAttributes['trace.session_id'] = ?")
		args = append(args, v)
	}
	if v := getStr("thread_id"); v != "" {
		conds = append(conds, "SpanAttributes['trace.thread_id'] = ?")
		args = append(args, v)
	}
	if v := getStr("model"); v != "" {
		conds = append(conds, "SpanAttributes['llm.model'] = ?")
		args = append(args, v)
	}
	if v := getStr("provider"); v != "" {
		conds = append(conds, "SpanAttributes['llm.provider'] = ?")
		args = append(args, v)
	}
	if tagsRaw, ok := filter["tags"]; ok {
		if tags, ok := tagsRaw.([]interface{}); ok {
			for _, t := range tags {
				if s, ok := t.(string); ok && s != "" {
					conds = append(conds, "SpanAttributes['trace.tags'] LIKE ?")
					args = append(args, "%"+s+"%")
				}
			}
		}
	}

	// One row per trace. We pull the root span's IO + a max timestamp.
	// LIMIT keeps a runaway rule from melting the DB.
	const maxTracesPerTick = 1000
	q := fmt.Sprintf(`
		SELECT
			TraceId,
			max(Timestamp) AS ts,
			maxIf(SpanAttributes['trace.input'], ParentSpanId = '') AS input,
			maxIf(SpanAttributes['trace.output'], ParentSpanId = '') AS output,
			any(ResourceAttributes['deployment.environment']) AS environment
		FROM otel_traces
		WHERE %s
		GROUP BY TraceId
		ORDER BY ts ASC
		LIMIT %d
	`, strings.Join(conds, " AND "), maxTracesPerTick)

	rows, err := r.chConn.Query(ctx, q, args...)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()

	out := []traceForScoring{}
	var latest time.Time
	for rows.Next() {
		var (
			traceID, inputStr, outputStr, env string
			ts                                time.Time
		)
		if err := rows.Scan(&traceID, &ts, &inputStr, &outputStr, &env); err != nil {
			logger.WithError(err).Warn("sampling runner: scan trace")
			continue
		}
		if ts.After(latest) {
			latest = ts
		}
		out = append(out, traceForScoring{
			TraceID:     traceID,
			Timestamp:   ts,
			Input:       parseMaybeJSON(inputStr),
			Output:      parseMaybeJSON(outputStr),
			Environment: env,
		})
	}
	return out, latest, nil
}

// parseMaybeJSON returns the JSON-parsed value if the string parses,
// otherwise returns the raw string. Scorers downstream handle either.
func parseMaybeJSON(s string) interface{} {
	if s == "" {
		return nil
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var v interface{}
		if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
			return v
		}
	}
	return s
}

// stableSample maps (ruleID + traceID) → [0, 1) via FNV-32 and returns
// true if the resulting fraction is below the rule's sample_rate. Same
// inputs always produce the same decision, so replaying a tick doesn't
// double-score; rules with overlapping filters get independent samples.
func stableSample(ruleID, traceID string, rate float64) bool {
	if rate >= 1.0 {
		return true
	}
	if rate <= 0 {
		return false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(ruleID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(traceID))
	v := float64(h.Sum32()) / float64(^uint32(0))
	return v < rate
}

// scoreFromResult converts a ScoreResult into a scores.Score, picking the
// right value field by data_type. Builtin scorers can return numeric or
// boolean; LLM-judge scorers return whichever matches their cfg.DataType.
func scoreFromResult(traceID string, cfg ScoreConfig, result *ScoreResult, ruleID, env string) *scores.Score {
	dt := strings.ToLower(cfg.DataType)
	// Built-in scorers always report a string-like result data type via
	// data_type prefix "builtin_". They return either bool or float64.
	if strings.HasPrefix(dt, "builtin_") {
		// Treat the value's Go type as the source of truth.
		switch v := result.Value.(type) {
		case bool:
			s := scores.BooleanScore(traceID, cfg.Name, v, scores.ScoreSourceEval)
			s.ConfigID = cfg.ID
			s.Comment = composeComment(result.Reason, ruleID)
			s.Environment = env
			return s
		case float64:
			s := scores.NumericScore(traceID, cfg.Name, v, scores.ScoreSourceEval)
			s.ConfigID = cfg.ID
			s.Comment = composeComment(result.Reason, ruleID)
			s.Environment = env
			return s
		case string:
			s := scores.CategoricalScore(traceID, cfg.Name, v, scores.ScoreSourceEval)
			s.ConfigID = cfg.ID
			s.Comment = composeComment(result.Reason, ruleID)
			s.Environment = env
			return s
		}
		return nil
	}

	// Non-builtin scorers (LLM judge / code): pick the value field by the
	// scorer's OUTPUT type, not data_type. A choice scorer maps its chosen
	// label to a numeric 0..1 score, so it records as numeric.
	switch effectiveOutputType(cfg) {
	case "numeric", "choice":
		var v float64
		switch t := result.Value.(type) {
		case float64:
			v = t
		case int:
			v = float64(t)
		case int64:
			v = float64(t)
		default:
			return nil
		}
		s := scores.NumericScore(traceID, cfg.Name, v, scores.ScoreSourceEval)
		s.ConfigID = cfg.ID
		s.Comment = composeComment(result.Reason, ruleID)
		s.Environment = env
		return s
	case "categorical":
		v, ok := result.Value.(string)
		if !ok {
			return nil
		}
		s := scores.CategoricalScore(traceID, cfg.Name, v, scores.ScoreSourceEval)
		s.ConfigID = cfg.ID
		s.Comment = composeComment(result.Reason, ruleID)
		s.Environment = env
		return s
	case "boolean":
		v, ok := result.Value.(bool)
		if !ok {
			return nil
		}
		s := scores.BooleanScore(traceID, cfg.Name, v, scores.ScoreSourceEval)
		s.ConfigID = cfg.ID
		s.Comment = composeComment(result.Reason, ruleID)
		s.Environment = env
		return s
	}
	return nil
}

func composeComment(reason, ruleID string) string {
	if reason == "" {
		return "sampling rule " + ruleID
	}
	return reason + " · sampling rule " + ruleID
}

// SamplingScheduler is the polling loop that drives ExecuteSamplingRule
// for every due rule. One per process; multi-replica safety via the same
// pg advisory lock pattern the eval Scheduler uses.
type SamplingScheduler struct {
	runner       *Runner
	pollInterval time.Duration
	leaderKey    string
	stopOnce     sync.Once
	stop         chan struct{}
}

// StartSamplingScheduler boots the polling loop in a goroutine.
// Safe to call when chConn is nil (we just no-op the ticks).
func StartSamplingScheduler(ctx context.Context, runner *Runner) *SamplingScheduler {
	s := &SamplingScheduler{
		runner:       runner,
		pollInterval: 30 * time.Second,
		leaderKey:    "sampling-eval-runner",
		stop:         make(chan struct{}),
	}
	go s.run(ctx)
	logger.Info("sampling eval scheduler started")
	return s
}

func (s *SamplingScheduler) run(ctx context.Context) {
	t := time.NewTicker(s.pollInterval)
	defer t.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *SamplingScheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

type samplingRuleDueRow struct {
	ID       string `db:"id"`
	TenantID string `db:"tenant_id"`
}

func (s *SamplingScheduler) tick(ctx context.Context) {
	if s.runner == nil || s.runner.db == nil {
		return
	}
	// Leader lock — only one replica drives the loop.
	conn, err := s.runner.db.Connx(ctx)
	if err != nil {
		logger.WithError(err).Warn("sampling scheduler: conn")
		return
	}
	defer conn.Close()
	var locked bool
	if err := conn.GetContext(ctx, &locked, `SELECT pg_try_advisory_lock(hashtext($1))`, s.leaderKey); err != nil {
		logger.WithError(err).Warn("sampling scheduler: acquire lock")
		return
	}
	if !locked {
		return
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, s.leaderKey); err != nil {
			logger.WithError(err).Warn("sampling scheduler: release lock")
		}
	}()

	var due []samplingRuleDueRow
	// Due when: enabled, interval > 0, and (last_run_at IS NULL OR
	// last_run_at + interval has elapsed). interval=0 disables auto-runs
	// (manual-only via RunSamplingEvalRuleNow).
	err = s.runner.db.SelectContext(ctx, &due, `
		SELECT id, tenant_id FROM sampling_eval_rules
		WHERE enabled = TRUE
		  AND interval_seconds > 0
		  AND (last_run_at IS NULL OR last_run_at + (interval_seconds * INTERVAL '1 second') <= NOW())
		ORDER BY COALESCE(last_run_at, to_timestamp(0)) ASC
		LIMIT 200
	`)
	if err != nil {
		if err == sql.ErrNoRows {
			return
		}
		logger.WithError(err).Warn("sampling scheduler: list due")
		return
	}

	for _, r := range due {
		select {
		case <-ctx.Done():
			return
		default:
		}
		summary, err := s.runner.ExecuteSamplingRule(ctx, r.TenantID, r.ID)
		if err != nil {
			logger.WithError(err).WithFields(map[string]interface{}{
				"rule_id": r.ID,
				"tenant":  r.TenantID,
				"sampled": summary.TracesSampled,
				"matched": summary.TracesMatched,
				"scores":  summary.ScoresRecorded,
			}).Warn("sampling scheduler: execute rule")
			continue
		}
		logger.WithFields(map[string]interface{}{
			"rule_id": r.ID,
			"tenant":  r.TenantID,
			"matched": summary.TracesMatched,
			"sampled": summary.TracesSampled,
			"scores":  summary.ScoresRecorded,
		}).Info("sampling scheduler: rule tick complete")
	}
}
