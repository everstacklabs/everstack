package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// httpClient is a package-level client tuned for large streaming
// downloads. No request timeout on the body so multi-GB rootfs reads
// don't fail mid-stream; the context governs cancellation instead.
var httpClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          16,
	},
}

func defaultHTTPDo(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("snapshot: build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("snapshot: http get: %w", err)
	}
	return resp, nil
}

func bytesReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}

// countingReader wraps an io.Reader and tracks the number of bytes
// consumed. Used to fill StreamRecord.SizeBytes without buffering the
// entire body.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
