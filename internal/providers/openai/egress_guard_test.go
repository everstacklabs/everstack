package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/outboundguard"
	"github.com/everstacklabs/everstack/internal/providers/httpclient"
)

// A tenant's custom base URL must not reach internal addresses on a managed
// gateway, by either the unary or the streaming path.
func TestCustomBaseURLCannotReachInternalAddress(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	defer httpclient.SetPublicEndpointsForTest(true)()

	p := NewProvider(Config{BaseURL: srv.URL, APIKey: "tenant-key"})
	req := gw.ChatCompletionRequest{Model: "any", Messages: []gw.Message{{Role: "user"}}}

	if _, err := p.Chat(context.Background(), req); !errors.Is(err, outboundguard.ErrBlocked) {
		t.Fatalf("Chat = %v, want ErrBlocked", err)
	}
	err := p.ChatStream(context.Background(), req, func(gw.ChatResponseChunk) error { return nil })
	if !errors.Is(err, outboundguard.ErrBlocked) {
		t.Fatalf("ChatStream = %v, want ErrBlocked", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("internal server received %d requests", n)
	}
}
