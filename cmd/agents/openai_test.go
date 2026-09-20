package agents

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICommandRegisteredAndReadsConnectionJSONFromStdin(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/openai-agents/connections" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-evs-api-key") != "evs-key" {
			t.Fatal("missing auth")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "name": "demo"})
	}))
	defer srv.Close()
	t.Setenv("EVS_API_URL", srv.URL)
	t.Setenv("EVS_API_KEY", "evs-key")
	cmd := New()
	cmd.SetArgs([]string{"openai", "connections", "create"})
	cmd.SetIn(strings.NewReader(`{"name":"demo","api_key":"upstream-secret"}`))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got["api_key"] != "upstream-secret" {
		t.Fatalf("body=%v", got)
	}
	if strings.Contains(out.String(), "upstream-secret") {
		t.Fatal("credential leaked to output")
	}
}

func TestOpenAIEventsUsesDurableCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/v1/openai-agents/sessions/s1/events?after=9&limit=2" {
			t.Fatal(r.URL.RequestURI())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "next_cursor": 9, "has_more": false})
	}))
	defer srv.Close()
	t.Setenv("EVS_API_URL", srv.URL)
	t.Setenv("EVS_API_KEY", "key")
	cmd := New()
	cmd.SetArgs([]string{"openai", "sessions", "events", "s1", "--after", "9", "--limit", "2"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"next_cursor": 9`) {
		t.Fatal(out.String())
	}
}

func TestOpenAIApprovalResolveReadsDecisionFromStdin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/openai-agents/approvals/a1/resolve" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["decision"] != "approved" {
			t.Fatalf("body=%v", body)
		}
		_, _ = w.Write([]byte(`{"resolved":true}`))
	}))
	defer srv.Close()
	t.Setenv("EVS_API_URL", srv.URL)
	t.Setenv("EVS_API_KEY", "key")
	cmd := New()
	cmd.SetArgs([]string{"openai", "approvals", "resolve", "a1"})
	cmd.SetIn(strings.NewReader(`{"decision":"approved"}`))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"resolved": true`) {
		t.Fatal(out.String())
	}
}
