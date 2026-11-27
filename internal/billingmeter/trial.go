package billingmeter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/everstacklabs/everstack/internal/billingcredit"
	"github.com/everstacklabs/everstack/internal/billingtrial"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// The model trial on the gateway (docs/design/free-trial-onboarding.md 3,
// slice S3). A provider built on the trial row carries ProvenanceTrial; the
// billing middleware wraps it in a trial gate that validates each request,
// takes one free request from the organization's allowance and the day's
// global ceiling (internal/billingtrial), and refuses with a
// ModelTrialRefusedError otherwise. A trial unit is recorded as an attempt
// and never charged: chargeable() requires ProvenancePlatform.

// ProvenanceTrial marks a provider built on the model trial's row.
const ProvenanceTrial = "trial"

// Model trial refusal reasons, the code a /v1 caller reads in error.code and
// a Connect caller in ErrorInfo.reason (docs/design/free-trial-onboarding.md
// 3.6).
const (
	ReasonModelTrialExhausted        = "model_trial_exhausted"
	ReasonModelTrialExpired          = "model_trial_expired"
	ReasonModelTrialPaused           = "model_trial_paused"
	ReasonModelTrialRateLimited      = "model_trial_rate_limited"
	ReasonModelTrialTextOnly         = "model_trial_text_only"
	ReasonModelTrialToolsUnsupported = "model_trial_tools_unsupported"
	ReasonModelTrialInputTooLarge    = "model_trial_input_too_large"
	ReasonModelTrialModelUnsupported = "model_trial_model_unsupported"
	ReasonModelTrialNotEligible      = "model_trial_not_eligible"
)

// ErrModelTrialRefused matches any ModelTrialRefusedError via errors.Is.
var ErrModelTrialRefused = errors.New("model trial refused")

// ModelTrialRefusedError is a terminal refusal of a free request: it is never
// retried and never fails over to the fallback plan (gw.IsNonRetriableError
// and retrypolicy recognise it through ModelTrialRefusal), because a retry or
// a fallback target would either be refused again or be served on a key the
// caller did not choose. Message is customer copy: no internal ids.
type ModelTrialRefusedError struct {
	Reason  string
	Message string
	// RetryAfter is set for ReasonModelTrialRateLimited.
	RetryAfter time.Duration
}

func (e *ModelTrialRefusedError) Error() string {
	if e.Message == "" {
		return e.Reason
	}
	return e.Message
}

// ModelTrialRefusal names the refusal; its presence is what makes the error
// terminal to the retry and fallback logic.
func (e *ModelTrialRefusedError) ModelTrialRefusal() string { return e.Reason }

// HTTPStatusCode is 429 for the per-organization interval (an SDK retry
// after Retry-After is the desired outcome) and 402 for every other refusal
// (OpenAI SDKs do not retry 402).
func (e *ModelTrialRefusedError) HTTPStatusCode() int {
	if e.Reason == ReasonModelTrialRateLimited {
		return http.StatusTooManyRequests
	}
	return http.StatusPaymentRequired
}

// RetryAfterSeconds is the Retry-After value for a rate-limited refusal,
// rounded up, at least 1.
func (e *ModelTrialRefusedError) RetryAfterSeconds() int {
	s := int((e.RetryAfter + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

// Is lets errors.Is(err, ErrModelTrialRefused) match.
func (e *ModelTrialRefusedError) Is(target error) bool { return target == ErrModelTrialRefused }

// AsModelTrialRefusal finds a model trial refusal in err's chain.
func AsModelTrialRefusal(err error) (*ModelTrialRefusedError, bool) {
	var e *ModelTrialRefusedError
	if errors.As(err, &e) && e != nil {
		return e, true
	}
	return nil, false
}

// ModelTrialConnectError renders a model trial refusal in err's chain as a
// Connect ResourceExhausted error carrying the reason as a
// google.rpc.ErrorInfo detail, the shape that survives the gRPC hop to
// grpc-gateway, where middleware.SpendLimitStatus answers /v1 with 402 (or
// 429 with Retry-After for the interval). ok is false when err holds no
// refusal.
func ModelTrialConnectError(err error) (*connect.Error, bool) {
	e, ok := AsModelTrialRefusal(err)
	if !ok {
		return nil, false
	}
	cErr := connect.NewError(connect.CodeResourceExhausted, e)
	meta := map[string]string{}
	if e.Reason == ReasonModelTrialRateLimited {
		meta["retry_after_seconds"] = strconv.Itoa(e.RetryAfterSeconds())
	}
	if detail, derr := connect.NewErrorDetail(&errdetails.ErrorInfo{
		Reason: e.Reason, Domain: "everstack.ai", Metadata: meta,
	}); derr == nil {
		cErr.AddDetail(detail)
	}
	return cErr, true
}

// ModelTrialRefusal builds the refusal for reason with its customer copy.
// model names the trial's model; maxInputBytes and retryAfter fill the
// reasons that quote them.
func ModelTrialRefusal(reason, model string, maxInputBytes int, retryAfter time.Duration, resumesAt time.Time) *ModelTrialRefusedError {
	ownKey := "Add your own OpenAI or Anthropic key to keep going."
	var msg string
	switch reason {
	case ReasonModelTrialExhausted:
		msg = "You have used all your free requests. " + ownKey
	case ReasonModelTrialExpired:
		msg = "Your free requests have expired. " + ownKey
	case ReasonModelTrialPaused:
		if !resumesAt.IsZero() {
			msg = "Free requests are paused until 00:00 UTC. Add your own OpenAI or Anthropic key to keep going now."
		} else {
			msg = "Free requests are paused. Add your own OpenAI or Anthropic key to keep going now."
		}
	case ReasonModelTrialRateLimited:
		e := &ModelTrialRefusedError{Reason: reason, RetryAfter: retryAfter}
		e.Message = fmt.Sprintf("Free requests are limited to one every few seconds. Try again in %d seconds.", e.RetryAfterSeconds())
		return e
	case ReasonModelTrialTextOnly:
		msg = "Free requests are text only. Add your own key to send images or files."
	case ReasonModelTrialToolsUnsupported, ReasonModelTrialModelUnsupported:
		msg = fmt.Sprintf("Free requests cover chat with %s only. Add your own key to use tools or other models.", model)
	case ReasonModelTrialInputTooLarge:
		msg = fmt.Sprintf("Free requests accept up to %d bytes of text per request. Add your own key to send longer prompts.", maxInputBytes)
	default:
		reason = ReasonModelTrialNotEligible
		msg = "Free requests are not available for this organization. " + ownKey
	}
	return &ModelTrialRefusedError{Reason: reason, Message: msg}
}

// RefusalFromTake maps a billingtrial take refusal to the gateway reason.
// Any other error (the database could not be asked) fails closed as paused.
func RefusalFromTake(err error, model string) *ModelTrialRefusedError {
	var re *billingtrial.RefusedError
	if !errors.As(err, &re) {
		return ModelTrialRefusal(ReasonModelTrialPaused, model, 0, 0, time.Time{})
	}
	switch re.Reason {
	case billingtrial.ReasonExhausted:
		return ModelTrialRefusal(ReasonModelTrialExhausted, model, 0, 0, time.Time{})
	case billingtrial.ReasonExpired:
		return ModelTrialRefusal(ReasonModelTrialExpired, model, 0, 0, time.Time{})
	case billingtrial.ReasonPaused:
		return ModelTrialRefusal(ReasonModelTrialPaused, model, 0, 0, re.ResumesAt)
	case billingtrial.ReasonRateLimited:
		return ModelTrialRefusal(ReasonModelTrialRateLimited, model, 0, re.RetryAfter, time.Time{})
	default: // not_eligible, revoked
		return ModelTrialRefusal(ReasonModelTrialNotEligible, model, 0, 0, time.Time{})
	}
}

// ModelTrialNote carries the organization's remaining free requests from the
// trial gate, deep in the provider stack, up to the handler that sets the
// x-evs-model-trial response header.
type ModelTrialNote struct {
	mu        sync.Mutex
	remaining int
	set       bool
}

type modelTrialNoteKey struct{}

// WithModelTrialNote returns ctx carrying a fresh note, and the note.
func WithModelTrialNote(ctx context.Context) (context.Context, *ModelTrialNote) {
	n := &ModelTrialNote{}
	return context.WithValue(ctx, modelTrialNoteKey{}, n), n
}

// NoteModelTrialRemaining records the remaining count on ctx's note, if any.
func NoteModelTrialRemaining(ctx context.Context, remaining int) {
	n, _ := ctx.Value(modelTrialNoteKey{}).(*ModelTrialNote)
	if n == nil {
		return
	}
	n.mu.Lock()
	n.remaining, n.set = remaining, true
	n.mu.Unlock()
}

// Remaining reports the last remaining count noted, if a free request was
// taken for this request.
func (n *ModelTrialNote) Remaining() (int, bool) {
	if n == nil {
		return 0, false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.remaining, n.set
}

// trialSettingsTTL is how long the settings row is trusted (design 4.3: the
// kill switch applies within 10 s). The take statement reads enabled, the
// ceiling and the interval live in its own transaction regardless.
const trialSettingsTTL = 10 * time.Second

// trialEligibleTTL bounds how long an organization's eligibility is cached;
// bundles rebuild every few seconds and eligibility is stable.
const trialEligibleTTL = time.Minute

type trialCache struct {
	mu         sync.Mutex
	settings   billingtrial.Settings
	settingsAt time.Time
	eligible   map[string]trialEligibility
}

type trialEligibility struct {
	ok bool
	at time.Time
}

// TrialSettings returns the model trial settings, cached for 10 s. An error
// is never cached and means the caller must refuse (fail closed).
func (m *Meter) TrialSettings(ctx context.Context) (billingtrial.Settings, error) {
	if !m.Active() {
		return billingtrial.Settings{}, errors.New("billing database is not configured")
	}
	m.trial.mu.Lock()
	if !m.trial.settingsAt.IsZero() && time.Since(m.trial.settingsAt) < trialSettingsTTL {
		s := m.trial.settings
		m.trial.mu.Unlock()
		return s, nil
	}
	m.trial.mu.Unlock()
	s, err := billingtrial.LoadSettings(ctx, m.billingDB)
	if err != nil {
		return billingtrial.Settings{}, err
	}
	m.trial.mu.Lock()
	m.trial.settings, m.trial.settingsAt = s, time.Now()
	m.trial.mu.Unlock()
	return s, nil
}

// TakeTrial takes one free request for org. org must be the billing org
// resolved from the authenticated context (ResolveOrg), never request data.
func (m *Meter) TakeTrial(ctx context.Context, org string) (billingtrial.TakeResult, error) {
	if !m.Active() {
		return billingtrial.TakeResult{}, errors.New("billing database is not configured")
	}
	id, err := uuid.Parse(strings.TrimSpace(org))
	if err != nil {
		return billingtrial.TakeResult{}, &billingtrial.RefusedError{Reason: billingtrial.ReasonNotEligible}
	}
	return billingtrial.Take(ctx, m.billingDB, id, time.Now())
}

// RefundTrial returns the request taken by takeID to org. It runs on a
// context detached from the request, so a refund is not lost to a cancel.
func (m *Meter) RefundTrial(ctx context.Context, org string, takeID uuid.UUID) error {
	if !m.Active() {
		return errors.New("billing database is not configured")
	}
	id, err := uuid.Parse(strings.TrimSpace(org))
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = billingtrial.Refund(rctx, m.billingDB, id, takeID, time.Now())
	return err
}

// TrialEligible reports whether org's bundle should carry the trial row. It
// is a pure read, run at bundle build for requests that have nothing to do
// with the trial, so it never grants: nothing is written and no trial clock
// starts here. Eligible means the trial is enabled and the organization holds
// a signup claim (design 3.3) or a non-revoked trial row (an exhausted or
// expired one included, so its requests are refused with the right reason).
// While the trial is disabled nobody is eligible, so the row is never merged
// and a disabled trial changes no routing. Results are cached for a minute;
// errors are not cached and mean "not eligible".
func (m *Meter) TrialEligible(ctx context.Context, org string) (bool, error) {
	if !m.Active() {
		return false, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(org))
	if err != nil {
		return false, nil
	}
	settings, err := m.TrialSettings(ctx)
	if err != nil {
		return false, err
	}
	if !settings.Enabled {
		return false, nil
	}
	key := id.String()
	m.trial.mu.Lock()
	if e, ok := m.trial.eligible[key]; ok && time.Since(e.at) < trialEligibleTTL {
		m.trial.mu.Unlock()
		return e.ok, nil
	}
	m.trial.mu.Unlock()

	st, err := billingtrial.GetStatus(ctx, m.billingDB, id, time.Now())
	if err != nil {
		return false, err
	}
	m.trial.mu.Lock()
	if m.trial.eligible == nil {
		m.trial.eligible = map[string]trialEligibility{}
	}
	m.trial.eligible[key] = trialEligibility{ok: st.Eligible, at: time.Now()}
	m.trial.mu.Unlock()
	return st.Eligible, nil
}

// GrantTrial creates org's trial row from its signup claim, on the
// organization's first free request (design 3.3): the claims are synced
// first (billingcredit.SyncSignupCredit, idempotent), then
// billingtrial.GrantFromSignupClaim. It reports whether a row exists
// afterwards. The trial path calls it only when a take found no row, so the
// 30 days start at the first trial request, never at an unrelated one.
func (m *Meter) GrantTrial(ctx context.Context, org string) (bool, error) {
	if !m.Active() {
		return false, errors.New("billing database is not configured")
	}
	id, err := uuid.Parse(strings.TrimSpace(org))
	if err != nil {
		return false, nil
	}
	now := time.Now()
	if _, err := billingcredit.SyncSignupCredit(ctx, m.billingDB, id.String(), now); err != nil {
		logger.WithFields("org", id.String(), "error", err.Error()).Warn("billing: model trial could not sync the signup claim")
	}
	return billingtrial.GrantFromSignupClaim(ctx, m.billingDB, id, now)
}
