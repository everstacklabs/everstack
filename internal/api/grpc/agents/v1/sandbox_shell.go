package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/gorilla/mux"
)

// shellMessage is the JSON envelope for WebSocket messages from the client.
type shellMessage struct {
	Type string `json:"type"` // "input" or "resize"
	Data string `json:"data"` // raw terminal input
	Rows uint16 `json:"rows"` // for resize
	Cols uint16 `json:"cols"` // for resize
}

// shellServerEvent is the JSON envelope for control messages from the
// server to the client. Data frames stay binary as before; control
// frames are sent as text so the client can JSON-parse them and
// distinguish them cheaply. The client always tries to parse text
// frames as a shellServerEvent and renders the bytes to xterm only
// when the parse fails (matching the existing best-effort path on the
// client→server direction).
type shellServerEvent struct {
	Type string `json:"type"` // "session", "session_gone", "sandbox_gone"
	// SessionID is the persistent shell session ID assigned/resumed
	// by the backend. The client stores this in sessionStorage so a
	// reconnect can pass it back via the shell_session query param.
	SessionID string `json:"session_id,omitempty"`
	// Reattached is true when this stream resumed an existing tmux
	// session rather than creating a new one. The UI shows a
	// "reconnected" banner in this case.
	Reattached bool `json:"reattached,omitempty"`
	// Transport names the host↔guest channel that carried this shell
	// — "vsock" (legacy) or "ws" (Phase 5a/b HTTP control plane).
	// Empty for non-Firecracker backends or older fcagent versions
	// that pre-date the Phase 5b flag. The admin UI surfaces this as
	// a small chip in the Shell tab so operators rolling out the new
	// transport can see at a glance which path each session landed on.
	Transport string `json:"transport,omitempty"`
	// Message is a human-readable detail for terminal-style events
	// (e.g. "session ended"). Optional.
	Message string `json:"message,omitempty"`
}

// HandleSandboxShell upgrades the HTTP connection to a WebSocket and opens an
// interactive shell in the sandbox for the given session.
func (s *Server) HandleSandboxShell(w http.ResponseWriter, r *http.Request) {
	// Extract session_id: prefer gorilla mux vars (direct route), fall back to URL parsing (grpc-gateway).
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

	// Verify sandbox exists for the session. After a gateway pod restart
	// the in-memory cache is empty for sandboxes this pod didn't directly
	// create — fall back to the DB so the shell stays reachable across
	// rollouts. The actual VM lives on fcagent regardless.
	inst, ok := s.sandboxMgr.GetInstance(sessionID)
	if !ok {
		dbCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		dbInst, dbErr := s.sandboxMgr.LookupInstanceBySession(dbCtx, sessionID)
		cancel()
		if dbErr != nil {
			http.Error(w, `{"error":"no sandbox for this session"}`, http.StatusNotFound)
			return
		}
		inst = dbInst
	}

	// Authenticate: same-origin (admin UI) or SSH key signature (CLI)
	if err := s.authenticateShellRequest(r, inst.ID, inst.Config.TenantID); err != nil {
		http.Error(w, `{"error":"unauthorized: `+err.Error()+`"}`, http.StatusUnauthorized)
		return
	}

	s.openShellWebSocket(w, r, sessionID, inst.ID)
}

// shellSessionFromRequest reads the optional persistent shell session
// ID from the request. We prefer the query string (`?shell_session=…`)
// because xterm.js / browser WebSocket clients can't set custom
// headers on the initial upgrade. Empty return means "create a new
// session" — the backend assigns one and the server emits it back
// to the client in the first text frame.
func shellSessionFromRequest(r *http.Request) string {
	return r.URL.Query().Get("shell_session")
}

// HandleSandboxShellByIDOrName upgrades the HTTP connection to a WebSocket and
// opens an interactive shell in the sandbox identified by sandbox ID or name.
func (s *Server) HandleSandboxShellByIDOrName(w http.ResponseWriter, r *http.Request) {
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
		// Pod-restart fallback: in-memory cache is empty but the row
		// exists in the DB. Pull the row's session_id and route through
		// the same DB-backed path as the by-session handler.
		dbCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		dbInst, dbErr := s.sandboxMgr.LookupInstanceByIDFromDB(dbCtx, identifier)
		cancel()
		if dbErr != nil {
			http.Error(w, `{"error":"sandbox not found"}`, http.StatusNotFound)
			return
		}
		inst = dbInst
	}

	// Authenticate: same-origin (admin UI) or SSH key signature (CLI)
	if err := s.authenticateShellRequest(r, inst.ID, inst.Config.TenantID); err != nil {
		http.Error(w, `{"error":"unauthorized: `+err.Error()+`"}`, http.StatusUnauthorized)
		return
	}

	sessionID := inst.Config.SessionID
	if sessionID == "" {
		http.Error(w, `{"error":"sandbox has no associated session"}`, http.StatusInternalServerError)
		return
	}

	s.openShellWebSocket(w, r, sessionID, inst.ID)
}

// openShellWebSocket upgrades the connection to a WebSocket and bridges it to
// an interactive shell in the sandbox identified by sessionID. sandboxID is
// the resolved lifecycle-row id, used to classify a "VM gone" condition as
// recoverable (auto-restoring) vs terminal so the client knows whether to
// keep reconnecting.
func (s *Server) openShellWebSocket(w http.ResponseWriter, r *http.Request, sessionID, sandboxID string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow any origin for development
	})
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("sandbox_shell: websocket upgrade failed")
		return
	}
	defer conn.CloseNow()

	// After websocket.Accept hijacks the HTTP connection, r.Context() is
	// cancelled by the HTTP server (the request is "done" from its perspective).
	// Use a fresh context for Docker operations so they don't fail immediately.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Open a shell session in the sandbox. The ?shell_session=<id>
	// query param drives client-side single-flight: every connect for
	// a given (sandbox, browser tab) must pass the SAME id so that
	// concurrent retries / React re-mounts / refreshes converge on
	// one tmux session instead of forking new ones. Missing param is
	// honored (server generates an id and returns it) but logged so
	// we can spot legacy clients in production.
	requestedShellSession := shellSessionFromRequest(r)
	if requestedShellSession == "" {
		logger.WithFields("sandbox_session", sessionID).
			Debug("sandbox_shell: client did not supply shell_session id — server will generate one (legacy client)")
	}
	shell, err := s.sandboxMgr.Shell(ctx, sessionID, []string{"/bin/sh"}, requestedShellSession)
	if err != nil {
		// A specific "session_gone" surface lets the client decide
		// whether to surface "your shell ended" to the user or
		// transparently retry without the session_id. The error
		// string from the manager bubbles up the typed marker.
		if strings.Contains(err.Error(), "session_gone") || strings.Contains(err.Error(), "session is gone") {
			_ = writeShellEvent(ctx, conn, shellServerEvent{
				Type:    "session_gone",
				Message: "your shell session ended; start a new one",
			})
			conn.Close(websocket.StatusGoingAway, "shell session is gone")
			return
		}
		// The VM behind this sandbox is gone (dead on every fcagent).
		// Whether the client should give up or keep reconnecting depends
		// on the lifecycle row: a row that still wants the sandbox running
		// is being auto-restored (HealthSweeper marks it error →
		// RecoveryChecker revives it), so we send a non-terminal
		// "sandbox_recovering" and the client keeps retrying until the new
		// VM answers. Only a terminal row gets "sandbox_gone" (stop the
		// reconnect loop, render a reprovision state). Without this split
		// the client either retried a truly-dead sandbox forever or — once
		// auto-recovery existed — gave up moments before the VM came back.
		if isSandboxGoneError(err) {
			ev := s.shellGoneEvent(ctx, sandboxID)
			_ = writeShellEvent(ctx, conn, shellServerEvent{Type: ev.Type, Message: ev.Message})
			conn.Close(ev.CloseCode, ev.Type)
			return
		}
		conn.Close(websocket.StatusInternalError, "failed to open shell: "+err.Error())
		return
	}
	defer shell.Conn.Close()

	// Tell the client which persistent session it's on, even if we
	// just created one. The client persists this in sessionStorage so
	// it can pass it back as ?shell_session=… on reconnect.
	if shell.ShellSessionID != "" {
		_ = writeShellEvent(ctx, conn, shellServerEvent{
			Type:       "session",
			SessionID:  shell.ShellSessionID,
			Reattached: shell.Reattached,
			Transport:  shell.Transport,
		})
	}

	shellDone := make(chan struct{})

	// Goroutine: read from shell → write to WebSocket.
	// When the shell exits, cancel ctx so that conn.Read in the main loop unblocks.
	go func() {
		defer close(shellDone)
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, err := shell.Conn.Read(buf)
			if n > 0 {
				if writeErr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					logger.WithFields("error", err.Error()).Debug("sandbox_shell: shell read error")
				}
				return
			}
		}
	}()

	// Goroutine: protocol-level WebSocket ping every 20s.
	// Detects half-open connections (NAT timeout, mobile sleep, network
	// hairpin) within 25s instead of waiting for a TCP timeout (30-120s).
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Main loop: read from WebSocket → write to shell
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			// The WebSocket read ended. When it's because the shell read
			// side closed (shellDone), distinguish a clean shell exit (user
			// typed `exit`; the VM is still alive) from a VM death (the
			// vsock transport vanished): a dead VM must surface as
			// recovering/gone so the client keeps reconnecting through an
			// auto-recovery instead of seeing a misleading "shell exited"
			// and stopping. ctx is already cancelled here, so probe + write
			// on a fresh context.
			select {
			case <-shellDone:
				probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, statusErr := s.sandboxMgr.BackendStatus(probeCtx, sandboxID)
				if isSandboxGoneError(statusErr) {
					ev := s.shellGoneEvent(probeCtx, sandboxID)
					_ = writeShellEvent(probeCtx, conn, shellServerEvent{Type: ev.Type, Message: ev.Message})
					probeCancel()
					conn.Close(ev.CloseCode, ev.Type)
				} else {
					probeCancel()
					conn.Close(websocket.StatusNormalClosure, "shell exited")
				}
			default:
			}
			return
		}

		var msg shellMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			// Treat as raw input if not valid JSON
			if _, wErr := shell.Conn.Write(data); wErr != nil {
				return
			}
			continue
		}

		switch msg.Type {
		case "input":
			if _, wErr := shell.Conn.Write([]byte(msg.Data)); wErr != nil {
				return
			}
		case "resize":
			if shell.Resize != nil && msg.Rows > 0 && msg.Cols > 0 {
				_ = shell.Resize(msg.Rows, msg.Cols)
			}
		case "ping":
			_ = writeShellEvent(ctx, conn, shellServerEvent{Type: "pong"})
		}
	}
}

// shellGoneResolution describes how the shell socket should tell the
// client about a "VM gone" condition.
type shellGoneResolution struct {
	Type      string               // control event type, also the close reason
	Message   string               // human-readable message for the client banner
	CloseCode websocket.StatusCode // WebSocket close code
}

// shellGoneEvent classifies a gone VM as recoverable (auto-restoring) or
// terminal and returns the event/close the shell socket should emit. A
// recoverable sandbox (lifecycle row still desires running, not
// terminating/terminated) yields a non-terminal "sandbox_recovering" so
// the client keeps reconnecting until the revived VM answers; a terminal
// one yields "sandbox_gone" so the client stops its reconnect loop.
func (s *Server) shellGoneEvent(ctx context.Context, sandboxID string) shellGoneResolution {
	if s.sandboxMgr != nil && s.sandboxMgr.IsSandboxRecoverable(ctx, sandboxID) {
		return shellGoneResolution{
			Type:      "sandbox_recovering",
			Message:   "this sandbox restarted and is being restored; reconnecting…",
			CloseCode: websocket.StatusServiceRestart,
		}
	}
	return shellGoneResolution{
		Type:      "sandbox_gone",
		Message:   "this sandbox is no longer running; reprovision to continue",
		CloseCode: websocket.StatusGoingAway,
	}
}

// writeShellEvent sends a control event as a JSON text frame. The
// client distinguishes these from data frames by frame type (text vs.
// binary) — data flows always use MessageBinary.
func writeShellEvent(ctx context.Context, conn *websocket.Conn, ev shellServerEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}
