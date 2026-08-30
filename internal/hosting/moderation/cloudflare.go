package moderation

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

const cloudflareAPIBaseURL = "https://api.cloudflare.com/client/v4"

type CloudflareKVConfig struct {
	AccountID   string
	NamespaceID string
	APIToken    string
	BaseURL     string
	HTTPClient  *http.Client
}

// CloudflareKVEnforcer projects moderation state into the SITES_KILL Workers
// KV namespace consumed before the sites Worker's Cache API lookup.
type CloudflareKVEnforcer struct {
	accountID   string
	namespaceID string
	apiToken    string
	baseURL     string
	httpClient  *http.Client
}

func NewCloudflareKVEnforcer(cfg CloudflareKVConfig) (*CloudflareKVEnforcer, error) {
	cfg.AccountID = strings.TrimSpace(cfg.AccountID)
	cfg.NamespaceID = strings.TrimSpace(cfg.NamespaceID)
	cfg.APIToken = strings.TrimSpace(cfg.APIToken)
	if cfg.AccountID == "" || cfg.NamespaceID == "" || cfg.APIToken == "" {
		return nil, errors.New("Cloudflare KV account, namespace, and API token are required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = cloudflareAPIBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &CloudflareKVEnforcer{
		accountID:   cfg.AccountID,
		namespaceID: cfg.NamespaceID,
		apiToken:    cfg.APIToken,
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		httpClient:  client,
	}, nil
}

func (e *CloudflareKVEnforcer) Apply(ctx context.Context, action Action) error {
	if action.SiteID == "" || action.Generation <= 0 {
		return errors.New("moderation action site and generation are required")
	}
	var disabled bool
	switch action.Kind {
	case ActionKindTakedown:
		disabled = true
	case ActionKindRestore:
		disabled = false
	default:
		return fmt.Errorf("unsupported moderation action %q", action.Kind)
	}
	value, err := json.Marshal(struct {
		ActionID   string `json:"action_id"`
		SiteID     string `json:"site_id"`
		Generation int64  `json:"generation"`
		Disabled   bool   `json:"disabled"`
		Reason     string `json:"reason,omitempty"`
	}{
		ActionID: action.ID, SiteID: action.SiteID, Generation: action.Generation,
		Disabled: disabled, Reason: string(action.Reason),
	})
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf(
		"%s/accounts/%s/storage/kv/namespaces/%s/values/%s",
		e.baseURL,
		url.PathEscape(e.accountID),
		url.PathEscape(e.namespaceID),
		url.PathEscape(action.Slug),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(value))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare KV request failed: %w", err)
	}
	defer resp.Body.Close()
	const maxResponseBytes = 4 << 10
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Cloudflare KV returned HTTP %d", resp.StatusCode)
	}
	if readErr != nil {
		return fmt.Errorf("Cloudflare KV response could not be read: %w", readErr)
	}
	if len(payload) > maxResponseBytes {
		return errors.New("Cloudflare KV response exceeded the safety limit")
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	var envelope struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return errors.New("Cloudflare KV returned an invalid response")
	}
	if !envelope.Success {
		return errors.New("Cloudflare KV rejected the request")
	}
	return nil
}
