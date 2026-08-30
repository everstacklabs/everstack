package api_key_selector

import (
	"context"
	"errors"
	"hash/fnv"
	"sort"

	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
)

var (
	// ErrAllKeysRateLimited is returned when all API keys are rate-limited
	ErrAllKeysRateLimited = errors.New("all API keys are rate-limited")
	// ErrNoKeysAvailable is returned when no keys are available
	ErrNoKeysAvailable = errors.New("no API keys available")
)

// Selector selects an API key from a list based on weights and rate limits
type Selector struct {
	rateLimitMonitor *ratelimit.Monitor
}

// New creates a new API key selector
func New(monitor *ratelimit.Monitor) *Selector {
	return &Selector{
		rateLimitMonitor: monitor,
	}
}

// SelectAPIKey selects an API key using weighted sticky-hash algorithm
// This uses the same algorithm as the load balancer in server.go:304-330
// provider is the provider name (e.g., "openai", "anthropic") used for rate limit tracking
func (s *Selector) SelectAPIKey(
	ctx context.Context,
	keys []*provider_api_keys.ProviderAPIKey,
	stickyKey string,
	provider string,
) (*provider_api_keys.ProviderAPIKey, error) {
	if len(keys) == 0 {
		return nil, ErrNoKeysAvailable
	}

	// 1. Filter: Remove rate-limited and inactive keys
	available := s.filterAvailableKeys(keys, provider)

	if len(available) == 0 {
		return nil, ErrAllKeysRateLimited
	}

	// 2. Build cumulative weight distribution (same algorithm as server.go:304-318)
	total := 0
	cumulative := make([]int, len(available))
	for i, key := range available {
		w := key.Weight
		if w <= 0 {
			w = 1
		}
		total += w
		cumulative[i] = total
	}

	// 3. Sticky hash selection (same algorithm as server.go:320-330)
	// Use FNV-32a hash to ensure consistent routing for the same sticky key
	h := fnv.New32a()
	h.Write([]byte(stickyKey))
	r := int(h.Sum32() % uint32(total))

	// Binary search to find the appropriate key
	idx := sort.SearchInts(cumulative, r+1)
	if idx < 0 || idx >= len(available) {
		idx = 0 // Fallback to first key
	}

	return available[idx], nil
}

// filterAvailableKeys removes inactive and rate-limited keys
func (s *Selector) filterAvailableKeys(keys []*provider_api_keys.ProviderAPIKey, provider string) []*provider_api_keys.ProviderAPIKey {
	var available []*provider_api_keys.ProviderAPIKey
	for _, key := range keys {
		if !key.IsActive {
			continue
		}

		// Check rate limit per key ID with format "provider:keyID"
		rateLimitKey := provider + ":" + key.ID
		if s.rateLimitMonitor != nil && s.rateLimitMonitor.IsRateLimited(rateLimitKey) {
			continue
		}

		available = append(available, key)
	}
	return available
}
