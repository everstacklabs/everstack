package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSSEOutlivesOrdinaryRequestTimeoutAndHonorsCancellation(t *testing.T) {
	const requestTimeout = 20 * time.Millisecond
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(5 * requestTimeout):
			_, _ = io.WriteString(w, "data: {\"type\":\"agent.session.idle\"}\n\n")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
		<-r.Context().Done()
	}), nil)
	c.http.Timeout = requestTimeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := c.Subscribe(ctx, "s", StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Next()
	if err != nil || event.Type != "agent.session.idle" {
		t.Fatalf("healthy idle stream was interrupted: event=%s err=%v", event.Type, err)
	}
	if c.http.Timeout != requestTimeout {
		t.Fatal("subscription changed the timeout for ordinary API requests")
	}
	cancel()
	if _, err = stream.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("stream did not honor session cancellation: %v", err)
	}
}

func TestSSEMultilineUnknownEventAndEOF(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		if r.URL.RequestURI() != "/v1/agents/sessions/s/events?stream=true" {
			t.Fatal(r.URL.RequestURI())
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Fatal("accept")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		io.WriteString(w, "event: ignored\ndata: {\"type\":\"future.event\",\ndata: \"new_field\":42}\n\n")
	}), nil)
	s, err := c.Subscribe(context.Background(), "s", StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ev, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "future.event" || ev.Extra["new_field"] == nil {
		t.Fatalf("event=%#v", ev)
	}
	if _, err = s.Next(); !errors.Is(err, ErrStreamEOF) {
		t.Fatalf("EOF=%v", err)
	}
}

func TestSSEOversizedAndMalformed(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		max           int
	}{{"oversized", "data: " + strings.Repeat("x", 100) + "\n\n", 32}, {"malformed", "data: {\n\n", 1024}} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.payload)
			}), func(c *Config) { c.MaxEventBytes = tc.max })
			s, e := c.Subscribe(context.Background(), "s", StreamOptions{})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			_, e = s.Next()
			var pe *Error
			if !errors.As(e, &pe) || pe.Kind != ErrorProtocol {
				t.Fatalf("error=%v", e)
			}
		})
	}
}

func TestSSEContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}), nil)
	s, err := c.Subscribe(ctx, "s", StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = s.Next()
	if err == nil {
		t.Fatal("expected cancellation")
	}
	s.Close()
}

func TestStreamCursorRejectedWithoutRequest(t *testing.T) {
	var called bool
	c := testClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }), nil)
	_, err := c.Subscribe(context.Background(), "s", StreamOptions{Cursor: "event_1"})
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrorUnsupported {
		t.Fatalf("error=%v", err)
	}
	if called {
		t.Fatal("unsupported cursor reached server")
	}
}
