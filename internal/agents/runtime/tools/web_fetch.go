package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// WebFetchHandler handles the web_fetch synthetic tool for fetching web page content.
type WebFetchHandler struct {
	HTTPClient *http.Client
	JinaAPIKey string // optional — empty = local-only extraction
	Emitter    *agentrt.Emitter
}

func (h *WebFetchHandler) Name() string { return "web_fetch" }

// client returns the HTTP client used for fetches. When none is injected
// (production wiring passes nil), it returns a process-wide SSRF-guarded client
// that refuses to dial internal/metadata/private addresses — web_fetch resolves
// arbitrary agent-supplied URLs, so it must never become an SSRF vector. Tests
// inject an explicit client (e.g. httptest) to exercise the fetch logic.
func (h *WebFetchHandler) client() *http.Client {
	if h.HTTPClient != nil {
		return h.HTTPClient
	}
	return guardedHTTPClient()
}

func (h *WebFetchHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "web_fetch",
			Description: "Fetch a web page and extract its text content. Use after web_search to read full documentation or articles.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "The URL to fetch.",
					},
					"max_length": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum characters of content to return (default: 15000).",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (h *WebFetchHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}

	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return "", fmt.Errorf("url must start with http:// or https://")
	}

	maxLength := 15000
	if m, ok := args["max_length"].(float64); ok && m > 0 {
		maxLength = int(m)
	}

	// Emit fetch start event
	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventWebFetchStart,
			Timestamp: time.Now(),
			Data:      map[string]interface{}{"url": rawURL, "max_length": maxLength},
		})
	}

	// Try Jina Reader API first if API key is set
	method := "local"
	if h.JinaAPIKey != "" {
		content, err := h.fetchViaJina(ctx, rawURL, maxLength)
		if err == nil {
			method = "jina"
			if h.Emitter != nil {
				h.Emitter.Emit(agentrt.Event{
					Type:      agentrt.EventWebFetchComplete,
					Timestamp: time.Now(),
					Data:      map[string]interface{}{"url": rawURL, "method": method, "length": len(content)},
				})
			}
			return content, nil
		}
		// Fall through to local extraction on Jina failure
	}

	result, err := h.fetchLocal(ctx, rawURL, maxLength)
	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventWebFetchComplete,
			Timestamp: time.Now(),
			Data:      map[string]interface{}{"url": rawURL, "method": method, "length": len(result)},
		})
	}
	return result, err
}

// fetchViaJina uses the Jina Reader API (r.jina.ai) to extract clean markdown from a URL.
func (h *WebFetchHandler) fetchViaJina(ctx context.Context, rawURL string, maxLength int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://r.jina.ai/"+rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("jina request creation failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.JinaAPIKey)
	req.Header.Set("Accept", "text/markdown")
	req.Header.Set("X-Return-Format", "markdown")

	resp, err := h.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("jina fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("jina returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("jina read failed: %w", err)
	}

	content := strings.TrimSpace(string(body))
	if len(content) > maxLength {
		content = content[:maxLength]
	}

	return fmt.Sprintf("Content from %s (via Jina Reader, %d characters):\n\n%s", rawURL, len(content), content), nil
}

// fetchLocal is the local fallback that fetches and extracts text from a URL.
func (h *WebFetchHandler) fetchLocal(ctx context.Context, rawURL string, maxLength int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Everstack-Agent/1.0 (web_fetch tool)")
	req.Header.Set("Accept", "text/html, text/plain, application/json, */*")

	resp, err := h.client().Do(req)
	if err != nil {
		return fmt.Sprintf("Failed to fetch %s: %s", rawURL, err.Error()), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Fetch returned status %d for %s", resp.StatusCode, rawURL), nil
	}

	// Read up to 1MB raw
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return fmt.Sprintf("Failed to read response from %s: %s", rawURL, err.Error()), nil
	}

	content := string(body)
	contentType := resp.Header.Get("Content-Type")

	if strings.Contains(contentType, "text/html") {
		content = htmlToText(content)
	}

	if len(content) > maxLength {
		content = content[:maxLength]
	}

	return fmt.Sprintf("Content from %s (%d characters):\n\n%s", rawURL, len(content), content), nil
}

// htmlToText performs simple HTML-to-text extraction without external dependencies.
var (
	reRemoveScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reRemoveStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reRemoveNav    = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	reRemoveFooter = regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`)
	reRemoveHeader = regexp.MustCompile(`(?is)<header[^>]*>.*?</header>`)
	reBlockToNL    = regexp.MustCompile(`(?i)<(?:br|p|div|li|tr|h[1-6])[^>]*>`)
	reStripTags    = regexp.MustCompile(`<[^>]+>`)
	reWhitespace   = regexp.MustCompile(`[ \t]+`)
	reBlankLines   = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(html string) string {
	// Remove script, style, nav, footer, header blocks entirely
	text := reRemoveScript.ReplaceAllString(html, "")
	text = reRemoveStyle.ReplaceAllString(text, "")
	text = reRemoveNav.ReplaceAllString(text, "")
	text = reRemoveFooter.ReplaceAllString(text, "")
	text = reRemoveHeader.ReplaceAllString(text, "")
	// Convert block-level tags to newlines
	text = reBlockToNL.ReplaceAllString(text, "\n")
	// Strip remaining HTML tags
	text = reStripTags.ReplaceAllString(text, "")
	// Collapse horizontal whitespace
	text = reWhitespace.ReplaceAllString(text, " ")
	// Collapse excessive blank lines
	text = reBlankLines.ReplaceAllString(text, "\n\n")
	// Trim
	text = strings.TrimSpace(text)
	return text
}
