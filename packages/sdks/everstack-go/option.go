package everstack

import (
	"net/http"
	"time"
)

type config struct {
	baseURL    string
	apiKey     string
	provider   string
	orgID      string
	userID     string
	headers    map[string]string
	httpClient *http.Client
	timeout    int // seconds
}

// Option configures the Client.
type Option func(*config)

// WithBaseURL sets a custom API base URL.
func WithBaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// WithProvider sets the default provider header (e.g. "@openai").
func WithProvider(provider string) Option {
	return func(c *config) { c.provider = provider }
}

// WithOrgID sets the organization ID header.
func WithOrgID(orgID string) Option {
	return func(c *config) { c.orgID = orgID }
}

// WithUserID sets the user ID header.
func WithUserID(userID string) Option {
	return func(c *config) { c.userID = userID }
}

// WithHeaders sets additional HTTP headers sent with every request.
func WithHeaders(headers map[string]string) Option {
	return func(c *config) { c.headers = headers }
}

// WithHTTPClient sets a custom *http.Client for all requests.
func WithHTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// WithTimeout sets the request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = int(d.Seconds()) }
}
