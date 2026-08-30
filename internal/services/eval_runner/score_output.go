package eval_runner

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ScoreInput carries the already-parsed values a set of scorers evaluate.
// Callers parse the raw JSON (input/output/expected/metadata) and, if RAG
// retrieval is configured, resolve the retrieved context before calling
// ScoreOutput — keeping the dispatch itself free of run/item coupling.
type ScoreInput struct {
	Input            interface{}
	Output           interface{}
	ExpectedOutput   interface{}
	Metadata         interface{}
	RetrievedContext string
}

// ScoreOutputByConfigIDs loads the given score configs (scoped to tenantID)
// and dispatches them against a single output. It is the entry point used by
// the synchronous EvalService.ScoreOutput RPC — loading is tenant-scoped in
// loadScoreConfigs so a caller can never score against another tenant's
// configs. namespace scopes any sandbox execution (pass a synthetic id such
// as "playground-<uuid>").
func (r *Runner) ScoreOutputByConfigIDs(ctx context.Context, tenantID, namespace string, in ScoreInput, configIDs []string) (map[string]interface{}, error) {
	configs, err := r.loadScoreConfigs(ctx, tenantID, configIDs)
	if err != nil {
		return nil, err
	}
	return r.ScoreOutput(ctx, tenantID, namespace, in, configs), nil
}

// ScoreOutput runs the given score configs against a single output and returns
// a map of score name -> value, plus "<name>_reason" and "<name>_error" keys,
// matching the shape written into eval_run_items.scores.
//
// namespace scopes any sandbox execution: the background eval-run path passes
// the run ID; synchronous callers (e.g. the playground ScoreOutput RPC) pass a
// synthetic id such as "playground-<uuid>". This is the single scorer-dispatch
// implementation shared by the background runner (processItem) and the
// synchronous EvalService.ScoreOutput RPC — do not fork it.
func (r *Runner) ScoreOutput(ctx context.Context, tenantID, namespace string, in ScoreInput, configs []ScoreConfig) map[string]interface{} {
	scores := map[string]interface{}{}
	if len(configs) == 0 {
		return scores
	}

	codeScorerInput := CodeScorerInput{
		Input:          in.Input,
		Output:         in.Output,
		ExpectedOutput: in.ExpectedOutput,
		Context:        in.RetrievedContext,
		Metadata:       in.Metadata,
	}

	for _, cfg := range configs {
		var result *ScoreResult
		var scorerErr error

		switch {
		case IsBuiltinScorer(cfg.DataType):
			// Built-in deterministic scorer (no LLM, no sandbox).
			// Falls through if the data_type prefix matches but no
			// scorer is registered (returns matched=false).
			var matched bool
			result, matched, scorerErr = runBuiltinScorer(ctx, cfg, in.Input, in.Output, in.ExpectedOutput, in.Metadata, in.RetrievedContext)
			if !matched {
				continue
			}
		case cfg.ScorerCode != "" && cfg.ScorerLanguage != "":
			// Code scorer: user-supplied code, so it must run inside sandbox
			// isolation. This branch fails closed — it never silently falls
			// back to in-process exec, which would be remote code execution
			// on the API host. The only in-process path is the server-gated
			// escape hatch (RunnerOpts.AllowUnsandboxedScorers).
			switch {
			case cfg.UseSandbox && r.sandboxScorer != nil:
				result, scorerErr = r.sandboxScorer.ScoreInSandbox(ctx, namespace, tenantID, cfg, codeScorerInput)
			case cfg.UseSandbox:
				scorerErr = fmt.Errorf("code scorer %q requires sandbox isolation but the sandbox manager is unavailable", cfg.Name)
			case r.allowUnsandboxedScorers:
				result, scorerErr = r.runCodeScorer(ctx, cfg, codeScorerInput)
			default:
				scorerErr = fmt.Errorf("unsandboxed code scorers are disabled on this server; set use_sandbox=true for scorer %q", cfg.Name)
			}
		case hasDagDefinition(cfg.DagDefinition):
			result, scorerErr = r.runDagScorer(ctx, cfg, in.Input, in.Output, in.ExpectedOutput)
		case cfg.EvalPrompt != "" || len(cfg.Messages) > 0:
			// LLM judge (single eval_prompt or multi-part messages). The
			// sandbox path only renders the legacy single eval_prompt, so
			// messages-based or choice judges must run in-process where
			// buildJudgeMessages + choice parsing apply.
			legacyJudge := cfg.EvalPrompt != "" && len(cfg.Messages) == 0 && len(cfg.ChoiceScores) == 0
			if cfg.UseSandbox && r.sandboxScorer != nil && legacyJudge {
				result, scorerErr = r.sandboxScorer.ScoreInSandbox(ctx, namespace, tenantID, cfg, codeScorerInput)
			} else {
				result, scorerErr = r.runScorer(ctx, tenantID, cfg, in.Input, in.Output, in.ExpectedOutput, in.Metadata, in.RetrievedContext)
			}
		default:
			continue
		}

		if scorerErr != nil {
			logger.WithFields("scorer", cfg.Name).WithError(scorerErr).Warn("eval_runner: scorer failed")
			scores[cfg.Name+"_error"] = scorerErr.Error()
			continue
		}
		scores[cfg.Name] = result.Value
		if result.Reason != "" {
			scores[cfg.Name+"_reason"] = result.Reason
		}
	}
	return scores
}
