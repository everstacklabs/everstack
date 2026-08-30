package eval_runner

import "testing"

func TestDeriveParityEffectiveScorerType(t *testing.T) {
	tests := []struct {
		name string
		cfg  ScoreConfig
		want string
	}{
		{
			name: "builtin data type",
			cfg:  ScoreConfig{DataType: "builtin_exact_match"},
			want: "builtin",
		},
		{
			name: "python scorer code",
			cfg:  ScoreConfig{ScorerCode: "x", ScorerLanguage: "python"},
			want: "python",
		},
		{
			name: "typescript scorer code",
			cfg:  ScoreConfig{ScorerCode: "x", ScorerLanguage: "typescript"},
			want: "typescript",
		},
		{
			name: "javascript scorer code",
			cfg:  ScoreConfig{ScorerCode: "x", ScorerLanguage: "javascript"},
			want: "javascript",
		},
		{
			name: "eval prompt judge",
			cfg:  ScoreConfig{EvalPrompt: "judge this"},
			want: "llm_judge",
		},
		{
			name: "numeric eval prompt follows dispatch order",
			cfg:  ScoreConfig{DataType: "NUMERIC", EvalPrompt: "judge"},
			want: "llm_judge",
		},
		{
			name: "empty config is manual",
			cfg:  ScoreConfig{},
			want: "manual",
		},
		{
			name: "llm judge data type without prompt is manual",
			cfg:  ScoreConfig{DataType: "LLM_JUDGE"},
			want: "manual",
		},
		{
			name: "code scorer data type without code is manual",
			cfg:  ScoreConfig{DataType: "CODE_SCORER"},
			want: "manual",
		},
		{
			name: "explicit scorer type wins",
			cfg:  ScoreConfig{DataType: "builtin_exact_match", ScorerType: "python"},
			want: "python",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveScorerType(tt.cfg); got != tt.want {
				t.Fatalf("effectiveScorerType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveParityEffectiveOutputType(t *testing.T) {
	tests := []struct {
		name string
		cfg  ScoreConfig
		want string
	}{
		{
			name: "choice scores produce choice output",
			cfg: ScoreConfig{
				ChoiceScores: []ScoreConfigChoice{{Choice: "yes", Score: 1}},
			},
			want: "choice",
		},
		{
			name: "manual categorical data type",
			cfg:  ScoreConfig{DataType: "CATEGORICAL"},
			want: "categorical",
		},
		{
			name: "manual boolean data type",
			cfg:  ScoreConfig{DataType: "BOOLEAN"},
			want: "boolean",
		},
		{
			name: "llm judge categorical defaults numeric",
			cfg:  ScoreConfig{DataType: "CATEGORICAL", EvalPrompt: "judge"},
			want: "numeric",
		},
		{
			name: "explicit output type wins",
			cfg: ScoreConfig{
				OutputType:   "numeric",
				ChoiceScores: []ScoreConfigChoice{{Choice: "yes", Score: 1}},
			},
			want: "numeric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveOutputType(tt.cfg); got != tt.want {
				t.Fatalf("effectiveOutputType() = %q, want %q", got, tt.want)
			}
		})
	}
}
