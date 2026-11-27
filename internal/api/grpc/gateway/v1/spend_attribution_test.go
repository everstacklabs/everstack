package v1

// Spend attribution end to end through the chat handler
// (docs/design/spend-limits-v2.md, slice S1): on the customer's own provider
// keys every attempt the handler makes (first try, race legs, fallbacks,
// retries) is recorded exactly once, attributed to the request's API key,
// with its list-price estimate.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/billing"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	gatewayconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
)

func byokAttemptServer(t *testing.T, sink *attemptSink, factors []validator.FallbackFactorConfig, providers ...*scriptedModel) (*Server, *catalog.Cache) {
	t.Helper()
	cat := catalog.NewCache()
	if err := cat.Load([]byte(attemptCatalog), []byte("providers: {}\n")); err != nil {
		t.Fatal(err)
	}
	cfg := &validator.GatewayConfig{LoadBalancer: validator.LoadBalancerConfig{Fallback: validator.FallbackConfig{Enabled: true, Factors: factors}}}
	s := CreateServerWithContext(context.Background(), cfg, &validator.FeaturesConfig{})
	s.feat.Gateway.EnableStreaming = true
	for _, p := range providers {
		s.defaultBundle.Load().reg.Register(billing.Wrap(p, billing.Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, KeyID: "provider-key", CatalogCache: cat}))
	}
	return s, cat
}

func apiKeyRequestCtx(keyID string) context.Context {
	return contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant-1", "hash", keyID)
}

type attributedAttempt struct {
	model string
	role  string
	est   int64
}

// attributed checks every unit carries keyID and an estimate, that no unit
// settled twice, and returns them by model.
func (s *attemptSink) attributed(t *testing.T, keyID string) map[string][]attributedAttempt {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := map[string][]attributedAttempt{}
	for _, u := range s.units {
		if seen[u.ID] {
			t.Fatalf("settle unit %s recorded twice", u.ID)
		}
		seen[u.ID] = true
		if u.Micros != 0 {
			t.Fatalf("BYOK attempt charged %d", u.Micros)
		}
		if u.Attempt.APIKeyID != keyID {
			t.Fatalf("%s attempt key %q, want %q", u.Attempt.Model, u.Attempt.APIKeyID, keyID)
		}
		if u.Attempt.EstCostMicros == nil {
			t.Fatalf("%s attempt has no estimate", u.Attempt.Model)
		}
		out[u.Attempt.Model] = append(out[u.Attempt.Model], attributedAttempt{u.Attempt.Model, u.Attempt.Role, *u.Attempt.EstCostMicros})
	}
	return out
}

func listMicros(t *testing.T, cat *catalog.Cache, provider, model string, in, out int) int64 {
	t.Helper()
	c := metrics.NewCostCalculatorFromCache(cat).CalculateCostWithCacheTokens(provider, model, in, out, 0, 0)
	if !c.Priceable {
		t.Fatalf("%s must be priced", model)
	}
	return billingmeter.ListPriceMicros(c.EstimatedUSD)
}

// Primary fails, a parallel race of two fallbacks runs: three attempts, each
// recorded once, all attributed to the key; only the winner generated tokens.
func TestBYOKRaceAndFailedPrimaryAttributedOnce(t *testing.T) {
	sink := newAttemptSink()
	loserInFlight := make(chan struct{})
	s, cat := byokAttemptServer(t, sink,
		[]validator.FallbackFactorConfig{{Name: "race", Priority: 1, Strategy: "parallel", Models: models("m-a", "m-b")}},
		&scriptedModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")},
		&scriptedModel{name: "pa", model: "m-a", waitFor: loserInFlight},
		&scriptedModel{name: "pb", model: "m-b", untilCancelled: true, entered: loserInFlight},
	)
	if err := processChatCompletion(apiKeyRequestCtx("key-race"), s, chatReq("m-primary", false), func(*pb.ChatCompletionResponse) error { return nil }); err != nil {
		t.Fatal(err)
	}
	sink.waitUnits(3)
	got := sink.attributed(t, "key-race")
	want := map[string]attributedAttempt{
		"m-primary": {"m-primary", billingmeter.RoleFailedFallback, 0},
		"m-a":       {"m-a", billingmeter.RoleServed, listMicros(t, cat, "pa", "m-a", 1000, 100)},
		"m-b":       {"m-b", billingmeter.RoleRaceLoser, 0},
	}
	for model, w := range want {
		if len(got[model]) != 1 || got[model][0] != w {
			t.Fatalf("%s = %+v, want exactly [%+v]", model, got[model], w)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected attempts: %+v", got)
	}
}

// A stream that fails before sending anything, a retried fallback target
// that fails twice, and the fallback that serves: four attempts, each once.
// (A stream that breaks after content ends the request instead of falling
// back; TestBYOKBrokenStreamCarriesEstimate covers its estimate.)
func TestBYOKStreamFallbackAndRetryAttributedOnce(t *testing.T) {
	sink := newAttemptSink()
	s, cat := byokAttemptServer(t, sink,
		[]validator.FallbackFactorConfig{
			{Name: "retrying", Priority: 1, Strategy: "priority", Models: models("m-b"), MaxAttempts: 2},
			{Name: "last", Priority: 2, Strategy: "priority", Models: models("m-c")},
		},
		&scriptedModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")},
		&scriptedModel{name: "pb", model: "m-b", fail: errors.New("upstream temporarily unavailable")},
		&scriptedModel{name: "pc", model: "m-c"},
	)
	if err := processChatCompletion(apiKeyRequestCtx("key-fb"), s, chatReq("m-primary", true), func(*pb.ChatCompletionResponse) error { return nil }); err != nil {
		t.Fatal(err)
	}
	got := sink.attributed(t, "key-fb")
	if p := got["m-primary"]; len(p) != 1 || p[0].role != billingmeter.RoleFailedFallback || p[0].est != 0 {
		t.Fatalf("failed primary stream = %+v, want one failed_fallback with no estimate", p)
	}
	if b := got["m-b"]; len(b) != 2 || b[0].est != 0 || b[1].est != 0 {
		t.Fatalf("retried target = %+v, want two zero-estimate attempts", b)
	}
	if c := got["m-c"]; len(c) != 1 || c[0].role != billingmeter.RoleServed || c[0].est != listMicros(t, cat, "pc", "m-c", 1000, 100) {
		t.Fatalf("served fallback = %+v", c)
	}
}

// A stream that breaks after content, with fallback configured, ends the
// request: one attempt, carrying its character estimate.
func TestBYOKBrokenStreamCarriesEstimate(t *testing.T) {
	sink := newAttemptSink()
	s, _ := byokAttemptServer(t, sink,
		[]validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-b")}},
		&scriptedModel{name: "pp", model: "m-primary", streamFailAfterContent: true},
		&scriptedModel{name: "pb", model: "m-b"},
	)
	if err := processChatCompletion(apiKeyRequestCtx("key-fb"), s, chatReq("m-primary", true), func(*pb.ChatCompletionResponse) error { return nil }); err == nil {
		t.Fatal("the broken stream must surface its error")
	}
	got := sink.attributed(t, "key-fb")
	if p := got["m-primary"]; len(p) != 1 || p[0].est <= 0 {
		t.Fatalf("broken primary stream = %+v, want one attempt with a positive estimate", p)
	}
	if len(got["m-b"]) != 0 {
		t.Fatalf("fallback dispatched after content was delivered: %+v", got["m-b"])
	}
}

// Without an API key (session, agent trigger) attempts are recorded with no
// key, never another request's.
func TestBYOKAttemptWithoutAPIKeyHasNoKeyID(t *testing.T) {
	sink := newAttemptSink()
	s, _ := byokAttemptServer(t, sink, nil, &scriptedModel{name: "pp", model: "m-primary"})
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	if err := processChatCompletion(ctx, s, chatReq("m-primary", false), func(*pb.ChatCompletionResponse) error { return nil }); err != nil {
		t.Fatal(err)
	}
	got := sink.attributed(t, "")
	if len(got["m-primary"]) != 1 {
		t.Fatalf("attempts = %+v", got)
	}
}

// keyIDInterceptor stands in for the API key interceptor: it installs a
// verified key id on every handler context.
type keyIDInterceptor struct{ keyID string }

func (k keyIDInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return next(contextkeys.WithAuthenticatedAPIKeyID(ctx, "tenant-1", "hash", k.keyID), req)
	}
}
func (k keyIDInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
func (k keyIDInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(contextkeys.WithAuthenticatedAPIKeyID(ctx, "tenant-1", "hash", k.keyID), conn)
	}
}

// runBYOKResponse serves one Responses request over the Connect handler on
// a BYOK provider and returns the sink and catalog.
func runBYOKResponse(t *testing.T, p *scriptedStream, stream bool, keyID string) (*attemptSink, *catalog.Cache) {
	t.Helper()
	prev := globalResponseStore
	globalResponseStore = newResponseStore()
	t.Cleanup(func() { globalResponseStore = prev })
	cat := catalog.NewCache()
	if err := cat.Load([]byte(streamCatalog), []byte("providers: {}\n")); err != nil {
		t.Fatal(err)
	}
	sink := newAttemptSink()
	s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{}, &validator.FeaturesConfig{})
	s.defaultBundle.Load().reg.Register(billing.Wrap(p, billing.Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, KeyID: "provider-key", CatalogCache: cat}))
	path, handler := gatewayconnect.NewGatewayServiceHandler(s, connect.WithInterceptors(keyIDInterceptor{keyID}))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	backend := httptest.NewUnstartedServer(mux)
	backend.EnableHTTP2 = true
	backend.StartTLS()
	t.Cleanup(backend.Close)

	client := gatewayconnect.NewGatewayServiceClient(backend.Client(), backend.URL, connect.WithGRPC())
	rs, err := client.CreateResponse(context.Background(), connect.NewRequest(&pb.CreateResponseRequest{
		Model: streamModelName, Stream: stream,
		Input: []*pb.ResponseInput{{Type: "message", Role: pb.Role_ROLE_USER,
			Content: []*pb.ContentPart{{Type: "text", Data: &pb.ContentPart_Text{Text: "hi"}}}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for rs.Receive() {
		_ = rs.Msg()
	}
	_ = rs.Close()
	sink.waitUnits(1)
	return sink, cat
}

// Responses, unary and streaming, through the handler: one attempt each,
// attributed to the key, with its list-price estimate; a stream that breaks
// before its usage chunk carries the character estimate.
func TestBYOKResponsesAttributedOnce(t *testing.T) {
	t.Run("unary", func(t *testing.T) {
		sink, cat := runBYOKResponse(t, &scriptedStream{}, false, "key-resp")
		got := sink.attributed(t, "key-resp")
		if r := got[streamModelName]; len(r) != 1 || r[0].est != listMicros(t, cat, "ps", streamModelName, 7, 3) {
			t.Fatalf("unary = %+v", got)
		}
	})
	t.Run("streaming", func(t *testing.T) {
		p := &scriptedStream{turns: [][]gw.ChatResponseChunk{{textChunk("hello"), finishChunk("stop", 7, 5)}}}
		sink, cat := runBYOKResponse(t, p, true, "key-resp")
		got := sink.attributed(t, "key-resp")
		if r := got[streamModelName]; len(r) != 1 || r[0].est != listMicros(t, cat, "ps", streamModelName, 7, 5) {
			t.Fatalf("streaming = %+v", got)
		}
	})
	t.Run("stream broke before usage", func(t *testing.T) {
		p := &scriptedStream{turns: [][]gw.ChatResponseChunk{{textChunk(strings.Repeat("o", 400))}}, fail: errors.New("upstream reset")}
		sink, _ := runBYOKResponse(t, p, true, "key-resp")
		got := sink.attributed(t, "key-resp")
		if r := got[streamModelName]; len(r) != 1 || r[0].est <= 0 {
			t.Fatalf("aborted stream = %+v, want one attempt with a positive estimate", got)
		}
	})
}
