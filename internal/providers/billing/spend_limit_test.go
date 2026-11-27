package billing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// spendLimitSink is a fakeSink that also checks spend limits, the optional
// capability *billingmeter.Meter provides.
type spendLimitSink struct {
	*fakeSink
	mu      sync.Mutex
	refuse  error
	checked []spendCheck
}

type spendCheck struct {
	org      string
	internal bool
}

func (s *spendLimitSink) CheckSpendLimit(_ context.Context, org string, internal bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = append(s.checked, spendCheck{org, internal})
	return s.refuse
}

func refusal() error {
	return billingmeter.SpendLimitError(billingmeter.SpendLimit{Period: "monthly", LimitMicros: 10_000_000}, time.Now())
}

// A platform-key attempt over the organization's spend limit is refused
// before the provider is called, with the customer-facing error, and the
// refusal is neither settled nor charged.
func TestSpendLimitRefusesPlatformAttemptBeforeDispatch(t *testing.T) {
	sink := &spendLimitSink{fakeSink: newSink(), refuse: refusal()}
	inner := &fakeProvider{}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))

	_, err := p.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"})
	var typed *billingmeter.SpendLimitExceededError
	if !errors.As(err, &typed) {
		t.Fatalf("Chat error = %v, want the spend limit refusal", err)
	}
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want resource_exhausted", connect.CodeOf(err))
	}
	if inner.calls != 0 {
		t.Fatalf("provider called %d times after a refusal", inner.calls)
	}
	if len(sink.settled()) != 0 {
		t.Fatal("a refused attempt must not settle")
	}
	if n := platformInflight.current("org-a"); n != 0 {
		t.Fatalf("in-flight slot leaked by a refusal: %d", n)
	}
	if len(sink.checked) != 1 || sink.checked[0].org != "org-a" || sink.checked[0].internal {
		t.Fatalf("checked = %+v", sink.checked)
	}

	err = p.ChatStream(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}, func(gw.ChatResponseChunk) error { return nil })
	if !errors.As(err, &typed) || inner.calls != 0 {
		t.Fatalf("stream: err=%v calls=%d", err, inner.calls)
	}
}

// Under the limit (or in shadow, where the check never refuses) the attempt
// is served as before.
func TestSpendLimitAllowsWhenNotRefused(t *testing.T) {
	sink := &spendLimitSink{fakeSink: newSink()}
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1, CompletionTokens: 1}}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))
	ctx := contextkeys.WithInternalCall(tenantCtx("tenant-a"))
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d", inner.calls)
	}
	if len(sink.checked) != 1 || !sink.checked[0].internal {
		t.Fatalf("internal flag not passed: %+v", sink.checked)
	}
}

// BYOK attempts spend the customer's own provider key, not Everstack
// credit, so a spend limit never refuses them.
func TestSpendLimitNeverChecksBYOK(t *testing.T) {
	sink := &spendLimitSink{fakeSink: newSink(), refuse: refusal()}
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1, CompletionTokens: 1}}
	p := Wrap(inner, Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, CatalogCache: admissionCatalog(t)})
	if _, err := p.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatalf("BYOK Chat refused: %v", err)
	}
	if len(sink.checked) != 0 {
		t.Fatalf("BYOK attempt was checked: %+v", sink.checked)
	}
}
