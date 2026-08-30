package ratelimit

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/providers/catalog"
)

// RateLimitInfo contains rate limit information from provider responses
type RateLimitInfo struct {
	Provider  string    `json:"provider"`
	KeyID     string    `json:"key_id,omitempty"` // NEW: Specific API key ID (optional)
	Model     string    `json:"model,omitempty"`
	Timestamp time.Time `json:"timestamp"`

	// Request limits
	RequestLimit     int `json:"request_limit"`
	RequestRemaining int `json:"request_remaining"`
	RequestReset     int `json:"request_reset_seconds"`

	// Token limits
	TokenLimit     int `json:"token_limit"`
	TokenRemaining int `json:"token_remaining"`
	TokenReset     int `json:"token_reset_seconds"`

	// Rate limited?
	IsRateLimited bool `json:"is_rate_limited"`
	RetryAfter    int  `json:"retry_after_seconds,omitempty"`
}

// Monitor tracks rate limit information across providers
type Monitor struct {
	mu              sync.RWMutex
	providers       map[string]*RateLimitInfo
	subscribers     map[int]func(RateLimitInfo)
	nextSubID       int
	providerCatalog *catalog.Catalog
}

// Global instance
var GlobalMonitor = NewMonitor()

// DebugHeaderDiscovery enables scanning every response for unknown rate-limit headers.
// Disabled by default since it runs on every API call. Enable for development only.
var DebugHeaderDiscovery = false

func NewMonitor() *Monitor {
	// Try to load provider catalog, but don't fail if it's not available
	cat, _ := catalog.New()
	return &Monitor{
		providers:       make(map[string]*RateLimitInfo),
		subscribers:     make(map[int]func(RateLimitInfo)),
		providerCatalog: cat,
	}
}

// SetProviderCatalog updates the provider catalog (useful for dependency injection)
func (m *Monitor) SetProviderCatalog(cat *catalog.Catalog) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerCatalog = cat
}

// NewCatalogFromDefaults creates a new provider catalog from default configuration
func NewCatalogFromDefaults() (*catalog.Catalog, error) {
	return catalog.New()
}

// GetKnownProviders returns a list of all known providers from the catalog
func (m *Monitor) GetKnownProviders() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.providerCatalog == nil {
		// Fallback to hardcoded providers if catalog not available
		return []string{"openai", "anthropic"}
	}

	return m.providerCatalog.GetAllProviderNames()
}

// Subscribe to rate limit updates. Returns an unsubscribe function that must be
// called when the subscriber is done (e.g., when an SSE connection closes).
func (m *Monitor) Subscribe(callback func(RateLimitInfo)) func() {
	m.mu.Lock()
	id := m.nextSubID
	m.nextSubID++
	m.subscribers[id] = callback
	m.mu.Unlock()

	return func() {
		m.mu.Lock()
		delete(m.subscribers, id)
		m.mu.Unlock()
	}
}

// Update rate limit info for a provider or specific API key
func (m *Monitor) Update(info RateLimitInfo) {
	// Build tracking key: "provider:keyID" if KeyID is set, otherwise just "provider"
	key := info.Provider
	if info.KeyID != "" {
		key = info.Provider + ":" + info.KeyID
	}

	m.mu.Lock()
	m.providers[key] = &info
	// Periodically evict stale entries to prevent unbounded growth
	m.evictStaleProviders()
	subscribers := make([]func(RateLimitInfo), 0, len(m.subscribers))
	for _, cb := range m.subscribers {
		subscribers = append(subscribers, cb)
	}
	m.mu.Unlock()

	// Log rate limit status
	logFields := map[string]interface{}{
		"provider":          info.Provider,
		"model":             info.Model,
		"request_remaining": info.RequestRemaining,
		"token_remaining":   info.TokenRemaining,
		"is_rate_limited":   info.IsRateLimited,
		"retry_after":       info.RetryAfter,
	}
	if info.KeyID != "" {
		logFields["key_id"] = info.KeyID
	}
	
	// Build structured payload for rate limit update
	payload := logger.NewPayload().
		WithRateLimit(int64(info.RequestRemaining), int64(info.TokenRemaining), info.IsRateLimited).
		WithProvider(info.Provider, info.Model, "", 0, 0).
		Build()
	
	logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventRateLimitUpdated).
		WithPayload(payload).
		WithFields(logFields).Info("rate limit status updated")

	// Notify subscribers
	for _, callback := range subscribers {
		go callback(info) // Don't block
	}
}

// GetStatus returns current rate limit status for a provider or API key
// Parameter can be: provider name, keyID, or "provider:keyID"
func (m *Monitor) GetStatus(providerOrKeyID string) *RateLimitInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if info, exists := m.providers[providerOrKeyID]; exists {
		// Return a copy to avoid races
		copy := *info
		return &copy
	}
	return nil
}

// IsRateLimited checks if a provider or API key is currently rate limited
// Parameter can be: provider name, keyID, or "provider:keyID"
func (m *Monitor) IsRateLimited(providerOrKeyID string) bool {
	info := m.GetStatus(providerOrKeyID)
	if info == nil {
		return false
	}

	// Check if we're still in the retry period
	if info.IsRateLimited && info.RetryAfter > 0 {
		elapsed := time.Since(info.Timestamp)
		return elapsed < time.Duration(info.RetryAfter)*time.Second
	}

	// Check if we're close to limits (less than 10 requests or 1000 tokens)
	return info.RequestRemaining < 10 || info.TokenRemaining < 1000
}

// Unified rate-limit header mapping and parsing (moved maps/types to config.go)

func firstInt(headers http.Header, keys []string) (int, bool) {
	for _, k := range keys {
		if v := strings.TrimSpace(headers.Get(k)); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// firstResetSeconds tries duration (e.g., "12s"), integer seconds, or RFC3339 timestamp → seconds until reset
func firstResetSeconds(headers http.Header, keys []string) (int, bool) {
	for _, k := range keys {
		v := strings.TrimSpace(headers.Get(k))
		if v == "" {
			continue
		}
		// Try duration
		if d, err := time.ParseDuration(v); err == nil {
			return int(d.Seconds()), true
		}
		// Try integer seconds
		if n, err := strconv.Atoi(v); err == nil {
			return n, true
		}
		// Try RFC3339 timestamp
		if ts, err := time.Parse(time.RFC3339, v); err == nil {
			return int(time.Until(ts).Seconds()), true
		}
	}
	return 0, false
}

// ParseHeaders extracts rate limit info from provider response headers using unified mapping
func ParseHeaders(headers http.Header, provider, model string) RateLimitInfo {
	p := strings.ToLower(strings.TrimSpace(provider))
	m, ok := providerHeaderMaps[p]
	if !ok {
		m = stdHeaderMap
	}

	info := RateLimitInfo{Provider: provider, Model: model, Timestamp: time.Now()}

	if n, ok := firstInt(headers, m.ReqLimit); ok {
		info.RequestLimit = n
	}
	if n, ok := firstInt(headers, m.ReqRemaining); ok {
		info.RequestRemaining = n
	}
	if n, ok := firstResetSeconds(headers, m.ReqReset); ok {
		info.RequestReset = n
	}

	if n, ok := firstInt(headers, m.TokenLimit); ok {
		info.TokenLimit = n
	}
	if n, ok := firstInt(headers, m.TokenRemaining); ok {
		info.TokenRemaining = n
	}
	if n, ok := firstResetSeconds(headers, m.TokenReset); ok {
		info.TokenReset = n
	}

	if n, ok := firstInt(headers, m.RetryAfter); ok {
		info.IsRateLimited = true
		info.RetryAfter = n
	}

	// Discovery: log unknown rate-limit style headers (gated behind debug flag to avoid hot-path overhead)
	if DebugHeaderDiscovery {
		if unknown := discoverUnknownRateLimitHeaders(headers, m); len(unknown) > 0 {
			logger.WithFields("provider", provider, "unknown_headers", strings.Join(unknown, ",")).Debug("ratelimit: discovered provider headers")
		}
	}
	return info
}

// discoverUnknownRateLimitHeaders returns present header keys that look like rate-limit headers
// but are not covered by the provided mapping.
func discoverUnknownRateLimitHeaders(headers http.Header, mapping rateLimitHeaderMap) []string {
	known := make(map[string]struct{})
	add := func(keys []string) {
		for _, k := range keys {
			known[strings.ToLower(k)] = struct{}{}
		}
	}
	add(mapping.ReqLimit)
	add(mapping.ReqRemaining)
	add(mapping.ReqReset)
	add(mapping.TokenLimit)
	add(mapping.TokenRemaining)
	add(mapping.TokenReset)
	add(mapping.RetryAfter)

	var out []string
	for k := range headers {
		lk := strings.ToLower(k)
		if _, ok := known[lk]; ok {
			continue
		}
		if strings.Contains(lk, "ratelimit") || strings.Contains(lk, "rate-limit") || strings.Contains(lk, "x-rate-limit") {
			out = append(out, k)
		}
		if lk == "retry-after" { // include if not already in mapping
			out = append(out, k)
		}
	}
	return out
}

// ShouldBackoff returns whether requests to a provider should be backed off
func (m *Monitor) ShouldBackoff(provider string) (bool, time.Duration) {
	info := m.GetStatus(provider)
	if info == nil {
		return false, 0
	}

	// If explicitly rate limited
	if info.IsRateLimited && info.RetryAfter > 0 {
		elapsed := time.Since(info.Timestamp)
		remaining := time.Duration(info.RetryAfter)*time.Second - elapsed
		if remaining > 0 {
			return true, remaining
		}
	}

	// Proactive backoff when close to limits
	if info.RequestRemaining <= 5 {
		return true, time.Duration(info.RequestReset) * time.Second
	}

	if info.TokenRemaining <= 500 {
		return true, time.Duration(info.TokenReset) * time.Second
	}

	return false, 0
}

// providerEntryTTL is how long a provider's rate limit info is kept after the last update.
const providerEntryTTL = 1 * time.Hour

// evictStaleProviders removes provider entries that haven't been updated within the TTL.
// Called periodically from Update to prevent unbounded growth.
func (m *Monitor) evictStaleProviders() {
	cutoff := time.Now().Add(-providerEntryTTL)
	for key, info := range m.providers {
		if info.Timestamp.Before(cutoff) {
			delete(m.providers, key)
		}
	}
}
