package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

func defaultHTTPDo(ctx context.Context, signedURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		// The parse error quotes the URL; never surface it.
		return nil, fmt.Errorf("snapshot: build request failed")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// *url.Error embeds the full request URL, which is a presigned
		// bearer credential. Keep only the underlying cause.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, fmt.Errorf("snapshot: http get: %v", urlErr.Err)
		}
		return nil, fmt.Errorf("snapshot: http get failed")
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
