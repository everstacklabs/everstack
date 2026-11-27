// Package client implements bounded, non-retrying A2A JSON-RPC calls. Protocol
// selection is explicit: legacy 0.2/0.3 and 1.0 have different wire contracts.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/outboundguard"
	"github.com/google/uuid"
)

const Legacy = "a2a-legacy"
const V1 = "a2a-1.0"
const Timeout = 120 * time.Second
const MaxResponse = 8 << 20

// Connection is resolved on the server for each operation, never from model
// arguments. Tenant is an optional remote routing identifier, not local auth.
type Connection struct{ Endpoint, Protocol, Token, Tenant string }
type Result struct {
	TaskID    string `json:"task_id,omitempty"`
	ContextID string `json:"context_id,omitempty"`
	State     string `json:"state"`
	Text      string `json:"text"`
}

func Terminal(state string) bool {
	return state == "completed" || state == "failed" || state == "canceled" || state == "rejected"
}

// Error exposes a fixed diagnostic category without remote bodies or URLs.
// An error after dispatch never establishes that an effectful send was rejected.
type Error struct{ Code string }

func (e *Error) Error() string  { return "external agent: " + e.Code }
func failure(code string) error { return &Error{Code: code} }

type Client struct {
	HTTPClient       *http.Client
	PrivateEndpoints []string
}

var (
	transportsOnce   sync.Once
	publicTransport  *http.Transport
	privateTransport *http.Transport
)

// guardedTransports builds the two dial policies once. Both use the shared
// outbound guard's address rules; neither uses outboundguard.NewTransport,
// whose 10s response-header timeout would cut off a legacy server that runs
// the task before answering.
func guardedTransports() (*http.Transport, *http.Transport) {
	transportsOnce.Do(func() {
		build := func(dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
			return &http.Transport{
				Proxy:                  nil, // an env proxy would bypass destination checks
				DialContext:            dial,
				ForceAttemptHTTP2:      true,
				MaxIdleConns:           32,
				IdleConnTimeout:        60 * time.Second,
				TLSHandshakeTimeout:    10 * time.Second,
				ExpectContinueTimeout:  time.Second,
				MaxResponseHeaderBytes: 64 << 10,
			}
		}
		// Public unicast only; the resolved address is the one dialed.
		publicTransport = build(outboundguard.DialContext(outboundguard.Config{}))
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: privateControl}
		privateTransport = build(dialer.DialContext)
	})
	return publicTransport, privateTransport
}

// privateControl runs on the concrete post-resolution address for an
// operator-approved endpoint. It adds RFC1918 and IPv6 ULA to the public
// unicast set; loopback, link-local (metadata), CGNAT and special-use ranges
// stay blocked.
func privateControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: malformed dial address", outboundguard.ErrBlocked)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: unresolved dial target", outboundguard.ErrBlocked)
	}
	addr = addr.Unmap()
	if addr.IsPrivate() || outboundguard.IsPublicUnicast(addr) {
		return nil
	}
	return fmt.Errorf("%w: non-public address %s", outboundguard.ErrBlocked, addr)
}

func Validate(c Connection) error {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return failure("invalid_endpoint")
	}
	if c.Token != "" && u.Scheme != "https" {
		return failure("https_required")
	}
	if c.Protocol != Legacy && c.Protocol != V1 {
		return failure("unsupported_protocol")
	}
	if strings.ContainsAny(c.Token, "\r\n") {
		return failure("invalid_credential")
	}
	if len(c.Tenant) > 1024 || (c.Tenant != "" && c.Protocol == Legacy) {
		// tenant is an A2A 1.0 request field; legacy servers do not define it.
		return failure("invalid_routing_tenant")
	}
	return nil
}
func (c *Client) httpClient(endpoint string) *http.Client {
	var result http.Client
	if c.HTTPClient != nil {
		// Trusted injection (tests, embedders); deadlines and the redirect
		// policy below still apply.
		result = *c.HTTPClient
	} else {
		public, private := guardedTransports()
		result.Transport = public
		result.Timeout = Timeout
		for _, allowed := range c.PrivateEndpoints {
			if a := strings.TrimSpace(allowed); a != "" && a == endpoint {
				result.Transport = private
				break
			}
		}
	}
	if result.Timeout <= 0 || result.Timeout > Timeout {
		result.Timeout = Timeout
	}
	result.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	result.Jar = nil
	return &result
}
func (c *Client) Send(ctx context.Context, conn Connection, message, messageID string) (Result, error) {
	if strings.TrimSpace(message) == "" || len(message) > 64<<10 || messageID == "" {
		return Result{}, failure("invalid_message")
	}
	method := "message/send"
	msg := map[string]any{"kind": "message", "role": "user", "messageId": messageID, "parts": []any{map[string]any{"kind": "text", "text": message}}}
	configuration := map[string]any{"acceptedOutputModes": []string{"text/plain"}, "blocking": false}
	if conn.Protocol == V1 {
		method = "SendMessage"
		msg = map[string]any{"role": "ROLE_USER", "messageId": messageID, "parts": []any{map[string]any{"text": message}}}
		configuration = map[string]any{"acceptedOutputModes": []string{"text/plain"}, "returnImmediately": true}
	}
	params := map[string]any{"message": msg, "configuration": configuration}
	if conn.Tenant != "" {
		params["tenant"] = conn.Tenant
	}
	return c.call(ctx, conn, method, params, true)
}
func (c *Client) Get(ctx context.Context, conn Connection, task string) (Result, error) {
	return c.task(ctx, conn, task, false)
}
func (c *Client) Cancel(ctx context.Context, conn Connection, task string) (Result, error) {
	return c.task(ctx, conn, task, true)
}
func (c *Client) task(ctx context.Context, conn Connection, task string, cancel bool) (Result, error) {
	if !validID(task) {
		return Result{}, failure("invalid_task_id")
	}
	method := "tasks/get"
	if cancel {
		method = "tasks/cancel"
	}
	if conn.Protocol == V1 {
		method = "GetTask"
		if cancel {
			method = "CancelTask"
		}
	}
	params := map[string]any{"id": task}
	if conn.Tenant != "" {
		params["tenant"] = conn.Tenant
	}
	result, err := c.call(ctx, conn, method, params, false)
	if err == nil && result.TaskID != task {
		return Result{}, failure("task_id_mismatch")
	}
	return result, err
}
func (c *Client) call(ctx context.Context, conn Connection, method string, params any, send bool) (Result, error) {
	if err := Validate(conn); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	id := uuid.NewString()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return Result{}, failure("invalid_request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, failure("invalid_endpoint")
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	contextkeys.WriteAgentHop(ctx, req.Header)
	if conn.Protocol == V1 {
		req.Header.Set("A2A-Version", "1.0")
	}
	if conn.Token != "" {
		req.Header.Set("Authorization", "Bearer "+conn.Token)
	}
	response, err := c.httpClient(conn.Endpoint).Do(req)
	if err != nil {
		return Result{}, failure("transport_error")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{}, failure(fmt.Sprintf("http_%d", response.StatusCode))
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return Result{}, failure("invalid_content_type")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxResponse+1))
	if err != nil {
		return Result{}, failure("read_failed")
	}
	if len(raw) > MaxResponse {
		return Result{}, failure("response_too_large")
	}
	result, err := decode(raw, id, conn.Protocol, send)
	if len(conn.Token) >= 8 {
		// Never persist a remote-supplied handle containing this credential.
		// Very short tokens would match ordinary IDs and text, so skip them.
		if strings.Contains(result.TaskID, conn.Token) || strings.Contains(result.ContextID, conn.Token) {
			return Result{}, failure("invalid_task_id")
		}
		result.Text = strings.ReplaceAll(result.Text, conn.Token, "[redacted]")
	}
	return result, err
}

// Rejected reports whether err proves the remote refused the request before
// doing any work: an HTTP refusal or a JSON-RPC parse/request/method/params
// error, or an A2A-defined refusal. Transport failures, timeouts, 408, 5xx,
// -32603 internal errors, -32006 (the agent ran but answered invalidly) and
// malformed bodies are not proof, so callers must treat them as unknown.
func Rejected(err error) bool {
	code := ErrorCode(err)
	switch code {
	case "http_400", "http_401", "http_403", "http_404", "http_405", "http_406", "http_413", "http_415", "http_422", "http_429":
		return true
	}
	if !strings.HasPrefix(code, "rpc_") {
		return false
	}
	n, convErr := strconv.Atoi(strings.TrimPrefix(code, "rpc_"))
	if convErr != nil {
		return false
	}
	switch {
	case n == -32700, n == -32600, n == -32601, n == -32602:
		return true
	case n == -32006:
		return false
	case n <= -32001 && n >= -32099:
		return true
	}
	return false
}

func ErrorCode(err error) string {
	var remote *Error
	if errors.As(err, &remote) {
		return remote.Code
	}
	return "remote_unavailable"
}
