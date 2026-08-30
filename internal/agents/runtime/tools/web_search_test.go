package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// searxngResult renders one SearXNG JSON result.
func searxngResults(items ...map[string]string) map[string]interface{} {
	results := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		results = append(results, map[string]interface{}{
			"title":   it["title"],
			"url":     it["url"],
			"content": it["content"],
		})
	}
	return map[string]interface{}{"results": results}
}

func TestWebSearchHandler_EmptyQuery(t *testing.T) {
	h := &WebSearchHandler{SearXNGURL: "http://searxng.local", HTTPClient: http.DefaultClient}
	_, err := h.Execute(context.Background(), map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("expected 'query is required' error, got: %v", err)
	}
}

func TestWebSearchHandler_NotConfigured(t *testing.T) {
	h := &WebSearchHandler{HTTPClient: http.DefaultClient}
	_, err := h.Execute(context.Background(), map[string]interface{}{"query": "anything"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected 'not configured' error when SearXNGURL is empty, got: %v", err)
	}
}

func TestWebSearchHandler_FormatResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/search" {
			t.Errorf("expected path /search, got %q", got)
		}
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("expected format=json, got %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "golang testing" {
			t.Errorf("expected q=golang testing, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResults(
			map[string]string{"title": "Go Testing", "url": "https://go.dev/testing", "content": "Official Go testing docs."},
			map[string]string{"title": "Go by Example", "url": "https://gobyexample.com/testing", "content": "Testing examples."},
		))
	}))
	defer srv.Close()

	h := &WebSearchHandler{SearXNGURL: srv.URL, HTTPClient: srv.Client()}
	result, err := h.Execute(context.Background(), map[string]interface{}{
		"query": "golang testing",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Go Testing") {
		t.Errorf("result should contain 'Go Testing', got: %s", result)
	}
	if !strings.Contains(result, "https://go.dev/testing") {
		t.Errorf("result should contain URL, got: %s", result)
	}
	if !strings.Contains(result, "Official Go testing docs.") {
		t.Errorf("result should contain the snippet, got: %s", result)
	}
	if !strings.Contains(result, "Found 2 results") {
		t.Errorf("result should contain 'Found 2 results', got: %s", result)
	}
}

func TestWebSearchHandler_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "rate limited")
	}))
	defer srv.Close()

	h := &WebSearchHandler{SearXNGURL: srv.URL, HTTPClient: srv.Client()}
	result, err := h.Execute(context.Background(), map[string]interface{}{
		"query": "test",
	})
	if err == nil {
		t.Fatalf("expected error, got result: %s", result)
	}
	if !strings.Contains(err.Error(), "status 429") {
		t.Errorf("error should contain status code 429, got: %s", err.Error())
	}
}

func TestWebSearchHandler_MaxResults(t *testing.T) {
	// SearXNG has no count parameter; the handler slices the returned page to
	// max_results. Server returns 3, we request 2 → "Found 2 results".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResults(
			map[string]string{"title": "Result 1", "url": "https://example.com/1", "content": "First."},
			map[string]string{"title": "Result 2", "url": "https://example.com/2", "content": "Second."},
			map[string]string{"title": "Result 3", "url": "https://example.com/3", "content": "Third."},
		))
	}))
	defer srv.Close()

	h := &WebSearchHandler{SearXNGURL: srv.URL, HTTPClient: srv.Client()}
	result, err := h.Execute(context.Background(), map[string]interface{}{
		"query":       "test query slice",
		"max_results": float64(2),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Found 2 results") {
		t.Errorf("result should contain 'Found 2 results', got: %s", result)
	}
	if strings.Contains(result, "Result 3") {
		t.Errorf("result should have been sliced to 2, got: %s", result)
	}
}
