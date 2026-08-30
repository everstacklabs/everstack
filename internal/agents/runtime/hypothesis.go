package runtime

import (
	"encoding/json"
	"strings"
)

// AttemptConfig captures the inputs that distinguish one attempt from another
// for hypothesis-diff purposes. It is intentionally a flat struct so the
// serialized hypothesis_diff JSON has stable key names that the verdict-rate
// dashboards can query without parsing nested shapes.
type AttemptConfig struct {
	Model            string            `json:"model,omitempty"`
	PromptTemplateID string            `json:"prompt_template_id,omitempty"`
	PromptVersion    string            `json:"prompt_version,omitempty"`
	SamplingParams   map[string]any    `json:"sampling_params,omitempty"`
	Tools            []string          `json:"tools,omitempty"`
	SystemPrompt     string            `json:"system_prompt,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// ComputeHypothesisDiff produces a structured "what changed vs parent" blob.
// Only fields that differ are included so the resulting JSON is small and
// dashboards can show "this attempt changed model + sampling params"
// without parsing every field. SystemPrompt is included only as a
// presence-or-not flag because storing two prompts verbatim per attempt
// is wasteful for diff purposes; the full prompt lives on the agent_branches
// instruction column already.
func ComputeHypothesisDiff(parent, child *AttemptConfig) json.RawMessage {
	if child == nil {
		return json.RawMessage("{}")
	}
	if parent == nil {
		// First attempt — no diff possible. Record the child config as a
		// baseline anchor so downstream readers can still tell what the
		// attempt was run with.
		buf, _ := json.Marshal(map[string]any{"baseline": child})
		return buf
	}
	out := map[string]any{}
	if parent.Model != child.Model {
		out["model"] = []string{parent.Model, child.Model}
	}
	if parent.PromptTemplateID != child.PromptTemplateID {
		out["prompt_template_id"] = []string{parent.PromptTemplateID, child.PromptTemplateID}
	}
	if parent.PromptVersion != child.PromptVersion {
		out["prompt_version"] = []string{parent.PromptVersion, child.PromptVersion}
	}
	if !equalToolLists(parent.Tools, child.Tools) {
		out["tools"] = map[string][]string{"from": parent.Tools, "to": child.Tools}
	}
	if !equalAnyMaps(parent.SamplingParams, child.SamplingParams) {
		out["sampling_params"] = map[string]any{"from": parent.SamplingParams, "to": child.SamplingParams}
	}
	if (parent.SystemPrompt == "") != (child.SystemPrompt == "") ||
		strings.TrimSpace(parent.SystemPrompt) != strings.TrimSpace(child.SystemPrompt) {
		// Record only that the prompt changed, not the full text — see
		// the doc comment above.
		out["system_prompt_changed"] = true
	}
	buf, _ := json.Marshal(out)
	return buf
}

func equalToolLists(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalAnyMaps(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		// Compare via JSON round-trip for stable equality across nested
		// structures without bringing in reflect.DeepEqual.
		aj, _ := json.Marshal(av)
		bj, _ := json.Marshal(bv)
		if string(aj) != string(bj) {
			return false
		}
	}
	return true
}
