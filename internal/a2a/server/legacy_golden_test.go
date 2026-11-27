package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These bytes were captured from the unmodified legacy handler after the
// existing server tests passed. Only random identifiers are normalized.
func TestLegacyWireGolden(t *testing.T) {
	cases := []struct{ name, request string }{
		{"success", `{"jsonrpc":"2.0","id":7,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hello"},{"text":" world"}]}}}`},
		{"failure", `{"jsonrpc":"2.0","id":"failure","method":"message/send","params":{"message":{"parts":[{"text":"fail"}]}}}`},
		{"empty", `{"jsonrpc":"2.0","id":8,"method":"message/send","params":{}}`},
		{"bad_params", `{"jsonrpc":"2.0","id":8,"method":"message/send","params":[]}`},
		{"stream", `{"jsonrpc":"2.0","id":9,"method":"message/stream"}`},
		{"unknown", `{"jsonrpc":"2.0","id":10,"method":"SendMessage"}`},
		{"parse", `{`},
		{"invalid", `{"jsonrpc":"1.0","id":11,"method":"message/send"}`},
	}
	ids := regexp.MustCompile(`[a-f0-9]{32}`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(Deps{Runner: stubRunner{fn: func(_ context.Context, _, _, msg string) (string, error) {
				if msg == "fail" {
					return "", errors.New("fixture failure")
				}
				return "echo:" + msg, nil
			}}})
			for _, version := range []string{"", "0.2", "0.3"} {
				req := httptestRequest(tc.request, version)
				rr := recordRequest(newRouter(s), req)
				got := ids.ReplaceAllString(rr.Body.String(), "<id>")
				path := filepath.Join("testdata", "legacy_"+tc.name+".golden")
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if got != string(want) {
					t.Fatalf("version %q wire changed:\ngot  %s\nwant %s", version, got, want)
				}
				if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/json" || rr.Header().Get("A2A-Version") != "" {
					t.Fatalf("legacy headers/status changed: %d %v", rr.Code, rr.Header())
				}
			}
		})
	}
}

func httptestRequest(body, version string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/a2a/agents/agent-1", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	if version != "" {
		req.Header.Set("A2A-Version", version)
	}
	return req
}
func recordRequest(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}
