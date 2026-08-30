package everstack

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
)

// Stream provides an iterator over server-sent events of type T.
type Stream[T any] struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	current T
	err     error
	done    bool
}

func newStream[T any](ctx context.Context, t *Transport, method, path string, body any) (*Stream[T], error) {
	rc, err := t.StreamRaw(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	return &Stream[T]{
		body:    rc,
		scanner: bufio.NewScanner(rc),
	}, nil
}

// Next advances to the next event. Returns false when the stream is exhausted or an error occurs.
func (s *Stream[T]) Next() bool {
	if s.done {
		return false
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			s.done = true
			return false
		}

		var v T
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			s.err = err
			s.done = true
			return false
		}
		s.current = v
		return true
	}
	if err := s.scanner.Err(); err != nil {
		s.err = err
	}
	s.done = true
	return false
}

// Current returns the most recently decoded event.
func (s *Stream[T]) Current() T {
	return s.current
}

// Err returns the first error encountered during iteration.
func (s *Stream[T]) Err() error {
	return s.err
}

// Close releases the underlying connection.
func (s *Stream[T]) Close() error {
	if s.body != nil {
		return s.body.Close()
	}
	return nil
}
