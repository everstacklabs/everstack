package billing

// Billed inference attempts at the dispatch seam: every attempt the handler
// tagged becomes one settle unit carrying its attempt record (role, provider,
// model, tokens, estimated flag), and the role decides the charge.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func onlyUnit(t *testing.T, sink *fakeSink) billingmeter.SettleUnit {
	t.Helper()
	units := sink.settled()
	if len(units) != 1 {
		t.Fatalf("want exactly one settle unit, got %d: %+v", len(units), units)
	}
	if units[0].Attempt == nil {
		t.Fatalf("settle unit has no attempt record: %+v", units[0])
	}
	return units[0]
}

func chargedUnits(units []billingmeter.SettleUnit) []billingmeter.SettleUnit {
	var out []billingmeter.SettleUnit
	for _, u := range units {
		if u.Micros > 0 {
			out = append(out, u)
		}
	}
	return out
}

func TestServedAttemptRecord(t *testing.T) {
	sink := newSink()
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 500}}, platformCfg(sink, testCatalog(t)))
	ctx := billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindPrimary)
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	u := onlyUnit(t, sink)
	a := u.Attempt
	if a.Role != billingmeter.RoleServed || a.RequestID != "corr-1" || a.Provider != "fake" || a.Model != "priced-1" ||
		a.Provenance != billingmeter.ProvenancePlatform || a.InputTokens != 1000 || a.OutputTokens != 500 || a.Estimated {
		t.Fatalf("attempt record wrong: %+v", a)
	}
	if u.Micros != 31250 {
		t.Fatalf("served attempt must be charged: micros = %d", u.Micros)
	}
}

// A stream that fails part way after generating tokens is charged when the
// customer's fallback or retry configuration made the attempt, and recorded
// free when the platform made it.
func TestFailedAttemptWithTokensChargedByKind(t *testing.T) {
	cases := []struct {
		kind    billingmeter.AttemptKind
		role    string
		charged bool
	}{
		{billingmeter.KindPrimary, billingmeter.RoleFailedFallback, true},
		{billingmeter.KindFallback, billingmeter.RoleFailedFallback, true},
		{billingmeter.KindRetry, billingmeter.RoleRetry, true},
		{billingmeter.KindPlatformRetry, billingmeter.RolePlatformRetry, false},
	}
	for _, c := range cases {
		sink := newSink()
		p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortUpstreamError}, platformCfg(sink, testCatalog(t)))
		ctx := billingmeter.WithAttemptKind(tenantCtx("tenant-a"), c.kind)
		if err := p.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error { return nil }); err == nil {
			t.Fatal("upstream error must surface")
		}
		u := onlyUnit(t, sink)
		if u.Attempt.Role != c.role {
			t.Fatalf("kind %d: role = %q, want %q", c.kind, u.Attempt.Role, c.role)
		}
		if got := u.Micros > 0; got != c.charged {
			t.Fatalf("kind %d: charged = %v (micros %d), want %v", c.kind, got, u.Micros, c.charged)
		}
		if !u.Attempt.Estimated || u.Attempt.OutputTokens != 100 || u.Attempt.InputTokens != 1000 {
			t.Fatalf("kind %d: estimate not recorded: %+v", c.kind, u.Attempt)
		}
	}
}

// A platform retry that serves is the served attempt and is charged once.
func TestPlatformRetryThatServesIsCharged(t *testing.T) {
	sink := newSink()
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000}}, platformCfg(sink, testCatalog(t)))
	ctx := billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindPlatformRetry)
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	u := onlyUnit(t, sink)
	if u.Attempt.Role != billingmeter.RoleServed || u.Micros != billingmeter.MicrosFor(0.010, testMarkup) {
		t.Fatalf("served platform retry: %+v micros %d", u.Attempt, u.Micros)
	}
}

// A failed attempt with no tokens is recorded (uncharged) when the handler
// tagged it, so a request group shows every attempt; untagged callers keep
// the old behaviour (nothing recorded), and an attempt dispatched on an
// already-dead request is not an attempt at all.
func TestZeroTokenFailures(t *testing.T) {
	sink := newSink()
	p := Wrap(&fakeProvider{err: errors.New("503 upstream")}, platformCfg(sink, testCatalog(t)))
	ctx := billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindPrimary)
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err == nil {
		t.Fatal("expected error")
	}
	u := onlyUnit(t, sink)
	if u.Attempt.Role != billingmeter.RoleFailedFallback || u.Micros != 0 || u.Attempt.InputTokens != 0 || u.Attempt.OutputTokens != 0 {
		t.Fatalf("zero-token failure record wrong: %+v micros %d", u.Attempt, u.Micros)
	}

	untagged := newSink()
	p2 := Wrap(&fakeProvider{err: errors.New("503 upstream")}, platformCfg(untagged, testCatalog(t)))
	if _, err := p2.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}); err == nil {
		t.Fatal("expected error")
	}
	if n := len(untagged.settled()); n != 0 {
		t.Fatalf("untagged zero-token failure must not be recorded: %d units", n)
	}

	dead := newSink()
	p3 := Wrap(&fakeProvider{err: context.Canceled}, platformCfg(dead, testCatalog(t)))
	dctx, cancel := context.WithCancel(billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindRetry))
	cancel()
	if _, err := p3.Chat(dctx, gw.ChatCompletionRequest{Model: "priced-1"}); err == nil {
		t.Fatal("expected error")
	}
	if n := len(dead.settled()); n != 0 {
		t.Fatalf("an attempt on a dead request must not be recorded: %d units", n)
	}
}

// A client that walks away mid-stream was served what it received: the
// attempt is served (and charged on the estimate), never a failed fallback.
func TestClientAbortIsServedEstimate(t *testing.T) {
	sink := newSink()
	p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortCtxCancel}, platformCfg(sink, testCatalog(t)))
	ctx, cancel := context.WithCancel(billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindFallback))
	defer cancel()
	chunks := 0
	_ = p.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error {
		chunks++
		if chunks == 4 {
			cancel()
		}
		return nil
	})
	u := onlyUnit(t, sink)
	if u.Attempt.Role != billingmeter.RoleServed || !u.Attempt.Estimated || u.Micros != billingmeter.MicrosFor(abortEstimateUSD, testMarkup) {
		t.Fatalf("client abort: %+v micros %d", u.Attempt, u.Micros)
	}
	if u.Metadata[billingmeter.MetaUsageEstimated] != true {
		t.Fatal("ledger metadata must still mark the estimate")
	}
}

// Race legs: the claimed leg is served, every other leg is a race loser, and
// both are charged for the tokens they generated. A loser cancelled before it
// returned anything is recorded with no tokens and no charge.
func TestRaceLegsRecordedAndCharged(t *testing.T) {
	sink := newSink()
	cat := testCatalog(t)
	race := billingmeter.NewRace()
	base := tenantCtx("tenant-a")
	winner := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000}}, platformCfg(sink, cat))
	finisher := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 2000}}, platformCfg(sink, cat))
	cancelled := Wrap(&fakeProvider{err: context.Canceled}, platformCfg(sink, cat))

	var wg sync.WaitGroup
	for i, p := range []gw.Provider{winner, finisher} {
		wg.Add(1)
		go func(i int, p gw.Provider) {
			defer wg.Done()
			_, _ = p.Chat(race.Leg(base, i), gw.ChatCompletionRequest{Model: "priced-1"})
		}(i, p)
	}
	wg.Wait()
	if n := len(sink.settled()); n != 0 {
		t.Fatalf("legs settle only once the race is decided: %d", n)
	}
	if !race.Claim(0) {
		t.Fatal("first claim wins")
	}
	if _, err := cancelled.Chat(race.Leg(base, 2), gw.ChatCompletionRequest{Model: "priced-1"}); err == nil {
		t.Fatal("cancelled leg must fail")
	}
	race.Close()

	units := sink.settled()
	if len(units) != 3 {
		t.Fatalf("every leg must be recorded: %+v", units)
	}
	roles := map[int64]string{}
	for _, u := range units {
		roles[u.Attempt.InputTokens] = u.Attempt.Role
	}
	if roles[1000] != billingmeter.RoleServed || roles[2000] != billingmeter.RoleRaceLoser || roles[0] != billingmeter.RoleRaceLoser {
		t.Fatalf("race roles wrong: %+v", roles)
	}
	if c := chargedUnits(units); len(c) != 2 {
		t.Fatalf("winner and finished loser are charged, the cancelled loser is not: %+v", c)
	}
}

// BYOK attempts are recorded for display and never charged, in every role.
func TestBYOKAttemptsRecordedNeverCharged(t *testing.T) {
	sink := newSink()
	cfg := Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, CatalogCache: testCatalog(t)}
	ok := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 100, CompletionTokens: 20}}, cfg)
	if _, err := ok.Chat(billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindPrimary), gw.ChatCompletionRequest{Model: "any"}); err != nil {
		t.Fatal(err)
	}
	failing := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortUpstreamError}, cfg)
	_ = failing.ChatStream(billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindRetry), abortReq(), func(gw.ChatResponseChunk) error { return nil })

	units := sink.settled()
	if len(units) != 2 {
		t.Fatalf("both BYOK attempts must be recorded: %+v", units)
	}
	if c := chargedUnits(units); len(c) != 0 {
		t.Fatalf("BYOK must never be charged: %+v", c)
	}
	if units[0].Attempt.Provenance != billingmeter.ProvenanceBYOK || units[0].Attempt.Role != billingmeter.RoleServed || units[0].Attempt.OutputTokens != 20 {
		t.Fatalf("BYOK record: %+v", units[0].Attempt)
	}
	if units[1].Attempt.Role != billingmeter.RoleRetry {
		t.Fatalf("BYOK retry role: %+v", units[1].Attempt)
	}
}

// A stream that breaks after generating tokens when nothing is configured to
// take over (or from an untagged caller) is interrupted, not a fallback, and
// is still charged for the tokens it generated.
func TestInterruptedStreamChargedNotCalledFallback(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"untagged":             tenantCtx("tenant-a"),
		"primary, no fallback": billingmeter.WithLastAttempt(billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindPrimary), true),
	} {
		sink := newSink()
		p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortUpstreamError}, platformCfg(sink, testCatalog(t)))
		_ = p.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error { return nil })
		u := onlyUnit(t, sink)
		if u.Attempt.Role != billingmeter.RoleInterrupted || u.Micros != billingmeter.MicrosFor(abortEstimateUSD, testMarkup) || !u.Attempt.Estimated {
			t.Fatalf("%s: %+v micros %d", name, u.Attempt, u.Micros)
		}
	}
}

// Internal calls (context summaries, cache lookups) are recorded as internal.
func TestInternalAttemptRecorded(t *testing.T) {
	sink := newSink()
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 10}}, platformCfg(sink, testCatalog(t)))
	ctx := contextkeys.WithInternalCall(tenantCtx("tenant-a"))
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	if u := onlyUnit(t, sink); !u.Attempt.Internal || u.Micros == 0 {
		t.Fatalf("internal attempt must be recorded as internal and still charged: %+v", u)
	}
}
