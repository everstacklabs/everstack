package v1

// End-to-end through the chat handler: the dispatch sites tag every attempt
// (first try, parallel race leg, fallback, configured retry) so the metering
// seam records each one with the right role and charge.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/billing"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// attemptSink captures settle units; every tenant bills to one org. Each
// settle is signalled on settled so a test can wait for the units it expects
// instead of polling the clock.
type attemptSink struct {
	mu      sync.Mutex
	units   []billingmeter.SettleUnit
	settled chan struct{}
}

func newAttemptSink() *attemptSink {
	return &attemptSink{settled: make(chan struct{}, 64)}
}

// waitUnits blocks until at least n units have settled. No deadline of its
// own: a missing settle fails through the test binary's -timeout.
func (s *attemptSink) waitUnits(n int) {
	for {
		s.mu.Lock()
		have := len(s.units)
		s.mu.Unlock()
		if have >= n {
			return
		}
		<-s.settled
	}
}

func (s *attemptSink) Active() bool    { return true }
func (s *attemptSink) Markup() float64 { return 1 }
func (s *attemptSink) ResolveOrg(context.Context) (string, bool, error) {
	return "org-1", true, nil
}
func (s *attemptSink) Settle(_ context.Context, u billingmeter.SettleUnit) {
	s.mu.Lock()
	s.units = append(s.units, u)
	s.mu.Unlock()
	if s.settled != nil {
		select {
		case s.settled <- struct{}{}:
		default:
		}
	}
}
func (s *attemptSink) CountUsage(string, billingmeter.UsageDelta) {}
func (s *attemptSink) ShadowAdmit(billingmeter.AdmissionRequest)  {}

// byModel lists (role, charged) per served model name, sorted.
func (s *attemptSink) byModel() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]string{}
	for _, u := range s.units {
		tag := u.Attempt.Role
		if u.Micros > 0 {
			tag += "+charged"
		}
		out[u.Attempt.Model] = append(out[u.Attempt.Model], tag)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// scriptedModel serves exactly one model with a fixed behaviour.
type scriptedModel struct {
	name  string
	model string
	// untilCancelled makes Chat block until its context is cancelled and
	// then fail: a race leg that can never win, whatever the scheduling.
	untilCancelled bool
	// entered, when set, is closed as Chat starts; waitFor, when set, holds
	// Chat until it is closed. Together they order two race legs without
	// relying on the scheduler.
	entered chan struct{}
	waitFor chan struct{}
	fail    error
	// streamFailAfterContent streams 400 characters, then fails.
	streamFailAfterContent bool
	calls                  int
	mu                     sync.Mutex
}

func (p *scriptedModel) Name() string                { return p.name }
func (p *scriptedModel) SupportsModel(m string) bool { return m == p.model }
func (p *scriptedModel) Chat(ctx context.Context, r gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.entered != nil {
		close(p.entered)
	}
	if p.waitFor != nil {
		<-p.waitFor
	}
	if p.untilCancelled {
		<-ctx.Done()
		return gw.ChatCompletionResponse{}, ctx.Err()
	}
	if p.fail != nil {
		return gw.ChatCompletionResponse{}, p.fail
	}
	return gw.ChatCompletionResponse{ID: "x", Model: r.Model, Choices: []gw.Choice{{Message: gw.NewMessage(gw.RoleAssistant, gw.Text("ok"))}},
		Usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100}}, nil
}
func (p *scriptedModel) ChatStream(ctx context.Context, r gw.ChatCompletionRequest, on func(gw.ChatResponseChunk) error) error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.streamFailAfterContent {
		msg := gw.NewMessage(gw.RoleAssistant, gw.Text(strings.Repeat("o", 400)))
		if err := on(gw.ChatResponseChunk{ID: "x", Model: r.Model, Choices: []gw.ChoiceDelta{{Delta: msg}}}); err != nil {
			return err
		}
		return errors.New("upstream 503: connection reset mid-stream")
	}
	if p.fail != nil {
		return p.fail
	}
	u := gw.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100}
	return on(gw.ChatResponseChunk{ID: "x", Model: r.Model, Usage: &u})
}
func (p *scriptedModel) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, errors.New("not used")
}

const attemptCatalog = `
providers:
  pp: {name: pp, models: [{name: m-primary, input_cost_per_1k: 0.01, output_cost_per_1k: 0.03, capabilities: [chat], status: available}]}
  pa: {name: pa, models: [{name: m-a, input_cost_per_1k: 0.01, output_cost_per_1k: 0.03, capabilities: [chat], status: available}]}
  pb: {name: pb, models: [{name: m-b, input_cost_per_1k: 0.01, output_cost_per_1k: 0.03, capabilities: [chat], status: available}]}
  pc: {name: pc, models: [{name: m-c, input_cost_per_1k: 0.01, output_cost_per_1k: 0.03, capabilities: [chat], status: available}]}
`

func attemptServer(t *testing.T, sink *attemptSink, factors []validator.FallbackFactorConfig, providers ...*scriptedModel) *Server {
	t.Helper()
	cat := catalog.NewCache()
	if err := cat.Load([]byte(attemptCatalog), []byte("providers: {}\n")); err != nil {
		t.Fatal(err)
	}
	cfg := &validator.GatewayConfig{LoadBalancer: validator.LoadBalancerConfig{Fallback: validator.FallbackConfig{Enabled: true, Factors: factors}}}
	s := CreateServerWithContext(context.Background(), cfg, &validator.FeaturesConfig{})
	s.feat.Gateway.EnableStreaming = true
	for _, p := range providers {
		s.defaultBundle.Load().reg.Register(billing.Wrap(p, billing.Config{Meter: sink, Provenance: billingmeter.ProvenancePlatform, KeyID: "k", CatalogCache: cat}))
	}
	return s
}

func chatReq(model string, stream bool) *pb.ChatCompletionRequest {
	return &pb.ChatCompletionRequest{Model: model, Stream: &stream, Messages: []*pb.Message{{Role: pb.Role_ROLE_USER, Content: []*pb.ContentPart{{Type: "text", Data: &pb.ContentPart_Text{Text: "hello"}}}}}}
}

func models(names ...string) []validator.FallbackModelConfig {
	out := make([]validator.FallbackModelConfig, len(names))
	for i, n := range names {
		out[i] = validator.FallbackModelConfig{Model: n}
	}
	return out
}

// Primary fails, a parallel race of two fallbacks runs: the leg that answers
// is served, the leg the winner cancels is an uncharged race loser, and the
// failed first try is recorded uncharged (it generated nothing). The losing
// leg blocks until cancelled, and the winner answers only once the loser is
// inside its provider call, so the outcome does not depend on scheduling (a
// leg first dispatched after the race was already decided would reach no
// provider and leave no record).
func TestHandlerRecordsRaceAndFailedPrimary(t *testing.T) {
	sink := newAttemptSink()
	loserInFlight := make(chan struct{})
	s := attemptServer(t, sink,
		[]validator.FallbackFactorConfig{{Name: "race", Priority: 1, Strategy: "parallel", Models: models("m-a", "m-b")}},
		&scriptedModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")},
		&scriptedModel{name: "pa", model: "m-a", waitFor: loserInFlight},
		&scriptedModel{name: "pb", model: "m-b", untilCancelled: true, entered: loserInFlight},
	)
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	if err := processChatCompletion(ctx, s, chatReq("m-primary", false), func(*pb.ChatCompletionResponse) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// The losing leg settles once the winner's cancel reaches it, possibly
	// after the handler returned: wait for all three attempts.
	sink.waitUnits(3)
	got := sink.byModel()
	if want := []string{billingmeter.RoleFailedFallback}; !equalStrings(got["m-primary"], want) {
		t.Fatalf("primary = %v, want %v", got["m-primary"], want)
	}
	if want := []string{billingmeter.RoleServed + "+charged"}; !equalStrings(got["m-a"], want) {
		t.Fatalf("winner = %v, want %v", got["m-a"], want)
	}
	// Cancelled before it returned anything: a race loser, uncharged.
	if want := []string{billingmeter.RoleRaceLoser}; !equalStrings(got["m-b"], want) {
		t.Fatalf("loser = %v, want %v", got["m-b"], want)
	}
	// One request id across every attempt.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	ids := map[string]bool{}
	for _, u := range sink.units {
		ids[u.Attempt.RequestID] = true
	}
	if len(ids) != 1 {
		t.Fatalf("attempts must share the request id: %v", ids)
	}
}

// A stream whose first try fails before sending anything and is taken over
// by a fallback whose configured retries repeat a failing target: the failed
// first try is an uncharged failed_fallback (it generated nothing), the
// failed first try of the retried target is a failed_fallback, its repeat a
// retry, and the served fallback is charged.
func TestHandlerRecordsStreamFallbackAndRetry(t *testing.T) {
	sink := newAttemptSink()
	s := attemptServer(t, sink,
		[]validator.FallbackFactorConfig{
			{Name: "retrying", Priority: 1, Strategy: "priority", Models: models("m-b"), MaxAttempts: 2},
			{Name: "last", Priority: 2, Strategy: "priority", Models: models("m-c")},
		},
		&scriptedModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")},
		&scriptedModel{name: "pb", model: "m-b", fail: errors.New("upstream temporarily unavailable")},
		&scriptedModel{name: "pc", model: "m-c"},
	)
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	if err := processChatCompletion(ctx, s, chatReq("m-primary", true), func(*pb.ChatCompletionResponse) error { return nil }); err != nil {
		t.Fatal(err)
	}
	got := sink.byModel()
	if want := []string{billingmeter.RoleFailedFallback}; !equalStrings(got["m-primary"], want) {
		t.Fatalf("primary = %v, want %v", got["m-primary"], want)
	}
	if want := []string{billingmeter.RoleFailedFallback, billingmeter.RoleRetry}; !equalStrings(got["m-b"], want) {
		t.Fatalf("retried target = %v, want %v", got["m-b"], want)
	}
	if want := []string{billingmeter.RoleServed + "+charged"}; !equalStrings(got["m-c"], want) {
		t.Fatalf("served = %v, want %v", got["m-c"], want)
	}
}

// A stream that breaks after content reached the client ends there, even
// with fallback configured: it is charged for the tokens it generated and no
// fallback target is dispatched.
func TestHandlerStreamBrokenAfterContentEndsWithoutFallback(t *testing.T) {
	sink := newAttemptSink()
	fallback := &scriptedModel{name: "pb", model: "m-b"}
	s := attemptServer(t, sink,
		[]validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-b")}},
		&scriptedModel{name: "pp", model: "m-primary", streamFailAfterContent: true},
		fallback,
	)
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	if err := processChatCompletion(ctx, s, chatReq("m-primary", true), func(*pb.ChatCompletionResponse) error { return nil }); err == nil {
		t.Fatal("the broken stream must surface its error")
	}
	got := sink.byModel()
	if p := got["m-primary"]; len(p) != 1 || !strings.HasSuffix(p[0], "+charged") {
		t.Fatalf("primary = %v, want one charged attempt", p)
	}
	if fallback.calls != 0 || len(got["m-b"]) != 0 {
		t.Fatalf("fallback dispatched after content was delivered: calls=%d attempts=%v", fallback.calls, got["m-b"])
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// With no fallback configured, a stream that breaks after generating tokens
// is interrupted (and charged for those tokens), never "fell back".
func TestHandlerRecordsInterruptedStreamWithoutFallback(t *testing.T) {
	sink := newAttemptSink()
	s := attemptServer(t, sink, nil,
		&scriptedModel{name: "pp", model: "m-primary", streamFailAfterContent: true},
	)
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	if err := processChatCompletion(ctx, s, chatReq("m-primary", true), func(*pb.ChatCompletionResponse) error { return nil }); err == nil {
		t.Fatal("the broken stream must surface its error")
	}
	if got, want := sink.byModel()["m-primary"], []string{billingmeter.RoleInterrupted + "+charged"}; !equalStrings(got, want) {
		t.Fatalf("primary = %v, want %v", got, want)
	}
}
