package v1

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gorilla/mux"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// HandleBrowserStream upgrades the HTTP connection to a WebSocket and relays
// frames/input between the client and the browser-streamer sidecar.
//
// The sidecar streamer runs at <pod>:6080/ws. This handler:
//   1. Exposes port 6080 on the sandbox (creates port-forward)
//   2. Connects to the sidecar streamer via WebSocket
//   3. Relays binary frames (sidecar → client) and text input (client → sidecar)
//
// Route: GET /v1/sandbox/{session_id}/browser/stream
func (s *Server) HandleBrowserStream(w http.ResponseWriter, r *http.Request) {
	logger.WithFields(
		"path", r.URL.Path,
		"method", r.Method,
		"origin", r.Header.Get("Origin"),
		"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"),
		"sec_fetch_mode", r.Header.Get("Sec-Fetch-Mode"),
		"host", r.Host,
	).Info("browser_stream: request received")
	sessionID := mux.Vars(r)["session_id"]
	if sessionID == "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		for i, p := range parts {
			if p == "sandbox" && i+1 < len(parts) {
				sessionID = parts[i+1]
				break
			}
		}
	}
	if sessionID == "" {
		http.Error(w, `{"error":"session_id is required"}`, http.StatusBadRequest)
		return
	}

	if s.sandboxMgr == nil {
		http.Error(w, `{"error":"sandbox feature is not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	inst, ok := s.sandboxMgr.GetInstance(sessionID)
	if !ok {
		logger.WithFields("session_id", sessionID).Warn("browser_stream: no sandbox instance found for session")
		http.Error(w, `{"error":"no sandbox for this session"}`, http.StatusNotFound)
		return
	}

	// Check that browser sidecar is configured
	if inst.Config.BrowserSidecar == nil {
		http.Error(w, `{"error":"browser sidecar is not configured for this sandbox"}`, http.StatusBadRequest)
		return
	}

	// Authenticate: same-origin (admin UI) or SSH key signature (CLI)
	if err := s.authenticateShellRequest(r, inst.ID, inst.Config.TenantID); err != nil {
		logger.WithFields("session_id", sessionID, "error", err.Error()).Warn("browser_stream: auth failed")
		http.Error(w, `{"error":"unauthorized: `+err.Error()+`"}`, http.StatusUnauthorized)
		return
	}

	logger.WithFields("session_id", sessionID, "sandbox_id", inst.ID).Info("browser_stream: auth passed, opening relay")
	streamPort := inst.Config.BrowserSidecar.StreamPort
	if streamPort == 0 {
		streamPort = 6080
	}

	s.openBrowserStreamWebSocket(w, r, sessionID, streamPort)
}

// openBrowserStreamWebSocket upgrades the client connection and creates a
// bidirectional WebSocket relay to the sidecar streamer.
func (s *Server) openBrowserStreamWebSocket(w http.ResponseWriter, r *http.Request, sessionID string, streamPort int) {
	// 1. Expose the sidecar streamer port
	logger.WithFields("session_id", sessionID, "stream_port", streamPort).
		Info("browser_stream: exposing streamer port")
	mapping, err := s.sandboxMgr.ExposePort(context.Background(), sessionID, streamPort, "tcp")
	if err != nil {
		logger.WithFields("error", err.Error(), "session_id", sessionID).
			Warn("browser_stream: failed to expose streamer port")
		http.Error(w, `{"error":"failed to expose browser streamer port"}`, http.StatusInternalServerError)
		return
	}
	logger.WithFields("session_id", sessionID, "backend_target", mapping.BackendTarget, "host_port", mapping.HostPort).
		Info("browser_stream: port exposed, checking streamer health")

	// 2. Wait for the streamer to be ready.
	// Use a detached context — r.Context() can be cancelled if the client disconnects
	// during the health-check window (e.g. React StrictMode re-mount).
	streamerWSURL := fmt.Sprintf("ws://%s/ws", mapping.BackendTarget)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer waitCancel()
	if err := waitForStreamer(waitCtx, mapping.BackendTarget, 10*time.Second); err != nil {
		logger.WithFields("error", err.Error(), "url", streamerWSURL, "backend_target", mapping.BackendTarget).
			Warn("browser_stream: streamer not ready")
		http.Error(w, `{"error":"browser streamer not ready"}`, http.StatusServiceUnavailable)
		return
	}
	logger.WithFields("session_id", sessionID, "backend_target", mapping.BackendTarget).
		Info("browser_stream: streamer healthy, upgrading to WebSocket")

	// 3. Accept WebSocket from client
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("browser_stream: websocket upgrade failed")
		return
	}
	defer clientConn.CloseNow()

	// Use a fresh context (HTTP request context gets cancelled after hijack)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 4. Connect to the sidecar streamer
	sidecarConn, _, err := websocket.Dial(ctx, streamerWSURL, nil)
	if err != nil {
		logger.WithFields("error", err.Error(), "url", streamerWSURL).
			Warn("browser_stream: failed to connect to sidecar streamer")
		clientConn.Close(websocket.StatusInternalError, "failed to connect to browser streamer")
		return
	}
	defer sidecarConn.CloseNow()

	// Increase read limit for binary frame data (frames can be large)
	sidecarConn.SetReadLimit(4 * 1024 * 1024) // 4MB
	clientConn.SetReadLimit(64 * 1024)          // 64KB for input events

	logger.WithFields("session_id", sessionID, "sidecar_url", streamerWSURL).
		Info("browser_stream: relay started, sidecar connected")

	done := make(chan struct{})

	// Goroutine: sidecar → client (binary JPEG frames)
	go func() {
		defer close(done)
		defer cancel()
		var frameCount int
		var totalBytes int64
		lastLogAt := time.Now()
		for {
			msgType, data, err := sidecarConn.Read(ctx)
			if err != nil {
				if ctx.Err() == nil {
					logger.WithFields("error", err.Error(), "frames_relayed", frameCount, "total_bytes", totalBytes).
						Warn("browser_stream: sidecar read error (relay dying)")
				}
				logger.WithFields("session_id", sessionID, "frames_relayed", frameCount, "total_bytes", totalBytes).
					Info("browser_stream: relay sidecar→client ended")
				return
			}
			frameCount++
			totalBytes += int64(len(data))
			if frameCount == 1 {
				logger.WithFields("session_id", sessionID, "msg_type", int(msgType), "size", len(data)).
					Info("browser_stream: first frame received from sidecar")
			}
			// Log frame stats every 5 seconds to trace throughput
			if time.Since(lastLogAt) >= 5*time.Second {
				logger.WithFields("session_id", sessionID, "frames_relayed", frameCount, "total_bytes", totalBytes, "last_frame_size", len(data)).
					Info("browser_stream: relay stats")
				lastLogAt = time.Now()
			}
			if writeErr := clientConn.Write(ctx, msgType, data); writeErr != nil {
				logger.WithFields("session_id", sessionID, "frames_relayed", frameCount, "error", writeErr.Error()).
					Warn("browser_stream: client write failed (relay dying)")
				return
			}
		}
	}()

	// Main loop: client → sidecar (text JSON input events)
	for {
		msgType, data, err := clientConn.Read(ctx)
		if err != nil {
			select {
			case <-done:
				logger.WithFields("session_id", sessionID).
					Info("browser_stream: client read ended (sidecar already closed)")
				clientConn.Close(websocket.StatusNormalClosure, "stream ended")
			default:
				logger.WithFields("session_id", sessionID, "error", err.Error()).
					Info("browser_stream: client read error")
			}
			return
		}
		if writeErr := sidecarConn.Write(ctx, msgType, data); writeErr != nil {
			logger.WithFields("session_id", sessionID, "error", writeErr.Error()).
				Warn("browser_stream: sidecar write failed for input event")
			return
		}
	}
}

// waitForStreamer polls the streamer health endpoint until it responds.
func waitForStreamer(ctx context.Context, backendTarget string, timeout time.Duration) error {
	healthURL := fmt.Sprintf("http://%s/health", backendTarget)
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(healthURL)
		if err == nil {
			io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("streamer at %s not ready after %s", backendTarget, timeout)
}

// HandleBrowserStreamByIDOrName upgrades the HTTP connection to a WebSocket
// for browser streaming, identified by sandbox ID or name.
func (s *Server) HandleBrowserStreamByIDOrName(w http.ResponseWriter, r *http.Request) {
	identifier := mux.Vars(r)["sandbox_id"]
	if identifier == "" {
		http.Error(w, `{"error":"sandbox_id is required"}`, http.StatusBadRequest)
		return
	}

	if s.sandboxMgr == nil {
		http.Error(w, `{"error":"sandbox feature is not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	inst, ok := s.sandboxMgr.GetBySandboxIDOrName(identifier)
	if !ok {
		http.Error(w, `{"error":"sandbox not found"}`, http.StatusNotFound)
		return
	}

	if inst.Config.BrowserSidecar == nil {
		http.Error(w, `{"error":"browser sidecar is not configured"}`, http.StatusBadRequest)
		return
	}

	if err := s.authenticateShellRequest(r, inst.ID, inst.Config.TenantID); err != nil {
		http.Error(w, `{"error":"unauthorized: `+err.Error()+`"}`, http.StatusUnauthorized)
		return
	}

	sessionID := inst.Config.SessionID
	if sessionID == "" {
		http.Error(w, `{"error":"sandbox has no associated session"}`, http.StatusInternalServerError)
		return
	}

	streamPort := inst.Config.BrowserSidecar.StreamPort
	if streamPort == 0 {
		streamPort = 6080
	}

	s.openBrowserStreamWebSocket(w, r, sessionID, streamPort)
}

