package features

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// PollerConfig configures the manifest poller
type PollerConfig struct {
	// EdgeURL is the base URL for the edge endpoint (e.g., https://features.everstack.com)
	EdgeURL string

	// PollInterval is how often to fetch new manifests (default: 60s)
	PollInterval time.Duration

	// CacheDir is where to cache manifests locally (default: ~/.everstack/)
	CacheDir string
}

// ManifestPoller polls for feature manifests from the Cloudflare KV edge
type ManifestPoller struct {
	mu sync.RWMutex

	config     PollerConfig
	tenantID   string
	tier       string
	publicKeys map[string]ed25519.PublicKey

	currentManifest *Manifest
	currentOverlay  *TenantOverlay
	resolved        map[string]*ResolvedFeature

	subscribers []func(map[string]*ResolvedFeature)
	httpClient  *http.Client

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewManifestPoller creates a new poller with the given config and public keys
func NewManifestPoller(config PollerConfig, publicKeys map[string]ed25519.PublicKey) *ManifestPoller {
	if config.PollInterval <= 0 {
		config.PollInterval = 60 * time.Second
	}
	if config.CacheDir == "" {
		home, _ := os.UserHomeDir()
		config.CacheDir = filepath.Join(home, ".everstack")
	}

	return &ManifestPoller{
		config:     config,
		publicKeys: publicKeys,
		resolved:   make(map[string]*ResolvedFeature),
		httpClient: &http.Client{Timeout: 15 * time.Second},
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

// SetTenantInfo sets the tenant ID and tier for resolution
func (p *ManifestPoller) SetTenantInfo(tenantID, tier string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tenantID = tenantID
	p.tier = tier
}

// Subscribe adds a callback for when resolved features change
func (p *ManifestPoller) Subscribe(fn func(map[string]*ResolvedFeature)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subscribers = append(p.subscribers, fn)
}

// Start begins polling for manifests
func (p *ManifestPoller) Start(ctx context.Context) {
	go func() {
		defer close(p.doneCh)

		// Try to load cached manifest first
		p.loadCachedManifest()

		// Initial fetch
		p.poll(ctx)

		ticker := time.NewTicker(p.config.PollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.poll(ctx)
			case <-p.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop stops the poller
func (p *ManifestPoller) Stop() {
	close(p.stopCh)
	<-p.doneCh
}

// GetResolved returns the current resolved features
func (p *ManifestPoller) GetResolved() map[string]*ResolvedFeature {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]*ResolvedFeature, len(p.resolved))
	for k, v := range p.resolved {
		copy := *v
		result[k] = &copy
	}
	return result
}

func (p *ManifestPoller) poll(ctx context.Context) {
	// Fetch global manifest
	globalData, err := p.fetchFromEdge(ctx, "features:global")
	if err != nil {
		logger.WithError(err).Warn("manifest_poller: failed to fetch global manifest")
	}

	// Fetch tenant overlay
	p.mu.RLock()
	tenantID := p.tenantID
	p.mu.RUnlock()

	var overlayData []byte
	if tenantID != "" {
		key := fmt.Sprintf("features:tenant:%s", tenantID)
		overlayData, err = p.fetchFromEdge(ctx, key)
		if err != nil {
			logger.WithError(err).Debug("manifest_poller: failed to fetch tenant overlay (may not exist)")
		}
	}

	changed := false

	if globalData != nil {
		manifest, err := VerifyManifest(p.publicKeys, globalData)
		if err != nil {
			logger.WithError(err).Warn("manifest_poller: failed to verify global manifest")
		} else {
			p.mu.Lock()
			p.currentManifest = manifest
			p.mu.Unlock()
			p.cacheToFile("features_global.json", globalData)
			changed = true
		}
	}

	if overlayData != nil {
		overlay, err := VerifyTenantOverlay(p.publicKeys, overlayData)
		if err != nil {
			logger.WithError(err).Warn("manifest_poller: failed to verify tenant overlay")
		} else {
			p.mu.Lock()
			p.currentOverlay = overlay
			p.mu.Unlock()
			p.cacheToFile("features_tenant.json", overlayData)
			changed = true
		}
	}

	if changed {
		p.resolve()
	}
}

// resolve evaluates features based on manifest + tier + tenant overlay
func (p *ManifestPoller) resolve() {
	p.mu.Lock()

	if p.currentManifest == nil {
		p.mu.Unlock()
		return
	}

	tier := p.tier
	overlay := p.currentOverlay
	resolved := make(map[string]*ResolvedFeature)

	for key, def := range p.currentManifest.Features {
		available := false

		// Status-aware resolution
		switch def.Status {
		case "deprecated":
			// Never available, overrides ignored
			continue

		case "development":
			// Only available via explicit tenant override
			available = false

		case "beta":
			// Enterprise tier only, regardless of min_tier
			if def.Enabled {
				available = tierRank(tier) >= tierRank("enterprise")
			}

		case "released", "":
			// Normal resolution: enabled + min_tier (empty = legacy data)
			if def.Enabled {
				if def.MinTier == "" {
					available = true
				} else {
					available = tierRank(tier) >= tierRank(def.MinTier)
				}
			}

		default:
			// Unknown status treated as development (safe default)
			available = false
		}

		// Apply tenant overlay (deprecated already skipped via continue)
		if overlay != nil {
			if ov, ok := overlay.Overrides[key]; ok {
				if ov.ExpiresAt == nil || ov.ExpiresAt.After(time.Now()) {
					available = ov.Enabled
				}
			}
		}

		if available {
			resolved[key] = &ResolvedFeature{
				Name:        def.Name,
				Description: def.Description,
				Status:      def.Status,
				Categories:  def.Categories,
				Enabled:     true,
			}
		}
	}

	p.resolved = resolved
	subs := make([]func(map[string]*ResolvedFeature), len(p.subscribers))
	copy(subs, p.subscribers)
	p.mu.Unlock()

	// Notify subscribers outside lock
	for _, fn := range subs {
		fn(resolved)
	}

	logger.Debugf("manifest_poller: resolved %d features for tier %s", len(resolved), tier)
}

func (p *ManifestPoller) fetchFromEdge(ctx context.Context, key string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s", p.config.EdgeURL, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("edge returned status %d for key %s", resp.StatusCode, key)
	}

	return io.ReadAll(resp.Body)
}

func (p *ManifestPoller) cacheToFile(filename string, data []byte) {
	dir := p.config.CacheDir
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	path := filepath.Join(dir, filename)
	_ = os.WriteFile(path, data, 0600)
}

func (p *ManifestPoller) loadCachedManifest() {
	dir := p.config.CacheDir

	globalPath := filepath.Join(dir, "features_global.json")
	if data, err := os.ReadFile(globalPath); err == nil {
		if manifest, err := VerifyManifest(p.publicKeys, data); err == nil {
			p.mu.Lock()
			p.currentManifest = manifest
			p.mu.Unlock()
			logger.Info("manifest_poller: loaded cached global manifest")
		}
	}

	overlayPath := filepath.Join(dir, "features_tenant.json")
	if data, err := os.ReadFile(overlayPath); err == nil {
		if overlay, err := VerifyTenantOverlay(p.publicKeys, data); err == nil {
			p.mu.Lock()
			p.currentOverlay = overlay
			p.mu.Unlock()
			logger.Info("manifest_poller: loaded cached tenant overlay")
		}
	}

	// Resolve with whatever we loaded from cache
	p.resolve()
}

// tierRank returns the numeric rank for tier comparison
func tierRank(tier string) int {
	switch tier {
	case "free":
		return 0
	case "basic":
		return 1
	case "pro":
		return 2
	case "enterprise":
		return 3
	default:
		return 0
	}
}

// ParseManifestJSON parses a manifest from raw JSON without verification.
// Only use this for loading hard-coded defaults where the data is trusted.
func ParseManifestJSON(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}
