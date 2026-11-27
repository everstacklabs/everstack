package decision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testRequest() Request {
	return Request{Model: "jev-1.13.0", State: json.RawMessage(`{"text":"request"}`), Questions: map[string]Question{"route": {Type: "choice", Criteria: json.RawMessage(`{"small":"simple","large":"complex"}`)}}}
}
func TestClientProtocolAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		wantError  bool
	}{
		{"valid", `{"model":"jev-1.13.0","answers":{"route":{"type":"choice","choice":"small","confidence":0.9,"probabilities":{"small":0.95,"large":0.05}}},"usage":{"input_tokens":10,"output_tokens":0}}`, 200, false},
		{"unknown option", `{"answers":{"route":{"type":"choice","choice":"attacker","confidence":1,"probabilities":{"attacker":1}}}}`, 200, true},
		{"missing answers", `{"answers":{}}`, 200, true},
		{"negative usage", `{"usage":{"input_tokens":-1}}`, 200, true},
		{"rate limit", "secret echoed upstream", 429, true},
		{"authentication", "secret echoed upstream", 401, true},
		{"invalid JSON", "not JSON", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Errorf("wrong protocol or auth")
				}
				var got Request
				if json.NewDecoder(r.Body).Decode(&got) != nil || got.Model != "jev-1.13.0" {
					t.Error("wrong request")
				}
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c, err := NewClient(srv.URL+"/v1/", "test-secret", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Decide(context.Background(), testRequest())
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("upstream body leaked")
			}
			if tc.code == 429 {
				var he *HTTPError
				if !errors.As(err, &he) || he.RetryAfter != "2" {
					t.Fatalf("missing retry metadata: %v", err)
				}
			}
		})
	}
}
func TestNoRedirectAndCancellation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("followed redirect, potentially leaked credentials")
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "secret", nil)
	if _, err := c.Decide(context.Background(), testRequest()); err == nil {
		t.Fatal("redirect accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Decide(ctx, testRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

type fakeDecision func(context.Context, Request) (Response, error)

func (f fakeDecision) Decide(ctx context.Context, r Request) (Response, error) { return f(ctx, r) }
func routingConfig() RoutingConfig {
	return RoutingConfig{Enabled: true, Model: "@kev/kev-latest", Fallback: "@openai/large", Targets: map[string]Target{"small": {Model: "@openai/small", Description: "Simple"}, "large": {Model: "@openai/large", Description: "Complex"}}}
}
func TestRoutingUsesAllowlistConfidenceTimeoutAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, choice string
		confidence   float64
		fail         bool
		want, reason string
	}{
		{"confident", "small", .95, false, "@openai/small", "selected"},
		{"uncertain", "small", .3, false, "@openai/large", "low_confidence"},
		{"unknown", "attacker", 1, false, "@openai/large", "unknown_choice"},
		{"unavailable", "", 0, true, "@openai/large", "provider_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fakeDecision(func(context.Context, Request) (Response, error) {
				if tc.fail {
					return Response{}, errors.New("down")
				}
				return Response{Answers: map[string]Answer{"route": {Type: "choice", Choice: tc.choice, Confidence: &tc.confidence}}}, nil
			})
			model, reason, err := routingConfig().Select(context.Background(), p, json.RawMessage(`"text"`))
			if err != nil || model != tc.want || reason != tc.reason {
				t.Fatalf("%s %s %v", model, reason, err)
			}
		})
	}
	cfg := routingConfig()
	cfg.TimeoutMS = 1
	p := fakeDecision(func(ctx context.Context, _ Request) (Response, error) { <-ctx.Done(); return Response{}, ctx.Err() })
	if m, _, err := cfg.Select(context.Background(), p, json.RawMessage(`"text"`)); err != nil || m != cfg.Fallback {
		t.Fatalf("timeout: %s %v", m, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, _, err := cfg.Select(ctx, p, json.RawMessage(`"text"`)); err == nil {
		t.Fatal("canceled parent fell back")
	}
}
func TestNoulAndScorePreserveZero(t *testing.T) {
	zero := 0.0
	req := Request{Model: "laya", State: json.RawMessage(`"state"`), Questions: map[string]Question{"n": {Type: "noul"}, "s": {Type: "score", Criteria: json.RawMessage(`["low","high"]`)}}}
	r := Response{Answers: map[string]Answer{"n": {Type: "noul", Noul: &zero}, "s": {Type: "score", Score: &zero, Confidence: &zero, Probabilities: map[string]float64{"0": 1, "1": 0}}}}
	if err := r.Validate(req); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(r)
	if !strings.Contains(string(body), `"noul":0`) || !strings.Contains(string(body), `"score":0`) {
		t.Fatal(string(body))
	}
}
