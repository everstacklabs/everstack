// Package systemone integrates TypeSafe Jev and the self-hosted Kev and Laya servers.
package systemone

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/httpclient"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	"github.com/everstacklabs/everstack/internal/services/api_key_selector"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/pkg/decision"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type Provider struct {
	name    string
	models  map[string]bool
	client  *decision.Client
	catalog *catalog.Cache
}

func init() {
	for _, name := range []string{"typesafe", "kev", "laya"} {
		factory.Default.Register(name, newFactory(name))
	}
}
func newFactory(name string) factory.ProviderFactory {
	return func(in factory.AggregatedInput) (gw.Provider, error) {
		// Decide does not pass the billing seam (it is not Chat, ChatStream
		// or Embed) and decision models have no wallet price, so a
		// platform-owned configuration (organization_id IS NULL on a managed
		// gateway) never serves decisions: that would be free platform
		// inference. Provenance comes from ownership, not key labels, so
		// this also covers the legacy single-key path and rows labelled
		// "config". Revisit when decision usage is priced and metered.
		if in.BillingProvenance == billingmeter.ProvenancePlatform {
			return nil, fmt.Errorf("%s requires a customer-owned key", name)
		}
		key := in.APIKey
		if len(in.APIKeys) > 0 {
			keys := []*provider_api_keys.ProviderAPIKey{}
			for _, k := range in.APIKeys {
				// These integrations currently use customer-owned endpoints/keys. Reject
				// platform-funded inference until decision usage is wired to wallet billing.
				if k.Source == "platform" {
					continue
				}
				keys = append(keys, &provider_api_keys.ProviderAPIKey{ID: k.ID, KeyName: k.KeyName, KeyEncrypted: k.KeyEncrypted, Weight: k.Weight, IsActive: k.IsActive, Source: k.Source})
			}
			if len(keys) == 0 {
				return nil, fmt.Errorf("%s requires a customer-owned key", name)
			}
			selected, err := api_key_selector.New(ratelimit.GlobalMonitor).SelectAPIKey(context.Background(), keys, in.StickyKey, name)
			if err != nil {
				return nil, fmt.Errorf("no available %s API key", name)
			}
			key = selected.KeyEncrypted
		}
		base := in.BaseURL
		if name == "typesafe" {
			if strings.TrimSpace(key) == "" {
				return nil, fmt.Errorf("typesafe requires an API key")
			}
			if base == "" {
				base = "https://api.typesafe.ai/v1"
			}
		} else if base == "" {
			return nil, fmt.Errorf("%s requires an explicit self-hosted base_url ending in /v1", name)
		}
		client, err := decision.NewClient(base, key, httpclient.Default().Transport)
		if err != nil {
			return nil, err
		}
		models := map[string]bool{}
		for _, m := range in.Models {
			models[strings.ToLower(m)] = true
		}
		return &Provider{name: name, models: models, client: client, catalog: in.CatalogCache}, nil
	}
}
func (p *Provider) Name() string                { return p.name }
func (p *Provider) SupportsModel(m string) bool { return p.models[strings.ToLower(m)] }
func (p *Provider) Decide(ctx context.Context, r decision.Request) (decision.Response, error) {
	if !p.SupportsModel(r.Model) {
		return decision.Response{}, fmt.Errorf("decision model is not enabled")
	}
	ctx, span := otel.Tracer("everstack/systemone").Start(ctx, "systemone.decide")
	defer span.End()
	span.SetAttributes(attribute.String("gen_ai.system", p.name), attribute.String("gen_ai.request.model", r.Model), attribute.String("gen_ai.operation.name", "decision"), attribute.String("key.source", "byok"))
	result, err := p.client.Decide(ctx, r)
	if err != nil {
		span.SetStatus(codes.Error, "decision provider failed")
		return result, err
	}
	span.SetAttributes(attribute.Int64("gen_ai.usage.input_tokens", result.Usage.InputTokens), attribute.Int64("gen_ai.usage.output_tokens", result.Usage.OutputTokens))
	attrs.SetLLMTokens(span, result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.InputTokens+result.Usage.OutputTokens)
	if p.catalog != nil {
		if model, ok := p.catalog.GetModel(p.name, r.Model); ok {
			inputCost := float64(result.Usage.InputTokens) * model.InputCostPer1k / 1000
			outputCost := float64(result.Usage.OutputTokens) * model.OutputCostPer1k / 1000
			attrs.SetLLMCost(span, inputCost, outputCost, inputCost+outputCost)
		}
	}
	return result, nil
}
func (p *Provider) Chat(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{}, gw.ErrNotSupported{Operation: "chat", Provider: p.name}
}
func (p *Provider) ChatStream(context.Context, gw.ChatCompletionRequest, func(gw.ChatResponseChunk) error) error {
	return gw.ErrNotSupported{Operation: "chat", Provider: p.name}
}
func (p *Provider) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, gw.ErrNotSupported{Operation: "embeddings", Provider: p.name}
}
