package gateway_test

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/billing"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
)

// capableProvider implements every optional capability, like the OpenAI
// adapter does.
type capableProvider struct{}

func (capableProvider) Name() string              { return "capable" }
func (capableProvider) SupportsModel(string) bool { return true }
func (capableProvider) Chat(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{}, nil
}
func (capableProvider) ChatStream(context.Context, gw.ChatCompletionRequest, func(gw.ChatResponseChunk) error) error {
	return nil
}
func (capableProvider) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}
func (capableProvider) GenerateImage(context.Context, gw.ImageGenerationRequest) (gw.ImageGenerationResponse, error) {
	return gw.ImageGenerationResponse{}, nil
}
func (capableProvider) EditImage(context.Context, gw.ImageEditRequest) (gw.ImageEditResponse, error) {
	return gw.ImageEditResponse{}, nil
}
func (capableProvider) CreateImageVariation(context.Context, gw.ImageVariationRequest) (gw.ImageVariationResponse, error) {
	return gw.ImageVariationResponse{}, nil
}
func (capableProvider) Moderate(context.Context, gw.ModerationRequest) (gw.ModerationResponse, error) {
	return gw.ModerationResponse{}, nil
}
func (capableProvider) Rerank(context.Context, gw.RerankRequest) (gw.RerankResponse, error) {
	return gw.RerankResponse{}, nil
}

type activeSink struct{}

func (activeSink) Active() bool                                     { return true }
func (activeSink) Markup() float64                                  { return 1 }
func (activeSink) ResolveOrg(context.Context) (string, bool, error) { return "org", true, nil }
func (activeSink) Settle(context.Context, billingmeter.SettleUnit)  {}
func (activeSink) CountUsage(string, billingmeter.UsageDelta)       {}
func (activeSink) ShadowAdmit(billingmeter.AdmissionRequest)        {}

// TestCapabilityLookupsSeeThroughMiddleware pins the provider chain every
// factory builds (tracing over billing over the base adapter). Images,
// moderation and rerank must be discoverable on a self-hosted or BYOK
// provider, exactly like audio, and must stay hidden behind a metered
// platform wrapper so they can never run unbilled.
func TestCapabilityLookupsSeeThroughMiddleware(t *testing.T) {
	cases := []struct {
		name      string
		provider  gw.Provider
		wantFound bool
	}{
		{"self-hosted (tracing over base)", tracing.NewMiddleware(billing.Wrap(capableProvider{}, billing.Config{})), true},
		{"byok (tracing over counted billing)", tracing.NewMiddleware(billing.Wrap(capableProvider{}, billing.Config{Meter: activeSink{}, Provenance: billingmeter.ProvenanceBYOK})), true},
		{"platform (tracing over metered billing)", tracing.NewMiddleware(billing.Wrap(capableProvider{}, billing.Config{Meter: activeSink{}, Provenance: billingmeter.ProvenancePlatform})), false},
		{"trial (tracing over trial gate)", tracing.NewMiddleware(billing.Wrap(capableProvider{}, billing.Config{Meter: activeSink{}, Provenance: billingmeter.ProvenanceTrial})), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := gw.NewRegistry()
			reg.Register(tc.provider)
			name := tc.provider.Name()

			_, _, image := reg.FindImageProvider()
			_, _, moderation := reg.FindModerationProvider()
			_, _, rerank := reg.FindRerankProvider()
			_, getImage := reg.GetImageProvider(name)
			_, getModeration := reg.GetModerationProvider(name)
			_, getRerank := reg.GetRerankProvider(name)
			for label, got := range map[string]bool{
				"FindImageProvider":      image,
				"FindModerationProvider": moderation,
				"FindRerankProvider":     rerank,
				"GetImageProvider":       getImage,
				"GetModerationProvider":  getModeration,
				"GetRerankProvider":      getRerank,
			} {
				if got != tc.wantFound {
					t.Errorf("%s found = %v, want %v", label, got, tc.wantFound)
				}
			}
		})
	}
}
