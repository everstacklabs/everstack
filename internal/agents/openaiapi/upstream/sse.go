package upstream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var ErrStreamEOF = errors.New("OpenAI Agents event stream closed before a terminal event")

type EventStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	max     int
	done    bool
}

func (c *Client) Subscribe(ctx context.Context, id string, o StreamOptions) (*EventStream, error) {
	if o.Cursor != "" {
		return nil, &Error{Kind: ErrorUnsupported, Operation: "subscribe", Message: "agents=v1 streams do not support replay cursors; resubscribe and reconcile saved items"}
	}
	u, _ := url.Parse(c.endpoint("agents", "sessions", id, "events"))
	q := u.Query()
	q.Set("stream", "true")
	u.RawQuery = q.Encode()
	req, err := c.newRequest(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	// A session stream can remain idle longer than the ordinary request
	// deadline. Its lifetime belongs to ctx; applying Client.Timeout here
	// tears down healthy subscriptions and falsely triggers recovery.
	streamClient := *c.http
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, &Error{Kind: ErrorTransport, Operation: "subscribe", Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, decodeHTTPError("subscribe", resp)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
		resp.Body.Close()
		return nil, &Error{Kind: ErrorProtocol, Operation: "subscribe", Message: "response is not text/event-stream", RequestID: requestID(resp)}
	}
	scanner := bufio.NewScanner(resp.Body)
	initial := 64 * 1024
	if c.maxEventBytes < initial {
		initial = c.maxEventBytes
	}
	scanner.Buffer(make([]byte, initial), c.maxEventBytes)
	return &EventStream{body: resp.Body, scanner: scanner, max: c.maxEventBytes}, nil
}
func (s *EventStream) Close() error { s.done = true; return s.body.Close() }
func (s *EventStream) Next() (Event, error) {
	if s.done {
		return Event{}, io.EOF
	}
	var data []string
	size := 0
	for s.scanner.Scan() {
		line := strings.TrimSuffix(s.scanner.Text(), "\r")
		size += len(line) + 1
		if size > s.max {
			return Event{}, &Error{Kind: ErrorProtocol, Operation: "read event stream", Message: "event exceeds configured size limit"}
		}
		if line == "" {
			if len(data) == 0 {
				size = 0
				continue
			}
			b := []byte(strings.Join(data, "\n"))
			var ev Event
			if e := json.Unmarshal(b, &ev); e != nil {
				return Event{}, &Error{Kind: ErrorProtocol, Operation: "decode event", Message: "malformed SSE JSON", Err: e}
			}
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			v := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(v, " ") {
				v = v[1:]
			}
			data = append(data, v)
		}
	}
	s.done = true
	if err := s.scanner.Err(); err != nil {
		// Scanner reports an oversized line as ErrTooLong through this path.
		if strings.Contains(err.Error(), "token too long") {
			return Event{}, &Error{Kind: ErrorProtocol, Operation: "read event stream", Message: "event exceeds configured size limit", Err: err}
		}
		return Event{}, &Error{Kind: ErrorTransport, Operation: "read event stream", Err: err}
	}
	return Event{}, ErrStreamEOF
}
