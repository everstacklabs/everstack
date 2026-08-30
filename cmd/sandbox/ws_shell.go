package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/term"
)

// ANSI color helpers for the connection banner.
const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiCyan  = "\033[36m"
	ansiGreen = "\033[32m"
)

// sandboxInfo holds metadata displayed in the connection banner.
type sandboxInfo struct {
	Name      string
	ID        string
	Image     string
	Status    string
	SessionID string
}

// wsShellMessage mirrors the server-side shellMessage envelope.
type wsShellMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
}

// wsShellEvent mirrors the server-side shellServerEvent envelope —
// control frames the server sends as MessageText (data frames stay
// binary). The CLI uses these to learn the persistent shell session
// ID so it can pass it back as ?shell_session=… on reconnect.
type wsShellEvent struct {
	Type       string `json:"type"`
	SessionID  string `json:"session_id,omitempty"`
	Reattached bool   `json:"reattached,omitempty"`
	Message    string `json:"message,omitempty"`
}

// printConnectionBanner prints a branded header before dropping into the shell.
func printConnectionBanner(w io.Writer, info sandboxInfo) {
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  %s%s◆ Everstack Sandbox%s\n", ansiBold, ansiCyan, ansiReset)
	fmt.Fprintf(w, "  %s│%s %sname:%s  %s\n", ansiDim, ansiReset, ansiDim, ansiReset, info.Name)
	if info.ID != "" {
		fmt.Fprintf(w, "  %s│%s %sid:%s    %s\n", ansiDim, ansiReset, ansiDim, ansiReset, info.ID)
	}
	if info.Image != "" {
		fmt.Fprintf(w, "  %s│%s %simage:%s %s\n", ansiDim, ansiReset, ansiDim, ansiReset, info.Image)
	}
	fmt.Fprintf(w, "  %s╰%s %sstatus:%s %s%s%s\n", ansiDim, ansiReset, ansiDim, ansiReset, ansiGreen, info.Status, ansiReset)
	fmt.Fprintf(w, "\n")
}

// runWebSocketShell connects to the sandbox shell WebSocket endpoint and
// bridges it to the local terminal in raw mode.
//
// Reconnect behavior: when the WebSocket drops mid-session and the
// server told us about a persistent shell session, we automatically
// reconnect with exponential backoff (1s, 2s, 4s, …, capped at 30s)
// using the cached shell_session ID. The user sees a "Reconnecting…"
// banner on stderr; xterm-style screen state stays as it was because
// we keep stdout untouched. A SESSION_GONE signal from the server
// aborts the loop with a clear message.
func runWebSocketShell(ctx context.Context, apiBaseURL, sandboxIDOrName string, info *sandboxInfo, auth *shellAuthParams) error {
	// Print connection banner before entering raw mode
	if info != nil {
		printConnectionBanner(os.Stderr, *info)
	}

	// Put terminal into raw mode once for the entire session — the
	// reconnect loop reuses the same raw mode so we don't get a brief
	// cooked-mode flash between attempts.
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal; WebSocket shell requires an interactive terminal")
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to set terminal raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	// Persistent shell session ID. Empty for the first attempt; the
	// server fills it in via a control frame and we reuse it on
	// subsequent reconnects.
	var persistentShellSessionID string

	// Reconnect loop. Each iteration is one WebSocket session.
	delay := time.Second
	const maxDelay = 30 * time.Second
	firstAttempt := true
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !firstAttempt {
			fmt.Fprintf(os.Stderr, "\r\n%s%s↻ Reconnecting…%s\r\n", ansiBold, ansiCyan, ansiReset)
		}

		assignedID, sessionGone, err := runOneShellSession(ctx, apiBaseURL, sandboxIDOrName, info, auth, persistentShellSessionID, fd)
		if assignedID != "" {
			persistentShellSessionID = assignedID
		}

		if sessionGone {
			// The persistent session is gone server-side. Don't
			// retry-attach to the same ID — surface the message and
			// exit. The user can rerun the CLI to start fresh.
			fmt.Fprintf(os.Stderr, "\r\n%s%s× shell session ended%s\r\n", ansiBold, ansiCyan, ansiReset)
			return nil
		}

		if err == nil {
			// Clean exit (shell exited, ctx cancelled). Done.
			return nil
		}

		// Network error. Back off and retry.
		fmt.Fprintf(os.Stderr, "\r\n%s%s! connection dropped: %v — retrying in %s%s\r\n", ansiBold, ansiCyan, err, delay, ansiReset)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
		firstAttempt = false
	}
}

// runOneShellSession runs a single WebSocket attach. Returns the
// assigned persistent shell session ID, whether the server reported
// the session as gone, and an error if the connection failed in a way
// that warrants reconnect.
func runOneShellSession(ctx context.Context, apiBaseURL, sandboxIDOrName string, info *sandboxInfo, auth *shellAuthParams, knownShellSessionID string, fd int) (string, bool, error) {
	// Determine WebSocket URL based on available info
	wsBase := httpToWS(apiBaseURL)
	var wsURL string
	if info != nil && info.SessionID != "" {
		wsURL = wsBase + "/v1/sandbox/" + info.SessionID + "/shell"
	} else {
		wsURL = wsBase + "/v1/sandbox/instances/" + sandboxIDOrName + "/shell"
	}

	params := url.Values{}
	if auth != nil {
		params.Set("ts", auth.Timestamp)
		params.Set("sig", auth.Signature)
		params.Set("fp", auth.Fingerprint)
		params.Set("alg", auth.Algorithm)
	}
	if knownShellSessionID != "" {
		params.Set("shell_session", knownShellSessionID)
	}
	if encoded := params.Encode(); encoded != "" {
		wsURL += "?" + encoded
	}

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{},
	})
	if err != nil {
		return "", false, fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()

	// Send initial terminal size
	if cols, rows, err := term.GetSize(fd); err == nil && rows > 0 && cols > 0 {
		sendResize(ctx, conn, uint16(rows), uint16(cols))
	}

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// SIGWINCH → resize
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	go func() {
		for {
			select {
			case <-sessCtx.Done():
				return
			case <-sigCh:
				if cols, rows, err := term.GetSize(fd); err == nil && rows > 0 && cols > 0 {
					sendResize(sessCtx, conn, uint16(rows), uint16(cols))
				}
			}
		}
	}()

	// Server → stdout (with text-frame control parsing).
	var assignedShellSessionID string
	var sessionGone bool
	readDone := make(chan struct{})
	var readErr error
	go func() {
		defer close(readDone)
		for {
			frameType, data, err := conn.Read(sessCtx)
			if err != nil {
				readErr = err
				return
			}
			if frameType == websocket.MessageText {
				// Try to parse as a control event; fall through to
				// stdout if it doesn't match (some servers send xterm
				// bytes as text frames). This is best-effort and only
				// matters for the session-id capture.
				var ev wsShellEvent
				if jErr := json.Unmarshal(data, &ev); jErr == nil && ev.Type != "" {
					switch ev.Type {
					case "session":
						assignedShellSessionID = ev.SessionID
					case "session_gone":
						sessionGone = true
						cancel()
					}
					continue
				}
			}
			if _, wErr := os.Stdout.Write(data); wErr != nil {
				readErr = wErr
				return
			}
		}
	}()

	// stdin → server
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				msg := wsShellMessage{Type: "input", Data: string(buf[:n])}
				payload, _ := json.Marshal(msg)
				if wErr := conn.Write(sessCtx, websocket.MessageText, payload); wErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Wait for either side to finish.
	select {
	case <-readDone:
	case <-sessCtx.Done():
	}

	conn.Close(websocket.StatusNormalClosure, "")

	if sessionGone {
		return assignedShellSessionID, true, nil
	}

	// Distinguish "user/ctx ended" from "connection dropped". A
	// context-cancel from us is clean; anything else is a network
	// error the reconnect loop should retry.
	if ctx.Err() != nil {
		return assignedShellSessionID, false, nil
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		// Was it a normal-close from the server? In coder/websocket
		// the close arrives as an *websocket.CloseError; treat the
		// normal-closure status as clean exit.
		var ce websocket.CloseError
		if errors.As(readErr, &ce) && ce.Code == websocket.StatusNormalClosure {
			return assignedShellSessionID, false, nil
		}
		return assignedShellSessionID, false, readErr
	}
	return assignedShellSessionID, false, nil
}

func sendResize(ctx context.Context, conn *websocket.Conn, rows, cols uint16) {
	msg := wsShellMessage{
		Type: "resize",
		Rows: rows,
		Cols: cols,
	}
	data, _ := json.Marshal(msg)
	conn.Write(ctx, websocket.MessageText, data)
}

// httpToWS converts an HTTP(S) URL to a WS(S) URL.
func httpToWS(u string) string {
	u = strings.TrimRight(u, "/")
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://")
	}
	if strings.HasPrefix(u, "http://") {
		return "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u
}
