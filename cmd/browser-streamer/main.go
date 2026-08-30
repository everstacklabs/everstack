// browser-streamer runs inside the browser sidecar container alongside Chromium.
// It captures CDP screencast frames and serves them over a WebSocket, while
// relaying mouse/keyboard input from the client back to Chromium via CDP.
//
// Architecture:
//
//	Client (admin UI) ←WebSocket→ Go backend (relay) ←WebSocket→ browser-streamer ←CDP→ Chromium
//
// Environment variables:
//
//	STREAMER_PORT       - WebSocket listen port (default: 6080)
//	STREAMER_CDP_URL    - CDP endpoint (default: http://127.0.0.1:9222)
//	STREAMER_QUALITY    - JPEG quality 1-100 (default: 80)
//	STREAMER_MAX_WIDTH  - Max capture width (default: 1280)
//	STREAMER_MAX_HEIGHT - Max capture height (default: 720)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type config struct {
	Port      int
	CDPURL    string
	Quality   int
	MaxWidth  int
	MaxHeight int
}

func loadConfig() config {
	cfg := config{
		Port:      6080,
		CDPURL:    "http://127.0.0.1:9222",
		Quality:   80,
		MaxWidth:  1280,
		MaxHeight: 720,
	}
	if v := os.Getenv("STREAMER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Port = n
		}
	}
	if v := os.Getenv("STREAMER_CDP_URL"); v != "" {
		cfg.CDPURL = v
	}
	if v := os.Getenv("STREAMER_QUALITY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			cfg.Quality = n
		}
	}
	if v := os.Getenv("STREAMER_MAX_WIDTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxWidth = n
		}
	}
	if v := os.Getenv("STREAMER_MAX_HEIGHT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxHeight = n
		}
	}
	return cfg
}

// inputEvent is a JSON message from the client for mouse/keyboard input.
type inputEvent struct {
	Type string `json:"type"` // "mouse", "keyboard", "scroll"

	// Mouse fields
	X          float64 `json:"x,omitempty"`
	Y          float64 `json:"y,omitempty"`
	Button     string  `json:"button,omitempty"` // "left", "right", "middle"
	Action     string  `json:"action,omitempty"` // "move", "down", "up", "click"
	ClickCount int     `json:"clickCount,omitempty"`

	// Keyboard fields
	Key     string `json:"key,omitempty"`
	Code    string `json:"code,omitempty"`
	Text    string `json:"text,omitempty"`
	KeyDown bool   `json:"keyDown,omitempty"`

	// Scroll fields
	DeltaX float64 `json:"deltaX,omitempty"`
	DeltaY float64 `json:"deltaY,omitempty"`

	// Modifier keys
	Modifiers int `json:"modifiers,omitempty"` // bitmask: Alt=1, Ctrl=2, Meta=4, Shift=8
}

// streamer manages the CDP screencast and broadcasts frames to connected clients.
type streamer struct {
	cfg     config
	browser *rod.Browser
	page    *rod.Page

	mu             sync.RWMutex
	clients        map[*websocket.Conn]context.CancelFunc
	lastFrame      []byte // most recent JPEG frame — sent to new clients on connect
	snapshotErrors int    // consecutive snapshot failures (for log throttling)
}

func newStreamer(cfg config) *streamer {
	return &streamer{
		cfg:     cfg,
		clients: make(map[*websocket.Conn]context.CancelFunc),
	}
}

func (s *streamer) addClient(conn *websocket.Conn, cancel context.CancelFunc) {
	s.mu.Lock()
	s.clients[conn] = cancel
	s.mu.Unlock()
}

func (s *streamer) removeClient(conn *websocket.Conn) {
	s.mu.Lock()
	delete(s.clients, conn)
	s.mu.Unlock()
}

func (s *streamer) broadcastFrame(data []byte) {
	s.mu.Lock()
	// Store a copy of the latest frame so new clients get an immediate snapshot.
	s.lastFrame = make([]byte, len(data))
	copy(s.lastFrame, data)
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	for conn, cancel := range s.clients {
		// Non-blocking write with a short deadline — drop slow clients
		wCtx, wCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		err := conn.Write(wCtx, websocket.MessageBinary, data)
		wCancel()
		if err != nil {
			// Client can't keep up or disconnected — tear it down
			cancel()
		}
	}
}

// connectBrowser waits for Chromium to be available, connects via CDP, and
// returns the first page.
func (s *streamer) connectBrowser(ctx context.Context) error {
	log.Printf("Waiting for Chromium at %s...", s.cfg.CDPURL)

	// Poll until CDP is ready
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := http.Get(s.cfg.CDPURL + "/json/version")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	wsURL, err := launcher.ResolveURL(s.cfg.CDPURL)
	if err != nil {
		return fmt.Errorf("resolve CDP URL: %w", err)
	}

	browser := rod.New().ControlURL(wsURL)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("connect to browser: %w", err)
	}

	s.browser = browser

	// Get or create the first page
	pages, err := browser.Pages()
	if err != nil || len(pages) == 0 {
		page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			return fmt.Errorf("create page: %w", err)
		}
		s.page = page
	} else {
		s.page = pages[0]
	}

	log.Println("Connected to Chromium via CDP")
	return nil
}

// startScreencast begins the CDP Page.startScreencast and broadcasts frames
// using a single persistent event listener (no per-frame EachEvent).
func (s *streamer) startScreencast(ctx context.Context) error {
	quality := s.cfg.Quality
	maxWidth := s.cfg.MaxWidth
	maxHeight := s.cfg.MaxHeight
	everyNth := 1
	err := proto.PageStartScreencast{
		Format:        proto.PageStartScreencastFormatJpeg,
		Quality:       &quality,
		MaxWidth:      &maxWidth,
		MaxHeight:     &maxHeight,
		EveryNthFrame: &everyNth,
	}.Call(s.page)
	if err != nil {
		return fmt.Errorf("start screencast: %w", err)
	}

	log.Printf("Screencast started (quality=%d, max=%dx%d)", s.cfg.Quality, s.cfg.MaxWidth, s.cfg.MaxHeight)

	// Inject a hidden CSS animation to force Chromium to continuously repaint.
	// CDP screencast only fires frames on visual changes — without this, static
	// pages produce 0 fps. The animation is invisible (1px transparent element
	// with an opacity animation) but forces the compositor to generate new frames
	// at ~30fps, which the screencast picks up.
	s.injectRepaintDriver()

	// Use a single persistent channel-based listener for all frames.
	// EachEvent per frame has a gap between listener teardown and re-registration
	// that causes missed frames — this approach never misses.
	go s.listenFrames(ctx)

	// Capture an initial screenshot so there's always a lastFrame for new clients.
	go func() {
		time.Sleep(1 * time.Second)
		s.captureSnapshot()
	}()

	// Fallback: if the repaint driver gets removed (page navigation replaces DOM),
	// re-inject it and capture a snapshot. Runs every 2s — cheap check.
	go s.maintainRepaintDriver(ctx)

	// Guaranteed frame delivery: capture a screenshot every 33ms (~30fps).
	// The screencast + repaint driver should also deliver ~30fps, but if the
	// screencast listener fails silently (e.g., EachEvent breaks after page
	// navigation), this ensures clients always see smooth frames.
	go func() {
		ticker := time.NewTicker(33 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.RLock()
				hasClients := len(s.clients) > 0
				s.mu.RUnlock()
				if hasClients {
					s.captureSnapshot()
				}
			}
		}
	}()

	return nil
}

// injectRepaintDriver adds a tiny invisible animated element to the page that
// forces Chromium to continuously repaint, keeping CDP screencast at ~30fps.
func (s *streamer) injectRepaintDriver() {
	_, err := s.page.Eval(`() => {
		if (document.getElementById('__screencast_repaint_driver')) return;
		const el = document.createElement('div');
		el.id = '__screencast_repaint_driver';
		el.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;pointer-events:none;z-index:2147483647;opacity:0;';
		el.innerHTML = '<style>@keyframes __sr{0%{opacity:0.001}50%{opacity:0.002}100%{opacity:0.001}}#__screencast_repaint_driver{animation:__sr 33ms infinite}</style>';
		document.documentElement.appendChild(el);
	}`)
	if err != nil {
		log.Printf("injectRepaintDriver: failed: %v", err)
	}
}

// maintainRepaintDriver periodically checks that the repaint driver element
// is still in the DOM (navigations replace it) and re-injects if missing.
func (s *streamer) maintainRepaintDriver(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if the element exists; re-inject if not
			result, err := s.page.Eval(`() => !!document.getElementById('__screencast_repaint_driver')`)
			if err != nil || !result.Value.Bool() {
				s.injectRepaintDriver()
			}
		}
	}
}

func (s *streamer) listenFrames(ctx context.Context) {
	// Subscribe to screencast frame events using the browser's CDP client.
	// Rod's page.Event() returns a channel of raw CDP events — we filter for
	// Page.screencastFrame and decode manually. This avoids the EachEvent
	// per-frame pattern which has gaps.
	pageCh := make(chan *proto.PageScreencastFrame, 16)

	go func() {
		// Use Rod's built-in event system with a long-lived listener.
		// EachEvent with a func that never returns true keeps listening forever.
		s.page.EachEvent(func(e *proto.PageScreencastFrame) bool {
			select {
			case pageCh <- e:
			default:
				// Drop frame if channel is full (back-pressure)
			}
			return false // never stop — keep listening
		})
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-pageCh:
			if frame == nil {
				continue
			}
			// Broadcast raw JPEG bytes (Rod auto-decodes base64)
			s.broadcastFrame(frame.Data)

			// Acknowledge so CDP sends the next frame
			_ = proto.PageScreencastFrameAck{
				SessionID: frame.SessionID,
			}.Call(s.page)
		}
	}
}

// captureSnapshot takes a one-off screenshot, stores it as lastFrame, and
// broadcasts it to all connected clients. This ensures:
//  1. New clients get a fresh frame on connect (not a stale about:blank).
//  2. Connected clients see page updates even when the CDP screencast
//     doesn't fire (static pages with no visual changes).
func (s *streamer) captureSnapshot() {
	if s.page == nil {
		return
	}
	quality := s.cfg.Quality
	data, err := s.page.Screenshot(false, &proto.PageCaptureScreenshot{
		Format:  proto.PageCaptureScreenshotFormatJpeg,
		Quality: &quality,
	})
	if err != nil {
		// Don't log every failure at 10fps — only log occasionally
		s.snapshotErrors++
		if s.snapshotErrors == 1 || s.snapshotErrors%100 == 0 {
			log.Printf("captureSnapshot: failed (count=%d): %v", s.snapshotErrors, err)
		}
		return
	}
	s.snapshotErrors = 0
	// broadcastFrame stores the frame as lastFrame AND sends to all clients
	s.broadcastFrame(data)
}

// handleInput processes a client input event and dispatches it to Chromium via CDP.
func (s *streamer) handleInput(ev inputEvent) {
	switch ev.Type {
	case "mouse":
		s.handleMouseInput(ev)
	case "keyboard":
		s.handleKeyboardInput(ev)
	case "scroll":
		s.handleScrollInput(ev)
	}
}

func (s *streamer) handleMouseInput(ev inputEvent) {
	var dispatchType proto.InputDispatchMouseEventType
	switch ev.Action {
	case "move":
		dispatchType = proto.InputDispatchMouseEventTypeMouseMoved
	case "down":
		dispatchType = proto.InputDispatchMouseEventTypeMousePressed
	case "up":
		dispatchType = proto.InputDispatchMouseEventTypeMouseReleased
	case "click":
		// Synthesize press + release
		s.handleMouseInput(inputEvent{
			Type: "mouse", Action: "down", X: ev.X, Y: ev.Y,
			Button: ev.Button, ClickCount: 1, Modifiers: ev.Modifiers,
		})
		s.handleMouseInput(inputEvent{
			Type: "mouse", Action: "up", X: ev.X, Y: ev.Y,
			Button: ev.Button, ClickCount: 1, Modifiers: ev.Modifiers,
		})
		return
	default:
		return
	}

	button := proto.InputMouseButtonLeft
	switch ev.Button {
	case "right":
		button = proto.InputMouseButtonRight
	case "middle":
		button = proto.InputMouseButtonMiddle
	}

	clickCount := ev.ClickCount
	if clickCount == 0 && (ev.Action == "down" || ev.Action == "up") {
		clickCount = 1
	}

	_ = proto.InputDispatchMouseEvent{
		Type:       dispatchType,
		X:          ev.X,
		Y:          ev.Y,
		Button:     button,
		ClickCount: clickCount,
		Modifiers:  ev.Modifiers,
	}.Call(s.page)
}

func (s *streamer) handleKeyboardInput(ev inputEvent) {
	var dispatchType proto.InputDispatchKeyEventType
	if ev.KeyDown {
		dispatchType = proto.InputDispatchKeyEventTypeKeyDown
	} else {
		dispatchType = proto.InputDispatchKeyEventTypeKeyUp
	}

	_ = proto.InputDispatchKeyEvent{
		Type:      dispatchType,
		Key:       ev.Key,
		Code:      ev.Code,
		Text:      ev.Text,
		Modifiers: ev.Modifiers,
	}.Call(s.page)

	// For printable characters, also send a char event on keyDown
	if ev.KeyDown && ev.Text != "" {
		_ = proto.InputDispatchKeyEvent{
			Type:      proto.InputDispatchKeyEventTypeChar,
			Text:      ev.Text,
			Modifiers: ev.Modifiers,
		}.Call(s.page)
	}
}

func (s *streamer) handleScrollInput(ev inputEvent) {
	_ = proto.InputDispatchMouseEvent{
		Type:      proto.InputDispatchMouseEventTypeMouseWheel,
		X:         ev.X,
		Y:         ev.Y,
		DeltaX:    ev.DeltaX,
		DeltaY:    ev.DeltaY,
		Modifiers: ev.Modifiers,
	}.Call(s.page)
}

// handleWebSocket handles a single WebSocket connection: broadcasts frames to it
// and reads input events from it.
func (s *streamer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.CloseNow()

	// After websocket.Accept hijacks the HTTP connection, r.Context() is
	// cancelled by the HTTP server (the request is "done" from its perspective).
	// Use a fresh context so reads/writes don't fail immediately.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn.SetReadLimit(64 * 1024) // 64KB for input events

	s.addClient(conn, cancel)
	defer s.removeClient(conn)

	log.Println("Client connected")

	// Capture a FRESH screenshot for the new client. The cached lastFrame may
	// be stale (e.g., about:blank from startup) if Rod navigated the page after
	// the initial capture and the screencast didn't fire (static page, no repaints).
	s.captureSnapshot()

	s.mu.RLock()
	snapshot := s.lastFrame
	s.mu.RUnlock()
	if len(snapshot) > 0 {
		wCtx, wCancel := context.WithTimeout(ctx, 500*time.Millisecond)
		_ = conn.Write(wCtx, websocket.MessageBinary, snapshot)
		wCancel()
		log.Printf("Sent fresh frame to new client (%d bytes)", len(snapshot))
	}

	// Read input events from client
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}

		var ev inputEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}

		s.handleInput(ev)
	}

	log.Println("Client disconnected")
}

func (s *streamer) healthHandler(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	n := len(s.clients)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","clients":%d}`, n)
}

func main() {
	cfg := loadConfig()
	s := newStreamer(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	// Connect to Chromium
	if err := s.connectBrowser(ctx); err != nil {
		log.Fatalf("Failed to connect to browser: %v", err)
	}

	// Start screencast
	if err := s.startScreencast(ctx); err != nil {
		log.Fatalf("Failed to start screencast: %v", err)
	}

	// Serve WebSocket
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/health", s.healthHandler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		server.Shutdown(shutdownCtx)
	}()

	log.Printf("Browser streamer listening on %s", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
