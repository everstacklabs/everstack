package billing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/billingtrial"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
)

// TrialGate is the Sink capability the model trial needs
// (*billingmeter.Meter implements it): the settings (cached, fail closed),
// the take-one statement and its refund. org is always the billing org the
// Sink resolved from the authenticated context.
type TrialGate interface {
	TrialSettings(ctx context.Context) (billingtrial.Settings, error)
	TakeTrial(ctx context.Context, org string) (billingtrial.TakeResult, error)
	RefundTrial(ctx context.Context, org string, takeID uuid.UUID) error
	// GrantTrial creates the organization's trial row from its signup
	// claim; called only when a take found no row, so the trial's clock
	// starts at its first free request (design 3.3).
	GrantTrial(ctx context.Context, org string) (bool, error)
}

// TrialModelServable reports whether model can be served on the trial row:
// the trial shapes Chat Completions requests only, so a model the OpenAI
// provider would send to the Responses API (catalog api_type responses, or a
// codex model) is refused, since that path is unshaped and stores requests.
func TrialModelServable(cache *catalog.Cache, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" || strings.Contains(strings.ToLower(model), "-codex") {
		return false
	}
	if cache != nil {
		if def, ok := cache.GetModel("openai", model); ok && def != nil && strings.EqualFold(strings.TrimSpace(def.APIType), "responses") {
			return false
		}
	}
	return true
}

// Model trial metrics (docs/design/free-trial-onboarding.md 8.2). Ops only:
// no state of the trial ever reaches a customer outside the product.
var (
	trialRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "model_trial_requests_total",
		Help: "Model trial requests by outcome: served, refunded, or the refusal reason.",
	}, []string{"outcome"})
	trialDailyUsed = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "model_trial_daily_used",
		Help: "Free requests taken today (UTC) against the global daily ceiling, as of the last take on this replica.",
	})
	trialCapReached = promauto.NewCounter(prometheus.CounterOpts{
		Name: "model_trial_cap_reached_total",
		Help: "Model trial requests refused because the day's global ceiling was reached.",
	})
	trialTokens = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "model_trial_tokens",
		Help:    "Provider-reported tokens per served model trial request, to check the worst-case assumption.",
		Buckets: []float64{50, 100, 250, 500, 1000, 2000, 4000, 8000, 16000},
	}, []string{"direction"})
	trialRefundFailed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "model_trial_refund_failed_total",
		Help: "Model trial refunds that could not be written; the request stays counted (fails safe).",
	})
)

// trialOutcome is the metric label for a refusal reason.
func trialOutcome(reason string) string {
	switch reason {
	case billingmeter.ReasonModelTrialToolsUnsupported:
		return "tools"
	default:
		return strings.TrimPrefix(reason, "model_trial_")
	}
}

// trialMiddleware serves a provider built on the model trial's row
// (docs/design/free-trial-onboarding.md 3.1, 3.5). It is the only way a
// request reaches the trial key, so it is deliberately narrow:
//
//   - it implements Chat and ChatStream only; Embed is refused, and it
//     implements no other capability (Responses, audio, image, rerank,
//     moderation), so registry type assertions find nothing;
//   - it does NOT implement gw.Unwrapper, so UnwrapTo and the capability
//     discovery helpers stop here and can never reach the raw OpenAI client;
//   - every request is validated (text only, no tools, input bytes, the one
//     model, post-route), reshaped (output clamped, reasoning none, no
//     metadata), and takes one free request before it is dispatched.
//
// inner is the counting billing Middleware (never debits, records the
// attempt with provenance trial); nil means the trial cannot be served on
// this gateway and every request is refused.
type trialMiddleware struct {
	name    string
	inner   gw.Provider
	sink    Sink
	gate    TrialGate
	catalog *catalog.Cache
}

// wrapTrial builds the trial gate for a provider on the trial row. It never
// returns inner, whatever the configuration: without an active meter that
// can take requests, the result refuses everything.
func wrapTrial(inner gw.Provider, cfg Config) gw.Provider {
	t := &trialMiddleware{name: inner.Name()}
	gate, ok := cfg.Meter.(TrialGate)
	if !ok || cfg.Meter == nil || !cfg.Meter.Active() {
		logger.WithFields("provider", inner.Name()).Error("billing: model trial provider built without an active trial gate; every request is refused")
		return t
	}
	t.sink, t.gate, t.catalog = cfg.Meter, gate, cfg.CatalogCache
	t.inner = &Middleware{
		inner:      inner,
		cfg:        cfg,
		debit:      false,
		calculator: metrics.NewCostCalculatorFromCache(cfg.CatalogCache),
		now:        func() time.Time { return time.Now().UTC() },
		newID:      uuid.NewString,
	}
	return t
}

// Name returns the wrapped provider's name.
func (t *trialMiddleware) Name() string { return t.name }

// SupportsModel delegates to the wrapped provider; the model itself is
// checked against the settings on every request.
func (t *trialMiddleware) SupportsModel(model string) bool {
	return t.inner != nil && t.inner.SupportsModel(model)
}

// Embed is not part of the trial.
func (t *trialMiddleware) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	model := ""
	if s, ok := t.settings(ctx); ok {
		model = s.Model
	}
	return gw.EmbeddingsResponse{}, t.refuse(billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialModelUnsupported, model, 0, 0, time.Time{}))
}

func (t *trialMiddleware) refuse(e *billingmeter.ModelTrialRefusedError) error {
	trialRequests.WithLabelValues(trialOutcome(e.Reason)).Inc()
	return e
}

func (t *trialMiddleware) settings(ctx context.Context) (billingtrial.Settings, bool) {
	if t.gate == nil {
		return billingtrial.Settings{}, false
	}
	s, err := t.gate.TrialSettings(ctx)
	if err != nil {
		return billingtrial.Settings{}, false
	}
	return s, true
}

// trialTake is one free request taken for one dispatch.
type trialTake struct {
	org string
	res billingtrial.TakeResult
}

// admit validates and reshapes req, then takes one free request. Nothing is
// taken for a request that fails validation.
func (t *trialMiddleware) admit(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionRequest, *trialTake, error) {
	if t.inner == nil {
		return req, nil, t.refuse(billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialPaused, "", 0, 0, time.Time{}))
	}
	// The gateway's own calls (memory extraction, digests, compaction
	// summaries) never spend a customer's free requests.
	if contextkeys.IsInternalCall(ctx) {
		trialRequests.WithLabelValues("internal").Inc()
		return req, nil, billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialModelUnsupported, "", 0, 0, time.Time{})
	}
	settings, err := t.gate.TrialSettings(ctx)
	if err != nil {
		// Fail closed (design 4.3): free traffic refused costs a few
		// activations; served unbounded it costs money.
		logger.WithFields("provider", t.name, "error", err.Error()).Warn("billing: model trial settings unreadable; refusing")
		return req, nil, t.refuse(billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialPaused, "", 0, 0, time.Time{}))
	}
	if !TrialModelServable(t.catalog, settings.Model) {
		return req, nil, t.refuse(billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialModelUnsupported, settings.Model, 0, 0, time.Time{}))
	}
	shaped, refusal := shapeTrialRequest(req, settings)
	if refusal != nil {
		return req, nil, t.refuse(refusal)
	}
	// The organization comes only from the authenticated context (the
	// API key or session's tenant, mapped to its billing org), never from
	// request data. Could not tell: refuse as paused (fail closed).
	org, ok, err := t.sink.ResolveOrg(ctx)
	if err != nil {
		logger.WithFields("provider", t.name, "error", err.Error()).Warn("billing: model trial could not resolve the organization; refusing")
		return req, nil, t.refuse(billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialPaused, settings.Model, 0, 0, time.Time{}))
	}
	if !ok || org == "" {
		return req, nil, t.refuse(billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialNotEligible, settings.Model, 0, 0, time.Time{}))
	}
	res, err := t.gate.TakeTrial(ctx, org)
	var noRow *billingtrial.RefusedError
	if errors.As(err, &noRow) && noRow.Reason == billingtrial.ReasonNotEligible {
		// First free request: create the row from the signup claim, then
		// take again. An organization without a claim stays not eligible.
		if granted, gerr := t.gate.GrantTrial(ctx, org); gerr != nil {
			logger.WithFields("provider", t.name, "org", org, "error", gerr.Error()).Warn("billing: model trial grant failed; refusing")
		} else if granted {
			res, err = t.gate.TakeTrial(ctx, org)
		}
	}
	if err != nil {
		refusal := billingmeter.RefusalFromTake(err, settings.Model)
		var re *billingtrial.RefusedError
		if !errors.As(err, &re) {
			logger.WithFields("provider", t.name, "org", org, "error", err.Error()).Warn("billing: model trial take failed; refusing")
		} else if re.Reason == billingtrial.ReasonPaused && !re.ResumesAt.IsZero() {
			trialCapReached.Inc()
		}
		return req, nil, t.refuse(refusal)
	}
	trialDailyUsed.Set(float64(res.DayUsed))
	billingmeter.NoteModelTrialRemaining(ctx, res.Remaining())
	return shaped, &trialTake{org: org, res: res}, nil
}

// shapeTrialRequest applies docs/design/free-trial-onboarding.md 3.5: the
// post-route model must be the trial's model; text only; no tools, tool
// messages or tool_choice; at most max_input_bytes of text summed over every
// message; reasoning none; output clamped to min(requested, max). Metadata is
// dropped (OpenAI stores any request that carries it) and the request goes
// to Chat Completions, never the Responses API.
func shapeTrialRequest(req gw.ChatCompletionRequest, s billingtrial.Settings) (gw.ChatCompletionRequest, *billingmeter.ModelTrialRefusedError) {
	refusal := func(reason string) *billingmeter.ModelTrialRefusedError {
		return billingmeter.ModelTrialRefusal(reason, s.Model, s.MaxInputBytes, 0, time.Time{})
	}
	if strings.TrimSpace(s.Model) == "" || !strings.EqualFold(strings.TrimSpace(req.Model), strings.TrimSpace(s.Model)) {
		return req, refusal(billingmeter.ReasonModelTrialModelUnsupported)
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return req, refusal(billingmeter.ReasonModelTrialToolsUnsupported)
	}
	inputBytes := 0
	for _, msg := range req.Messages {
		if msg.Role == gw.RoleTool || msg.Role == gw.RoleFunction || len(msg.ToolCalls) > 0 || msg.ToolCallID != "" {
			return req, refusal(billingmeter.ReasonModelTrialToolsUnsupported)
		}
		for _, part := range msg.Content {
			if part.ToolCallID != nil || part.FunctionCall != nil {
				return req, refusal(billingmeter.ReasonModelTrialToolsUnsupported)
			}
			if !strings.EqualFold(part.Type, "text") || part.Text == nil ||
				part.ImageURL != nil || part.FileID != nil || part.ProviderJSON != nil {
				return req, refusal(billingmeter.ReasonModelTrialTextOnly)
			}
			inputBytes += len(*part.Text)
		}
	}
	if s.MaxInputBytes <= 0 || inputBytes > s.MaxInputBytes {
		return req, refusal(billingmeter.ReasonModelTrialInputTooLarge)
	}
	if s.MaxOutputTokens <= 0 {
		return req, refusal(billingmeter.ReasonModelTrialPaused)
	}

	out := req
	out.Model = s.Model
	out.Tools, out.ToolChoice = nil, nil
	out.Metadata = nil
	out.UseResponsesAPI = false
	requested := req.Sampling.MaxCompletionTokens
	if requested <= 0 {
		requested = req.Sampling.MaxTokens
	}
	if requested <= 0 || requested > s.MaxOutputTokens {
		requested = s.MaxOutputTokens
	}
	out.Sampling.MaxCompletionTokens = requested
	out.Sampling.MaxTokens = 0
	out.Sampling.ReasoningEffort = "none"
	out.Sampling.ReasoningBudget = nil
	out.Sampling.ReasoningEnabled = nil
	return out, nil
}

// refund returns a taken request. A failed refund is logged and counted and
// the request stays counted, which fails safe.
func (t *trialMiddleware) refund(ctx context.Context, tk *trialTake) {
	if tk == nil {
		return
	}
	if err := t.gate.RefundTrial(ctx, tk.org, tk.res.ID); err != nil {
		trialRefundFailed.Inc()
		logger.WithFields("provider", t.name, "org", tk.org, "error", err.Error()).Warn("billing: model trial refund failed; the request stays counted")
		return
	}
	trialRequests.WithLabelValues("refunded").Inc()
}

func observeTrialUsage(u *gw.Usage) {
	if u == nil {
		return
	}
	trialTokens.WithLabelValues("input").Observe(float64(u.PromptTokens))
	trialTokens.WithLabelValues("output").Observe(float64(u.CompletionTokens))
}

// Chat serves one unary free request. A taken request is refunded only when
// the provider answered with an error status (no output was produced); a
// timeout, a cancel or any other failure keeps the count, because the
// provider may have billed it.
func (t *trialMiddleware) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	shaped, tk, err := t.admit(ctx, req)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	resp, err := t.inner.Chat(ctx, shaped)
	if err != nil {
		var up *gw.UpstreamError
		if ctx.Err() == nil && errors.As(err, &up) && up.StatusCode >= 400 {
			t.refund(ctx, tk)
		}
		return resp, err
	}
	trialRequests.WithLabelValues("served").Inc()
	observeTrialUsage(&resp.Usage)
	return resp, nil
}

// ChatStream serves one streaming free request. A taken request is refunded
// only when the stream ended in error before its first content chunk and
// the caller had not gone away.
func (t *trialMiddleware) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	shaped, tk, err := t.admit(ctx, req)
	if err != nil {
		return err
	}
	sawContent := false
	var usage *gw.Usage
	err = t.inner.ChatStream(ctx, shaped, func(chunk gw.ChatResponseChunk) error {
		for _, c := range chunk.Choices {
			if messageChars(c.Delta) > 0 || len(c.Delta.ToolCalls) > 0 {
				sawContent = true
			}
		}
		if chunk.Usage != nil {
			u := *chunk.Usage
			usage = &u
		}
		return onChunk(chunk)
	})
	if err != nil {
		if !sawContent && ctx.Err() == nil {
			t.refund(ctx, tk)
		}
		return err
	}
	trialRequests.WithLabelValues("served").Inc()
	observeTrialUsage(usage)
	return nil
}
