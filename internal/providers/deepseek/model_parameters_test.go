package deepseek

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRequestSerializesReasoningEffortAndExplicitZeroSampling(t *testing.T) {
	t.Parallel()

	zero := 0.0
	payload, err := json.Marshal(dsChatReq{
		Model:           "deepseek-reasoner",
		Temperature:     &zero,
		TopP:            &zero,
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	encoded := string(payload)
	for _, want := range []string{
		`"temperature":0`,
		`"top_p":0`,
		`"reasoning_effort":"high"`,
	} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("payload = %s, want %s", encoded, want)
		}
	}
}
