package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const MaxBodyBytes = 4 << 20

type Client struct {
	endpoint, key string
	http          *http.Client
}

// HTTPError intentionally excludes upstream bodies, which can echo prompts or credentials.
type HTTPError struct {
	StatusCode int
	RetryAfter string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("decision provider returned HTTP %d", e.StatusCode)
}

func NewClient(baseURL, apiKey string, transport http.RoundTripper) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("decision base URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{endpoint: strings.TrimRight(baseURL, "/") + "/systemone", key: apiKey, http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Decide(ctx context.Context, r Request) (Response, error) {
	var out Response
	if err := r.Validate(); err != nil {
		return out, err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return out, err
	}
	if len(body) > MaxBodyBytes {
		return out, errors.New("decision request exceeds 4 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return out, errors.New("could not create decision request")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, errors.New("decision provider unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, &HTTPError{StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil || len(payload) > MaxBodyBytes {
		return out, errors.New("invalid decision response body")
	}
	if json.Unmarshal(payload, &out) != nil {
		return out, errors.New("invalid decision response JSON")
	}
	if err := out.Validate(r); err != nil {
		return Response{}, err
	}
	return out, nil
}
