package main

// SSE (Server-Sent Events) handler for streaming sandbox logs over
// HTTP. Mounted on the existing :9091 metrics listener so clients
// have one host:port to talk to for /metrics + /healthz + /logs.
//
// Why SSE and not WebSocket: logs are server→client one-way. SSE is
// a tiny subset of HTTP that browsers, curl, and any reverse proxy
// handle natively — no upgrade dance, no framing protocol to fight
// with corporate proxies, no library on the client side beyond
// EventSource. The existing gRPC Logs RPC stays in place for
// programmatic consumers; SSE is the simpler alternative for
// dashboards and shell-script integrations.
//
// Wire format per https://html.spec.whatwg.org/multipage/server-sent-events.html:
//
//	data: <log line>\n\n
//
// Multi-line entries get one `data:` per line and a single \n\n at
// the end. We don't currently emit those (FormatEntry produces a
// single line), but the dispatcher is line-safe so future multi-
// line entries don't break the spec.

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// handleLogsSSE serves GET /logs/{sandboxID} as Server-Sent Events.
//
// Query params:
//   - tail=N         start by replaying the last N entries (default 100)
//   - since=UNIX     only stream entries with timestamp ≥ UNIX (seconds)
//   - timestamps=1   include "[hh:mm:ss] " prefix in each event
//   - follow=1       keep the connection open and stream new entries
//                    as they arrive. Default true (typical use is live
//                    tailing in the admin UI); follow=0 emits the
//                    snapshot then closes.
//
// On client disconnect (browser close, gateway drop) the handler
// observes r.Context() cancellation, unsubscribes from the buffer,
// and returns. No goroutine leak.
func (s *AgentServer) handleLogsSSE(w http.ResponseWriter, r *http.Request) {
	// Path is /logs/{sandboxID} — strip the prefix to get the ID.
	id := strings.TrimPrefix(r.URL.Path, "/logs/")
	id = strings.TrimSpace(id)
	if id == "" {
		http.Error(w, "missing sandbox ID in path", http.StatusBadRequest)
		return
	}

	// Confirm the sandbox exists on this fcagent before opening the
	// stream. Without this check the client would get a successful
	// HTTP 200 SSE stream that never emits anything, which looks
	// identical to "no log activity" — a confusing UX.
	rc, err := s.backend.Logs(r.Context(), id, parseLogsOpts(r))
	if err != nil {
		// Match the gRPC Logs status mapping: "VM not found" → 404.
		// Anything else → 500.
		if strings.Contains(err.Error(), "VM not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	streamSSE(w, r, rc)
}

// streamSSE handles the wire-level SSE protocol: headers, heartbeat,
// line-based event emission, client-disconnect handling. Split out
// from handleLogsSSE so tests can drive it against a synthetic
// io.ReadCloser without spinning up a real backend.
func streamSSE(w http.ResponseWriter, r *http.Request, rc io.ReadCloser) {
	defer rc.Close()

	// Closing rc when the request context is canceled is the only
	// reliable way to unblock a Read that's waiting on follow-mode
	// log activity. Without this, a browser close (gateway drop /
	// kubelet probe failure / user close-tab) leaves the read
	// blocked until the kernel times out the TCP socket — minutes,
	// not the seconds we want. The defer above will Close again,
	// which is idempotent for io.Pipe.
	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-r.Context().Done():
			_ = rc.Close()
		case <-stopCh:
		}
	}()

	// SSE headers. Cache-Control: no-store stops intermediary proxies
	// from buffering the response (and stalling the stream from the
	// client's POV). X-Accel-Buffering: no specifically disables nginx
	// proxy buffering — required when the kubernetes ingress is nginx.
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		// All Go stdlib http handlers support flushing — this would
		// only fire under a buggy middleware that wraps the writer.
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Heartbeat ticker: emit a comment line every 30s so middleware-
	// imposed read timeouts don't close the connection during a
	// quiet sandbox. ":\n\n" is the SSE spec's no-op comment.
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// The Logs ReadCloser delivers newline-separated text. Read in
	// modest chunks and emit each line as one SSE event so the
	// client sees each log entry as a distinct EventSource message.
	buf := make([]byte, 4096)
	var pending []byte
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := w.Write([]byte(":\n\n")); err != nil {
				return
			}
			flusher.Flush()
		default:
		}

		n, readErr := rc.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for {
				nl := -1
				for i, b := range pending {
					if b == '\n' {
						nl = i
						break
					}
				}
				if nl < 0 {
					break
				}
				line := pending[:nl]
				pending = pending[nl+1:]
				if len(line) == 0 {
					continue
				}
				// SSE event: one data line. The trailing \n\n
				// delimits the event for the client parser.
				if _, err := fmt.Fprintf(w, "data: %s\n\n", line); err != nil {
					return
				}
				flusher.Flush()
			}
		}
		if readErr != nil {
			// EOF on the read pipe means follow=false snapshot
			// finished. Any other error closes the stream.
			return
		}
	}
}

// parseLogsOpts pulls the SSE query string into the same
// sandbox.LogsOptions shape the gRPC Logs path uses. Defaults match
// the gRPC behavior except follow defaults to TRUE here — SSE is
// almost always used for live tailing, so making the caller pass
// ?follow=1 each time would be a footgun.
func parseLogsOpts(r *http.Request) sandbox.LogsOptions {
	q := r.URL.Query()

	opts := sandbox.LogsOptions{
		Tail:       100,
		Follow:     true,
		Timestamps: false,
	}

	if v := strings.TrimSpace(q.Get("tail")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			opts.Tail = n
		}
	}
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			opts.Since = time.Unix(n, 0)
		}
	}
	if v := strings.TrimSpace(q.Get("timestamps")); v != "" {
		opts.Timestamps = parseBoolQuery(v)
	}
	if v := strings.TrimSpace(q.Get("follow")); v != "" {
		opts.Follow = parseBoolQuery(v)
	}
	return opts
}

// parseBoolQuery accepts the common "true|false|1|0|yes|no" forms.
// Falsy unrecognized inputs default to false so a typo doesn't
// accidentally flip follow on/off.
func parseBoolQuery(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

