package logbuffer

import (
	"bytes"
	"time"
)

// TeeWriter is an io.Writer that captures written data line-by-line into
// the sandbox log buffer. Used to tee shell output for the Logs tab.
type TeeWriter struct {
	buf    *Buffer
	partial []byte
}

// NewTeeWriter creates a TeeWriter that writes to the given buffer.
func NewTeeWriter(buf *Buffer) *TeeWriter {
	return &TeeWriter{buf: buf}
}

func (w *TeeWriter) Write(p []byte) (int, error) {
	w.partial = append(w.partial, p...)
	for {
		idx := bytes.IndexByte(w.partial, '\n')
		if idx < 0 {
			// Flush partial lines longer than 4KB to avoid unbounded buffering
			if len(w.partial) > 4096 {
				w.buf.Append(Entry{Timestamp: time.Now(), Stream: "stdout", Line: string(w.partial)})
				w.partial = w.partial[:0]
			}
			break
		}
		line := string(w.partial[:idx])
		w.partial = w.partial[idx+1:]
		w.buf.Append(Entry{Timestamp: time.Now(), Stream: "stdout", Line: line})
	}
	return len(p), nil
}
