package billing

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// unpricedRejected counts platform attempts refused before dispatch
	// because the catalog has no price for the model (reason unpriced_model).
	unpricedRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_unpriced_model_rejected_total",
		Help: "Platform-key attempts refused before dispatch because the model has no catalog price.",
	}, []string{"provider", "modality"})
	// unpricedAfterDispatch counts served platform attempts whose response
	// could not be priced after all: spend that went unbilled. Must stay 0.
	unpricedAfterDispatch = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_unpriced_after_dispatch_total",
		Help: "Served platform-key attempts that could not be priced after dispatch; spend unbilled.",
	}, []string{"provider", "modality"})
	// byokUnpriced counts BYOK attempts that generated tokens on a model
	// the catalog cannot price: their spend estimate is 0 (not counted).
	byokUnpriced = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_byok_unpriced_attempts_total",
		Help: "BYOK attempts that generated tokens on a model with no catalog price; their spend estimate is 0.",
	}, []string{"provider", "modality"})
	// usageEstimated counts served platform attempts that reported no
	// usage and were settled on the gateway's character estimate instead.
	usageEstimated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_usage_estimated_total",
		Help: "Served platform-key attempts settled on an estimate because the provider reported no usage.",
	}, []string{"provider", "modality"})
	// abortedStreamSettled counts platform streams that ended abnormally
	// (client disconnect, cancellation, timeout, upstream error) after
	// content was produced and were settled anyway. usage is "reported"
	// when the provider's usage chunk had arrived, "estimated" when the
	// character estimate was billed instead.
	abortedInThinking = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_aborted_in_thinking_total",
		Help: "Streams that broke off while the provider held unemitted reasoning (counted into the estimate on platform keys).",
	}, []string{"provider", "modality"})
	abortedStreamSettled = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_aborted_stream_settled_total",
		Help: "Platform-key streams that ended abnormally after producing content and were settled, by usage source.",
	}, []string{"provider", "modality", "usage"})
	// estimateRatio is the G1 estimator error: admission estimate over the
	// settled charge, per platform attempt. The S4 verification publishes
	// its median and tail before S5 trusts the estimate as a reservation.
	estimateRatio = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "billing_admission_estimate_ratio",
		Help:    "Admission estimate divided by the settled charge per platform-key attempt (>1: the reservation would have covered it).",
		Buckets: []float64{0.25, 0.5, 0.8, 1, 1.25, 1.5, 2, 3, 5, 10, 25, 100},
	}, []string{"modality"})
	// byokEstimateRatio is the spend limit estimator error on BYOK: the
	// list-price estimate held in P over the settled list-price cost.
	byokEstimateRatio = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "billing_spend_byok_estimate_ratio",
		Help:    "Spend limit pending estimate divided by the settled list-price cost per BYOK attempt (>1: P over-counted it).",
		Buckets: []float64{0.25, 0.5, 0.8, 1, 1.25, 1.5, 2, 3, 5, 10, 25, 100},
	}, []string{"modality"})
	// spendShadowPanics counts recovered panics in the dispatch-side spend
	// limit v2 shadow check. Must stay 0.
	spendShadowPanics = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_spend_limit_dispatch_panics_total",
		Help: "Recovered panics in the dispatch-side spend limit shadow check. Must stay 0; attempts are unaffected.",
	})
	// shadowPanics counts recovered panics in the dispatch-side shadow
	// admission. Must stay 0.
	shadowPanics = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_admission_dispatch_panics_total",
		Help: "Recovered panics in dispatch-side shadow admission; the attempt was served unaffected.",
	})
	// inflightAtDispatch is the org's in-flight platform attempts on this
	// replica at each dispatch (including the attempt itself): the data
	// behind the MAX_INFLIGHT_PLATFORM_REQUESTS value decision.
	inflightAtDispatch = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "billing_admission_inflight_at_dispatch",
		Help:    "Per-organization in-flight platform-key attempts on this replica at dispatch, including the new attempt.",
		Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128, 256},
	})
)
