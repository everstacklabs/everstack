package hosting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Purger invalidates edge caches for a slug after republish or takedown.
type Purger interface {
	PurgeSlug(ctx context.Context, slug string) error
}

// NoopPurger is used when Cloudflare credentials are not configured; the
// sites-worker's short manifest TTL bounds staleness instead.
type NoopPurger struct{}

func (NoopPurger) PurgeSlug(context.Context, string) error { return nil }

// CloudflarePurger purges {slug}.{baseDomain} via the zone purge API.
type CloudflarePurger struct {
	ZoneID     string
	APIToken   string
	BaseDomain string
	HTTPClient *http.Client
}

func (p *CloudflarePurger) PurgeSlug(ctx context.Context, slug string) error {
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	// Host-based purge invalidates every cached URL for the site in one call.
	body, err := json.Marshal(map[string]any{
		"hosts": []string{fmt.Sprintf("%s.%s", slug, p.BaseDomain)},
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/purge_cache", p.ZoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare purge returned %d", resp.StatusCode)
	}
	return nil
}
