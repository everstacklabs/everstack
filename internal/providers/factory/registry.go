package factory

import (
	"context"
	"strings"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/compact"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// SummarizerResolver is a function the compaction middleware calls
// the first time it needs to summarise — wrapping the gateway router
// so the compactor can ask "give me a ChatProvider for model X".
//
// Why a closure rather than a direct router pointer: when a provider
// factory runs, the router is mid-construction and can't resolve the
// summarisation model yet. Caller wires this in start_api.go after the
// router is fully built. nil is acceptable — the middleware will skip
// summarisation tiers (truncation tier still fires).
type SummarizerResolver func(ctx context.Context, model string) (gw.ChatProvider, error)

// AggregatedInput is the combined configuration for a provider derived from
// gateway.yaml entries. The factory should fill in defaults as needed and
// return a configured gw.Provider.
type AggregatedInput struct {
	Provider     string
	APIKey       string        // Legacy: single key
	APIKeys      []APIKeyInput // New: multiple keys from DB or YAML
	BaseURL      string
	Models       []string
	StickyKey    string         // For sticky-hash key selection
	CatalogCache *catalog.Cache // Model catalog for cost calculation
	// CompactConfig configures the gateway-side compaction middleware
	// that wraps every provider built through this factory. Disabled
	// configs short-circuit at runtime so wrapping is cheap.
	CompactConfig compact.Config
	// SummarizerResolve is the lazy provider-resolution callback used
	// by the compaction middleware to obtain the summarisation model.
	// May be nil; see SummarizerResolver doc.
	SummarizerResolve SummarizerResolver
	// Original model blocks for advanced use (optional)
	Raw []validator.ModelConfig
}

// APIKeyInput represents a single API key for provider initialization
// This is a simplified version of domain.ProviderAPIKey for factory use
type APIKeyInput struct {
	ID           string
	KeyName      string
	KeyEncrypted string
	Weight       int
	IsActive     bool
	Source       string
}

type ProviderFactory func(in AggregatedInput) (gw.Provider, error)

type Registry struct {
	factories map[string]ProviderFactory
}

func NewRegistry() *Registry { return &Registry{factories: make(map[string]ProviderFactory)} }

func (r *Registry) Register(name string, f ProviderFactory) { r.factories[strings.ToLower(name)] = f }

func (r *Registry) Get(name string) (ProviderFactory, bool) {
	f, ok := r.factories[strings.ToLower(name)]
	return f, ok
}

// Default global registry to be populated by providers in their init().
var Default = NewRegistry()
