package vertex_ai

import (
	"context"
	"net/http"
	"testing"

	"golang.org/x/oauth2"
)

func TestBuildEndpoint(t *testing.T) {
	got, err := buildEndpoint("https://us-central1-aiplatform.googleapis.com/v1/projects/demo/locations/us-central1", "claude-3-7-sonnet@20250219", false)
	if err != nil {
		t.Fatalf("buildEndpoint: %v", err)
	}
	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/demo/locations/us-central1/publishers/anthropic/models/claude-3-7-sonnet@20250219:generateContent"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSetHeadersWithStaticToken(t *testing.T) {
	p, err := NewProvider(Config{BaseURL: "https://example.com", TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"})})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	h := http.Header{}
	if err := p.setHeaders(context.Background(), h); err != nil {
		t.Fatalf("setHeaders: %v", err)
	}
	if got := h.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("unexpected auth header: %s", got)
	}
}
