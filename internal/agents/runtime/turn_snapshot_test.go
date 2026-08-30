package runtime

import (
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestApproxMessagesBytes(t *testing.T) {
	txt := "hello world"
	msgs := []gw.Message{
		{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: &txt}}},
		{Role: gw.RoleAssistant, Content: []gw.ContentPart{{Type: "text", Text: &txt}}},
	}
	got := approxMessagesBytes(msgs)
	// 4 (user) + 9 (assistant) + 11 + 11 = 35 bytes
	if got != 35 {
		t.Fatalf("approxMessagesBytes = %d, want 35", got)
	}
}

func TestApproxMessagesBytes_Empty(t *testing.T) {
	if got := approxMessagesBytes(nil); got != 0 {
		t.Fatalf("expected 0 for nil slice, got %d", got)
	}
}

func TestSummarizeToolResults_Empty(t *testing.T) {
	if got := summarizeToolResults(nil); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestSummarizeToolResults_GroupsByName(t *testing.T) {
	got := summarizeToolResults(map[string]ToolResultMeta{
		"call-1": {Result: "read_file:ok", Success: true},
		"call-2": {Result: "read_file:err", Success: false},
		"call-3": {Result: "write_file:ok", Success: true},
	})
	// Tool names are sorted alphabetically
	if !strings.Contains(got, "read_file=ok:1,err:1") {
		t.Errorf("missing read_file aggregate in %q", got)
	}
	if !strings.Contains(got, "write_file=ok:1,err:0") {
		t.Errorf("missing write_file aggregate in %q", got)
	}
	if strings.Index(got, "read_file") > strings.Index(got, "write_file") {
		t.Errorf("expected alphabetical order, got %q", got)
	}
}
