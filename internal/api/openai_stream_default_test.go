package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
)

// forwardedStream runs one chat request through an OpenAI-compatible entry
// point plus the request transform, and reports the `stream` field the native
// handler would receive: its value, and whether the key was present at all.
func forwardedStream(t *testing.T, entry func(http.Handler) http.Handler, path, body string, hdr map[string]string) (value interface{}, present bool) {
	t.Helper()
	var forwarded map[string]interface{}
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read forwarded body: %v", err)
		}
		if err := json.Unmarshal(raw, &forwarded); err != nil {
			t.Fatalf("forwarded body is not JSON: %v\n%s", err, raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(protoChatFrames))
	})

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	entry(withOpenAICompatTransform(native)).ServeHTTP(httptest.NewRecorder(), req)

	if forwarded == nil {
		t.Fatalf("native handler was never reached for %s", path)
	}
	value, present = forwarded["stream"]
	return value, present
}

// An OpenAI client that omits `stream` is asking for a non-streaming call. The
// native handler would otherwise apply the instance streaming default and
// stream the upstream call, so the surface has to say false explicitly.
func TestOpenAISurfaceTreatsAbsentStreamAsFalse(t *testing.T) {
	const messages = `"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]`

	cases := []struct {
		name  string
		entry func(http.Handler) http.Handler
		path  string
		body  string
		hdr   map[string]string
		want  interface{}
	}{
		{"alias, stream omitted", withOpenAIRoundTrip, "/openai/v1/chat/completions", `{` + messages + `}`, bearer(liveKey), false},
		{"alias, stream null", withOpenAIRoundTrip, "/openai/v1/chat/completions", `{` + messages + `,"stream":null}`, bearer(liveKey), false},
		{"alias, stream false", withOpenAIRoundTrip, "/openai/v1/chat/completions", `{` + messages + `,"stream":false}`, bearer(liveKey), false},
		{"alias, stream true", withOpenAIRoundTrip, "/openai/v1/chat/completions", `{` + messages + `,"stream":true}`, bearer(liveKey), true},
		{"v1 bearer, stream omitted", withV1OpenAICompat, "/v1/chat/completions", `{` + messages + `}`, bearer(liveKey), false},
		{"v1 bearer, stream true", withV1OpenAICompat, "/v1/chat/completions", `{` + messages + `,"stream":true}`, bearer(liveKey), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, present := forwardedStream(t, tc.entry, tc.path, tc.body, tc.hdr)
			if !present {
				t.Fatalf("stream was not forwarded; the native handler would apply the instance default")
			}
			if got != tc.want {
				t.Fatalf("forwarded stream = %v, want %v", got, tc.want)
			}
		})
	}
}

// A request that offers tools keeps the instance default. The handler's unary
// branch runs the server-side tool loop on any tool call, so sending a client
// that runs its own tools down it would change its answer and multiply its
// upstream calls.
func TestOpenAISurfaceLeavesStreamAbsentWhenToolsAreOffered(t *testing.T) {
	const messages = `"model":"gpt-5.5","messages":[{"role":"user","content":"weather in Paris?"}]`
	const tool = `{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}`

	offered := []struct {
		name string
		body string
	}{
		{"one tool", `{` + messages + `,"tools":[` + tool + `]}`},
		{"malformed tools", `{` + messages + `,"tools":"get_weather"}`},
	}
	for _, tc := range offered {
		t.Run(tc.name, func(t *testing.T) {
			if got, present := forwardedStream(t, withOpenAIRoundTrip, "/openai/v1/chat/completions", tc.body, bearer(liveKey)); present {
				t.Fatalf("stream was forced to %v for a request that offers tools", got)
			}
		})
	}

	// No tools to call means no tool loop to trip: these are plain chat calls.
	none := []struct {
		name string
		body string
	}{
		{"empty tools", `{` + messages + `,"tools":[]}`},
		{"null tools", `{` + messages + `,"tools":null}`},
	}
	for _, tc := range none {
		t.Run(tc.name, func(t *testing.T) {
			got, present := forwardedStream(t, withOpenAIRoundTrip, "/openai/v1/chat/completions", tc.body, bearer(liveKey))
			if !present || got != false {
				t.Fatalf("forwarded stream = %v (present %v), want false", got, present)
			}
		})
	}

	// An explicit choice is the caller's, tools or not.
	if got, present := forwardedStream(t, withOpenAIRoundTrip, "/openai/v1/chat/completions",
		`{`+messages+`,"tools":[`+tool+`],"stream":false}`, bearer(liveKey)); !present || got != false {
		t.Fatalf("explicit stream:false with tools was not forwarded as given: %v (present %v)", got, present)
	}
}

// Native /v1 callers authenticate with the api-key header and no bearer. An
// absent `stream` there keeps its existing meaning (the instance default), so
// the field must reach the handler untouched.
func TestNativeV1KeepsAbsentStreamAbsent(t *testing.T) {
	_, present := forwardedStream(t, withV1OpenAICompat, "/v1/chat/completions",
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{common.EverstackApiKey: liveKey})
	if present {
		t.Fatalf("native /v1 request gained a stream field; its default must stay the instance's")
	}
}
