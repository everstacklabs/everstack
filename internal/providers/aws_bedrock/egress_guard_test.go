package aws_bedrock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/httpclient"
)

// The AWS SDK brings its own HTTP client, so the shared guard only applies if
// it is handed over explicitly.
func TestCustomEndpointCannotReachInternalAddress(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	defer httpclient.SetPublicEndpointsForTest(true)()

	p, err := NewProvider(Config{Region: "us-east-1", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Chat(context.Background(), gw.ChatCompletionRequest{Model: "any", Messages: []gw.Message{{Role: "user"}}})

	if n := hits.Load(); n != 0 {
		t.Fatalf("internal server received %d requests", n)
	}
}
