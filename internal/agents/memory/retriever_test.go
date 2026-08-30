package memory

import (
	"testing"
	"time"
)

func TestDeduplicateFacts(t *testing.T) {
	key := func(k string) *string { return &k }

	userFacts := []*AgentMemory{
		{ID: "u1", FactKey: key("user.name"), Content: "Arnab (user override)", Scope: MemoryScopeUser},
	}
	agentFacts := []*AgentMemory{
		{ID: "a1", FactKey: key("user.name"), Content: "Arnab", Scope: MemoryScopeAgent},
		{ID: "a2", FactKey: key("project.lang"), Content: "Go", Scope: MemoryScopeAgent},
	}
	globalFacts := []*AgentMemory{
		{ID: "g1", FactKey: key("project.lang"), Content: "Python", Scope: MemoryScopeGlobal},
		{ID: "g2", FactKey: key("company.name"), Content: "Everstack", Scope: MemoryScopeGlobal},
	}

	result := deduplicateFacts(userFacts, agentFacts, globalFacts)

	if len(result) != 3 {
		t.Fatalf("expected 3 deduplicated facts, got %d", len(result))
	}

	// user.name should come from user scope (u1), not agent (a1)
	found := false
	for _, m := range result {
		if m.FactKey != nil && *m.FactKey == "user.name" {
			if m.ID != "u1" {
				t.Errorf("user.name should be from user scope (u1), got %s", m.ID)
			}
			found = true
		}
	}
	if !found {
		t.Error("user.name not found in results")
	}

	// project.lang should come from agent scope (a2), not global (g1)
	for _, m := range result {
		if m.FactKey != nil && *m.FactKey == "project.lang" {
			if m.ID != "a2" {
				t.Errorf("project.lang should be from agent scope (a2), got %s", m.ID)
			}
		}
	}

	// company.name should come from global scope (g2)
	for _, m := range result {
		if m.FactKey != nil && *m.FactKey == "company.name" {
			if m.ID != "g2" {
				t.Errorf("company.name should be from global scope (g2), got %s", m.ID)
			}
		}
	}
}

func TestDeduplicateFacts_NilFactKeys(t *testing.T) {
	// Facts without keys should all be included (no dedup)
	agentFacts := []*AgentMemory{
		{ID: "a1", Content: "some fact without key", Scope: MemoryScopeAgent},
		{ID: "a2", Content: "another fact without key", Scope: MemoryScopeAgent},
	}

	result := deduplicateFacts(nil, agentFacts, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 facts (no dedup for nil keys), got %d", len(result))
	}
}

func TestMergeMemories(t *testing.T) {
	a := []*AgentMemory{{ID: "1"}, {ID: "2"}}
	b := []*AgentMemory{{ID: "2"}, {ID: "3"}}
	c := []*AgentMemory{{ID: "3"}, {ID: "4"}}

	result := mergeMemories(a, b, c)
	if len(result) != 4 {
		t.Fatalf("expected 4 unique memories, got %d", len(result))
	}
}

func TestGroupByScope(t *testing.T) {
	memories := []*AgentMemory{
		{ID: "1", Scope: MemoryScopeUser},
		{ID: "2", Scope: MemoryScopeAgent},
		{ID: "3", Scope: MemoryScopeGlobal},
		{ID: "4", Scope: MemoryScopeAgent},
	}

	user, agent, global := groupByScope(memories)
	if len(user) != 1 {
		t.Errorf("expected 1 user, got %d", len(user))
	}
	if len(agent) != 2 {
		t.Errorf("expected 2 agent, got %d", len(agent))
	}
	if len(global) != 1 {
		t.Errorf("expected 1 global, got %d", len(global))
	}
}

func TestFormatScopedMemoryBlock_Empty(t *testing.T) {
	block := formatScopedMemoryBlock(nil, nil, nil, nil)
	if block != "" {
		t.Errorf("expected empty string for no memories, got %q", block)
	}
}

func TestFormatScopedMemoryBlock_WithScopes(t *testing.T) {
	key := func(k string) *string { return &k }

	facts := []*AgentMemory{
		{FactKey: key("user.name"), Content: "Arnab", Scope: MemoryScopeUser},
		{FactKey: key("project.lang"), Content: "Go", Scope: MemoryScopeAgent},
		{FactKey: key("company.name"), Content: "Everstack", Scope: MemoryScopeGlobal},
	}
	instructions := []*AgentMemory{
		{Content: "Use pnpm", Scope: MemoryScopeAgent},
	}
	summaries := []*AgentMemory{
		{Content: "Worked on memory system", CreatedAt: time.Now().Add(-1 * time.Hour)},
	}

	block := formatScopedMemoryBlock(facts, instructions, summaries, nil)

	if block == "" {
		t.Fatal("expected non-empty block")
	}

	// Check sections exist
	for _, section := range []string{
		"<agent_memory>",
		"</agent_memory>",
		"## Personal Context (User-specific)",
		"## Agent Knowledge",
		"## Organization Knowledge",
		"## Instructions",
		"## Recent Session Context",
		"user.name: Arnab",
		"project.lang: Go",
		"company.name: Everstack",
		"Use pnpm",
		"Worked on memory system",
	} {
		if !contains(block, section) {
			t.Errorf("block missing %q", section)
		}
	}
}

func TestFormatScopedMemoryBlock_WithRAG(t *testing.T) {
	ragResults := []string{"Document about Kubernetes deployment"}
	block := formatScopedMemoryBlock(nil, nil, nil, ragResults)

	if !contains(block, "## Reference Documents") {
		t.Error("block missing Reference Documents section")
	}
	if !contains(block, "Kubernetes deployment") {
		t.Error("block missing RAG content")
	}
}

func TestFormatAge(t *testing.T) {
	tests := []struct {
		age    time.Duration
		expect string
	}{
		{30 * time.Minute, "30 minutes ago"},
		{3 * time.Hour, "3 hours ago"},
		{48 * time.Hour, "2 days ago"},
	}

	for _, tt := range tests {
		got := formatAge(time.Now().Add(-tt.age))
		if got != tt.expect {
			t.Errorf("formatAge(%v): got %q, want %q", tt.age, got, tt.expect)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
