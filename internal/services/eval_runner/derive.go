package eval_runner

import "strings"

// effectiveScorerType returns the scorer's implementation type. It prefers the
// explicit ScorerType column and, for legacy rows written before that column
// existed, falls back to the SAME order the runtime dispatch uses
// (builtin prefix -> code -> eval_prompt/messages -> manual). Deriving off the
// dispatch order rather than a data_type string table is deliberate: a legacy
// row can carry data_type=NUMERIC and a non-empty eval_prompt and still run as
// an LLM judge, and this keeps the classification faithful to what actually runs.
func effectiveScorerType(cfg ScoreConfig) string {
	if cfg.ScorerType != "" {
		return cfg.ScorerType
	}
	dt := strings.ToLower(cfg.DataType)
	switch {
	case strings.HasPrefix(dt, "builtin_"):
		return "builtin"
	case cfg.ScorerCode != "" && cfg.ScorerLanguage != "":
		if l := strings.ToLower(cfg.ScorerLanguage); l != "" {
			return l
		}
		return "typescript"
	case cfg.EvalPrompt != "" || len(cfg.Messages) > 0:
		return "llm_judge"
	default:
		return "manual"
	}
}

// effectiveOutputType returns the score's shape (numeric|boolean|categorical|
// choice). It prefers the explicit OutputType column, treats any config with
// choice scores as "choice", and otherwise derives from the data_type for
// manual/human configs (which is where numeric/boolean/categorical is meaningful).
func effectiveOutputType(cfg ScoreConfig) string {
	if cfg.OutputType != "" {
		return cfg.OutputType
	}
	if len(cfg.ChoiceScores) > 0 {
		return "choice"
	}
	if effectiveScorerType(cfg) == "manual" {
		switch strings.ToLower(cfg.DataType) {
		case "numeric", "boolean", "categorical":
			return strings.ToLower(cfg.DataType)
		}
	}
	return "numeric"
}

// choiceScoreFor maps a judge's chosen label to its configured 0..1 score,
// matching case-insensitively and trimming whitespace.
func choiceScoreFor(choices []ScoreConfigChoice, choice string) (float64, bool) {
	target := strings.TrimSpace(strings.ToLower(choice))
	for _, c := range choices {
		if strings.TrimSpace(strings.ToLower(c.Choice)) == target {
			return c.Score, true
		}
	}
	return 0, false
}
