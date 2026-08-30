package eval_runner

import "testing"

func TestEffectiveScorerType(t *testing.T) {
	cases := []struct {
		name string
		cfg  ScoreConfig
		want string
	}{
		{"explicit wins", ScoreConfig{ScorerType: "python", DataType: "NUMERIC", EvalPrompt: "x"}, "python"},
		{"builtin prefix", ScoreConfig{DataType: "builtin_exact_match"}, "builtin"},
		{"code scorer ts", ScoreConfig{DataType: "CODE_SCORER", ScorerCode: "return 1", ScorerLanguage: "typescript"}, "typescript"},
		{"code scorer js", ScoreConfig{ScorerCode: "return 1", ScorerLanguage: "javascript"}, "javascript"},
		// dispatch order: eval_prompt on a NUMERIC row still means llm_judge.
		{"legacy numeric+prompt is judge", ScoreConfig{DataType: "NUMERIC", EvalPrompt: "grade {{output}}"}, "llm_judge"},
		{"messages only is judge", ScoreConfig{DataType: "LLM_JUDGE", Messages: []ScoreConfigMessage{{Role: "user", Content: "hi"}}}, "llm_judge"},
		{"manual numeric", ScoreConfig{DataType: "NUMERIC"}, "manual"},
		{"manual boolean", ScoreConfig{DataType: "BOOLEAN"}, "manual"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveScorerType(tc.cfg); got != tc.want {
				t.Fatalf("effectiveScorerType = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveOutputType(t *testing.T) {
	cases := []struct {
		name string
		cfg  ScoreConfig
		want string
	}{
		{"explicit wins", ScoreConfig{OutputType: "choice", DataType: "NUMERIC"}, "choice"},
		{"choice scores imply choice", ScoreConfig{DataType: "LLM_JUDGE", ChoiceScores: []ScoreConfigChoice{{Choice: "A", Score: 1}}}, "choice"},
		{"manual numeric", ScoreConfig{DataType: "NUMERIC"}, "numeric"},
		{"manual boolean", ScoreConfig{DataType: "BOOLEAN"}, "boolean"},
		{"manual categorical", ScoreConfig{DataType: "CATEGORICAL"}, "categorical"},
		// A plain LLM judge (eval_prompt, no choices) defaults to numeric, NOT
		// the human-annotation shape — this is the bug the split fixes.
		{"llm judge defaults numeric", ScoreConfig{DataType: "LLM_JUDGE", EvalPrompt: "grade"}, "numeric"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveOutputType(tc.cfg); got != tc.want {
				t.Fatalf("effectiveOutputType = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChoiceScoreFor(t *testing.T) {
	choices := []ScoreConfigChoice{{Choice: "A", Score: 0.4}, {Choice: "B", Score: 0.6}, {Choice: "C", Score: 1.0}}
	if v, ok := choiceScoreFor(choices, "B"); !ok || v != 0.6 {
		t.Fatalf("B => %v,%v want 0.6,true", v, ok)
	}
	if v, ok := choiceScoreFor(choices, " c "); !ok || v != 1.0 {
		t.Fatalf("case/space-insensitive c => %v,%v want 1.0,true", v, ok)
	}
	if _, ok := choiceScoreFor(choices, "Z"); ok {
		t.Fatalf("unknown choice Z should not match")
	}
}

func TestExtractJudgeContent_StringAndArray(t *testing.T) {
	mk := func(content interface{}) map[string]interface{} {
		return map[string]interface{}{
			"choices": []interface{}{
				map[string]interface{}{
					"message": map[string]interface{}{"content": content},
				},
			},
		}
	}
	if got := extractJudgeContent(mk(`{"choice":"A"}`)); got != `{"choice":"A"}` {
		t.Fatalf("string content = %q", got)
	}
	arr := []interface{}{
		map[string]interface{}{"type": "text", "text": `{"choice":`},
		map[string]interface{}{"type": "text", "text": `"B"}`},
	}
	if got := extractJudgeContent(mk(arr)); got != `{"choice":"B"}` {
		t.Fatalf("array content = %q, want joined parts", got)
	}
	if got := extractJudgeContent(map[string]interface{}{"choices": []interface{}{}}); got != "" {
		t.Fatalf("empty choices = %q, want empty", got)
	}
}

func TestParseScoreResponse_Choice(t *testing.T) {
	cfg := ScoreConfig{
		Name:         "Factuality",
		DataType:     "LLM_JUDGE",
		OutputType:   "choice",
		ChoiceScores: []ScoreConfigChoice{{Choice: "A", Score: 0.4}, {Choice: "B", Score: 0.6}, {Choice: "C", Score: 1.0}},
	}

	t.Run("valid choice maps to score", func(t *testing.T) {
		val, reason, err := parseScoreResponse(`{"choice":"B","reasoning":"subset+consistent"}`, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if f, ok := val.(float64); !ok || f != 0.6 {
			t.Fatalf("value = %#v, want 0.6", val)
		}
		if reason == "" {
			t.Fatalf("expected reasoning to be carried through")
		}
	})

	t.Run("missing choice field errors", func(t *testing.T) {
		if _, _, err := parseScoreResponse(`{"reasoning":"forgot"}`, cfg); err == nil {
			t.Fatalf("expected error for missing choice")
		}
	})

	t.Run("unknown choice errors, keeps reason", func(t *testing.T) {
		_, reason, err := parseScoreResponse(`{"choice":"Z","reasoning":"why"}`, cfg)
		if err == nil {
			t.Fatalf("expected error for unconfigured choice")
		}
		if reason != "why" {
			t.Fatalf("reason = %q, want carried through", reason)
		}
	})
}
