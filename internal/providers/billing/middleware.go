// Package billing is the wallet-metering seam at provider dispatch
// (docs/design/prepaid-credits-v2.md section 6.1b). Every provider factory
// wraps its base provider here, innermost, beside the tracing middleware, so
// every caller that reaches a provider through the registry or router (the
// gateway handlers, the agent runtime, workflows, the memory extractor, the
// digest and compaction summarizers, the lib chat handler) is metered: every
// attempt, including fallback losers and parallel-race losers, is one settle
// unit with its own idempotency key.
//
// Scope: only platform-provenance providers on a shared gateway are wrapped.
// A BYOK provider (any configuration owned by an organization) and every
// provider on a self-hosted gateway is returned unwrapped, so its behaviour is
// byte-identical to before the seam existed.
//
// Fail closed on price: a platform attempt on a model the catalog cannot
// price is rejected before dispatch with UnpricedModelError, on every attempt
// (fallback and rotation included), so no platform key ever serves a model
// for free. The wrapper deliberately does not implement gw.Unwrapper: the
// capability discovery helpers (UnwrapTo, FindAudioProvider, ...) walk down
// through Unwrap, and a wrapper they could see through would let audio, image,
// rerank and moderation calls reach the base provider unmetered. Those
// modalities have no catalog price yet, so on a platform provider they are
// simply not discoverable until they are priced.
//
// Admission phase G1 (S4): every platform attempt is evaluated in dry-run at
// preDispatch (admission.go): estimate, in-flight guardrail, free allowlist,
// and an uncached wallet read off the request goroutine. The decision is
// logged and counted, never acted on; S5 flips it to a reservation.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
)

// Sink is what the middleware settles and counts through. *billingmeter.Meter
// is the production implementation; tests use a fake. A nil
// *billingmeter.Meter is a valid inactive sink.
type Sink interface {
	Active() bool
	Markup() float64
	ResolveOrg(ctx context.Context) (org string, ok bool, err error)
	Settle(ctx context.Context, u billingmeter.SettleUnit)
	// CountUsage records one served attempt on the organization's display
	// counters (requests and tokens), for any provenance. It must never
	// block or fail the caller.
	CountUsage(org string, d billingmeter.UsageDelta)
	// ShadowAdmit reports one G1 dry-run admission. It must never block,
	// fail or deny the caller.
	ShadowAdmit(req billingmeter.AdmissionRequest)
}

// spendLimiter is the optional Sink capability that checks the
// organization's Cloud spend limits before a platform-key attempt
// (*billingmeter.Meter implements it). It returns an error only when the
// limit is enforced and reached.
type spendLimiter interface {
	CheckSpendLimit(ctx context.Context, org string, internal bool) error
}

// spendBudget is the optional Sink capability that runs the spend limits v2
// evaluator (docs/design/spend-limits-v2.md 4.3) for every attempt, platform
// and BYOK (*billingmeter.Meter implements it). AdmitSpend adds the
// attempt's estimate to the in-flight total P and evaluates in shadow: it
// never refuses, blocks or alters the attempt. ReleaseSpend takes the
// estimate back out when the attempt ends.
type spendBudget interface {
	AdmitSpend(ctx context.Context, a billingmeter.SpendAttempt) billingmeter.PendingSpend
	ReleaseSpend(p billingmeter.PendingSpend)
}

// Config is what a provider factory knows about billing at construction.
type Config struct {
	// Meter is the wallet sink. Nil or inactive means the provider is not
	// wrapped.
	Meter Sink
	// Provenance is billingmeter.ProvenancePlatform, ProvenanceBYOK or
	// ProvenanceTrial. Only platform is metered; trial is served through the
	// model trial gate (trial.go) and never debited. Empty means "not a
	// managed gateway" and is never metered.
	Provenance string
	// KeyID is the served provider_api_keys row, for attribution.
	KeyID string
	// CatalogCache prices the attempt. A platform provider without a catalog
	// cannot price anything and therefore serves nothing.
	CatalogCache *catalog.Cache
}

// Metered reports whether a provider built with this config debits the
// wallet: platform provenance on a managed gateway with an active meter.
func (c Config) Metered() bool {
	return c.Provenance == billingmeter.ProvenancePlatform && c.Meter != nil && c.Meter.Active()
}

// Counted reports whether a provider built with this config feeds the usage
// display counters: any provenance on a managed gateway with an active meter.
// BYOK is counted (requests and tokens are usage, not charges) but never
// debited.
func (c Config) Counted() bool {
	return c.Provenance != "" && c.Meter != nil && c.Meter.Active()
}

// UnpricedModelError is returned before dispatch when a platform provider
// is asked for a model the catalog cannot price. It is not retryable (the
// same model will not become priced on retry) and carries the typed reason
// the design names.
type UnpricedModelError struct {
	Provider string
	Model    string
}

// Reason is the machine-readable denial reason.
const ReasonUnpricedModel = "unpriced_model"

func (e *UnpricedModelError) Error() string {
	return fmt.Sprintf("%s: model %q on provider %q has no catalog price and cannot be served on a platform key", ReasonUnpricedModel, e.Model, e.Provider)
}

// ErrUnpricedModel matches any UnpricedModelError via errors.Is.
var ErrUnpricedModel = errors.New(ReasonUnpricedModel)

// Is lets errors.Is(err, ErrUnpricedModel) match.
func (e *UnpricedModelError) Is(target error) bool { return target == ErrUnpricedModel }

// Middleware meters one provider. It implements the core Provider interface;
// responsesMiddleware adds the Responses API when the base has it. With
// debit set (platform provenance) every attempt is price-checked and settled
// against the wallet; without it (BYOK) the attempt is only counted.
type Middleware struct {
	inner      gw.Provider
	cfg        Config
	debit      bool
	calculator *metrics.CostCalculator
	now        func() time.Time
	newID      func() string
}

type responsesMiddleware struct {
	*Middleware
	responses gw.ResponsesProvider
}

// byokMiddleware counts a BYOK provider's attempts and is otherwise
// transparent: it implements Unwrapper so capability discovery (audio, image,
// rerank, moderation, voice enrolment) reaches the base exactly as before.
// Nothing here touches money, so see-through is safe.
type byokMiddleware struct {
	*Middleware
}

func (m *byokMiddleware) Unwrap() gw.Provider { return m.inner }

type byokResponsesMiddleware struct {
	*responsesMiddleware
}

func (m *byokResponsesMiddleware) Unwrap() gw.Provider { return m.inner }

// Wrap returns the metered provider for cfg, or inner unchanged when the
// provider is neither metered nor counted (self-hosted, or no billing DB).
func Wrap(inner gw.Provider, cfg Config) gw.Provider {
	if inner == nil {
		return inner
	}
	// The model trial's row is never returned unwrapped, whatever else is
	// configured: without a working gate its provider refuses everything.
	if cfg.Provenance == billingmeter.ProvenanceTrial {
		return wrapTrial(inner, cfg)
	}
	if !cfg.Counted() {
		return inner
	}
	m := &Middleware{
		inner:      inner,
		cfg:        cfg,
		debit:      cfg.Metered(),
		calculator: metrics.NewCostCalculatorFromCache(cfg.CatalogCache),
		now:        func() time.Time { return time.Now().UTC() },
		newID:      uuid.NewString,
	}
	rp, hasResponses := inner.(gw.ResponsesProvider)
	switch {
	case m.debit && hasResponses:
		return &responsesMiddleware{Middleware: m, responses: rp}
	case m.debit:
		return m
	case hasResponses:
		return &byokResponsesMiddleware{&responsesMiddleware{Middleware: m, responses: rp}}
	default:
		return &byokMiddleware{m}
	}
}

// Name returns the wrapped provider's name.
func (m *Middleware) Name() string { return m.inner.Name() }

// SupportsModel delegates to the wrapped provider.
func (m *Middleware) SupportsModel(model string) bool { return m.inner.SupportsModel(model) }

// attempt is the per-settle-unit state minted at dispatch.
type attempt struct {
	id       string
	org      string
	modality string
	model    string
	internal bool
	// apiKeyID is the verified gateway API key the request authenticated
	// with, read once at dispatch; empty when it used none.
	apiKeyID string
	// estCostMicros and unpriced are a BYOK attempt's list-price estimate,
	// set at settle (docs/design/spend-limits-v2.md 4.1).
	estCostMicros int64
	unpriced      bool
	// estimated marks a unit settled on the character estimate because
	// the provider reported no usage.
	estimated bool
	// estimateMicros is the G1 admission estimate (the reservation S5
	// would place), recorded on the settle unit to measure the estimator.
	estimateMicros int64
	// inflightOrg is set while the attempt holds an in-flight slot.
	inflightOrg string
	// spendEstimate is the estimate the spend limit evaluator holds in P
	// for this attempt (list price on BYOK, the marked-up G1 estimate on a
	// platform key); pending is the hold, and held is set while done still
	// owes its release.
	spendEstimate int64
	pending       billingmeter.PendingSpend
	held          bool
	// abortedInThinking marks an attempt whose stream broke off while the
	// provider still held unemitted reasoning.
	abortedInThinking bool
	// deadAtDispatch is true when the request was already over when the
	// attempt was dispatched (a handler retrying on a cancelled request):
	// such an attempt reaches no provider and is not recorded.
	deadAtDispatch bool
}

// preDispatch is the seam every attempt passes before the provider call:
// the price check (fail closed), org resolution and, for platform attempts,
// G1 shadow admission (never denies). Callers must defer m.done(a).
func (m *Middleware) preDispatch(ctx context.Context, modality, model string, est func() estimateInput) (*attempt, error) {
	if m.debit && (m.cfg.CatalogCache == nil || !m.calculator.IsPriced(m.inner.Name(), model)) {
		unpricedRejected.WithLabelValues(m.inner.Name(), modality).Inc()
		return nil, &UnpricedModelError{Provider: m.inner.Name(), Model: model}
	}
	a := &attempt{id: m.newID(), modality: modality, model: model, internal: contextkeys.IsInternalCall(ctx),
		apiKeyID: contextkeys.GetAPIKeyID(ctx), deadAtDispatch: ctx.Err() != nil}
	org, ok, err := m.cfg.Meter.ResolveOrg(ctx)
	switch {
	case err != nil:
		// Metering phase: the attempt is served; Settle reports the
		// unresolved org loudly if it is still unresolved at settle time.
		logger.WithFields("provider", m.inner.Name(), "model", model, "error", err.Error()).
			Warn("billing: could not resolve billable organization at dispatch")
	case ok:
		a.org = org
	}
	if m.debit {
		m.shadowAdmit(ctx, a, err, est)
		// Cloud spend limits gate only attempts that spend Everstack credit.
		// In shadow (the default) the check never refuses.
		if limiter, ok := m.cfg.Meter.(spendLimiter); ok {
			if refusal := limiter.CheckSpendLimit(ctx, a.org, a.internal); refusal != nil {
				m.done(a)
				return nil, refusal
			}
		}
	}
	// Spend limits v2: every attempt, platform and BYOK. Shadow only.
	m.shadowSpend(ctx, a, est)
	return a, nil
}

// shadowSpend runs the spend limits v2 check for one attempt: its estimate
// joins P (released by done) and the organization's v2 limits are evaluated
// in shadow. It never returns an error and a panic here never reaches the
// attempt.
func (m *Middleware) shadowSpend(ctx context.Context, a *attempt, est func() estimateInput) {
	budget, ok := m.cfg.Meter.(spendBudget)
	if !ok || a == nil || a.org == "" {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			spendShadowPanics.Inc()
			logger.WithFields("provider", m.inner.Name(), "model", a.model, "settle_unit", a.id, "panic", r).
				Error("billing: spend limit shadow check panicked; attempt served unaffected")
		}
	}()
	estimate := a.estimateMicros // platform: the marked-up G1 estimate, what it settles as
	if !m.debit && est != nil {
		estimate = m.estimateListMicros(a.model, est())
	}
	a.spendEstimate = estimate
	a.pending = budget.AdmitSpend(ctx, billingmeter.SpendAttempt{
		OrgID: a.org, APIKeyID: a.apiKeyID, EstimateMicros: estimate, Internal: a.internal,
	})
	a.held = true
}

// count feeds the display counters for one served attempt, whatever its
// provenance. The org lookup is retried once here so a transient failure at
// dispatch does not drop the count. Never fails the caller.
func (m *Middleware) count(ctx context.Context, a *attempt, in, out int) {
	if a == nil {
		return
	}
	if a.org == "" {
		if org, ok, err := m.cfg.Meter.ResolveOrg(ctx); err == nil && ok {
			a.org = org
		}
	}
	if a.org == "" {
		return
	}
	m.cfg.Meter.CountUsage(a.org, billingmeter.UsageDelta{Requests: 1, InputTokens: int64(in), OutputTokens: int64(out)})
}

// attemptEnd is how a dispatched attempt ended, for its attempt record.
type attemptEnd struct {
	// succeeded: the provider call returned without error.
	succeeded bool
	// clientGone: the caller cancelled the request while the attempt ran.
	clientGone bool
}

func served() attemptEnd { return attemptEnd{succeeded: true} }

// endOf classifies a finished provider call. ctx is the attempt's own
// (undetached) context.
func endOf(ctx context.Context, err error) attemptEnd {
	return attemptEnd{succeeded: err == nil, clientGone: err != nil && errors.Is(ctx.Err(), context.Canceled)}
}

// settle records a completed attempt that generated tokens (or served
// without reporting any) and hands it to the sink: priced and charged on a
// platform key; on BYOK recorded with its list-price estimate (no markup),
// never charged. resolved is the model the provider reports back (may be a
// dated variant); it is priced first, falling back to the requested model
// when the variant is not in the catalog.
func (m *Middleware) settle(ctx context.Context, a *attempt, end attemptEnd, resolvedModel string, in, out, cacheRead, cacheWrite int) {
	if a == nil {
		return
	}
	if !m.debit && a.estimated {
		// A BYOK character estimate is spend attribution only (the attempt
		// record). The usage display, and every meter fed from it, sees
		// provider-reported tokens: the request counts, the guess does not.
		m.count(ctx, a, 0, 0)
	} else {
		m.count(ctx, a, in, out)
	}
	model := strings.TrimSpace(resolvedModel)
	if model == "" {
		model = a.model
	}
	billingmeter.ObserveSettle(ctx, billingmeter.SettledUsage{
		Model: model, InputTokens: in, OutputTokens: out,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite, Estimated: a.estimated,
	})
	if !m.debit && (in > 0 || out > 0) {
		model = m.estimateBYOK(a, model, in, out, cacheRead, cacheWrite)
	}
	var micros int64
	var catalogUSD float64
	if m.debit && (in > 0 || out > 0) {
		var cost metrics.CostDetails
		cost = m.calculator.CalculateCostWithCacheTokens(m.inner.Name(), model, in, out, cacheRead, cacheWrite)
		if !cost.Priceable {
			model = a.model
			cost = m.calculator.CalculateCostWithCacheTokens(m.inner.Name(), model, in, out, cacheRead, cacheWrite)
		}
		if cost.Priceable {
			catalogUSD = cost.EstimatedUSD
			micros = billingmeter.MicrosFor(catalogUSD, m.cfg.Meter.Markup())
		} else {
			// preDispatch admitted this model as priced, so this is a
			// catalog change mid-flight or a bug: the tokens were spent and
			// cannot be billed. Loud, counted, never silent. The attempt is
			// still recorded, uncharged.
			unpricedAfterDispatch.WithLabelValues(m.inner.Name(), a.modality).Inc()
			logger.WithFields("provider", m.inner.Name(), "model", a.model, "served_model", resolvedModel,
				"settle_unit", a.id, "org", a.org, "input_tokens", in, "output_tokens", out).
				Error("billing: served platform attempt has no catalog price; spend unbilled")
		}
	}
	m.emit(ctx, a, end, model, int64(in), int64(out), micros, catalogUSD)
}

// estimateBYOK prices a BYOK attempt at catalog list price with the shared
// cost calculator and returns the model it priced. Never through
// MicrosFor(..., Markup()): markup is Everstack's rate card, and the
// customer's provider does not charge it. A model the catalog cannot price
// counts $0 and is flagged unpriced; BYOK never refuses it (custom and
// fine-tuned models must keep working).
func (m *Middleware) estimateBYOK(a *attempt, model string, in, out, cacheRead, cacheWrite int) string {
	cost := m.calculator.CalculateCostWithCacheTokens(m.inner.Name(), model, in, out, cacheRead, cacheWrite)
	if !cost.Priceable && model != a.model {
		if requested := m.calculator.CalculateCostWithCacheTokens(m.inner.Name(), a.model, in, out, cacheRead, cacheWrite); requested.Priceable {
			model, cost = a.model, requested
		}
	}
	if !cost.Priceable {
		a.unpriced = true
		byokUnpriced.WithLabelValues(m.inner.Name(), a.modality).Inc()
		return model
	}
	a.estCostMicros = billingmeter.ListPriceMicros(cost.EstimatedUSD)
	return model
}

// failed records an attempt that ended in error having generated nothing
// billable. Only attempts a handler tagged (an attempt kind or a race leg)
// are recorded, so a request's attempt group shows every try; an untagged
// caller's failure, and an attempt dispatched on an already-dead request,
// leave no record, as before.
func (m *Middleware) failed(ctx context.Context, a *attempt, end attemptEnd) {
	if a == nil || a.deadAtDispatch {
		return
	}
	_, _, isLeg := billingmeter.RaceLegFrom(ctx)
	if !isLeg && billingmeter.AttemptKindFrom(ctx) == billingmeter.KindUnset {
		return
	}
	m.emit(ctx, a, end, a.model, 0, 0, 0, 0)
}

// emit builds the attempt's settle unit and hands it to the sink with its
// role: a race leg through its race (which waits for the winner), any other
// attempt by its kind and how it ended. A role that is never charged
// (platform_retry) and a BYOK attempt carry no amount.
func (m *Middleware) emit(ctx context.Context, a *attempt, end attemptEnd, model string, in, out, micros int64, catalogUSD float64) {
	if a.org == "" {
		// One more try: the org lookup may have failed transiently before
		// the call.
		if org, ok, err := m.cfg.Meter.ResolveOrg(ctx); err == nil && ok {
			a.org = org
		}
	}
	if !m.debit {
		micros = 0
	}
	correlationID := correlation.GetCorrelationID(ctx)
	u := billingmeter.SettleUnit{
		ID:       a.id,
		OrgID:    a.org,
		Modality: a.modality,
		Micros:   micros,
		At:       m.now(),
		Metadata: map[string]any{
			billingmeter.MetaCorrelationID: correlationID,
			billingmeter.MetaProvider:      m.inner.Name(),
			billingmeter.MetaModel:         model,
			billingmeter.MetaKeyID:         m.cfg.KeyID,
			billingmeter.MetaProvenance:    m.cfg.Provenance,
			billingmeter.MetaInternal:      a.internal,
		},
		Attempt: &billingmeter.AttemptRecord{
			RequestID:    correlationID,
			Provider:     m.inner.Name(),
			Model:        model,
			Provenance:   m.cfg.Provenance,
			InputTokens:  in,
			OutputTokens: out,
			Estimated:    a.estimated,
			Internal:     a.internal,
			APIKeyID:     a.apiKeyID,
		},
	}
	if a.apiKeyID != "" {
		u.Metadata[billingmeter.MetaAPIKeyID] = a.apiKeyID
	}
	if !m.debit {
		// Every BYOK attempt carries its estimate; one that generated
		// nothing (a failure, a cancelled race leg) carries 0.
		est := a.estCostMicros
		u.Attempt.EstCostMicros = &est
		u.Attempt.Unpriced = a.unpriced
		observeBYOKEstimate(a.modality, a.spendEstimate, a.estCostMicros)
	}
	if m.debit {
		markup := m.cfg.Meter.Markup()
		u.Metadata[billingmeter.MetaCatalogUSD] = catalogUSD
		u.Metadata[billingmeter.MetaMarkup] = markup
	}
	if a.estimated {
		u.Metadata[billingmeter.MetaUsageEstimated] = true
	}
	if a.abortedInThinking {
		u.Metadata[billingmeter.MetaAbortedInThinking] = true
	}
	if a.estimateMicros > 0 {
		u.Metadata[billingmeter.MetaAdmissionEstimate] = a.estimateMicros
		observeEstimate(a.modality, a.estimateMicros, u.Micros)
	}
	settle := func(u billingmeter.SettleUnit) { m.cfg.Meter.Settle(ctx, u) }
	if race, idx, ok := billingmeter.RaceLegFrom(ctx); ok {
		// served and race_loser are both charged; the race only decides
		// the label.
		race.Finish(idx, u, settle)
		return
	}
	u.Attempt.Role = billingmeter.ResolveRole(billingmeter.AttemptKindFrom(ctx), billingmeter.IsLastAttempt(ctx), end.succeeded, end.clientGone)
	if !billingmeter.RoleCharged(u.Attempt.Role) {
		u.Micros = 0
	}
	settle(u)
}

// Chat meters one unary chat attempt.
func (m *Middleware) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	a, err := m.preDispatch(ctx, billingmeter.MetricChat, req.Model, func() estimateInput { return chatEstimate(req) })
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer m.done(a)
	resp, err := m.inner.Chat(ctx, req)
	if err != nil {
		m.failed(ctx, a, endOf(ctx, err))
		return resp, err
	}
	m.settle(ctx, a, served(), resp.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.CacheReadCount(), resp.Usage.CacheWriteCount())
	return resp, nil
}

// ChatStream meters one streaming chat attempt. Usage arrives in the final
// chunk, so a stream can end (client disconnect, cancellation, timeout,
// upstream error) after the tokens were generated but before usage was
// reported. The rule on a platform key: once any content was produced the
// attempt is settled, however the stream ended. Reported usage wins; without
// it the attempt settles the character estimate (prompt plus the output
// streamed so far, including a chunk the client may not have received:
// upstream generated it), marked usage_estimated. Every stream settle runs on
// a context detached from the request, because a cancelled request context
// would fail the settle-time org lookup and drop the unit. An attempt settles
// at most once, under its single settle-unit id.
//
// After a client disconnect the upstream is cancelled immediately rather than
// drained for its usage chunk: providers read on the request context, so
// draining would mean detaching every provider call from cancellation, which
// would also keep cancelled parallel-race losers and abandoned agent turns
// generating (and costing) tokens nobody reads. The estimate covers what was
// produced up to the cancel.
func (m *Middleware) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	a, err := m.preDispatch(ctx, billingmeter.MetricChat, req.Model, func() estimateInput { return chatEstimate(req) })
	if err != nil {
		return err
	}
	defer m.done(a)
	var usage *gw.Usage
	var model string
	outChars := 0
	// A provider that buffers output before emitting it (Anthropic
	// thinking) reports the buffered size here.
	pending := &billingmeter.PendingOutput{}
	err = m.inner.ChatStream(billingmeter.WithPendingOutput(ctx, pending), req, func(chunk gw.ChatResponseChunk) error {
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			u := *chunk.Usage
			usage = &u
		}
		for _, c := range chunk.Choices {
			outChars += messageChars(c.Delta)
		}
		return onChunk(chunk)
	})
	settleCtx, cancel := detachedSettleCtx(ctx)
	defer cancel()
	aborted := err != nil || ctx.Err() != nil
	end := endOf(ctx, err)
	if aborted && usage == nil {
		// Reasoning generated (and billed upstream) that no chunk carried:
		// count it into the estimate and flag the attempt.
		if n := pending.ReasoningChars(); n > 0 {
			outChars += n
			a.abortedInThinking = true
			abortedInThinking.WithLabelValues(m.inner.Name(), a.modality).Inc()
		}
	}
	switch {
	case usage != nil:
		if aborted && m.debit {
			abortedStreamSettled.WithLabelValues(m.inner.Name(), a.modality, "reported").Inc()
		}
		m.settle(settleCtx, a, end, model, usage.PromptTokens, usage.CompletionTokens, usage.CacheReadCount(), usage.CacheWriteCount())
	case err == nil || outChars > 0:
		// Served (or aborted after producing content) without reported
		// usage: tokens were spent, so settle the character estimate rather
		// than count them free, and say so. On a platform key this is the
		// charge; on BYOK it is the attempt's spend estimate.
		in, out := estimatePromptTokens(req), estimateTokens(outChars)
		cacheRead, cacheWrite := 0, 0
		if prompt, cr, cw, ok := pending.PromptUsage(); ok && prompt > 0 {
			// The provider already reported the prompt exactly (Anthropic's
			// message_start): only the output is estimated.
			in, cacheRead, cacheWrite = prompt, cr, cw
		}
		a.estimated = true
		if !m.debit {
			m.settle(settleCtx, a, end, model, in, out, cacheRead, cacheWrite)
			break
		}
		usageEstimated.WithLabelValues(m.inner.Name(), a.modality).Inc()
		reason := "no usage reported"
		if aborted {
			abortedStreamSettled.WithLabelValues(m.inner.Name(), a.modality, "estimated").Inc()
			reason = "stream aborted before usage"
		}
		fields := []any{"provider", m.inner.Name(), "model", req.Model, "settle_unit", a.id,
			"org", a.org, "estimated_input_tokens", in, "estimated_output_tokens", out, "reason", reason}
		if err != nil {
			fields = append(fields, "stream_error", err.Error())
		}
		if a.abortedInThinking {
			fields = append(fields, "aborted_in_thinking", true)
		}
		logger.WithFields(fields...).Warn("billing: platform stream settled on a character estimate")
		m.settle(settleCtx, a, end, model, in, out, cacheRead, cacheWrite)
	default:
		m.failed(settleCtx, a, end)
	}
	return err
}

// streamSettleTimeout bounds a stream's settle on its detached context. The
// settle can retry ResolveOrg (database lookups) when dispatch could not
// resolve the org; without a deadline a hung billing database would hold the
// handler until shutdown. If the lookup times out the unit reaches the sink
// with no org, where Meter.Settle drops it loudly (settle_unresolved_org
// counter plus an error log naming the settle unit); the wallet debit itself
// has its own budget and outbox fallback inside the meter.
var streamSettleTimeout = 10 * time.Second

// detachedSettleCtx keeps the request's values (tenant, correlation id) but
// not its cancellation, under streamSettleTimeout.
func detachedSettleCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), streamSettleTimeout)
}

// Embed meters one embeddings attempt. Providers that report no usage are
// billed on the same character estimate the tracing layer attributes.
func (m *Middleware) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	a, err := m.preDispatch(ctx, billingmeter.MetricEmbeddings, req.Model, func() estimateInput { return embedEstimate(req) })
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer m.done(a)
	resp, err := m.inner.Embed(ctx, req)
	if err != nil {
		m.failed(ctx, a, endOf(ctx, err))
		return resp, err
	}
	tokens := 0
	if resp.Usage != nil {
		tokens = resp.Usage.PromptTokens
	}
	if tokens <= 0 {
		tokens = len(req.Input) / 4
		if tokens < 1 {
			tokens = 1
		}
	}
	m.settle(ctx, a, served(), req.Model, tokens, 0, 0, 0)
	return resp, nil
}

// CreateResponse meters one unary Responses API attempt.
func (m *responsesMiddleware) CreateResponse(ctx context.Context, req gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
	a, err := m.preDispatch(ctx, billingmeter.MetricResponses, req.Model, func() estimateInput { return responsesEstimate(req) })
	if err != nil {
		return gw.CreateResponseResponse{}, err
	}
	defer m.done(a)
	resp, err := m.responses.CreateResponse(ctx, req)
	if err != nil {
		m.failed(ctx, a, endOf(ctx, err))
		return resp, err
	}
	m.settleResponse(ctx, a, served(), resp.Model, resp.Usage)
	return resp, nil
}

// CreateResponseStream meters one streaming Responses API attempt.
func (m *responsesMiddleware) CreateResponseStream(ctx context.Context, req gw.CreateResponseRequest, onChunk func(gw.CreateResponseResponse) error) error {
	a, err := m.preDispatch(ctx, billingmeter.MetricResponses, req.Model, func() estimateInput { return responsesEstimate(req) })
	if err != nil {
		return err
	}
	defer m.done(a)
	var usage *gw.ResponseUsage
	var model string
	err = m.responses.CreateResponseStream(ctx, req, func(resp gw.CreateResponseResponse) error {
		if resp.Model != "" {
			model = resp.Model
		}
		if resp.Usage != nil {
			u := *resp.Usage
			usage = &u
		}
		return onChunk(resp)
	})
	// Detached for the same reason as ChatStream. No in-repo provider
	// streams the Responses API natively yet, so the aborted-stream
	// estimate is not applied here; reported usage is still settled
	// however the stream ended.
	settleCtx, cancel := detachedSettleCtx(ctx)
	defer cancel()
	end := endOf(ctx, err)
	switch {
	case usage != nil:
		m.settleResponse(settleCtx, a, end, model, usage)
	case err == nil:
		m.settle(settleCtx, a, end, model, 0, 0, 0, 0)
	default:
		m.failed(settleCtx, a, end)
	}
	return err
}

func (m *responsesMiddleware) settleResponse(ctx context.Context, a *attempt, end attemptEnd, model string, usage *gw.ResponseUsage) {
	if usage == nil {
		m.settle(ctx, a, end, model, 0, 0, 0, 0)
		return
	}
	m.settle(ctx, a, end, model, usage.InputTokens, usage.OutputTokens, usage.CacheReadCount(), usage.CacheWriteCount())
}

// Character-based token estimate for streams that report no usage: about
// four characters per token, plus a small per-message overhead for roles and
// tool calls. The same heuristic as the agent runtime's EstimateTokens
// (internal/agents/runtime/token_counter.go), which this package cannot
// import.
const charsPerToken = 4

func estimateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + charsPerToken - 1) / charsPerToken
}

func messageChars(msg gw.Message) int {
	n := 0
	for _, part := range msg.Content {
		if part.Text != nil {
			n += len(*part.Text)
			continue
		}
		n += reasoningChars(part)
	}
	for _, tc := range msg.ToolCalls {
		n += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	return n
}

// reasoningChars is the visible reasoning in a provider-native thinking part
// (Anthropic and Mistral thinking blocks, OpenAI reasoning items), which
// carries no normalized Text. Reasoning is billed as output, so an aborted
// stream's estimate must count it. The signature and encrypted payloads are
// not text and are skipped.
func reasoningChars(part gw.ContentPart) int {
	if part.ProviderJSON == nil {
		return 0
	}
	switch part.Type {
	case "thinking", "redacted_thinking", "reasoning":
	default:
		return 0
	}
	var v any
	if err := json.Unmarshal(*part.ProviderJSON, &v); err != nil {
		return 0
	}
	return reasoningTextChars(v, "")
}

func reasoningTextChars(v any, key string) int {
	switch x := v.(type) {
	case string:
		if key == "thinking" || key == "text" {
			return len(x)
		}
	case map[string]any:
		n := 0
		for k, e := range x {
			n += reasoningTextChars(e, k)
		}
		return n
	case []any:
		n := 0
		for _, e := range x {
			n += reasoningTextChars(e, key)
		}
		return n
	}
	return 0
}

func estimatePromptTokens(req gw.ChatCompletionRequest) int {
	chars := 0
	for _, msg := range req.Messages {
		chars += 16 + messageChars(msg)
		for range msg.ToolCalls {
			chars += 20
		}
	}
	for _, tool := range req.Tools {
		chars += len(tool.Function.Name) + len(tool.Function.Description)
		if len(tool.Function.Parameters) > 0 {
			if raw, err := json.Marshal(tool.Function.Parameters); err == nil {
				chars += len(raw)
			}
		}
	}
	tokens := estimateTokens(chars)
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}
