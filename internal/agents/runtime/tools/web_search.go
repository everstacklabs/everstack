package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// WebSearchHandler handles the web_search synthetic tool using a self-hosted
// SearXNG metasearch instance (JSON API). No third-party API key required.
type WebSearchHandler struct {
	// SearXNGURL is the base URL of the SearXNG instance (e.g.
	// "http://searxng.everstack-sandboxes-dev.svc.cluster.local:8080").
	// Empty disables the tool.
	SearXNGURL string
	HTTPClient *http.Client
	Emitter    *agentrt.Emitter
}

func (h *WebSearchHandler) Name() string { return "web_search" }

func (h *WebSearchHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "web_search",
			Description: "Search the web for current information, documentation, tutorials, or solutions. Returns titles, URLs, and snippets from top results.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The search query.",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results to return (default: 5, max: 10).",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

// searxngResponse mirrors the SearXNG JSON search API
// (GET /search?format=json). Each result carries title, url, and a text
// snippet under "content".
type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

type webSearchCacheEntry struct {
	value     string
	expiresAt time.Time
}

var (
	webSearchCacheMu sync.Mutex
	webSearchCache   = map[string]webSearchCacheEntry{}
)

const (
	webSearchCacheTTL     = 5 * time.Minute
	webSearchTimeout      = 10 * time.Second
	webSearchMaxAttempts  = 3
	webSearchBaseBackoff  = 200 * time.Millisecond
	webSearchMaxBodyBytes = 512 * 1024
)

func (h *WebSearchHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if h.SearXNGURL == "" {
		return "", fmt.Errorf("web search is not configured")
	}

	maxResults := 5
	if m, ok := args["max_results"].(float64); ok && m > 0 {
		maxResults = int(m)
		if maxResults > 10 {
			maxResults = 10
		}
	}

	cacheKey := fmt.Sprintf("%s|%d", strings.ToLower(strings.TrimSpace(query)), maxResults)
	if cached, ok := getWebSearchCache(cacheKey); ok {
		return cached, nil
	}

	// Emit search start event
	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventWebSearchStart,
			Timestamp: time.Now(),
			Data:      map[string]interface{}{"query": query, "max_results": maxResults},
		})
	}

	// SearXNG has no result-count parameter; it returns a page of aggregated
	// results which we slice to maxResults below.
	reqURL := fmt.Sprintf("%s/search?q=%s&format=json&safesearch=1&language=en",
		strings.TrimRight(h.SearXNGURL, "/"), url.QueryEscape(query))

	httpClient := h.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: webSearchTimeout}
	} else if httpClient.Timeout == 0 {
		httpClient = &http.Client{
			Timeout:       webSearchTimeout,
			Transport:     httpClient.Transport,
			CheckRedirect: httpClient.CheckRedirect,
			Jar:           httpClient.Jar,
		}
	}

	var (
		resp   *http.Response
		body   []byte
		err    error
		status int
	)

	for attempt := 0; attempt < webSearchMaxAttempts; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if reqErr != nil {
			return "", fmt.Errorf("failed to create request: %w", reqErr)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Everstack-Agent/1.0 (web_search tool)")

		resp, err = httpClient.Do(req)
		if err != nil {
			if attempt < webSearchMaxAttempts-1 && shouldRetryError(err) {
				if backoffErr := sleepWithBackoff(ctx, attempt); backoffErr != nil {
					return "", backoffErr
				}
				continue
			}
			return "", fmt.Errorf("web search failed: %w", err)
		}

		status = resp.StatusCode
		body, err = io.ReadAll(io.LimitReader(resp.Body, webSearchMaxBodyBytes))
		resp.Body.Close()
		if err != nil {
			if attempt < webSearchMaxAttempts-1 {
				if backoffErr := sleepWithBackoff(ctx, attempt); backoffErr != nil {
					return "", backoffErr
				}
				continue
			}
			return "", fmt.Errorf("failed to read search response: %w", err)
		}

		if status != http.StatusOK {
			if attempt < webSearchMaxAttempts-1 && shouldRetryStatus(status) {
				if backoffErr := sleepWithBackoff(ctx, attempt); backoffErr != nil {
					return "", backoffErr
				}
				continue
			}
			return "", fmt.Errorf("web search API returned status %d: %s", status, string(body))
		}

		break
	}

	var searchResp searxngResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", fmt.Errorf("failed to parse search response: %w", err)
	}

	results := searchResp.Results
	if len(results) == 0 {
		return fmt.Sprintf("No results found for %q.", query), nil
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	// Emit results event with source metadata
	if h.Emitter != nil {
		sources := make([]map[string]string, len(results))
		for i, r := range results {
			sources[i] = map[string]string{
				"title":       r.Title,
				"url":         r.URL,
				"description": r.Content,
			}
		}
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventWebSearchResults,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"query":   query,
				"sources": sources,
				"count":   len(results),
			},
		})
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d results for %q:\n\n", len(results), query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. **%s** (%s)\n   %s\n\n", i+1, r.Title, r.URL, r.Content))
	}

	rendered := sb.String()
	setWebSearchCache(cacheKey, rendered)
	return rendered, nil
}

func getWebSearchCache(key string) (string, bool) {
	webSearchCacheMu.Lock()
	defer webSearchCacheMu.Unlock()
	entry, ok := webSearchCache[key]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(webSearchCache, key)
		return "", false
	}
	return entry.value, true
}

func setWebSearchCache(key, value string) {
	webSearchCacheMu.Lock()
	defer webSearchCacheMu.Unlock()
	webSearchCache[key] = webSearchCacheEntry{
		value:     value,
		expiresAt: time.Now().Add(webSearchCacheTTL),
	}
}

func shouldRetryStatus(status int) bool {
	return status == http.StatusTooManyRequests || (status >= 500 && status <= 599)
}

func shouldRetryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

func sleepWithBackoff(ctx context.Context, attempt int) error {
	backoff := webSearchBaseBackoff * time.Duration(1<<attempt)
	jitter := time.Duration(rand.Intn(100)) * time.Millisecond
	wait := backoff + jitter
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
