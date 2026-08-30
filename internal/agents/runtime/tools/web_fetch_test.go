package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchHandler_EmptyURL(t *testing.T) {
	h := &WebFetchHandler{HTTPClient: http.DefaultClient}
	_, err := h.Execute(context.Background(), map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("expected 'url is required' error, got: %v", err)
	}
}

func TestWebFetchHandler_InvalidScheme(t *testing.T) {
	h := &WebFetchHandler{HTTPClient: http.DefaultClient}
	_, err := h.Execute(context.Background(), map[string]interface{}{
		"url": "ftp://example.com/file.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "http://") {
		t.Fatalf("expected scheme error, got: %v", err)
	}
}

func TestWebFetchHandler_HTMLStripping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
<nav>Navigation links</nav>
<script>console.log("hidden");</script>
<style>.hidden { display: none; }</style>
<h1>Hello World</h1>
<p>This is a <b>test</b> paragraph.</p>
<footer>Footer content</footer>
</body>
</html>`)
	}))
	defer srv.Close()

	h := &WebFetchHandler{HTTPClient: srv.Client()}
	result, err := h.Execute(context.Background(), map[string]interface{}{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hello World") {
		t.Errorf("result should contain 'Hello World', got: %s", result)
	}
	if !strings.Contains(result, "test paragraph") {
		t.Errorf("result should contain 'test paragraph', got: %s", result)
	}
	if strings.Contains(result, "console.log") {
		t.Errorf("result should not contain script content, got: %s", result)
	}
	if strings.Contains(result, "Navigation links") {
		t.Errorf("result should not contain nav content, got: %s", result)
	}
	if strings.Contains(result, "Footer content") {
		t.Errorf("result should not contain footer content, got: %s", result)
	}
}

func TestWebFetchHandler_Truncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Write 200 characters
		fmt.Fprint(w, strings.Repeat("abcdefghij", 20))
	}))
	defer srv.Close()

	h := &WebFetchHandler{HTTPClient: srv.Client()}
	result, err := h.Execute(context.Background(), map[string]interface{}{
		"url":        srv.URL,
		"max_length": float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The content portion should be truncated to 50 characters
	// Format: "Content from {url} ({N} characters):\n\n{text}"
	if !strings.Contains(result, "(50 characters)") {
		t.Errorf("result should indicate 50 characters, got: %s", result)
	}
}

func TestWebFetchHandler_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "This is plain text content with <angle brackets> preserved.")
	}))
	defer srv.Close()

	h := &WebFetchHandler{HTTPClient: srv.Client()}
	result, err := h.Execute(context.Background(), map[string]interface{}{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "<angle brackets>") {
		t.Errorf("plain text should preserve angle brackets, got: %s", result)
	}
}
