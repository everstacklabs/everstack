package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// transformToProtoJSON runs an OpenAI chat body through the REST transform,
// decodes it exactly as the grpc-gateway does (DiscardUnknown), and returns
// the request re-encoded with proto field names: what survives here is what
// the handler can see.
func transformToProtoJSON(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	r := transformChatCompletionRequest(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var pb gatewaypb.ChatCompletionRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &pb); err != nil {
		t.Fatalf("transformed body does not decode: %v\n%s", err, raw)
	}
	out, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(&pb)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTransformChatKeepsOpenAIRequestFields(t *testing.T) {
	got := transformToProtoJSON(t, `{
		"model": "gpt-4o",
		"messages": [
			{"role": "developer", "content": "be brief"},
			{"role": "user", "name": "alice", "content": [{"type": "image_url", "image_url": {"url": "https://img", "detail": "low"}}]}
		],
		"tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}, "strict": true}}],
		"seed": 7, "top_k": 5, "verbosity": "low",
		"logprobs": true, "top_logprobs": 3, "logit_bias": {"50256": -100},
		"service_tier": "flex",
		"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "schema": {"type": "object"}, "strict": true}}
	}`)
	messages := got["messages"].([]interface{})
	if role := messages[0].(map[string]interface{})["role"]; role != "ROLE_SYSTEM" {
		t.Errorf("developer role = %v, want ROLE_SYSTEM", role)
	}
	user := messages[1].(map[string]interface{})
	if user["name"] != "alice" {
		t.Errorf("message name = %v", user["name"])
	}
	part := user["content"].([]interface{})[0].(map[string]interface{})
	if part["image_url"] != "https://img" || part["image_detail"] != "low" {
		t.Errorf("image part = %v", part)
	}
	fn := got["tools"].([]interface{})[0].(map[string]interface{})["function"].(map[string]interface{})
	if fn["strict"] != true {
		t.Errorf("tool strict = %v", fn["strict"])
	}
	wantSampling := map[string]interface{}{
		"seed": "7", "top_k": float64(5), "verbosity": "low",
		"logprobs": true, "top_logprobs": float64(3), "logit_bias": map[string]interface{}{"50256": float64(-100)},
	}
	if !reflect.DeepEqual(got["sampling"], wantSampling) {
		t.Errorf("sampling = %v, want %v", got["sampling"], wantSampling)
	}
	if got["service_tier"] != "flex" {
		t.Errorf("service_tier = %v", got["service_tier"])
	}
	rf, _ := got["response_format"].(map[string]interface{})
	if rf["type"] != "json_schema" || rf["json_schema"].(map[string]interface{})["name"] != "answer" {
		t.Errorf("response_format = %v", got["response_format"])
	}
}

// TestOpenAIChatParamsStillDropped is the inventory of OpenAI chat
// parameters the gateway still discards on the way to the handler. A
// parameter leaving this list is progress; one joining it is a regression.
func TestOpenAIChatParamsStillDropped(t *testing.T) {
	params := map[string]string{
		"audio":                  `{"voice":"alloy","format":"wav"}`,
		"frequency_penalty":      `0.5`,
		"function_call":          `"auto"`,
		"functions":              `[{"name":"f"}]`,
		"logit_bias":             `{"1":2}`,
		"logprobs":               `true`,
		"max_completion_tokens":  `9`,
		"max_tokens":             `9`,
		"metadata":               `{"k":"v"}`,
		"modalities":             `["text"]`,
		"parallel_tool_calls":    `false`,
		"prediction":             `{"type":"content","content":"x"}`,
		"presence_penalty":       `0.5`,
		"prompt_cache_key":       `"k"`,
		"prompt_cache_retention": `"24h"`,
		"reasoning_effort":       `"low"`,
		"response_format":        `{"type":"json_object"}`,
		"safety_identifier":      `"s"`,
		"seed":                   `7`,
		"service_tier":           `"flex"`,
		"stop":                   `["x"]`,
		"store":                  `true`,
		"stream_options":         `{"include_usage":true}`,
		"temperature":            `0.5`,
		"top_logprobs":           `2`,
		"top_p":                  `0.5`,
		"user":                   `"u"`,
		"verbosity":              `"low"`,
		"web_search_options":     `{}`,
	}
	var dropped []string
	for key, value := range params {
		got := transformToProtoJSON(t, `{"model":"m","messages":[{"role":"user","content":"hi"}],"`+key+`":`+value+`}`)
		sampling, _ := got["sampling"].(map[string]interface{})
		metadata, _ := got["metadata"].(map[string]interface{})
		_, top := got[key]
		_, inSampling := sampling[key]
		_, inMetadata := metadata[key]
		if !top && !inSampling && !inMetadata {
			dropped = append(dropped, key)
		}
	}
	sort.Strings(dropped)
	want := []string{
		"audio", "function_call", "functions", "modalities", "prediction",
		"prompt_cache_key", "prompt_cache_retention", "safety_identifier",
		"store", "stream_options", "web_search_options",
	}
	if !reflect.DeepEqual(dropped, want) {
		t.Fatalf("dropped OpenAI chat params = %v\nwant %v", dropped, want)
	}
}

func TestChatNGreaterThanOneIsRejected(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("n > 1 reached the gateway: %s", r.URL.Path)
	})
	for _, stream := range []string{``, `"stream":true,`, `"stream":false,`} {
		body := `{"model":"gpt-4o",` + stream + `"n":2,"messages":[{"role":"user","content":"hi"}]}`

		// Native /v1 caller: the grpc-gateway error shape.
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		withOpenAICompatTransform(inner).ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":3`) {
			t.Fatalf("native %s: status %d body %s", stream, rec.Code, rec.Body.String())
		}

		// OpenAI SDK caller (Bearer on /v1): the OpenAI error envelope.
		rec = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(common.Authorization, "Bearer evs_live_test")
		withV1OpenAICompat(withOpenAICompatTransform(inner)).ServeHTTP(rec, r)
		var envelope struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &envelope) != nil ||
			!strings.Contains(envelope.Error.Message, "n > 1") {
			t.Fatalf("openai %s: status %d body %s", stream, rec.Code, rec.Body.String())
		}
	}
}

func TestChatNOfOneIsAcceptedAndDropped(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","n":1,"messages":[{"role":"user","content":"hi"}]}`))
	out := transformChatCompletionRequest(r)
	raw, _ := io.ReadAll(out.Body)
	if strings.Contains(string(raw), `"n"`) {
		t.Fatalf("n=1 forwarded: %s", raw)
	}
}

// An OpenAI SDK pointed at /v1 sends its key as a Bearer on every JSON
// inference route, not only chat.
func TestV1BearerAcceptedOnJSONInferenceRoutes(t *testing.T) {
	for _, path := range []string{"/v1/images/generations", "/v1/moderations", "/v1/rerank"} {
		h := newHarness()
		rec := h.do(t, h.v1, http.MethodPost, path, `{"model":"m"}`, bearer(liveKey))
		if !h.seen.called || h.seen.apiKey != liveKey || h.seen.authorization != "" {
			t.Fatalf("%s: status %d, gateway called=%v apiKey=%q auth=%q: %s",
				path, rec.Code, h.seen.called, h.seen.apiKey, h.seen.authorization, rec.Body.String())
		}
	}
	// Multipart and binary routes stay out: the native routes cannot serve an
	// OpenAI SDK there.
	for _, path := range []string{"/v1/images/edits", "/v1/audio/speech", "/v1/audio/transcriptions"} {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		if isV1OpenAIPath(r) {
			t.Fatalf("%s unexpectedly treated as an OpenAI verb", path)
		}
	}
	if isV1OpenAIPath(httptest.NewRequest(http.MethodGet, "/v1/moderations", nil)) {
		t.Fatal("GET /v1/moderations treated as an OpenAI verb")
	}
}
