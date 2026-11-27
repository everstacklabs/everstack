package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestInboundAgentDepth(t *testing.T) {
	for _, tc := range []struct {
		header  string
		want    int
		allowed bool
	}{{"0", 1, true}, {"1", 2, true}, {"2", 0, false}, {"malformed", 0, false}, {"17", 0, false}} {
		ran := false
		s := newTestServer(Deps{Agents: &fakeLookup{found: true}, Runner: stubRunner{fn: func(ctx context.Context, _, _, _ string) (string, error) {
			ran = true
			if contextkeys.AgentHop(ctx) != tc.want {
				t.Error("hop not incremented")
			}
			return "ok", nil
		}}})
		r := httptest.NewRequest("POST", "/a2a/agents/a", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"parts":[{"kind":"text","text":"hello"}]}}}`))
		r.Header.Set("Authorization", "Bearer key")
		r.Header.Set(contextkeys.AgentHopHeader, tc.header)
		w := httptest.NewRecorder()
		newRouter(s).ServeHTTP(w, r)
		if ran != tc.allowed || !tc.allowed && !strings.Contains(w.Body.String(), "agent call depth limit reached") {
			t.Fatalf("header=%q ran=%v body=%s", tc.header, ran, w.Body)
		}
	}
}
