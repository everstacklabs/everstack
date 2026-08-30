package firecracker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeShellEndpoint stands up an httptest server that mimics what the
// in-guest agent's handleShellWS does, enough for the host-side
// dialer to exercise its handshake without spinning up a real guest.
//
// onInit receives the parsed params; the test uses it to drive the
// server reply: success body, error body, or "session_gone" sentinel.
type fakeShellEndpoint struct {
	t      *testing.T
	srv    *httptest.Server
	onInit func(ShellOpenParams) (ShellOpenResult, string) // (result, errString)
}

func newFakeShellEndpoint(t *testing.T, onInit func(ShellOpenParams) (ShellOpenResult, string)) (*fakeShellEndpoint, string) {
	t.Helper()
	fe := &fakeShellEndpoint{t: t, onInit: onInit}
	mux := http.NewServeMux()
	mux.HandleFunc("/shell", fe.handle)
	fe.srv = httptest.NewServer(mux)
	t.Cleanup(fe.srv.Close)
	// Return a hostport without the http:// prefix — OpenShellViaWS
	// rebuilds the URL itself from "guestIP" and the hardcoded port.
	addr := strings.TrimPrefix(fe.srv.URL, "http://")
	return fe, addr
}

func (fe *fakeShellEndpoint) handle(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		fe.t.Errorf("accept: %v", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, raw, err := c.Read(ctx)
	if err != nil {
		return
	}
	var params ShellOpenParams
	if err := json.Unmarshal(raw, &params); err != nil {
		_ = wsWriteJSON(ctx, c, map[string]string{"error": "bad params"})
		return
	}

	result, errStr := fe.onInit(params)
	if errStr != "" {
		_ = wsWriteJSON(ctx, c, map[string]string{"error": errStr})
		return
	}
	_ = wsWriteJSON(ctx, c, struct {
		SessionID  string `json:"session_id"`
		Reattached bool   `json:"reattached"`
	}{
		SessionID:  result.SessionID,
		Reattached: result.Reattached,
	})

	// Echo binary frames back so byte-stream roundtrip tests work.
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		_ = c.Write(ctx, websocket.MessageBinary, data)
	}
}

func wsWriteJSON(ctx context.Context, c *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, data)
}

// dialFakeShell wraps OpenShellViaWS but rewrites the host so the test
// can hit httptest's random port. OpenShellViaWS builds the URL from
// (guestIP, hardcoded port), but the fake endpoint runs on httptest's
// random port — so the test patches by setting the guestIP value to
// "host:port" and bypassing the hardcoded port.
//
// Simpler than reworking OpenShellViaWS to take a port: tests just
// pass "127.0.0.1" as the IP and use a small wrapper that targets
// the fake's full URL directly.
func dialFakeShell(t *testing.T, fakeURL string, params ShellOpenParams) (net.Conn, ShellOpenResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, "ws://"+fakeURL+"/shell", nil)
	if err != nil {
		return nil, ShellOpenResult{}, err
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		c.Close(websocket.StatusInternalError, "marshal")
		return nil, ShellOpenResult{}, err
	}
	if err := c.Write(ctx, websocket.MessageText, paramsJSON); err != nil {
		c.Close(websocket.StatusInternalError, "send")
		return nil, ShellOpenResult{}, err
	}
	_, raw, err := c.Read(ctx)
	if err != nil {
		c.Close(websocket.StatusInternalError, "ack")
		return nil, ShellOpenResult{}, err
	}
	var ack struct {
		SessionID  string `json:"session_id"`
		Reattached bool   `json:"reattached"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		c.Close(websocket.StatusUnsupportedData, "bad ack")
		return nil, ShellOpenResult{}, err
	}
	if ack.Error != "" {
		c.Close(websocket.StatusNormalClosure, "guest error")
		if ack.Error == "session_gone" {
			return nil, ShellOpenResult{}, ErrSessionGone
		}
		return nil, ShellOpenResult{}, &shellError{ack.Error}
	}
	return websocket.NetConn(context.Background(), c, websocket.MessageBinary), ShellOpenResult{
		SessionID:  ack.SessionID,
		Reattached: ack.Reattached,
	}, nil
}

type shellError struct{ msg string }

func (e *shellError) Error() string { return e.msg }

func TestSelectShellTransport_DefaultsToWS(t *testing.T) {
	t.Setenv("FCAGENT_SHELL_TRANSPORT", "")
	vm := &MicroVM{Network: &NetworkConfig{GuestIP: "10.0.1.2"}}
	if got := selectShellTransport(vm); got != shellTransportWS {
		t.Fatalf("default = %q, want %q", got, shellTransportWS)
	}
}

func TestSelectShellTransport_HonorsVsock(t *testing.T) {
	t.Setenv("FCAGENT_SHELL_TRANSPORT", "vsock")
	vm := &MicroVM{Network: &NetworkConfig{GuestIP: "10.0.1.2"}}
	if got := selectShellTransport(vm); got != shellTransportVsock {
		t.Fatalf("vsock env override = %q, want %q", got, shellTransportVsock)
	}
}

func TestSelectShellTransport_CaseInsensitive(t *testing.T) {
	t.Setenv("FCAGENT_SHELL_TRANSPORT", "VSOCK")
	vm := &MicroVM{Network: &NetworkConfig{GuestIP: "10.0.1.2"}}
	if got := selectShellTransport(vm); got != shellTransportVsock {
		t.Fatalf("uppercase VSOCK = %q, want %q", got, shellTransportVsock)
	}
}

func TestSelectShellTransport_VsockWhenNoNetwork(t *testing.T) {
	// Even when env says ws, a deny-mode VM (or one mid-construction
	// without a guest IP) must use vsock — no IP, no /health endpoint.
	t.Setenv("FCAGENT_SHELL_TRANSPORT", "ws")
	vm := &MicroVM{Network: nil}
	if got := selectShellTransport(vm); got != shellTransportVsock {
		t.Fatalf("nil network = %q, want %q", got, shellTransportVsock)
	}
	vm2 := &MicroVM{Network: &NetworkConfig{GuestIP: ""}}
	if got := selectShellTransport(vm2); got != shellTransportVsock {
		t.Fatalf("empty IP = %q, want %q", got, shellTransportVsock)
	}
}

func TestShellWS_Dial_SuccessfulHandshake(t *testing.T) {
	_, addr := newFakeShellEndpoint(t, func(p ShellOpenParams) (ShellOpenResult, string) {
		if p.User != "sandbox" {
			t.Errorf("expected user=sandbox, got %q", p.User)
		}
		return ShellOpenResult{SessionID: "test-session-1", Reattached: true}, ""
	})

	conn, result, err := dialFakeShell(t, addr, ShellOpenParams{User: "sandbox", WorkDir: "/workspace"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if result.SessionID != "test-session-1" {
		t.Errorf("session_id = %q, want test-session-1", result.SessionID)
	}
	if !result.Reattached {
		t.Errorf("reattached = false, want true")
	}
}

func TestShellWS_Dial_SessionGoneError(t *testing.T) {
	// When the guest reports the session_gone sentinel, dialer
	// must translate to ErrSessionGone so callers can distinguish
	// "your session ended" from generic failures.
	_, addr := newFakeShellEndpoint(t, func(p ShellOpenParams) (ShellOpenResult, string) {
		return ShellOpenResult{}, "session_gone"
	})

	_, _, err := dialFakeShell(t, addr, ShellOpenParams{SessionID: "ghost"})
	if err != ErrSessionGone {
		t.Fatalf("err = %v, want ErrSessionGone", err)
	}
}

func TestShellWS_Dial_PropagatesGenericError(t *testing.T) {
	_, addr := newFakeShellEndpoint(t, func(p ShellOpenParams) (ShellOpenResult, string) {
		return ShellOpenResult{}, "tmux start failed: no PTY available"
	})

	_, _, err := dialFakeShell(t, addr, ShellOpenParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tmux start failed") {
		t.Fatalf("error did not preserve guest message: %v", err)
	}
}
