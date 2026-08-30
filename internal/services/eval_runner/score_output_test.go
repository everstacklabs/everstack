package eval_runner

import "testing"

// TestScoreOutput_BuiltinExactMatch exercises the shared scorer dispatch that
// both the background runner (processItem) and the synchronous ScoreOutput RPC
// rely on. The builtin path touches no DB/sandbox, so a zero-value Runner is
// sufficient.
func TestScoreOutput_BuiltinExactMatch(t *testing.T) {
	r := &Runner{}

	cfg := ScoreConfig{
		ID:       "cfg-1",
		Name:     "ExactMatch",
		DataType: "builtin_exact_match",
	}

	t.Run("match", func(t *testing.T) {
		scores := r.ScoreOutput(nil, "tenant-1", "playground-test", ScoreInput{
			Output:         "hello",
			ExpectedOutput: "hello",
		}, []ScoreConfig{cfg})

		if got, ok := scores["ExactMatch"].(bool); !ok || got != true {
			t.Fatalf("expected ExactMatch=true, got %#v", scores["ExactMatch"])
		}
		if _, ok := scores["ExactMatch_reason"]; !ok {
			t.Fatalf("expected a reason for the score, got none: %#v", scores)
		}
		if _, ok := scores["ExactMatch_error"]; ok {
			t.Fatalf("did not expect an error key: %#v", scores)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		scores := r.ScoreOutput(nil, "tenant-1", "playground-test", ScoreInput{
			Output:         "hello",
			ExpectedOutput: "goodbye",
		}, []ScoreConfig{cfg})

		if got, ok := scores["ExactMatch"].(bool); !ok || got != false {
			t.Fatalf("expected ExactMatch=false, got %#v", scores["ExactMatch"])
		}
	})

	t.Run("no configs returns empty", func(t *testing.T) {
		scores := r.ScoreOutput(nil, "tenant-1", "playground-test", ScoreInput{Output: "x"}, nil)
		if len(scores) != 0 {
			t.Fatalf("expected empty scores, got %#v", scores)
		}
	})
}
