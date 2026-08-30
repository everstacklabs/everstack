package main

import "testing"

func TestParseVerdictThresholds_Valid(t *testing.T) {
	preds, err := parseVerdictThresholds([]string{"win:0.85", "draw:0.10"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(preds) != 2 {
		t.Fatalf("expected 2 predicates, got %d", len(preds))
	}
	if preds[0].bucket != "win" || preds[0].rate != 0.85 {
		t.Errorf("bad win predicate: %+v", preds[0])
	}
	if preds[1].bucket != "draw" || preds[1].rate != 0.10 {
		t.Errorf("bad draw predicate: %+v", preds[1])
	}
}

func TestParseVerdictThresholds_BadBucket(t *testing.T) {
	if _, err := parseVerdictThresholds([]string{"maybe:0.5"}); err == nil {
		t.Fatal("expected error for unknown bucket")
	}
}

func TestParseVerdictThresholds_BadRate(t *testing.T) {
	if _, err := parseVerdictThresholds([]string{"win:1.5"}); err == nil {
		t.Fatal("expected error for rate out of range")
	}
}

func TestEvalVerdictPredicate_Win(t *testing.T) {
	p := verdictPredicate{bucket: "win", rate: 0.85}
	if !evalVerdictPredicate(p, 0.90) {
		t.Error("win 0.90 should pass ≥0.85")
	}
	if evalVerdictPredicate(p, 0.80) {
		t.Error("win 0.80 should fail ≥0.85")
	}
}

func TestEvalVerdictPredicate_Fail(t *testing.T) {
	p := verdictPredicate{bucket: "fail", rate: 0.10}
	if !evalVerdictPredicate(p, 0.05) {
		t.Error("fail 0.05 should pass ≤0.10")
	}
	if evalVerdictPredicate(p, 0.15) {
		t.Error("fail 0.15 should fail ≤0.10")
	}
}
