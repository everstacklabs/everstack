package eval_runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScoreOutput_CodeScorerFailsClosed verifies the security posture of the
// code-scorer dispatch: user-supplied scorer code must never execute
// in-process unless the server-gated escape hatch
// (RunnerOpts.AllowUnsandboxedScorers) is enabled, and must never execute at
// all when sandbox isolation is requested but unavailable. A zero-value
// Runner has sandboxScorer == nil and allowUnsandboxedScorers == false,
// matching a deployment with no sandbox manager and the default opts.
func TestScoreOutput_CodeScorerFailsClosed(t *testing.T) {
	// Canary file: the scorer code writes it if it ever executes. It must
	// not exist after either dispatch below.
	canary := filepath.Join(t.TempDir(), "executed")
	code := "import pathlib\npathlib.Path(" + pyQuote(canary) + ").write_text('ran')\ndef score(input, output, expected, context, metadata):\n    return 1\n"

	in := ScoreInput{Input: "q", Output: "a", ExpectedOutput: "a"}

	assertFailedClosed := func(t *testing.T, scores map[string]interface{}, wantErrSubstr string) {
		t.Helper()
		if _, ok := scores["CodeScorer"]; ok {
			t.Fatalf("expected no score value, got %#v", scores["CodeScorer"])
		}
		errVal, ok := scores["CodeScorer_error"].(string)
		if !ok {
			t.Fatalf("expected CodeScorer_error string, got %#v", scores)
		}
		if !strings.Contains(errVal, wantErrSubstr) {
			t.Fatalf("error %q does not mention %q", errVal, wantErrSubstr)
		}
		if _, err := os.Stat(canary); !os.IsNotExist(err) {
			t.Fatalf("scorer code executed: canary file exists (stat err = %v)", err)
		}
	}

	t.Run("use_sandbox=true with no sandbox manager fails closed", func(t *testing.T) {
		r := &Runner{} // sandboxScorer == nil, allowUnsandboxedScorers == false
		cfg := ScoreConfig{
			ID:             "cfg-code-1",
			Name:           "CodeScorer",
			ScorerCode:     code,
			ScorerLanguage: "python",
			UseSandbox:     true,
		}
		scores := r.ScoreOutput(context.Background(), "tenant-1", "test-ns", in, []ScoreConfig{cfg})
		assertFailedClosed(t, scores, "requires sandbox isolation but the sandbox manager is unavailable")
	})

	t.Run("use_sandbox=false without server escape hatch fails closed", func(t *testing.T) {
		r := &Runner{}
		cfg := ScoreConfig{
			ID:             "cfg-code-2",
			Name:           "CodeScorer",
			ScorerCode:     code,
			ScorerLanguage: "python",
			UseSandbox:     false,
		}
		scores := r.ScoreOutput(context.Background(), "tenant-1", "test-ns", in, []ScoreConfig{cfg})
		assertFailedClosed(t, scores, "unsandboxed code scorers are disabled on this server")
	})
}

// pyQuote quotes a string as a Python string literal for the canary script.
func pyQuote(s string) string {
	return "\"" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "\"", "\\\"") + "\""
}
