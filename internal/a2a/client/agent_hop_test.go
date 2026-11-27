package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

type hopTransport func(*http.Request) (*http.Response, error)

func (f hopTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOutboundAgentHop(t *testing.T) {
	for _, hop := range []int{0, 1, 2} {
		c := &Client{HTTPClient: &http.Client{Transport: hopTransport(func(r *http.Request) (*http.Response, error) {
			want := ""
			if hop > 0 {
				want = strconv.Itoa(hop)
			}
			if r.Header.Get(contextkeys.AgentHopHeader) != want {
				t.Error("hop not propagated")
			}
			var req struct{ ID string }
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			body := `{"jsonrpc":"2.0","id":"` + req.ID + `","result":{"kind":"message","messageId":"response-id","role":"agent","parts":[{"kind":"text","text":"done"}]}}`
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}}
		if _, err := c.Send(contextkeys.WithAgentHop(context.Background(), hop), Connection{Endpoint: "https://example.test/a2a", Protocol: Legacy}, "hi", "id"); err != nil {
			t.Fatal(err)
		}
	}
}
