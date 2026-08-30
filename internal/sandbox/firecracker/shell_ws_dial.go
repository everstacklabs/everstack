package firecracker

// Host-side shell dialer that talks to the in-guest agent's /shell
// WebSocket endpoint (Phase 5a, cmd/sandbox-agent/shell_ws.go).
//
// Shell opens prefer this HTTP/WebSocket transport when a guest IP is
// available. Vsock remains available as a fallback so an operator can roll
// back to the legacy path without redeploying by setting
// FCAGENT_SHELL_TRANSPORT=vsock.
//
// The return type matches the vsock OpenShell exactly (net.Conn +
// ShellOpenResult) so backend.ShellWithSession swaps transports with
// a single switch — the downstream firecrackerShellConn doesn't need
// to know which transport carried it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
)

var ErrShellWSUnavailable = errors.New("shell WebSocket unavailable")

// osGetenv is the indirection target envValue routes through. Pulled
// out so tests can monkey-patch envValue without touching this name.
var osGetenv = os.Getenv

// shellWSPort is the in-guest HTTP listener port. Hardcoded in
// lockstep with cmd/sandbox-agent/main.go's httpListenPort — they
// must stay in sync. Phase 4 has the same coupling for /health.
const shellWSPort = 8080

// shellWSPath is the WebSocket-upgrade endpoint on the agent.
const shellWSPath = "/shell"

// Shell transport names used in env config + log lines.
const (
	shellTransportVsock = "vsock"
	shellTransportWS    = "ws"
)

// selectShellTransport returns "vsock" or "ws" for the given VM. The
// env knob FCAGENT_SHELL_TRANSPORT toggles the default; per-VM rules:
//
//   - Deny-mode VMs and VMs without a guest IP always use vsock -- the
//     WS path needs a routable TAP IP and the agent's HTTP listener.
//   - FCAGENT_SHELL_TRANSPORT=vsock forces legacy vsock.
//   - Otherwise the default is ws; ShellWithSession falls back to vsock only
//     when WS is unavailable at the transport/protocol layer.
//
// Kept lowercase + case-insensitive on the env input so a stray
// `WS` or `Ws` doesn't silently fall back to vsock.
func selectShellTransport(vm *MicroVM) string {
	if vm == nil || vm.Network == nil || vm.Network.GuestIP == "" {
		return shellTransportVsock
	}
	want := strings.ToLower(strings.TrimSpace(envValue("FCAGENT_SHELL_TRANSPORT")))
	switch want {
	case shellTransportVsock:
		return shellTransportVsock
	default:
		return shellTransportWS
	}
}

// envValue is a tiny wrapper around os.Getenv that lets the test
// suite shim the value without touching real env. Kept package-
// private; production callers go through os.Getenv directly.
var envValue = func(key string) string {
	return osGetenv(key)
}

// OpenShellViaWS dials the in-guest agent's /shell endpoint over
// HTTP/WebSocket, performs the JSON init handshake, and returns a
// net.Conn that carries shellframe binary frames in both directions.
//
// Mirror of VsockClient.OpenShell, transport-swapped. Callers receive
// the same shape (conn, ShellOpenResult, error) and can use the
// returned conn with the existing firecrackerShellConn wrapper.
//
// The guest's WS handler expects:
//
//  1. TEXT message: shellOpenParams JSON (sent by this function).
//  2. TEXT reply:   {session_id, reattached}  OR  {error}.
//  3. Thereafter:   binary messages carrying shellframe bytes.
//
// We surface ErrSessionGone for the "session_gone" error string so
// callers don't have to string-match — same convention vsock uses.
func OpenShellViaWS(ctx context.Context, guestIP, token string, params ShellOpenParams) (net.Conn, ShellOpenResult, error) {
	if guestIP == "" {
		return nil, ShellOpenResult{}, errors.New("OpenShellViaWS: empty guestIP")
	}
	url := fmt.Sprintf("ws://%s:%d%s", guestIP, shellWSPort, shellWSPath)

	// 30s handshake budget matches the vsock OpenShell path. Long
	// enough for cold tmux session create + attach; tight enough that
	// a wedged guest doesn't keep the gateway's gRPC stream alive
	// indefinitely.
	dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dialCancel()

	dialOpts := &websocket.DialOptions{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
	// Per-VM bearer token on the WS handshake — same auth the guest requires on
	// every :8080 endpoint except /health. Without it a co-resident guest could
	// open a shell into this VM.
	if token != "" {
		dialOpts.HTTPHeader = http.Header{"Authorization": {"Bearer " + token}}
	}
	c, _, err := websocket.Dial(dialCtx, url, dialOpts)
	if err != nil {
		return nil, ShellOpenResult{}, fmt.Errorf("%w: ws dial %s: %v", ErrShellWSUnavailable, url, err)
	}

	// Init handshake. Encode params and send as TEXT.
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		c.Close(websocket.StatusInternalError, "marshal params")
		return nil, ShellOpenResult{}, fmt.Errorf("marshal shellOpenParams: %w", err)
	}
	if err := c.Write(dialCtx, websocket.MessageText, paramsJSON); err != nil {
		c.Close(websocket.StatusInternalError, "send init")
		return nil, ShellOpenResult{}, fmt.Errorf("%w: ws send init: %v", ErrShellWSUnavailable, err)
	}

	// Read ack. The server replies with either a success body
	// {session_id, reattached} or {error: "..."}. Distinguish by
	// presence of the error field.
	typ, raw, err := c.Read(dialCtx)
	if err != nil {
		c.Close(websocket.StatusInternalError, "read ack")
		return nil, ShellOpenResult{}, fmt.Errorf("%w: ws read ack: %v", ErrShellWSUnavailable, err)
	}
	if typ != websocket.MessageText {
		c.Close(websocket.StatusUnsupportedData, "non-text ack")
		return nil, ShellOpenResult{}, fmt.Errorf("%w: ws ack: expected text, got %v", ErrShellWSUnavailable, typ)
	}

	var ack struct {
		SessionID  string `json:"session_id"`
		Reattached bool   `json:"reattached"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		c.Close(websocket.StatusUnsupportedData, "bad ack json")
		return nil, ShellOpenResult{}, fmt.Errorf("%w: ws ack unmarshal: %v (%q)", ErrShellWSUnavailable, err, raw)
	}
	if ack.Error != "" {
		c.Close(websocket.StatusNormalClosure, "guest reported error")
		if ack.Error == "session_gone" {
			return nil, ShellOpenResult{}, ErrSessionGone
		}
		return nil, ShellOpenResult{}, fmt.Errorf("guest shell_open: %s", ack.Error)
	}

	// Hand the WS conn back as a net.Conn so callers (specifically
	// firecrackerShellConn) can Read/Write bytes without knowing
	// they're on a WebSocket. NetConn returns an adapter that
	// transparently translates between binary messages and bytes.
	//
	// Context lifetime: NetConn's lifetime is bounded by the context
	// we pass. We hand it the parent ctx (not dialCtx) so the conn
	// stays alive for the lifetime of the shell session, not just
	// the handshake.
	netConn := websocket.NetConn(ctx, c, websocket.MessageBinary)
	return netConn, ShellOpenResult{
		SessionID:  ack.SessionID,
		Reattached: ack.Reattached,
	}, nil
}
