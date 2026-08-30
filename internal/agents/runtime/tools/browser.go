package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
)

// BrowserSessionContext provides shared state for all browser tool handlers
// within a single agent session. It manages the Rod browser lifecycle and
// connects to Chromium via CDP.
//
// When Pool is configured, Chromium runs in a standalone tenant-isolated pod.
// Otherwise, Chromium runs in the sandbox's browser sidecar for local and
// Kubernetes-backed development.
type BrowserSessionContext struct {
	SandboxCtx *SandboxSessionContext
	Config     sandbox.BrowserConfig
	Emitter    *agentrt.Emitter
	// PersistSnapshots makes automatic captures synchronous so an execution
	// cannot finish before its artifact sink has retained the final frame.
	// Interactive chat sessions keep the default asynchronous behavior.
	PersistSnapshots bool
	// Pool, when non-nil, provisions Chromium from the standalone browser pool
	// instead of the sandbox's browser sidecar.
	Pool *browserpool.Pool

	mu      sync.Mutex
	browser *rod.Browser
	page    *rod.Page
	pages   map[string]*rod.Page // tabID -> page
	started bool

	// elementMap holds the most recent mapping from sequential index to CSS
	// selector, built by browser_observe (and auto-called after navigation).
	// Tools like browser_click and browser_type can reference elements by
	// index instead of requiring the LLM to guess CSS selectors.
	elementMap map[int]string // index → unique CSS selector
}

// ensureBrowser lazily connects to Chromium and retries establishment once.
// Pooled browsers retry after discarding any bad lease. Sidecar browsers only
// retry after a dead sandbox guest is detected and recovered.
//
// The connect path (establishBrowser) can fail because the sandbox VM
// vanished mid-session — the symptom we hit was "sidecar not ready: ...
// vsock.sock: no such file". Unlike exec/file/port tools, the browser
// tool used to surface that raw. Now, on a dead-guest signature, it
// revives the sandbox (the same recoverSandbox path exec uses) and retries
// once. The orchestrator's RecoveryChecker also revives the row in
// parallel; either way the second attempt finds a live VM.
func (b *BrowserSessionContext) ensureBrowser(ctx context.Context) (*rod.Browser, error) {
	br, err := b.establishBrowser(ctx)
	if b.Pool != nil {
		if err == nil {
			return br, nil
		}
		logger.WithFields("session_id", b.SandboxCtx.SessionID, "error", err.Error()).
			Warn("browser: pooled browser unavailable; releasing and retrying once")
		b.resetBrowser()
		if releaseErr := b.Pool.Release(ctx, b.SandboxCtx.SessionID); releaseErr != nil {
			logger.WithFields("session_id", b.SandboxCtx.SessionID, "error", releaseErr.Error()).
				Warn("browser: failed to release pooled browser before retry")
		}
		return b.establishBrowser(ctx)
	}
	if err == nil || !isDeadGuestToolError(err) {
		return br, err
	}
	logger.WithFields("session_id", b.SandboxCtx.SessionID, "error", err.Error()).
		Warn("browser: sandbox guest unreachable; reviving and retrying once")
	if _, recErr := recoverSandbox(ctx, b.SandboxCtx); recErr != nil {
		return nil, fmt.Errorf("%w (sandbox recovery also failed: %v)", err, recErr)
	}
	// Drop any stale connection captured before the VM died so the retry
	// rebuilds cleanly against the revived guest.
	b.resetBrowser()
	return b.establishBrowser(ctx)
}

// establishBrowser is the single-attempt connect path. It obtains a ready CDP
// endpoint from the standalone pool or exposes the sandbox sidecar ports, then
// connects Rod.
func (b *BrowserSessionContext) establishBrowser(ctx context.Context) (*rod.Browser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.browser != nil {
		return b.browser, nil
	}

	var cdpBaseURL, sandboxID string
	streamAvailable := false
	if b.Pool != nil {
		if b.Emitter != nil {
			b.Emitter.Emit(agentrt.Event{
				Type:      agentrt.EventBrowserStart,
				SessionID: b.SandboxCtx.SessionID,
				Timestamp: time.Now(),
				SandboxID: "",
			})
		}

		lease, err := b.Pool.EnsureBrowser(ctx, b.SandboxCtx.SessionID, b.SandboxCtx.TenantID, b.Config)
		if err != nil {
			b.emitError("failed to provision pooled browser: " + err.Error())
			return nil, fmt.Errorf("browser: failed to provision pooled browser: %w", err)
		}
		cdpBaseURL = lease.CDPBaseURL
	} else {
		// Keep the sandbox sidecar path for local and Kubernetes-backed
		// development when the standalone browser pool is disabled.
		// 1. Ensure the sandbox pod is running (sidecar starts with it)
		inst, err := ensureSandbox(ctx, b.SandboxCtx)
		if err != nil {
			return nil, fmt.Errorf("browser: sandbox not available: %w", err)
		}
		sandboxID = inst.ID

		if b.Emitter != nil {
			b.Emitter.Emit(agentrt.Event{
				Type:      agentrt.EventBrowserStart,
				SessionID: b.SandboxCtx.SessionID,
				Timestamp: time.Now(),
				SandboxID: inst.ID,
			})
		}

		// 2. Wait for Chromium inside the sidecar to start listening before
		//    exposing the port. K8s port-forward (SPDY) dies permanently if
		//    the first connection hits a closed port, so we must confirm
		//    readiness from inside the pod first. The sandbox container shares
		//    the pod's network namespace, so localhost:9222 reaches the sidecar.
		if err := b.waitForSidecarReady(ctx, 30*time.Second); err != nil {
			b.emitError("Chromium sidecar not ready: " + err.Error())
			return nil, fmt.Errorf("browser: sidecar not ready: %w", err)
		}

		mapping, err := b.SandboxCtx.Manager.ExposePort(ctx, b.SandboxCtx.SessionID, b.Config.CDPPort, "tcp")
		if err != nil {
			b.emitError("failed to expose CDP port: " + err.Error())
			return nil, fmt.Errorf("browser: failed to expose CDP port %d: %w", b.Config.CDPPort, err)
		}

		cdpBaseURL = fmt.Sprintf("http://%s", mapping.BackendTarget)
		// Quick sanity check: sidecar is already confirmed listening, so this
		// should succeed almost immediately through the port-forward.
		if err := waitForCDP(ctx, cdpBaseURL, 10*time.Second); err != nil {
			b.emitError("CDP not reachable through port-forward: " + err.Error())
			return nil, fmt.Errorf("browser: CDP not reachable at %s: %w", cdpBaseURL, err)
		}

		// The same-origin stream endpoint exposes the sidecar lazily after
		// authenticating the viewer. Never send an internal backend target to
		// the browser or retain it in an execution event.
		streamAvailable = !b.Config.Headless
	}

	// 4. Resolve the CDP websocket URL and connect Rod
	wsURL, err := launcher.ResolveURL(cdpBaseURL)
	if err != nil {
		b.emitError("failed to resolve CDP URL: " + err.Error())
		return nil, fmt.Errorf("browser: failed to resolve CDP URL at %s: %w", cdpBaseURL, err)
	}

	browser := rod.New().ControlURL(wsURL)
	if err := browser.Connect(); err != nil {
		b.emitError("failed to connect to browser: " + err.Error())
		return nil, fmt.Errorf("browser: failed to connect via CDP: %w", err)
	}

	b.browser = browser
	b.pages = make(map[string]*rod.Page)
	b.started = true

	if b.Emitter != nil {
		readyData := map[string]interface{}{
			"headless":         b.Config.Headless,
			"stream_available": streamAvailable,
		}
		b.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventBrowserReady,
			SessionID: b.SandboxCtx.SessionID,
			Timestamp: time.Now(),
			SandboxID: sandboxID,
			Data:      readyData,
		})
	}

	return b.browser, nil
}

// ensurePage returns the current active page, creating one if needed.
// It prefers reusing the existing first page rather than creating a new tab,
// because the browser-streamer sidecar attaches its CDP screencast to the
// first page target. Creating a new target would leave the sidecar streaming
// a stale/empty page.
func (b *BrowserSessionContext) ensurePage(ctx context.Context) (*rod.Page, error) {
	if _, err := b.ensureBrowser(ctx); err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.page != nil {
		return b.page, nil
	}

	// Try to reuse the existing first page (the one the sidecar screencaster is attached to).
	pages, err := b.browser.Pages()
	if err == nil && len(pages) > 0 {
		b.page = pages[0]
		b.pages[string(pages[0].TargetID)] = pages[0]
		return b.page, nil
	}

	// Fallback: create a new page if none exist.
	page, err := b.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("browser: failed to create page: %w", err)
	}
	b.page = page
	b.pages[string(page.TargetID)] = page
	return page, nil
}

// resetBrowser drops the cached Rod connection without emitting a close
// event. Used by ensureBrowser before a retry so establishBrowser creates a
// clean connection to the new endpoint. Safe to call when nothing is connected.
func (b *BrowserSessionContext) resetBrowser() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.browser != nil {
		_ = b.browser.Close()
	}
	b.browser = nil
	b.page = nil
	b.pages = nil
	b.started = false
}

// Close tears down the browser connection and cleans up.
func (b *BrowserSessionContext) Close(ctx context.Context) {
	b.mu.Lock()
	if b.browser != nil {
		_ = b.browser.Close()
		b.browser = nil
		b.page = nil
		b.pages = nil
		b.started = false

		if b.Emitter != nil {
			b.Emitter.Emit(agentrt.Event{
				Type:      agentrt.EventBrowserClose,
				SessionID: b.SandboxCtx.SessionID,
				Timestamp: time.Now(),
			})
		}
	}
	pool := b.Pool
	sessionID := b.SandboxCtx.SessionID
	b.mu.Unlock()

	if pool != nil {
		// Release performs CDP and Kubernetes I/O, so it must run without the
		// browser state lock held.
		if err := pool.Release(ctx, sessionID); err != nil {
			logger.WithFields("session_id", sessionID, "error", err.Error()).
				Warn("browser: failed to release pooled browser")
		}
	}
}

// observePage runs a JS snippet that scans the DOM for interactive elements,
// assigns each a sequential index via data-mf-idx, and returns a structured
// representation the LLM can use to reference elements by number.
// Returns (observeOutput, error).
func (b *BrowserSessionContext) observePage(page *rod.Page) (string, error) {
	start := time.Now()
	// Timeout the JS eval — page.Eval can hang on JS-heavy sites where the
	// runtime is busy (e.g., Qatar Airways, heavy SPA frameworks).
	result, err := page.Timeout(10 * time.Second).Eval(observePageJS)
	evalDur := time.Since(start)
	if err != nil {
		logger.WithFields("error", err.Error(), "eval_ms", evalDur.Milliseconds()).
			Warn("browser_observe: page.Eval failed")
		return "", fmt.Errorf("observe page failed: %w", err)
	}
	logger.WithFields("eval_ms", evalDur.Milliseconds()).
		Info("browser_observe: JS eval completed")

	// Rod's gson.JSON has a gotcha: calling .Str() or .Val() parses the raw
	// bytes, after which .Unmarshal() fails ("value has been parsed"). We must
	// choose ONE parse path.
	var obs observeResult
	var parseErr error
	var parsePath string

	// Path 1: Unmarshal directly on raw bytes
	parseErr = result.Value.Unmarshal(&obs)
	if parseErr == nil {
		parsePath = "direct_unmarshal"
	}

	// Path 1b: raw bytes might be a JSON string — unwrap first
	if parseErr != nil {
		var jsonStr string
		if strErr := result.Value.Unmarshal(&jsonStr); strErr == nil && jsonStr != "" {
			parseErr = json.Unmarshal([]byte(jsonStr), &obs)
			if parseErr == nil {
				parsePath = "string_unwrap"
			}
		}
	}

	// Path 2: Re-marshal the parsed Go value back to JSON, then unmarshal
	if parseErr != nil {
		if jsonBytes, marshalErr := result.Value.MarshalJSON(); marshalErr == nil {
			var s string
			if json.Unmarshal(jsonBytes, &s) == nil && s != "" {
				parseErr = json.Unmarshal([]byte(s), &obs)
				if parseErr == nil {
					parsePath = "remarshal_string_unwrap"
				}
			} else {
				parseErr = json.Unmarshal(jsonBytes, &obs)
				if parseErr == nil {
					parsePath = "remarshal_direct"
				}
			}
		}
	}

	if parseErr != nil {
		logger.WithFields("error", parseErr.Error()).
			Warn("browser_observe: all parse paths failed")
		b.mu.Lock()
		b.elementMap = make(map[int]string)
		b.mu.Unlock()
		info, _ := page.Info()
		prefix := ""
		if info != nil {
			prefix = fmt.Sprintf("URL: %s\nTitle: %s\n", info.URL, info.Title)
		}
		raw := result.Value.Str()
		if len(raw) > browserMaxTextLen {
			raw = raw[:browserMaxTextLen] + "\n...(truncated)"
		}
		return fmt.Sprintf("%s(observe parse error: %s)\n%s", prefix, parseErr, raw), nil
	}

	logger.WithFields(
		"parse_path", parsePath,
		"elements_found", len(obs.Elements),
		"text_len", len(obs.Text),
		"total_ms", time.Since(start).Milliseconds(),
	).Info("browser_observe: parsed successfully")

	// Populate the element index map
	newMap := make(map[int]string, len(obs.Elements))
	for _, el := range obs.Elements {
		newMap[el.Idx] = el.Selector
	}
	b.mu.Lock()
	b.elementMap = newMap
	b.mu.Unlock()

	// Build COMPACT output — every token here accumulates in the LLM context
	// across iterations and causes rate limit hits (50K tokens/min on Haiku).
	// Target: <1500 chars total.
	var sb strings.Builder
	info, _ := page.Info()
	if info != nil {
		sb.WriteString(fmt.Sprintf("URL: %s\nTitle: %s\n", info.URL, info.Title))
	}

	// Show max 25 elements — the full index map is still stored for lookups.
	// Prioritize form elements (inputs, buttons, selects) over links.
	maxShown := 25
	sb.WriteString(fmt.Sprintf("Elements (%d total, showing %d):\n", len(obs.Elements), min(len(obs.Elements), maxShown)))
	shown := 0
	for _, el := range obs.Elements {
		if shown >= maxShown {
			break
		}
		// Truncate long descriptions
		desc := el.Description
		if len(desc) > 60 {
			desc = desc[:60] + "…"
		}
		sb.WriteString(fmt.Sprintf("[%d] %s\n", el.Idx, desc))
		shown++
	}
	if len(obs.Elements) > maxShown {
		sb.WriteString(fmt.Sprintf("…+%d more\n", len(obs.Elements)-maxShown))
	}
	return sb.String(), nil
}

type observeResult struct {
	Elements []observeElement `json:"elements"`
	Text     string           `json:"text"`
}
type observeElement struct {
	Idx         int    `json:"idx"`
	Selector    string `json:"selector"`
	Description string `json:"desc"`
}

// resolveElement returns a Rod element either by index (from the last observe)
// or by CSS selector. Index takes priority.
//
// For index-based lookups, if the stored selector is stale (element not found
// within 3s), we automatically re-observe the page to get fresh selectors and
// retry once. This handles SPA re-renders that invalidate structural CSS paths.
func (b *BrowserSessionContext) resolveElement(page *rod.Page, index float64, selector string) (*rod.Element, error) {
	if index > 0 {
		idx := int(index)
		b.mu.Lock()
		sel, ok := b.elementMap[idx]
		b.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("element index %d not found — call browser_observe first to index page elements", idx)
		}
		// First attempt with short timeout
		el, err := page.Timeout(browserElementTimeout).Element(sel)
		if err == nil {
			return el, nil
		}
		// Selector is stale — auto-re-observe and retry once
		logger.WithFields("index", idx, "stale_selector", sel).
			Info("browser: selector stale, auto-re-observing page")
		b.observePage(page)
		b.mu.Lock()
		newSel, ok := b.elementMap[idx]
		b.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("element [%d] no longer exists after re-observing page — the page layout changed", idx)
		}
		el, err = page.Timeout(browserElementTimeout).Element(newSel)
		if err != nil {
			return nil, fmt.Errorf("element [%d] not found even after re-observe (selector: %s)", idx, newSel)
		}
		logger.WithFields("index", idx, "old_selector", sel, "new_selector", newSel).
			Info("browser: resolved element after re-observe")
		return el, nil
	}
	if selector == "" {
		return nil, fmt.Errorf("either index or selector is required")
	}
	el, err := page.Timeout(browserElementTimeout).Element(selector)
	if err != nil {
		return nil, fmt.Errorf("element not found for selector '%s' (waited %s): %s", selector, browserElementTimeout, err.Error())
	}
	return el, nil
}

func (b *BrowserSessionContext) emitError(msg string) {
	if b.Emitter != nil {
		b.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventBrowserError,
			SessionID: b.SandboxCtx.SessionID,
			Timestamp: time.Now(),
			Error:     msg,
		})
	}
}

// BrowserToolNames returns the list of all browser tool names for registration.
func BrowserToolNames() []string { return browserToolNames }

var browserToolNames = []string{
	"browser_navigate",
	"browser_observe",
	"browser_click",
	"browser_type",
	"browser_screenshot",
	"browser_evaluate",
	"browser_wait",
	"browser_scroll",
	"browser_select",
	"browser_tabs",
}

// NewBrowserHandlers creates all browser synthetic tool handlers, each wrapped
// in a tracing decorator so every action emits a BROWSER span (M1-T4).
func NewBrowserHandlers(bctx *BrowserSessionContext) []SyntheticToolHandler {
	inner := []SyntheticToolHandler{
		&browserNavigateHandler{bctx: bctx},
		&browserObserveHandler{bctx: bctx},
		&browserClickHandler{bctx: bctx},
		&browserTypeHandler{bctx: bctx},
		&browserScreenshotHandler{bctx: bctx},
		&browserEvaluateHandler{bctx: bctx},
		&browserWaitHandler{bctx: bctx},
		&browserScrollHandler{bctx: bctx},
		&browserSelectHandler{bctx: bctx},
		&browserTabsHandler{bctx: bctx},
	}
	out := make([]SyntheticToolHandler, len(inner))
	for i, h := range inner {
		out[i] = &tracedBrowserHandler{SyntheticToolHandler: h, bctx: bctx}
	}
	return out
}

// tracedBrowserHandler wraps a browser tool handler and emits a BROWSER span
// around Execute. It embeds the inner handler so Name/Definition pass through,
// and forwards wireBrowserEmitter so WireBrowserEmitter still finds it.
type tracedBrowserHandler struct {
	SyntheticToolHandler
	bctx *BrowserSessionContext
}

func (t *tracedBrowserHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action := strings.TrimPrefix(t.Name(), "browser_")
	ctx, span := telemetry.StartBrowserSpan(ctx, action)
	defer span.End()
	if u, ok := args["url"].(string); ok && u != "" {
		span.SetAttributes(attribute.String(attrs.BrowserURL, u))
	}
	if sel, ok := args["selector"].(string); ok && sel != "" {
		span.SetAttributes(attribute.String(attrs.BrowserSelector, sel))
	}

	res, err := t.SyntheticToolHandler.Execute(ctx, args)
	actionErr := err
	if actionErr == nil && browserResultLooksFailed(res) {
		actionErr = fmt.Errorf("%s", truncateBrowserError(res, 500))
	}
	if actionErr != nil {
		telemetry.RecordError(span, actionErr)
		if t.bctx != nil {
			t.bctx.emitError(actionErr.Error())
		}
	}
	return res, err
}

func browserResultLooksFailed(result string) bool {
	normalized := strings.ToLower(strings.TrimSpace(result))
	return strings.HasPrefix(normalized, "failed") ||
		strings.HasPrefix(normalized, "click failed") ||
		strings.HasPrefix(normalized, "type failed") ||
		strings.HasPrefix(normalized, "select failed") ||
		strings.HasPrefix(normalized, "timeout waiting") ||
		strings.HasPrefix(normalized, "javascript evaluation failed") ||
		strings.Contains(normalized, " but could not save:")
}

func truncateBrowserError(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

// wireBrowserEmitter forwards emitter wiring to the inner handler so the UI
// event stream keeps working through the decorator.
func (t *tracedBrowserHandler) wireBrowserEmitter(emitter *agentrt.Emitter) {
	if w, ok := t.SyntheticToolHandler.(browserEmitterWirer); ok {
		w.wireBrowserEmitter(emitter)
	}
}

// WireBrowserEmitter sets the emitter on the browser context via the interceptor.
func WireBrowserEmitter(interceptor *ToolInterceptor, emitter *agentrt.Emitter) {
	for _, handler := range interceptor.Handlers {
		if bw, ok := handler.(browserEmitterWirer); ok {
			bw.wireBrowserEmitter(emitter)
			return // All browser handlers share the same context
		}
	}
}

// browserEmitterWirer is an internal interface for wiring the emitter.
type browserEmitterWirer interface {
	wireBrowserEmitter(emitter *agentrt.Emitter)
}

// waitForSidecarReady polls from inside the sandbox container (which shares
// the pod's network namespace) until Chromium's CDP port is accepting
// connections. This must complete before we create the K8s port-forward,
// because SPDY port-forwards die permanently on the first failed connection.
func (b *BrowserSessionContext) waitForSidecarReady(ctx context.Context, timeout time.Duration) error {
	// Use a shell loop with wget (available in the base image) to probe
	// the CDP /json/version endpoint. The sandbox and browser sidecar
	// share localhost in the same pod.
	probeCmd := fmt.Sprintf(
		"for i in $(seq 1 %d); do wget -q -O /dev/null http://127.0.0.1:%d/json/version 2>/dev/null && exit 0; sleep 0.5; done; exit 1",
		int(timeout.Seconds()*2), // iterations at 0.5s each
		b.Config.CDPPort,
	)
	result, err := b.SandboxCtx.Manager.Exec(ctx, b.SandboxCtx.SessionID, sandbox.ExecRequest{
		Command: []string{"sh", "-c", probeCmd},
		Timeout: timeout + 5*time.Second, // give exec a bit more than the probe loop
	})
	if err != nil {
		return fmt.Errorf("exec probe failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("Chromium not listening on port %d after %s", b.Config.CDPPort, timeout)
	}
	return nil
}

// waitForCDP polls the Chromium CDP endpoint until it responds or the timeout
// expires. The browser sidecar needs a few seconds to launch Chromium and
// bind to the debugging port.
func waitForCDP(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	versionURL := baseURL + "/json/version"
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(versionURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("CDP endpoint %s did not respond within %s", versionURL, timeout)
}

// observePageJS is injected into the page to extract interactive elements.
// It assigns each a sequential index via data-mf-idx and returns a JSON
// object with element descriptions and condensed page text.
const observePageJS = `() => {
	// Remove stale markers from previous observations
	document.querySelectorAll('[data-mf-idx]').forEach(el => el.removeAttribute('data-mf-idx'));

	const interactiveSels = [
		'a[href]', 'button', 'input', 'textarea', 'select',
		'[role="button"]', '[role="link"]', '[role="tab"]', '[role="menuitem"]',
		'[role="checkbox"]', '[role="radio"]', '[role="switch"]',
		'[role="combobox"]', '[role="listbox"]', '[role="option"]',
		'[onclick]', '[contenteditable="true"]',
		'details > summary',
	];
	const allInteractive = document.querySelectorAll(interactiveSels.join(','));

	function isVisible(el) {
		try {
			const s = getComputedStyle(el);
			if (s.display === 'none' || s.visibility === 'hidden') return false;
			// Check bounding rect — more reliable than offsetParent which returns
			// null inside fixed/sticky ancestors and in various edge cases.
			const r = el.getBoundingClientRect();
			if (r.width <= 0 || r.height <= 0) return false;
			// Allow elements anywhere on the page (not just viewport) — they may
			// become visible after scrolling. Only filter truly hidden elements.
			return true;
		} catch (e) {
			return false;
		}
	}

	// Build a unique CSS selector for an element.
	// IMPORTANT: Never use data-mf-idx as the selector — SPA frameworks (React,
	// Angular, Vue) re-render the DOM and strip unknown attributes, making
	// data-mf-idx selectors stale. Always use structural selectors that survive
	// framework re-renders.
	function uniqueSelector(el) {
		// 1. Stable id — best case
		if (el.id) return '#' + CSS.escape(el.id);
		// 2. Stable data-* or aria-* attributes (set by the site, not us)
		for (const attr of ['data-testid', 'data-test', 'data-cy', 'data-automation-id', 'name']) {
			const v = el.getAttribute(attr);
			if (v) return el.tagName.toLowerCase() + '[' + attr + '="' + CSS.escape(v) + '"]';
		}
		// 3. Unique aria-label on the element
		const ariaLabel = el.getAttribute('aria-label');
		if (ariaLabel && document.querySelectorAll(el.tagName.toLowerCase() + '[aria-label="' + CSS.escape(ariaLabel) + '"]').length === 1) {
			return el.tagName.toLowerCase() + '[aria-label="' + CSS.escape(ariaLabel) + '"]';
		}
		// 4. Structural path-based selector (survives framework re-renders)
		const parts = [];
		let cur = el;
		while (cur && cur !== document.body && parts.length < 6) {
			let sel = cur.tagName.toLowerCase();
			if (cur.id) { parts.unshift('#' + CSS.escape(cur.id)); break; }
			const parent = cur.parentElement;
			if (parent) {
				const siblings = Array.from(parent.children).filter(c => c.tagName === cur.tagName);
				if (siblings.length > 1) {
					sel += ':nth-of-type(' + (siblings.indexOf(cur) + 1) + ')';
				}
			}
			parts.unshift(sel);
			cur = parent;
		}
		return parts.join(' > ');
	}

	function describeElement(el) {
		const tag = el.tagName.toLowerCase();
		const role = el.getAttribute('role') || '';
		const type = el.getAttribute('type') || '';
		const label = (el.getAttribute('aria-label') || el.getAttribute('title') || '').slice(0, 30);
		const placeholder = (el.getAttribute('placeholder') || '').slice(0, 25);
		let text = (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 30);

		if (tag === 'a') return '<a> "' + (text || label || '') + '"';
		if (tag === 'button' || role === 'button') return '<button> "' + (text || label || '') + '"';
		if (tag === 'input') {
			let d = '<input';
			if (type && type !== 'text') d += ' type=' + type;
			d += '>';
			if (placeholder) d += ' "' + placeholder + '"';
			else if (label) d += ' "' + label + '"';
			const val = el.value || '';
			if (val) d += ' val="' + val.slice(0, 20) + '"';
			return d;
		}
		if (tag === 'textarea') return '<textarea>' + (placeholder ? ' "'+placeholder+'"' : label ? ' "'+label+'"' : '');
		if (tag === 'select') {
			const sel = el.options[el.selectedIndex];
			return '<select>' + (label ? ' "'+label+'"' : '') + (sel ? ' ="'+sel.text.slice(0,20)+'"' : '');
		}
		return '<' + tag + (role ? ' role='+role : '') + '> "' + (text || label || '') + '"';
	}

	const elements = [];
	let idx = 1;
	for (const el of allInteractive) {
		if (!isVisible(el)) continue;
		// Skip tiny or hidden elements
		const r = el.getBoundingClientRect();
		if (r.width < 5 || r.height < 5) continue;

		el.setAttribute('data-mf-idx', String(idx));
		const selector = uniqueSelector(el);
		elements.push({
			idx: idx,
			selector: selector,
			desc: describeElement(el),
		});
		idx++;
		if (idx > 200) break; // cap to prevent token explosion
	}

	return JSON.stringify({ elements: elements, text: '' });
}`
