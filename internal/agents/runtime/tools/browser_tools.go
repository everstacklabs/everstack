package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const browserMaxTextLen = 2048
const browserElementTimeout = 3 * time.Second // short initial wait — resolveElement auto-retries with re-observe

// ─── browser_navigate ───────────────────────────────────────────────

type browserNavigateHandler struct{ bctx *BrowserSessionContext }

func (h *browserNavigateHandler) Name() string { return "browser_navigate" }

func (h *browserNavigateHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserNavigateHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_navigate",
			Description: "Navigate to a URL. Returns numbered interactive elements — use indices with browser_click/browser_type.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to navigate to",
					},
					"wait_until": map[string]interface{}{
						"type":        "string",
						"description": "'domcontentloaded' (default) or 'load'",
						"enum":        []string{"domcontentloaded", "load"},
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (h *browserNavigateHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	start := time.Now()
	url, _ := args["url"].(string)
	if url == "" {
		return "", fmt.Errorf("url is required")
	}

	logger.WithFields("url", url).Info("browser_navigate: starting")

	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}
	logger.WithFields("url", url, "ensure_page_ms", time.Since(start).Milliseconds()).
		Info("browser_navigate: page ready")

	navStart := time.Now()
	if err := page.Navigate(url); err != nil {
		return fmt.Sprintf("Failed to navigate to %s: %s", url, err.Error()), nil
	}

	waitUntil, _ := args["wait_until"].(string)
	switch waitUntil {
	case "load":
		if err := page.WaitLoad(); err != nil {
			logger.WithFields("url", url, "error", err.Error()).
				Debug("browser_navigate: WaitLoad failed (continuing)")
		}
	default: // "domcontentloaded" or empty — fast default
		if err := page.Timeout(5*time.Second).WaitDOMStable(500*time.Millisecond, 0.1); err != nil {
			logger.WithFields("url", url, "error", err.Error()).
				Debug("browser_navigate: WaitDOMStable failed (continuing)")
		}
	}
	logger.WithFields("url", url, "wait_until", waitUntil, "nav_ms", time.Since(navStart).Milliseconds()).
		Info("browser_navigate: page loaded")

	// Dismiss cookie banners before observing — the LLM shouldn't see them.
	cookieStart := time.Now()
	dismissCookieBanners(page)
	logger.WithFields("cookie_dismiss_ms", time.Since(cookieStart).Milliseconds()).
		Info("browser_navigate: cookie dismiss done")

	if h.bctx.Emitter != nil {
		info, _ := page.Info()
		navData := map[string]interface{}{"url": url}
		if info != nil {
			navData["url"] = info.URL
			navData["title"] = info.Title
		}
		h.bctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventBrowserNavigate,
			SessionID: h.bctx.SandboxCtx.SessionID,
			Timestamp: time.Now(),
			Data:      navData,
		})
	}

	// Auto-observe: return the page state so the LLM knows what's on the page
	observed, err := h.bctx.observePage(page)
	if err != nil {
		info, _ := page.Info()
		title := ""
		if info != nil {
			title = info.Title
		}
		logger.WithFields("url", url, "error", err.Error(), "total_ms", time.Since(start).Milliseconds()).
			Warn("browser_navigate: observe failed")
		return fmt.Sprintf("Navigated to %s\nTitle: %s\n(Could not observe page: %s)", url, title, err.Error()), nil
	}

	// Auto-screenshot: emit a screenshot so the frontend browser panel has a
	// fallback to display even when the WebSocket stream isn't delivering frames.
	captureAutoScreenshot(h.bctx, page)

	logger.WithFields("url", url, "total_ms", time.Since(start).Milliseconds(), "result_len", len(observed)).
		Info("browser_navigate: completed")
	return observed, nil
}

// ─── browser_observe ────────────────────────────────────────────────

type browserObserveHandler struct{ bctx *BrowserSessionContext }

func (h *browserObserveHandler) Name() string { return "browser_observe" }

func (h *browserObserveHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserObserveHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_observe",
			Description: "Re-scan page for interactive elements. Call after page changes to get fresh indices.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func (h *browserObserveHandler) Execute(ctx context.Context, _ map[string]interface{}) (string, error) {
	start := time.Now()
	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	observed, err := h.bctx.observePage(page)
	if err != nil {
		return fmt.Sprintf("Failed to observe page: %s", err.Error()), nil
	}

	logger.WithFields("total_ms", time.Since(start).Milliseconds(), "result_len", len(observed)).
		Info("browser_observe: completed")
	emitBrowserActionWithSnapshot(h.bctx, page, "observe", "")
	return observed, nil
}

// ─── browser_click ──────────────────────────────────────────────────

type browserClickHandler struct{ bctx *BrowserSessionContext }

func (h *browserClickHandler) Name() string { return "browser_click" }

func (h *browserClickHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserClickHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_click",
			Description: "Click an element by index from browser_observe, or by CSS selector.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Element index from observe",
					},
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "CSS selector fallback",
					},
				},
			},
		},
	}
}

func (h *browserClickHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	start := time.Now()
	index, _ := args["index"].(float64)
	selector, _ := args["selector"].(string)

	ref := selector
	if index > 0 {
		ref = fmt.Sprintf("[%d]", int(index))
	}
	logger.WithFields("ref", ref, "index", index, "selector", selector).
		Info("browser_click: starting")

	if index == 0 && selector == "" {
		return "", fmt.Errorf("either index or selector is required")
	}

	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	el, err := h.bctx.resolveElement(page, index, selector)
	if err != nil {
		logger.WithFields("ref", ref, "error", err.Error()).
			Warn("browser_click: resolve failed")
		return fmt.Sprintf("Click failed: %s", err.Error()), nil
	}

	// Grab text before clicking (element may disappear after click)
	text, _ := el.Text()
	if len(text) > 100 {
		text = text[:100] + "..."
	}

	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Sprintf("Failed to click element: %s", err.Error()), nil
	}

	// Wait for page to settle after click — pages often load new content
	if err := page.Timeout(5*time.Second).WaitDOMStable(800*time.Millisecond, 0.1); err != nil {
		logger.WithFields("ref", ref, "error", err.Error()).
			Debug("browser_click: WaitDOMStable timed out (continuing)")
	}

	// Silently refresh element indices so the LLM's next browser_click/type works.
	// Do NOT return the full observe output — it adds thousands of tokens per click
	// and causes the conversation to hit rate limits.
	h.bctx.observePage(page)

	// Record the successful action and its post-action visual state.
	emitBrowserActionWithSnapshot(h.bctx, page, "click", ref)

	logger.WithFields("ref", ref, "text", text, "total_ms", time.Since(start).Milliseconds()).
		Info("browser_click: completed")

	info, _ := page.Info()
	pageTitle := ""
	if info != nil {
		pageTitle = info.Title
	}
	h.bctx.mu.Lock()
	elementCount := len(h.bctx.elementMap)
	h.bctx.mu.Unlock()

	return fmt.Sprintf("Clicked %s (text: %s). Page: %s (%d interactive elements). Call browser_observe to see element details.", ref, text, pageTitle, elementCount), nil
}

// ─── browser_type ───────────────────────────────────────────────────

type browserTypeHandler struct{ bctx *BrowserSessionContext }

func (h *browserTypeHandler) Name() string { return "browser_type" }

func (h *browserTypeHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserTypeHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_type",
			Description: "Type text into an input by index or CSS selector. Use submit=true to press Enter.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Element index from observe",
					},
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "CSS selector fallback",
					},
					"text": map[string]interface{}{
						"type":        "string",
						"description": "Text to type",
					},
					"clear": map[string]interface{}{
						"type":        "boolean",
						"description": "Clear input first",
					},
					"submit": map[string]interface{}{
						"type":        "boolean",
						"description": "Press Enter after typing",
					},
				},
				"required": []string{"text"},
			},
		},
	}
}

func (h *browserTypeHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	start := time.Now()
	index, _ := args["index"].(float64)
	selector, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	clear, _ := args["clear"].(bool)
	submit, _ := args["submit"].(bool)

	ref := selector
	if index > 0 {
		ref = fmt.Sprintf("[%d]", int(index))
	}
	logger.WithFields("ref", ref, "text_len", len(text), "clear", clear, "submit", submit).
		Info("browser_type: starting")

	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	if index == 0 && selector == "" {
		return "", fmt.Errorf("either index or selector is required")
	}

	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	el, err := h.bctx.resolveElement(page, index, selector)
	if err != nil {
		logger.WithFields("ref", ref, "error", err.Error()).
			Warn("browser_type: resolve failed")
		return fmt.Sprintf("Type failed: %s", err.Error()), nil
	}

	if clear {
		if err := el.SelectAllText(); err == nil {
			_ = el.Input("")
		}
	}

	if err := el.Input(text); err != nil {
		return fmt.Sprintf("Failed to type text: %s", err.Error()), nil
	}

	result := fmt.Sprintf("Typed %d characters into %s", len(text), ref)

	if submit {
		if err := el.Type(input.Enter); err != nil {
			result += fmt.Sprintf(" (Enter key failed: %s)", err.Error())
		} else {
			result += " and pressed Enter"
			// Wait for form submission / page transition to settle
			if waitErr := page.WaitDOMStable(800*time.Millisecond, 0.1); waitErr != nil {
				logger.WithFields("ref", ref, "error", waitErr.Error()).
					Debug("browser_type: WaitDOMStable after submit timed out (continuing)")
			}
			// Silently refresh element indices (compact — don't dump full observe)
			h.bctx.observePage(page)
			result += ". Call browser_observe to see the updated page."
		}
	}
	// Never include typed text in the event; retain only the target and the
	// post-action page image so secrets cannot leak into structured logs.
	emitBrowserActionWithSnapshot(h.bctx, page, "type", ref)

	logger.WithFields("ref", ref, "text_len", len(text), "submit", submit, "total_ms", time.Since(start).Milliseconds()).
		Info("browser_type: completed")
	return result, nil
}

// ─── browser_screenshot ─────────────────────────────────────────────

type browserScreenshotHandler struct{ bctx *BrowserSessionContext }

func (h *browserScreenshotHandler) Name() string { return "browser_screenshot" }

func (h *browserScreenshotHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserScreenshotHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_screenshot",
			Description: "Take a screenshot of the current page.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"full_page": map[string]interface{}{
						"type":        "boolean",
						"description": "Full page capture",
					},
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "Element CSS selector",
					},
					"return_base64": map[string]interface{}{
						"type":        "boolean",
						"description": "Return as base64",
					},
				},
			},
		},
	}
}

func (h *browserScreenshotHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	fullPage, _ := args["full_page"].(bool)
	selector, _ := args["selector"].(string)
	returnBase64, _ := args["return_base64"].(bool)

	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	var data []byte
	const jpegQuality = 60

	if selector != "" {
		el, elErr := page.Timeout(browserElementTimeout).Element(selector)
		if elErr != nil {
			return fmt.Sprintf("Element not found for selector '%s': %s", selector, elErr.Error()), nil
		}
		data, err = el.Screenshot(proto.PageCaptureScreenshotFormatJpeg, jpegQuality)
		if err != nil {
			return fmt.Sprintf("Failed to screenshot element: %s", err.Error()), nil
		}
	} else {
		data, err = page.Screenshot(fullPage, &proto.PageCaptureScreenshot{
			Format:  proto.PageCaptureScreenshotFormatJpeg,
			Quality: intPtr(jpegQuality),
		})
		if err != nil {
			return fmt.Sprintf("Failed to take screenshot: %s", err.Error()), nil
		}
	}

	if h.bctx.Emitter != nil {
		evtData := map[string]interface{}{
			"size_bytes": len(data),
			"selector":   selector,
			"full_page":  fullPage,
		}
		// Include base64 image in the event so the frontend can display
		// it as a fallback when the WebSocket stream isn't working.
		// Cap at 500KB raw (≈375KB JPEG) to avoid bloating SSE events.
		if len(data) <= 500_000 {
			evtData["image_base64"] = base64.StdEncoding.EncodeToString(data)
		}
		h.bctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventBrowserScreenshot,
			SessionID: h.bctx.SandboxCtx.SessionID,
			Timestamp: time.Now(),
			Data:      evtData,
		})
	}

	savePath := "/tmp/screenshot.jpg"
	var writeErr error
	canWriteSandbox := h.bctx.SandboxCtx != nil && h.bctx.SandboxCtx.Manager != nil
	if canWriteSandbox {
		writeErr = h.bctx.SandboxCtx.Manager.WriteFile(ctx, h.bctx.SandboxCtx.SessionID, savePath, data)
	}

	if returnBase64 {
		const maxBase64Bytes = 100_000
		b64 := base64.StdEncoding.EncodeToString(data)
		if len(b64) > maxBase64Bytes {
			if canWriteSandbox && writeErr == nil {
				return fmt.Sprintf("Screenshot saved to %s (%d bytes). Base64 omitted — too large.", savePath, len(data)), nil
			}
			return fmt.Sprintf("Screenshot taken (%d bytes) but too large to return as base64.", len(data)), nil
		}
		return fmt.Sprintf("Screenshot taken (%d bytes).\nBase64:\n%s", len(data), b64), nil
	}

	if canWriteSandbox && writeErr != nil {
		return fmt.Sprintf("Screenshot taken (%d bytes) but could not save: %s", len(data), writeErr.Error()), nil
	}
	if !canWriteSandbox {
		return fmt.Sprintf("Screenshot captured (%d bytes)", len(data)), nil
	}

	return fmt.Sprintf("Screenshot saved to %s (%d bytes)", savePath, len(data)), nil
}

// ─── browser_evaluate ───────────────────────────────────────────────

type browserEvaluateHandler struct{ bctx *BrowserSessionContext }

func (h *browserEvaluateHandler) Name() string { return "browser_evaluate" }

func (h *browserEvaluateHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserEvaluateHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_evaluate",
			Description: "Run JavaScript in the browser and return the result.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"expression": map[string]interface{}{
						"type":        "string",
						"description": "JS expression (use arrow function for complex logic)",
					},
				},
				"required": []string{"expression"},
			},
		},
	}
}

func (h *browserEvaluateHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	expression, _ := args["expression"].(string)
	if expression == "" {
		return "", fmt.Errorf("expression is required")
	}

	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	evalExpr := expression
	if !strings.HasPrefix(strings.TrimSpace(expression), "()") &&
		!strings.HasPrefix(strings.TrimSpace(expression), "function") {
		evalExpr = fmt.Sprintf("() => (%s)", expression)
	}

	result, err := page.Timeout(10 * time.Second).Eval(evalExpr)
	if err != nil {
		return fmt.Sprintf("JavaScript evaluation failed: %s", err.Error()), nil
	}

	val := result.Value.String()
	if len(val) > browserMaxTextLen {
		val = val[:browserMaxTextLen] + "...(truncated)"
	}

	emitBrowserActionWithSnapshot(h.bctx, page, "evaluate", "")
	return fmt.Sprintf("Result: %s", val), nil
}

// ─── browser_wait ───────────────────────────────────────────────────

type browserWaitHandler struct{ bctx *BrowserSessionContext }

func (h *browserWaitHandler) Name() string { return "browser_wait" }

func (h *browserWaitHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserWaitHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_wait",
			Description: "Wait for an element to appear/become visible.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "CSS selector",
					},
					"state": map[string]interface{}{
						"type":        "string",
						"description": "visible|hidden|attached",
						"enum":        []string{"visible", "hidden", "attached"},
					},
					"timeout_ms": map[string]interface{}{
						"type":        "integer",
						"description": "Timeout ms (default 10000)",
					},
				},
				"required": []string{"selector"},
			},
		},
	}
}

func (h *browserWaitHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	selector, _ := args["selector"].(string)
	if selector == "" {
		return "", fmt.Errorf("selector is required")
	}

	state, _ := args["state"].(string)
	if state == "" {
		state = "visible"
	}

	timeoutMs := 10000
	if tm, ok := args["timeout_ms"].(float64); ok && int(tm) > 0 {
		timeoutMs = int(tm)
	}

	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	timedPage := page.Timeout(timeout)

	switch state {
	case "hidden":
		el, elErr := timedPage.Element(selector)
		if elErr != nil {
			return fmt.Sprintf("Element '%s' is hidden/not found", selector), nil
		}
		if err := el.WaitInvisible(); err != nil {
			return fmt.Sprintf("Timeout waiting for '%s' to become hidden: %s", selector, err.Error()), nil
		}
		emitBrowserActionWithSnapshot(h.bctx, page, "wait_hidden", selector)
		return fmt.Sprintf("Element '%s' is now hidden", selector), nil
	case "attached":
		if _, err := timedPage.Element(selector); err != nil {
			return fmt.Sprintf("Timeout waiting for '%s' to be attached: %s", selector, err.Error()), nil
		}
		emitBrowserActionWithSnapshot(h.bctx, page, "wait_attached", selector)
		return fmt.Sprintf("Element '%s' is attached to DOM", selector), nil
	default: // "visible"
		el, elErr := timedPage.Element(selector)
		if elErr != nil {
			return fmt.Sprintf("Timeout waiting for '%s': %s", selector, elErr.Error()), nil
		}
		if err := el.WaitVisible(); err != nil {
			return fmt.Sprintf("Element '%s' found but not visible within timeout: %s", selector, err.Error()), nil
		}
		emitBrowserActionWithSnapshot(h.bctx, page, "wait_visible", selector)
		return fmt.Sprintf("Element '%s' is visible", selector), nil
	}
}

// ─── browser_scroll ─────────────────────────────────────────────────

type browserScrollHandler struct{ bctx *BrowserSessionContext }

func (h *browserScrollHandler) Name() string { return "browser_scroll" }

func (h *browserScrollHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserScrollHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_scroll",
			Description: "Scroll page by direction/amount, or scroll element into view by index.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"direction": map[string]interface{}{
						"type":        "string",
						"description": "Scroll direction.",
						"enum":        []string{"up", "down", "left", "right"},
					},
					"amount": map[string]interface{}{
						"type":        "integer",
						"description": "Number of pixels to scroll (default: 500).",
					},
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Element index to scroll into view",
					},
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "CSS selector fallback",
					},
				},
			},
		},
	}
}

func (h *browserScrollHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	direction, _ := args["direction"].(string)
	amount := 500
	if a, ok := args["amount"].(float64); ok && int(a) > 0 {
		amount = int(a)
	}
	index, _ := args["index"].(float64)
	selector, _ := args["selector"].(string)

	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	// Scroll element into view (by index or selector)
	if index > 0 || selector != "" {
		el, elErr := h.bctx.resolveElement(page, index, selector)
		if elErr != nil {
			return fmt.Sprintf("Scroll into view failed: %s", elErr.Error()), nil
		}
		if err := el.ScrollIntoView(); err != nil {
			return fmt.Sprintf("Failed to scroll element into view: %s", err.Error()), nil
		}
		ref := selector
		if index > 0 {
			ref = fmt.Sprintf("[%d]", int(index))
		}
		emitBrowserActionWithSnapshot(h.bctx, page, "scroll_into_view", ref)
		return fmt.Sprintf("Scrolled element %s into view", ref), nil
	}

	if direction == "" {
		return "", fmt.Errorf("either direction or index/selector is required")
	}

	var scrollX, scrollY int
	switch direction {
	case "up":
		scrollY = -amount
	case "down":
		scrollY = amount
	case "left":
		scrollX = -amount
	case "right":
		scrollX = amount
	}

	_, err = page.Timeout(5 * time.Second).Eval(fmt.Sprintf(`() => window.scrollBy(%d, %d)`, scrollX, scrollY))
	if err != nil {
		return fmt.Sprintf("Failed to scroll: %s", err.Error()), nil
	}

	pos, err := page.Timeout(5 * time.Second).Eval(`() => JSON.stringify({ x: window.scrollX, y: window.scrollY, maxX: document.body.scrollWidth - window.innerWidth, maxY: document.body.scrollHeight - window.innerHeight })`)
	if err != nil {
		return fmt.Sprintf("Scrolled %s by %dpx", direction, amount), nil
	}

	emitBrowserActionWithSnapshot(h.bctx, page, "scroll", direction)
	return fmt.Sprintf("Scrolled %s by %dpx. Position: %s", direction, amount, pos.Value.Str()), nil
}

// ─── browser_select ─────────────────────────────────────────────────

type browserSelectHandler struct{ bctx *BrowserSessionContext }

func (h *browserSelectHandler) Name() string { return "browser_select" }

func (h *browserSelectHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserSelectHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_select",
			Description: "Select option(s) in a dropdown by index or CSS selector.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Element index from observe",
					},
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "CSS selector fallback",
					},
					"values": map[string]interface{}{
						"type":        "array",
						"description": "Values to select.",
						"items":       map[string]interface{}{"type": "string"},
					},
				},
				"required": []string{"values"},
			},
		},
	}
}

func (h *browserSelectHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	index, _ := args["index"].(float64)
	selector, _ := args["selector"].(string)

	if index == 0 && selector == "" {
		return "", fmt.Errorf("either index or selector is required")
	}

	valuesRaw, _ := args["values"].([]interface{})
	if len(valuesRaw) == 0 {
		return "", fmt.Errorf("values array is required")
	}

	var values []string
	for _, v := range valuesRaw {
		if s, ok := v.(string); ok {
			values = append(values, s)
		}
	}

	page, err := h.bctx.ensurePage(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	el, err := h.bctx.resolveElement(page, index, selector)
	if err != nil {
		return fmt.Sprintf("Select failed: %s", err.Error()), nil
	}

	if err := el.Select(values, true, rod.SelectorTypeCSSSector); err != nil {
		return fmt.Sprintf("Failed to select values: %s", err.Error()), nil
	}

	ref := selector
	if index > 0 {
		ref = fmt.Sprintf("[%d]", int(index))
	}
	emitBrowserActionWithSnapshot(h.bctx, page, "select", ref)
	return fmt.Sprintf("Selected %v in %s", values, ref), nil
}

// ─── browser_tabs ───────────────────────────────────────────────────

type browserTabsHandler struct{ bctx *BrowserSessionContext }

func (h *browserTabsHandler) Name() string { return "browser_tabs" }

func (h *browserTabsHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	h.bctx.Emitter = emitter
}

func (h *browserTabsHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "browser_tabs",
			Description: "Manage browser tabs: list, new, switch, close.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"description": "Tab action",
						"enum":        []string{"list", "new", "switch", "close"},
					},
					"tab_id": map[string]interface{}{
						"type":        "string",
						"description": "Tab target ID",
					},
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL for new tab",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

func (h *browserTabsHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)

	browser, err := h.bctx.ensureBrowser(ctx)
	if err != nil {
		return fmt.Sprintf("Failed to initialize browser: %s", err.Error()), nil
	}

	switch action {
	case "list":
		pages, err := browser.Pages()
		if err != nil {
			return fmt.Sprintf("Failed to list tabs: %s", err.Error()), nil
		}
		h.bctx.mu.Lock()
		activePage := h.bctx.page
		h.bctx.mu.Unlock()
		var lines []string
		for _, p := range pages {
			info, _ := p.Info()
			title := ""
			url := ""
			if info != nil {
				title = info.Title
				url = info.URL
			}
			active := ""
			if activePage != nil && p.TargetID == activePage.TargetID {
				active = " (active)"
			}
			lines = append(lines, fmt.Sprintf("- %s: %s — %s%s", p.TargetID, title, url, active))
		}
		emitBrowserAction(h.bctx, "tabs_list", "")
		if activePage != nil {
			captureAutoScreenshot(h.bctx, activePage)
		}
		return fmt.Sprintf("Open tabs (%d):\n%s", len(pages), strings.Join(lines, "\n")), nil

	case "new":
		url, _ := args["url"].(string)
		if url == "" {
			url = "about:blank"
		}
		page, err := browser.Page(proto.TargetCreateTarget{URL: url})
		if err != nil {
			return fmt.Sprintf("Failed to open new tab: %s", err.Error()), nil
		}
		h.bctx.mu.Lock()
		h.bctx.page = page
		h.bctx.pages[string(page.TargetID)] = page
		h.bctx.mu.Unlock()
		emitBrowserActionWithSnapshot(h.bctx, page, "tabs_new", string(page.TargetID))
		return fmt.Sprintf("Opened new tab %s at %s", page.TargetID, url), nil

	case "switch":
		tabID, _ := args["tab_id"].(string)
		if tabID == "" {
			return "", fmt.Errorf("tab_id is required for switch action")
		}
		h.bctx.mu.Lock()
		targetPage, ok := h.bctx.pages[tabID]
		h.bctx.mu.Unlock()
		if !ok {
			pages, pErr := browser.Pages()
			if pErr != nil {
				return fmt.Sprintf("Failed to list tabs: %s", pErr.Error()), nil
			}
			for _, p := range pages {
				if string(p.TargetID) == tabID {
					targetPage = p
					h.bctx.mu.Lock()
					h.bctx.pages[tabID] = p
					h.bctx.mu.Unlock()
					break
				}
			}
			if targetPage == nil {
				return fmt.Sprintf("Tab '%s' not found", tabID), nil
			}
		}
		if _, err := targetPage.Activate(); err != nil {
			return fmt.Sprintf("Failed to switch to tab '%s': %s", tabID, err.Error()), nil
		}
		h.bctx.mu.Lock()
		h.bctx.page = targetPage
		h.bctx.mu.Unlock()
		emitBrowserActionWithSnapshot(h.bctx, targetPage, "tabs_switch", tabID)
		return fmt.Sprintf("Switched to tab %s", tabID), nil

	case "close":
		tabID, _ := args["tab_id"].(string)
		if tabID == "" {
			return "", fmt.Errorf("tab_id is required for close action")
		}
		h.bctx.mu.Lock()
		targetPage, ok := h.bctx.pages[tabID]
		if ok {
			delete(h.bctx.pages, tabID)
			if h.bctx.page != nil && string(h.bctx.page.TargetID) == tabID {
				h.bctx.page = nil
			}
		}
		h.bctx.mu.Unlock()
		if targetPage != nil {
			_ = targetPage.Close()
		}
		emitBrowserAction(h.bctx, "tabs_close", tabID)
		h.bctx.mu.Lock()
		activePage := h.bctx.page
		h.bctx.mu.Unlock()
		if activePage != nil {
			captureAutoScreenshot(h.bctx, activePage)
		}
		return fmt.Sprintf("Closed tab %s", tabID), nil

	default:
		return "", fmt.Errorf("unknown action: %s (expected: list, new, switch, close)", action)
	}
}

// ─── Cookie consent auto-dismiss ────────────────────────────────────

func dismissCookieBanners(page *rod.Page) {
	// Brief delay for consent banners to render (they often load async).
	time.Sleep(500 * time.Millisecond)

	// Use a timeout — page.Eval can hang indefinitely on JS-heavy sites
	// if the page's JavaScript runtime is busy.
	_, _ = page.Timeout(3 * time.Second).Eval(`() => {
		function tryDismiss(doc) {
			const selectors = [
				'#onetrust-accept-btn-handler',
				'.onetrust-close-btn-handler',
				'#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll',
				'#CybotCookiebotDialogBodyButtonAccept',
				'#didomi-notice-agree-button',
				'.didomi-popup-notice-buttons button:first-child',
				'.qc-cmp2-summary-buttons button[mode="primary"]',
				'button.fc-cta-consent',
				'button.fc-primary-button',
				'.trustarc-agree-btn',
				'#truste-consent-button',
				'[data-tracking-opt-in-accept]',
				'div[class*="consent"] button[class*="accept"]',
				'button[id*="accept"]',
				'button[id*="consent"]',
				'button[class*="accept"]',
				'button[class*="consent"]',
				'a[id*="accept"]',
				'a[class*="accept-cookies"]',
			];
			for (const sel of selectors) {
				try {
					const el = doc.querySelector(sel);
					if (el) {
						const r = el.getBoundingClientRect();
						if (r.width > 0 && r.height > 0) {
							el.click();
							return 'clicked: ' + sel;
						}
					}
				} catch (e) {}
			}
			// Text-based fallback — match common accept/allow button text
			const acceptTexts = ['Accept all', 'Accept All', 'I Accept', 'Accept Cookies',
				'Accept all cookies', 'Allow All', 'Allow all', 'Agree', 'Got it',
				'I agree', 'Accept & Continue', 'Allow Cookies', 'Accept'];
			const buttons = doc.querySelectorAll('button, a[role="button"], [class*="btn"], [role="button"]');
			for (const btn of buttons) {
				const t = (btn.textContent || '').trim();
				if (acceptTexts.some(txt => t === txt || t.toLowerCase() === txt.toLowerCase())) {
					const r = btn.getBoundingClientRect();
					if (r.width > 0 && r.height > 0) {
						btn.click();
						return 'clicked by text: ' + t;
					}
				}
			}
			return null;
		}
		// Try main document first
		let result = tryDismiss(document);
		if (result) return result;
		// Try inside iframes (some CMPs render in iframes)
		try {
			const iframes = document.querySelectorAll('iframe');
			for (const iframe of iframes) {
				try {
					const iframeDoc = iframe.contentDocument || iframe.contentWindow?.document;
					if (iframeDoc) {
						result = tryDismiss(iframeDoc);
						if (result) return result + ' (iframe)';
					}
				} catch (e) {} // cross-origin iframes will throw
			}
		} catch (e) {}
		return 'no banner found';
	}`)
}

// ─── Helpers ────────────────────────────────────────────────────────

func intPtr(v int) *int { return &v }

// captureAutoScreenshot captures synchronously for durable workflow runs and
// asynchronously for interactive chat sessions.
func captureAutoScreenshot(bctx *BrowserSessionContext, page *rod.Page) {
	if bctx != nil && bctx.PersistSnapshots {
		emitAutoScreenshot(bctx, page)
		return
	}
	go emitAutoScreenshot(bctx, page)
}

func emitAutoScreenshot(bctx *BrowserSessionContext, page *rod.Page) {
	if bctx.Emitter == nil || page == nil {
		return
	}
	quality := 60
	data, err := page.Screenshot(false, &proto.PageCaptureScreenshot{
		Format:  proto.PageCaptureScreenshotFormatJpeg,
		Quality: &quality,
	})
	if err != nil {
		logger.WithFields("error", err.Error()).Debug("emitAutoScreenshot: failed")
		return
	}
	// Cap at 500KB to avoid bloating SSE events
	if len(data) > 500_000 {
		return
	}
	bctx.Emitter.Emit(agentrt.Event{
		Type:      agentrt.EventBrowserScreenshot,
		SessionID: bctx.SandboxCtx.SessionID,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"size_bytes":   len(data),
			"image_base64": base64.StdEncoding.EncodeToString(data),
			"auto":         true,
		},
	})
	logger.WithFields("size_bytes", len(data)).Debug("emitAutoScreenshot: emitted")
}

func emitBrowserAction(bctx *BrowserSessionContext, action, selector string) {
	if bctx.Emitter != nil {
		bctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventBrowserAction,
			SessionID: bctx.SandboxCtx.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"action":   action,
				"selector": selector,
			},
		})
	}
}

func emitBrowserActionWithSnapshot(bctx *BrowserSessionContext, page *rod.Page, action, selector string) {
	emitBrowserAction(bctx, action, selector)
	captureAutoScreenshot(bctx, page)
}
