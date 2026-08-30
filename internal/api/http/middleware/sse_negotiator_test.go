package middleware

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeChunk(t *testing.T, b []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("chunk not JSON: %v (%s)", err, b)
	}
	return m
}

func TestTransformToOpenAIChunkShape(t *testing.T) {
	s := &sseWriter{format: "openai"}

	first := decodeChunk(t, s.transformToOpenAIChunk(
		`{"result":{"id":"chat_1","model":"gpt-5.5","created":1751600000,"choices":[{"message":{"content":[{"text":"hel"}]}}]}}`))

	if first["object"] != "chat.completion.chunk" {
		t.Fatalf("object = %v", first["object"])
	}
	if first["id"] != "chat_1" || first["model"] != "gpt-5.5" {
		t.Fatalf("stream fields missing: %v", first)
	}
	choices := first["choices"].([]interface{})
	if len(choices) != 1 {
		t.Fatalf("choices = %v", choices)
	}
	c0 := choices[0].(map[string]interface{})
	delta := c0["delta"].(map[string]interface{})
	if delta["role"] != "assistant" || delta["content"] != "hel" {
		t.Fatalf("first delta must prime role: %v", delta)
	}
	if fr, present := c0["finish_reason"]; !present || fr != nil {
		t.Fatalf("finish_reason must be present and null mid-stream: %v present=%v", fr, present)
	}

	// Second frame: no role repeat, id/model inherited from stream state.
	second := decodeChunk(t, s.transformToOpenAIChunk(
		`{"result":{"choices":[{"message":{"content":[{"text":"lo"}]},"finish_reason":"stop"}]}}`))
	if second["id"] != "chat_1" || second["model"] != "gpt-5.5" {
		t.Fatalf("stream fields not carried: %v", second)
	}
	c0 = second["choices"].([]interface{})[0].(map[string]interface{})
	delta = c0["delta"].(map[string]interface{})
	if _, hasRole := delta["role"]; hasRole {
		t.Fatalf("role must only be sent once: %v", delta)
	}
	if c0["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v", c0["finish_reason"])
	}
}

func TestTransformToOpenAIChunkUsage(t *testing.T) {
	s := &sseWriter{format: "openai"}
	chunk := decodeChunk(t, s.transformToOpenAIChunk(
		`{"result":{"id":"chat_1","usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8},"choices":[]}}`))
	usage, ok := chunk["usage"].(map[string]interface{})
	if !ok || usage["total_tokens"].(float64) != 8 {
		t.Fatalf("usage not forwarded: %v", chunk)
	}
}

func TestTransformToOpenAIChunkPreservesProviderContent(t *testing.T) {
	t.Parallel()

	s := &sseWriter{format: "openai"}
	chunk := decodeChunk(t, s.transformToOpenAIChunk(
		`{"result":{"id":"chat_1","choices":[{"message":{"content":[{"provider_json":"{\"type\":\"thinking\",\"thinking\":[{\"type\":\"text\",\"text\":\"trace\"}]}"},{"text":"answer","provider_json":"{\"type\":\"text\",\"text\":\"answer\"}"}]}}]}}`))
	choices := chunk["choices"].([]interface{})
	delta := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
	if delta["content"] != "answer" {
		t.Fatalf("content = %v, want answer", delta["content"])
	}
	providerContent, ok := delta["provider_content"].([]interface{})
	if !ok || len(providerContent) != 2 {
		t.Fatalf("provider_content = %#v, want two native chunks", delta["provider_content"])
	}
	first := providerContent[0].(map[string]interface{})
	if first["type"] != "thinking" {
		t.Fatalf("first native chunk = %#v, want thinking", first)
	}
}

func TestTransformToOpenAIChunkErrorFrame(t *testing.T) {
	s := &sseWriter{format: "openai"}

	out := s.transformToOpenAIChunk(`{"error":{"code":5,"message":"model not found: x","type":"error"}}`)
	if !strings.Contains(string(out), `"error"`) || !strings.Contains(string(out), "model not found: x") {
		t.Fatalf("error frame swallowed: %s", out)
	}

	out = s.transformToOpenAIChunk(`{"code":16,"message":"Invalid API key"}`)
	if !strings.Contains(string(out), "Invalid API key") {
		t.Fatalf("grpc status frame swallowed: %s", out)
	}
}

func TestTransformToOpenAIChunkFinishReasonEnumPrefix(t *testing.T) {
	s := &sseWriter{format: "openai"}
	chunk := decodeChunk(t, s.transformToOpenAIChunk(
		`{"result":{"id":"c","choices":[{"message":{"content":[]},"finish_reason":"FINISH_REASON_STOP"}]}}`))
	c0 := chunk["choices"].([]interface{})[0].(map[string]interface{})
	if c0["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v, want stop", c0["finish_reason"])
	}
}
