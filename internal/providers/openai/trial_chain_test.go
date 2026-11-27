package openai

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/billingtrial"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"
)

// trialGateSink is an active billing sink with the model trial gate.
type trialGateSink struct{}

func (trialGateSink) Active() bool    { return true }
func (trialGateSink) Markup() float64 { return 1 }
func (trialGateSink) ResolveOrg(context.Context) (string, bool, error) {
	return "11111111-1111-1111-1111-111111111111", true, nil
}
func (trialGateSink) Settle(context.Context, billingmeter.SettleUnit)      {}
func (trialGateSink) CountUsage(string, billingmeter.UsageDelta)           {}
func (trialGateSink) ShadowAdmit(billingmeter.AdmissionRequest)            {}
func (trialGateSink) RefundTrial(context.Context, string, uuid.UUID) error { return nil }
func (trialGateSink) GrantTrial(context.Context, string) (bool, error)     { return false, nil }
func (trialGateSink) TrialSettings(context.Context) (billingtrial.Settings, error) {
	return billingtrial.Settings{Enabled: true, Model: "gpt-5.4-mini", MaxOutputTokens: 300, MaxInputBytes: 4000}, nil
}
func (trialGateSink) TakeTrial(context.Context, string) (billingtrial.TakeResult, error) {
	return billingtrial.TakeResult{}, &billingtrial.RefusedError{Reason: billingtrial.ReasonExhausted}
}

// Built exactly as the bundle builds the trial row (the openai factory, its
// logging, compact and tracing layers around the billing seam), the provider
// gives no path to the raw OpenAI client: not through UnwrapTo, not through
// the registry's Responses lookup, and not through any capability helper.
func TestTrialProviderChainCannotReachTheRawClient(t *testing.T) {
	f, ok := factory.Default.Get("openai")
	if !ok {
		t.Fatal("openai factory not registered")
	}
	p, err := f(factory.AggregatedInput{
		Provider:          "openai",
		Models:            []string{"gpt-5.4-mini"},
		APIKey:            "sk-test-not-a-real-key",
		BillingMeter:      trialGateSink{},
		BillingProvenance: billingmeter.ProvenanceTrial,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gw.UnwrapTo[*Provider](p); ok {
		t.Fatal("UnwrapTo reached the raw OpenAI client")
	}
	if _, ok := gw.UnwrapTo[gw.ResponsesProvider](p); ok {
		t.Fatal("UnwrapTo reached the Responses API")
	}
	if _, ok := gw.UnwrapAudioProvider(p); ok {
		t.Fatal("audio reachable on the trial row")
	}
	if _, ok := gw.UnwrapTo[gw.ImageProvider](p); ok {
		t.Fatal("images reachable on the trial row")
	}
	if _, ok := gw.UnwrapTo[gw.ModerationProvider](p); ok {
		t.Fatal("moderation reachable on the trial row")
	}
	reg := gw.NewRegistry()
	reg.Register(p)
	if _, ok := reg.GetResponsesProvider("openai"); ok {
		t.Fatal("the registry exposes the Responses API on the trial row")
	}
	// And the gate is in the path: a refused take never dispatches.
	_, err = p.Chat(context.Background(), gw.ChatCompletionRequest{Model: "gpt-5.4-mini",
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hi"))}})
	if r, ok := billingmeter.AsModelTrialRefusal(err); !ok || r.Reason != billingmeter.ReasonModelTrialExhausted {
		t.Fatalf("err = %v, want model_trial_exhausted", err)
	}
}
