package streaming

import (
	"io"
	"net/http"
)

// ZeroCopyWriter optimizes data transfer by leveraging io.ReaderFrom if available.
// This allows the Go runtime to use optimization like splice(2) or sendfile(2)
// where supported, avoiding user-space buffer copies.
type ZeroCopyWriter struct {
	w http.ResponseWriter
}

// NewZeroCopyWriter creates a wrapper around http.ResponseWriter.
func NewZeroCopyWriter(w http.ResponseWriter) *ZeroCopyWriter {
	return &ZeroCopyWriter{w: w}
}

// WriteFrom streams data from src to the response writer.
// If the underlying ResponseWriter supports ReadFrom (which standard net/http does),
// it delegates to that implementation for potential zero-copy transfer.
func (z *ZeroCopyWriter) WriteFrom(src io.Reader) (int64, error) {
	if rf, ok := z.w.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	// Fallback to io.Copy which uses a 32KB buffer
	return io.Copy(z.w, src)
}

// Write implements io.Writer.
func (z *ZeroCopyWriter) Write(p []byte) (int, error) {
	return z.w.Write(p)
}

// Flush ensures any buffered data is sent to the client.
func (z *ZeroCopyWriter) Flush() {
	if f, ok := z.w.(http.Flusher); ok {
		f.Flush()
	}
}

