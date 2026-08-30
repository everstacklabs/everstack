package v1

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/agents/trigger"
)

func TestTriggerNameExistsExcludesCurrentTrigger(t *testing.T) {
	existing := []*trigger.Trigger{
		{ID: "trigger-1", Name: "daily"},
		{ID: "trigger-2", Name: "incoming"},
	}

	if !triggerNameExists(existing, "daily", "") {
		t.Fatal("new duplicate trigger name was not detected")
	}
	if triggerNameExists(existing, "daily", "trigger-1") {
		t.Fatal("unchanged current trigger name was treated as a duplicate")
	}
	if triggerNameExists(existing, "missing", "") {
		t.Fatal("unused trigger name was treated as a duplicate")
	}
}
