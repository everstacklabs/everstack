package billing

import (
	"context"
	"strings"
	"sync"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/pkg/plans"
)

// G1 shadow admission at dispatch (docs/design/prepaid-credits-v2.md 6.1,
// slice S4). For every platform attempt the middleware estimates the
// reservation S5 would place, counts the attempt against the per-org
// in-flight guardrail, checks the free-tier allowlist, and hands the result
// to the meter, which reads the wallet uncached off the request goroutine and
// logs and counts the would-deny decision. Nothing here denies, delays or
// alters an attempt: the served bytes are identical with or without it.

// defaultMaxOutputTokens is used when neither the request nor the catalog
// gives an output cap.
const defaultMaxOutputTokens = 4096

// estimateInput is the request shape the estimator prices.
type estimateInput struct {
	promptTokens    int
	maxOutputTokens int
}

// estimateMicros is the marked-up reservation for one attempt: prompt tokens
// at the input rate plus the output cap at the output rate, times the
// rate-card markup from plans.json credit_pricing.inference_markup, applied
// once exactly as settle applies it. The headroom over the actual charge
// comes from max_tokens, not from a factor, so estimate_ratio centres near 1
// for calls that use their cap. The output cap is the request's max_tokens, else the catalog's
// max_output_tokens, else defaultMaxOutputTokens. 0 when unpriced.
func (m *Middleware) estimateMicros(model string, in estimateInput) int64 {
	usd := m.estimateUSD(model, in)
	if usd <= 0 {
		return 0
	}
	return billingmeter.MicrosFor(usd, m.cfg.Meter.Markup())
}

// estimateListMicros is the same estimate at catalog list price, with no
// markup: what a BYOK attempt settles as (est_cost_micros), and so what the
// spend limit evaluator holds in P for it (docs/design/spend-limits-v2.md
// 4.3). 0 when unpriced.
func (m *Middleware) estimateListMicros(model string, in estimateInput) int64 {
	usd := m.estimateUSD(model, in)
	if usd <= 0 {
		return 0
	}
	return billingmeter.ListPriceMicros(usd)
}

// estimateUSD prices prompt tokens at the input rate plus the output cap at
// the output rate, at catalog list price. 0 when unpriced.
func (m *Middleware) estimateUSD(model string, in estimateInput) float64 {
	maxOut := in.maxOutputTokens
	switch {
	case maxOut < 0:
		maxOut = 0 // no output tokens (embeddings)
	case maxOut == 0:
		maxOut = m.catalogMaxOutput(model)
	}
	cost := m.calculator.CalculateCostWithCacheTokens(m.inner.Name(), model, in.promptTokens, maxOut, 0, 0)
	if !cost.Priceable || cost.EstimatedUSD <= 0 {
		return 0
	}
	return cost.EstimatedUSD
}

func (m *Middleware) catalogMaxOutput(model string) int {
	if def := m.catalogModel(model); def != nil && def.MaxOutputTokens > 0 {
		return def.MaxOutputTokens
	}
	return defaultMaxOutputTokens
}

// catalogModel resolves model the way pricing does (exact, then without a
// "[1m]"-style variant marker, then the longest dated-snapshot prefix).
func (m *Middleware) catalogModel(model string) *catalog.ModelDefinition {
	c := m.cfg.CatalogCache
	if c == nil {
		return nil
	}
	def, ok := c.ResolveModel(m.inner.Name(), model)
	if !ok {
		return nil
	}
	return def
}

// modelLabel is the bounded metric label for model: the catalog entry's
// name, never the raw request string (prefix matching would let any caller
// mint label values).
func (m *Middleware) modelLabel(model string) string {
	if def := m.catalogModel(model); def != nil && def.Name != "" {
		return strings.ToLower(def.Name)
	}
	return "uncatalogued"
}

func chatEstimate(req gw.ChatCompletionRequest) estimateInput {
	maxOut := req.Sampling.MaxCompletionTokens
	if maxOut <= 0 {
		maxOut = req.Sampling.MaxTokens
	}
	return estimateInput{promptTokens: estimatePromptTokens(req), maxOutputTokens: maxOut}
}

func embedEstimate(req gw.EmbeddingsRequest) estimateInput {
	tokens := estimateTokens(len(req.Input))
	if tokens < 1 {
		tokens = 1
	}
	// Embeddings have no output tokens; a zero cap prices input only.
	return estimateInput{promptTokens: tokens, maxOutputTokens: -1}
}

// responsesEstimate counts text sizes already in memory (no serialization on
// the request goroutine): instructions, plus each input item's text with the
// same per-message overhead the chat estimate uses.
func responsesEstimate(req gw.CreateResponseRequest) estimateInput {
	chars := len(req.Instructions)
	for _, item := range req.Input {
		chars += 16 + len(item.ItemID)
		for _, part := range item.Content {
			if part.Text != nil {
				chars += len(*part.Text)
			}
		}
	}
	for _, tool := range req.Tools {
		chars += len(tool.Function.Name) + len(tool.Function.Description)
	}
	tokens := estimateTokens(chars)
	if tokens < 1 {
		tokens = 1
	}
	return estimateInput{promptTokens: tokens, maxOutputTokens: req.MaxOutputTokens}
}

// inflightCounter counts in-flight platform attempts per organization on
// this replica: the MAX_INFLIGHT_PLATFORM_REQUESTS guardrail (6.8). Keyed by
// organization id only.
type inflightCounter struct {
	mu    sync.Mutex
	byOrg map[string]int
}

func (c *inflightCounter) acquire(org string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byOrg == nil {
		c.byOrg = make(map[string]int)
	}
	c.byOrg[org]++
	return c.byOrg[org]
}

func (c *inflightCounter) release(org string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := c.byOrg[org]; n <= 1 {
		delete(c.byOrg, org)
	} else {
		c.byOrg[org] = n - 1
	}
}

func (c *inflightCounter) current(org string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byOrg[org]
}

// platformInflight is process-wide: every provider instance on this replica
// serving the same org shares one count.
var platformInflight = &inflightCounter{}

// admissionPolicy is the plans.json input to admission, read once.
type admissionPolicy struct {
	plans       *plans.PlansConfig
	inflightCap int
}

var (
	admissionPolicyOnce sync.Once
	admissionPolicyVal  admissionPolicy
)

func currentAdmissionPolicy() admissionPolicy {
	admissionPolicyOnce.Do(func() {
		if cfg, err := plans.Embedded(); err == nil && cfg != nil {
			admissionPolicyVal = admissionPolicy{plans: cfg, inflightCap: cfg.MaxInflightPlatformRequests()}
		}
	})
	return admissionPolicyVal
}

// shadowAdmit runs G1 admission for one platform attempt that preDispatch
// already price-checked. It takes the in-flight slot (released by done) and
// never returns an error: the decision is reported, not acted on.
func (m *Middleware) shadowAdmit(ctx context.Context, a *attempt, resolveErr error, est func() estimateInput) {
	// Registered first: a bug anywhere below must neither fail the attempt
	// nor leak the in-flight slot it may already hold.
	defer func() {
		if r := recover(); r != nil {
			m.done(a)
			shadowPanics.Inc()
			logger.WithFields("provider", m.inner.Name(), "model", a.model, "settle_unit", a.id, "panic", r).
				Error("billing: shadow admission panicked; attempt served unaffected")
		}
	}()
	policy := currentAdmissionPolicy()
	if est != nil {
		a.estimateMicros = m.estimateMicros(a.model, est())
	}
	req := billingmeter.AdmissionRequest{
		OrgID:          a.org,
		ResolveErr:     resolveErr,
		Provider:       m.inner.Name(),
		Model:          a.model,
		ModelLabel:     m.modelLabel(a.model),
		Modality:       a.modality,
		KeyID:          m.cfg.KeyID,
		SettleUnit:     a.id,
		CorrelationID:  correlation.GetCorrelationID(ctx),
		EstimateMicros: a.estimateMicros,
		FreeAllowed:    policy.plans.FreeInferenceAllowed(m.inner.Name(), a.model),
		Internal:       a.internal,
		InflightCap:    policy.inflightCap,
	}
	if a.org != "" {
		req.Inflight = platformInflight.acquire(a.org)
		a.inflightOrg = a.org
		inflightAtDispatch.Observe(float64(req.Inflight))
	}
	m.cfg.Meter.ShadowAdmit(req)
}

// done releases the attempt's in-flight slot and its spend estimate hold
// (P). Every entry point defers it, so an error, a cancel or a panic in the
// provider call releases both. Safe on a nil attempt, on attempts that never
// took either, and when called twice.
func (m *Middleware) done(a *attempt) {
	if a == nil {
		return
	}
	if a.held {
		a.held = false
		if budget, ok := m.cfg.Meter.(spendBudget); ok {
			budget.ReleaseSpend(a.pending)
		}
		a.pending = billingmeter.PendingSpend{}
	}
	if a.inflightOrg == "" {
		return
	}
	platformInflight.release(a.inflightOrg)
	a.inflightOrg = ""
}

// observeBYOKEstimate records the list-price estimator's error for one
// settled BYOK attempt: the estimate held in P over the attempt's settled
// list-price cost. >1 means P over-counted it (the source of pending_block).
func observeBYOKEstimate(modality string, estimate, actual int64) {
	if estimate <= 0 || actual <= 0 {
		return
	}
	byokEstimateRatio.WithLabelValues(strings.TrimSpace(modality)).Observe(float64(estimate) / float64(actual))
}

// observeEstimate records the estimator's error for one settled platform
// attempt: estimate / actual, both marked up. >1 means the reservation
// would have covered the call.
func observeEstimate(modality string, estimate, actual int64) {
	if estimate <= 0 || actual <= 0 {
		return
	}
	estimateRatio.WithLabelValues(strings.TrimSpace(modality)).Observe(float64(estimate) / float64(actual))
}
