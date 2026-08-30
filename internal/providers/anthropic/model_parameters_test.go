package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func claudePayload(t *testing.T, sampling gw.SamplingParams) string {
	t.Helper()
	p := &Provider{apiKey: "test-key"}
	req, err := p.toClaude(gw.ChatCompletionRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: strPointer("hi")}}}},
		Sampling: sampling,
	})
	if err != nil {
		t.Fatalf("toClaude() error = %v", err)
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(encoded)
}

func strPointer(s string) *string { return &s }

func TestClaudeForwardsTopPAndTopK(t *testing.T) {
	t.Parallel()

	topK := 40
	payload := claudePayload(t, gw.SamplingParams{
		TopP:           0.9,
		TopPConfigured: true,
		TopK:           &topK,
	})
	for _, want := range []string{`"top_p":0.9`, `"top_k":40`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload = %s, want %s", payload, want)
		}
	}
}

func TestClaudeOmitsUnsetSamplingWindow(t *testing.T) {
	t.Parallel()

	payload := claudePayload(t, gw.SamplingParams{})
	for _, unwanted := range []string{`"top_p"`, `"top_k"`} {
		if strings.Contains(payload, unwanted) {
			t.Fatalf("payload = %s, want %s omitted", payload, unwanted)
		}
	}
}
