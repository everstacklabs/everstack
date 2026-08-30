package eval_runner

import (
	"context"
	"strings"
	"testing"
)

// A verdict-only DAG traverses with NO LLM call, so it exercises the walker +
// validator end to end without a gateway.
func TestDagScorerVerdictOnly(t *testing.T) {
	def := []byte(`{
		"root": "v1",
		"nodes": {
			"v1": { "id": "v1", "type": "verdict", "score": 0.8 }
		}
	}`)
	if err := ValidateDagDefinition(def); err != nil {
		t.Fatalf("valid verdict-only DAG rejected: %v", err)
	}

	r := &Runner{}
	res, err := r.runDagScorer(context.Background(), ScoreConfig{Name: "dag", DagDefinition: def}, "in", "out", "exp")
	if err != nil {
		t.Fatalf("runDagScorer: %v", err)
	}
	got, ok := toFloat64(res.Value)
	if !ok || got != 0.8 {
		t.Fatalf("score = %v (ok=%v), want 0.8", res.Value, ok)
	}
	if !strings.Contains(res.Reason, "v1") {
		t.Fatalf("reason should record the traversed path, got %q", res.Reason)
	}
}

func TestDagScorerRoutesToVerdict(t *testing.T) {
	// A binary_judgement whose two edges both lead to fixed-score verdicts.
	// We only validate the graph here (routing requires an LLM call); the
	// point is the validator accepts a well-formed multi-node DAG.
	def := []byte(`{
		"root": "j1",
		"nodes": {
			"j1": { "id": "j1", "type": "binary_judgement", "prompt": "Is the answer correct? {{output}}",
				"edges": [ {"label": "yes", "target": "pass"}, {"label": "no", "target": "fail"} ] },
			"pass": { "id": "pass", "type": "verdict", "score": 1.0 },
			"fail": { "id": "fail", "type": "verdict", "score": 0.0 }
		}
	}`)
	if err := ValidateDagDefinition(def); err != nil {
		t.Fatalf("valid DAG rejected: %v", err)
	}
}

func TestDagScorerValidatorRejects(t *testing.T) {
	cases := []struct {
		name string
		def  string
	}{
		{"empty", `{}`},
		{"missing root node", `{"root":"x","nodes":{"y":{"id":"y","type":"verdict","score":0.5}}}`},
		{"unknown node type", `{"root":"a","nodes":{"a":{"id":"a","type":"frobnicate"}}}`},
		{"verdict without score or sub_scorer", `{"root":"a","nodes":{"a":{"id":"a","type":"verdict"}}}`},
		{"verdict score out of range", `{"root":"a","nodes":{"a":{"id":"a","type":"verdict","score":1.5}}}`},
		{"decision node without edges", `{"root":"a","nodes":{"a":{"id":"a","type":"binary_judgement","prompt":"p"}}}`},
		{"edge to missing node", `{"root":"a","nodes":{"a":{"id":"a","type":"task","prompt":"p","edges":[{"label":"go","target":"ghost"}]}}}`},
		{"no reachable verdict", `{"root":"a","nodes":{"a":{"id":"a","type":"task","prompt":"p","edges":[{"label":"loop","target":"b"}]},"b":{"id":"b","type":"task","prompt":"p","edges":[{"label":"loop","target":"a"}]}}}`},
		{"cycle", `{"root":"a","nodes":{"a":{"id":"a","type":"task","prompt":"p","edges":[{"label":"x","target":"a"}]}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateDagDefinition([]byte(tc.def)); err == nil {
				t.Fatalf("expected %s to be rejected, but validation passed", tc.name)
			}
		})
	}
}
