package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const betaHeader = "agents=v1"

type Config struct {
	BaseURL, APIKey, Project, Organization string
	Timeout                                time.Duration
	HTTPClient                             *http.Client
	MaxEventBytes                          int
}
type Client struct {
	base                       *url.URL
	key, project, organization string
	http                       *http.Client
	maxEventBytes              int
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, &Error{Kind: ErrorInvalid, Operation: "configure", Message: "API key is required"}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, &Error{Kind: ErrorInvalid, Operation: "configure", Message: "base URL must be absolute", Err: err}
	}
	if u.Scheme != "https" && !isLoopback(u.Hostname()) {
		return nil, &Error{Kind: ErrorInvalid, Operation: "configure", Message: "base URL must use HTTPS except for loopback test servers"}
	}
	hc := &http.Client{}
	if cfg.HTTPClient != nil {
		*hc = *cfg.HTTPClient
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	hc.Timeout = cfg.Timeout
	prior := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && via[0].Header.Get("Authorization") != "" {
			return errors.New("redirect rejected because request carries credentials")
		}
		if prior != nil {
			return prior(req, via)
		}
		return http.ErrUseLastResponse
	}
	if cfg.MaxEventBytes == 0 {
		cfg.MaxEventBytes = 1 << 20
	}
	return &Client{base: u, key: cfg.APIKey, project: cfg.Project, organization: cfg.Organization, http: hc, maxEventBytes: cfg.MaxEventBytes}, nil
}
func isLoopback(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }

func (c *Client) endpoint(parts ...string) string {
	u := *c.base
	p := strings.TrimRight(u.Path, "/")
	for _, v := range parts {
		p += "/" + url.PathEscape(v)
	}
	u.Path = p
	return u.String()
}
func (c *Client) newRequest(ctx context.Context, method, target string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("OpenAI-Beta", betaHeader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.project != "" {
		req.Header.Set("OpenAI-Project", c.project)
	}
	if c.organization != "" {
		req.Header.Set("OpenAI-Organization", c.organization)
	}
	return req, nil
}
func (c *Client) doJSON(ctx context.Context, op, method, target string, body, out any, ambiguous bool) error {
	req, err := c.newRequest(ctx, method, target, body)
	if err != nil {
		return &Error{Kind: ErrorInvalid, Operation: op, Err: err}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Kind: ErrorTransport, Operation: op, Ambiguous: ambiguous, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeHTTPError(op, resp)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return &Error{Kind: ErrorProtocol, Operation: op, Err: err}
	}
	if err = json.Unmarshal(b, out); err != nil {
		return &Error{Kind: ErrorProtocol, Operation: op, Message: "malformed JSON response", RequestID: requestID(resp), Err: err}
	}
	setPageRaw(out, b)
	return nil
}
func requestID(r *http.Response) string {
	if v := r.Header.Get("x-request-id"); v != "" {
		return v
	}
	return r.Header.Get("request-id")
}
func decodeHTTPError(op string, r *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var envelope struct {
		Error struct{ Message, Type, Code string }
	}
	_ = json.Unmarshal(b, &envelope)
	kind := ErrorUpstream
	switch r.StatusCode {
	case 401:
		kind = ErrorUnauthenticated
	case 403:
		kind = ErrorForbidden
	case 429:
		kind = ErrorRateLimited
	}
	msg := envelope.Error.Message
	if msg == "" {
		msg = http.StatusText(r.StatusCode)
	}
	return &Error{Kind: kind, Operation: op, StatusCode: r.StatusCode, Code: envelope.Error.Code, Message: msg, RequestID: requestID(r), RetryAfter: r.Header.Get("Retry-After")}
}
func setPageRaw(out any, b []byte) {
	switch p := out.(type) {
	case *Page[Session]:
		p.Raw = append([]byte(nil), b...)
		pageExtra(b, &p.Extra)
	case *Page[Item]:
		p.Raw = append([]byte(nil), b...)
		pageExtra(b, &p.Extra)
	case *Page[Artifact]:
		p.Raw = append([]byte(nil), b...)
		pageExtra(b, &p.Extra)
	}
}
func pageExtra(b []byte, dst *map[string]json.RawMessage) {
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) == nil {
		for _, k := range []string{"object", "data", "first_id", "last_id", "has_more"} {
			delete(m, k)
		}
		if len(m) > 0 {
			*dst = m
		}
	}
}

func (c *Client) CreateSession(ctx context.Context, in CreateSessionRequest) (*Session, error) {
	var out Session
	err := c.doJSON(ctx, "create session", http.MethodPost, c.endpoint("agents", "sessions"), in, &out, true)
	return &out, err
}
func (c *Client) GetSession(ctx context.Context, id string) (*Session, error) {
	var out Session
	err := c.doJSON(ctx, "get session", http.MethodGet, c.endpoint("agents", "sessions", id), nil, &out, false)
	return &out, err
}
func (c *Client) ListSessions(ctx context.Context, o ListOptions) (*Page[Session], error) {
	var out Page[Session]
	err := c.doJSON(ctx, "list sessions", http.MethodGet, withList(c.endpoint("agents", "sessions"), o), nil, &out, false)
	return &out, err
}
func (c *Client) DeleteSession(ctx context.Context, id string) (*Deleted, error) {
	var out Deleted
	err := c.doJSON(ctx, "delete session", http.MethodDelete, c.endpoint("agents", "sessions", id), nil, &out, true)
	return &out, err
}
func (c *Client) SendInput(ctx context.Context, id string, input []InputMessage) error {
	return c.SendInputWithOptions(ctx, id, input, EventOptions{})
}
func (c *Client) SendInputWithOptions(ctx context.Context, id string, input []InputMessage, o EventOptions) error {
	return c.postEvents(ctx, "send input", id, []any{map[string]any{"type": "agent.session.input.message", "input": input}}, o, true)
}
func (c *Client) Cancel(ctx context.Context, id string) error {
	return c.CancelWithOptions(ctx, id, EventOptions{})
}
func (c *Client) CancelWithOptions(ctx context.Context, id string, o EventOptions) error {
	return c.postEvents(ctx, "cancel turn", id, []any{map[string]any{"type": "agent.session.input.cancel"}}, o, true)
}

// CancelSession is an alias for Cancel for service integrations that model the
// operation at session scope. The upstream event cancels only the active turn.
func (c *Client) CancelSession(ctx context.Context, id string) error { return c.Cancel(ctx, id) }
func (c *Client) CancelSessionWithOptions(ctx context.Context, id string, o EventOptions) error {
	return c.CancelWithOptions(ctx, id, o)
}
func (c *Client) SubmitToolResult(ctx context.Context, id string, r ToolResult) error {
	return c.SubmitToolResultWithOptions(ctx, id, r, EventOptions{})
}
func (c *Client) SubmitToolResultWithOptions(ctx context.Context, id string, r ToolResult, o EventOptions) error {
	e := map[string]any{"type": "agent.session.input.tool_result", "turn_id": r.TurnID, "call_id": r.CallID, "success": r.Success}
	if r.Success {
		e["output"] = r.Output
	} else {
		e["error"] = r.Error
	}
	return c.postEvents(ctx, "submit tool result", id, []any{e}, o, true)
}
func (c *Client) postEvents(ctx context.Context, op, id string, events []any, o EventOptions, ambiguous bool) error {
	if len(o.IdempotencyKey) > 256 {
		return &Error{Kind: ErrorInvalid, Operation: op, Message: "idempotency key exceeds 256 bytes"}
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.endpoint("agents", "sessions", id, "events"), map[string]any{"events": events})
	if err != nil {
		return &Error{Kind: ErrorInvalid, Operation: op, Err: err}
	}
	if o.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", o.IdempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Kind: ErrorTransport, Operation: op, Ambiguous: ambiguous, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeHTTPError(op, resp)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
func (c *Client) ListItems(ctx context.Context, id string, o ListOptions) (*Page[Item], error) {
	var out Page[Item]
	err := c.doJSON(ctx, "list items", http.MethodGet, withList(c.endpoint("agents", "sessions", id, "items"), o), nil, &out, false)
	return &out, err
}
func withList(s string, o ListOptions) string {
	u, _ := url.Parse(s)
	q := u.Query()
	if o.After != "" {
		q.Set("after", o.After)
	}
	if o.Before != "" {
		q.Set("before", o.Before)
	}
	if o.Order != "" {
		q.Set("order", o.Order)
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Client) ListArtifacts(ctx context.Context, sid string, o ListOptions) (*Page[Artifact], error) {
	var out Page[Artifact]
	err := c.doJSON(ctx, "list artifacts", http.MethodGet, withList(c.endpoint("agents", "sessions", sid, "artifacts"), o), nil, &out, false)
	return &out, err
}
func (c *Client) GetArtifact(ctx context.Context, sid, aid string) (*Artifact, error) {
	var out Artifact
	err := c.doJSON(ctx, "get artifact", http.MethodGet, c.endpoint("agents", "sessions", sid, "artifacts", aid), nil, &out, false)
	return &out, err
}
func (c *Client) DeleteArtifact(ctx context.Context, sid, aid string) (*Deleted, error) {
	var out Deleted
	err := c.doJSON(ctx, "delete artifact", http.MethodDelete, c.endpoint("agents", "sessions", sid, "artifacts", aid), nil, &out, true)
	return &out, err
}
func (c *Client) DownloadArtifact(ctx context.Context, sid, aid string) (io.ReadCloser, http.Header, error) {
	op := "download artifact"
	req, err := c.newRequest(ctx, http.MethodGet, c.endpoint("agents", "sessions", sid, "artifacts", aid, "content"), nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, &Error{Kind: ErrorTransport, Operation: op, Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, nil, decodeHTTPError(op, resp)
	}
	return resp.Body, resp.Header.Clone(), nil
}
