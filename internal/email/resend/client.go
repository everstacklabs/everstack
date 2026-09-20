package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const resendAPIURL = "https://api.resend.com/emails"

// sendTimeout bounds a single Resend API call. Without it the zero-value
// http.Client waits forever, so one hung connection pins the caller's
// goroutine indefinitely. Every caller here sits on a user-facing auth
// request (login, magic link, invite), so the user just watches it spin.
const sendTimeout = 10 * time.Second

// Client wraps the Resend API client
type Client struct {
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Resend client
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: sendTimeout},
	}
}

// SendEmailParams holds parameters for sending an email
type SendEmailParams struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html,omitempty"`
	Text    string   `json:"text,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
	Tags    []Tag    `json:"tags,omitempty"`
}

// Tag represents an email tag for tracking
type Tag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SendEmailResponse is the response from sending an email
type SendEmailResponse struct {
	ID string `json:"id"`
}

// SendEmail sends an email via Resend
func (c *Client) SendEmail(ctx context.Context, params SendEmailParams) (*SendEmailResponse, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal email params: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var errorResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errorResp)
		return nil, fmt.Errorf("resend API error (%d): %v", resp.StatusCode, errorResp)
	}

	var result SendEmailResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}
