package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.Handler, mutate func(*Config)) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	cfg := Config{BaseURL: s.URL + "/v1", APIKey: "secret", Project: "proj", Organization: "org", Timeout: time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func checkHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer secret" {
		t.Fatalf("authorization = %q", got)
	}
	if got := r.Header.Get("OpenAI-Beta"); got != "agents=v1" {
		t.Fatalf("beta = %q", got)
	}
	if r.Header.Get("OpenAI-Project") != "proj" || r.Header.Get("OpenAI-Organization") != "org" {
		t.Fatal("missing ownership headers")
	}
}

func TestSessionMethodsUseDocumentedRoutesAndPreserveExtensions(t *testing.T) {
	var seen []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			io.WriteString(w, `{"id":"sess_1","status":"idle","future":{"x":1},"environment":{"id":"env_1","type":"none","future_env":true}}`)
		case strings.Contains(r.URL.Path, "sess_1"):
			io.WriteString(w, `{"id":"sess_1","deleted":true}`)
		default:
			io.WriteString(w, `{"object":"list","data":[{"id":"sess_1"}],"has_more":false,"future_page":"x"}`)
		}
	}), nil)
	s, err := c.CreateSession(context.Background(), CreateSessionRequest{Agent: Agent{Model: "gpt-test"}, Input: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sess_1" || s.Extra["future"] == nil || s.Environment.Extra["future_env"] == nil {
		t.Fatalf("extensions not preserved: %#v", s)
	}
	p, err := c.ListSessions(context.Background(), ListOptions{After: "sess_0", Limit: 20, Order: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Extra["future_page"] == nil {
		t.Fatal("page extension missing")
	}
	if _, err = c.DeleteSession(context.Background(), "sess_1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /v1/agents/sessions", "GET /v1/agents/sessions?after=sess_0&limit=20&order=desc", "DELETE /v1/agents/sessions/sess_1"}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n%s", strings.Join(seen, "\n"))
	}
}

func TestInputCancelAndToolResultEnvelopes(t *testing.T) {
	var bodies []string
	var keys []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/agents/sessions/s/events" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusNoContent)
	}), nil)
	ctx := context.Background()
	if err := c.SendInputWithOptions(ctx, "s", []InputMessage{{Role: "user", Content: []InputContent{{Type: "input_text", Text: "hi"}}}}, EventOptions{IdempotencyKey: "input-1"}); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelSession(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if err := c.SubmitToolResult(ctx, "s", ToolResult{TurnID: "t", CallID: "c", Success: true, Output: `{"ok":true}`}); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"agent.session.input.message"`, `"agent.session.input.cancel"`, `"agent.session.input.tool_result"`} {
		found := false
		for _, b := range bodies {
			found = found || strings.Contains(b, needle)
		}
		if !found {
			t.Fatalf("missing %s in %#v", needle, bodies)
		}
	}
	if len(bodies) != 3 {
		t.Fatalf("bodies=%d", len(bodies))
	}
	if keys[0] != "input-1" || keys[1] != "" || keys[2] != "" {
		t.Fatalf("idempotency keys=%v", keys)
	}
}

func TestRateLimitErrorRetainsSafeMetadata(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.Header().Set("x-request-id", "req_1")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"slow down","code":"rate_limit"}}`)
	}), nil)
	_, err := c.GetSession(context.Background(), "s")
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error=%v", err)
	}
	if e.Kind != ErrorRateLimited || e.RetryAfter != "12" || e.RequestID != "req_1" || e.Code != "rate_limit" {
		t.Fatalf("error=%#v", e)
	}
	if strings.Contains(e.Error(), "secret") {
		t.Fatal("secret leaked")
	}
}

func TestCreateTransportFailureIsAmbiguousAndNotRetried(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); panic(http.ErrAbortHandler) }), nil)
	_, err := c.CreateSession(context.Background(), CreateSessionRequest{Agent: Agent{Model: "x"}, Input: "x"})
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrorTransport || !e.Ambiguous {
		t.Fatalf("error=%#v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestMalformedJSONIsProtocolError(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "{") }), nil)
	_, err := c.GetSession(context.Background(), "s")
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrorProtocol {
		t.Fatalf("error=%v", err)
	}
}

func TestRedirectWithCredentialsIsNotFollowed(t *testing.T) {
	var targetCalled bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalled = true }))
	defer target.Close()
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}), nil)
	_, err := c.GetSession(context.Background(), "s")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Kind != ErrorTransport {
		t.Fatalf("error=%v", err)
	}
	if targetCalled {
		t.Fatal("credential-bearing redirect was followed")
	}
}

func TestArtifactRoutes(t *testing.T) {
	var paths []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "application/octet-stream")
			io.WriteString(w, "file")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			io.WriteString(w, `{"id":"a","deleted":true}`)
		} else if strings.HasSuffix(r.URL.Path, "/a") {
			io.WriteString(w, `{"id":"a","path":"/workspace/outputs/a","size_bytes":4,"environment_id":"env"}`)
		} else {
			io.WriteString(w, `{"data":[{"id":"a"}],"has_more":false}`)
		}
	}), nil)
	ctx := context.Background()
	if _, e := c.ListArtifacts(ctx, "s", ListOptions{}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.GetArtifact(ctx, "s", "a"); e != nil {
		t.Fatal(e)
	}
	body, _, e := c.DownloadArtifact(ctx, "s", "a")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(body)
	body.Close()
	if string(b) != "file" {
		t.Fatal(string(b))
	}
	if _, e = c.DeleteArtifact(ctx, "s", "a"); e != nil {
		t.Fatal(e)
	}
	if len(paths) != 4 {
		t.Fatalf("paths=%v", paths)
	}
}
