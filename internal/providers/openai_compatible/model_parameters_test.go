package openai_compatible

import (
	"encoding/json"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestOptionalTopPPreservesConfiguredZero(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(openaiChatReq{
		Model: "compatible-model",
		TopP: optionalTopP(gw.SamplingParams{
			TopP:           0,
			TopPConfigured: true,
		}),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(payload), `"top_p":0`) {
		t.Fatalf("payload = %s, want configured zero top_p", payload)
	}
}

func TestCompatibleRequestCarriesTopKAndSeed(t *testing.T) {
	t.Parallel()

	topK := 20
	seed := int64(7)
	payload, err := json.Marshal(openaiChatReq{
		Model: "compatible-model",
		TopK:  &topK,
		Seed:  &seed,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, want := range []string{`"top_k":20`, `"seed":7`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("payload = %s, want %s", payload, want)
		}
	}
}

func TestCompatibleRequestOmitsUnsetTopKAndSeed(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(openaiChatReq{Model: "compatible-model"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, unwanted := range []string{`"top_k"`, `"seed"`} {
		if strings.Contains(string(payload), unwanted) {
			t.Fatalf("payload = %s, want %s omitted", payload, unwanted)
		}
	}
}
