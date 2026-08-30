package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComputeHypothesisDiff_FirstAttempt(t *testing.T) {
	child := &AttemptConfig{Model: "claude-opus-4-7"}
	got := ComputeHypothesisDiff(nil, child)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["baseline"]; !ok {
		t.Fatalf("expected baseline anchor key, got %s", string(got))
	}
}

func TestComputeHypothesisDiff_ModelChange(t *testing.T) {
	parent := &AttemptConfig{Model: "claude-opus-4-7", Tools: []string{"read_file"}}
	child := &AttemptConfig{Model: "gpt-5", Tools: []string{"read_file"}}
	got := ComputeHypothesisDiff(parent, child)
	s := string(got)
	if !strings.Contains(s, "claude-opus-4-7") || !strings.Contains(s, "gpt-5") {
		t.Fatalf("expected from/to model values, got %s", s)
	}
	if strings.Contains(s, "tools") {
		t.Fatalf("tools unchanged, should not appear: %s", s)
	}
}

func TestComputeHypothesisDiff_SystemPromptChangePresenceOnly(t *testing.T) {
	parent := &AttemptConfig{SystemPrompt: "be concise"}
	child := &AttemptConfig{SystemPrompt: "be verbose"}
	got := ComputeHypothesisDiff(parent, child)
	s := string(got)
	if !strings.Contains(s, `"system_prompt_changed":true`) {
		t.Fatalf("expected system_prompt_changed flag, got %s", s)
	}
	if strings.Contains(s, "be concise") || strings.Contains(s, "be verbose") {
		t.Fatalf("full prompt should not be recorded in diff, got %s", s)
	}
}

func TestComputeHypothesisDiff_NoChange(t *testing.T) {
	cfg := &AttemptConfig{Model: "gpt-5", Tools: []string{"a", "b"}}
	got := ComputeHypothesisDiff(cfg, cfg)
	if string(got) != "{}" {
		t.Fatalf("expected empty diff, got %s", string(got))
	}
}
