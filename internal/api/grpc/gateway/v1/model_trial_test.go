package v1

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/billingtrial"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/retrypolicy"
	"github.com/everstacklabs/everstack/internal/providers/billing"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// trialAttemptSink is attemptSink with the model trial gate; every take is
// answered with takeErr.
type trialAttemptSink struct {
	*attemptSink
	mu      sync.Mutex
	takeErr error
	takes   int
}

func (s *trialAttemptSink) TrialSettings(context.Context) (billingtrial.Settings, error) {
	return billingtrial.Settings{Enabled: true, Model: "m-primary", MaxOutputTokens: 300, MaxInputBytes: 4000}, nil
}
func (s *trialAttemptSink) TakeTrial(context.Context, string) (billingtrial.TakeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.takes++
	if s.takeErr != nil {
		return billingtrial.TakeResult{}, s.takeErr
	}
	return billingtrial.TakeResult{ID: uuid.New(), Used: s.takes, Granted: 20}, nil
}
func (s *trialAttemptSink) RefundTrial(context.Context, string, uuid.UUID) error { return nil }
func (s *trialAttemptSink) GrantTrial(context.Context, string) (bool, error)     { return false, nil }

func trialReason(t *testing.T, err error) (connect.Code, string, map[string]string) {
	t.Helper()
	var cErr *connect.Error
	if !errors.As(err, &cErr) {
		t.Fatalf("err = %v, want a Connect error", err)
	}
	for _, d := range cErr.Details() {
		v, derr := d.Value()
		if derr != nil {
			continue
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok {
			return cErr.Code(), info.Reason, info.Metadata
		}
	}
	t.Fatalf("no ErrorInfo on %v", err)
	return 0, "", nil
}

// A refusal on the trial row is terminal through the chat handler: no
// configured retry, no key rotation and no fallback target is tried, unary
// or streaming, and the caller gets ResourceExhausted with the reason.
func TestModelTrialRefusalNeverRetriesOrFallsBack(t *testing.T) {
	for name, takeErr := range map[string]error{
		"exhausted":    &billingtrial.RefusedError{Reason: billingtrial.ReasonExhausted},
		"rate limited": &billingtrial.RefusedError{Reason: billingtrial.ReasonRateLimited, RetryAfter: 3 * time.Second},
	} {
		for _, stream := range []bool{false, true} {
			sink := &trialAttemptSink{attemptSink: newAttemptSink(), takeErr: takeErr}
			fallback := &scriptedModel{name: "pb", model: "m-b"}
			s := attemptServer(t, sink.attemptSink,
				[]validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-b"), MaxAttempts: 3}},
				fallback)
			primary := &scriptedModel{name: "pp", model: "m-primary"}
			s.defaultBundle.Load().reg.Register(billing.Wrap(primary, billing.Config{Meter: sink, Provenance: billingmeter.ProvenanceTrial}))

			ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
			err := processChatCompletion(ctx, s, chatReq("m-primary", stream), func(*pb.ChatCompletionResponse) error { return nil })
			code, reason, meta := trialReason(t, err)
			if code != connect.CodeResourceExhausted {
				t.Fatalf("%s stream=%v: code = %v", name, stream, code)
			}
			if name == "exhausted" && reason != billingmeter.ReasonModelTrialExhausted {
				t.Fatalf("reason = %s", reason)
			}
			if name == "rate limited" && (reason != billingmeter.ReasonModelTrialRateLimited || meta["retry_after_seconds"] != "3") {
				t.Fatalf("reason = %s, meta = %v", reason, meta)
			}
			if sink.takes != 1 {
				t.Fatalf("%s stream=%v: takes = %d, want exactly 1 (no retry)", name, stream, sink.takes)
			}
			if primary.calls != 0 || fallback.calls != 0 {
				t.Fatalf("%s stream=%v: primary calls = %d, fallback calls = %d; want none", name, stream, primary.calls, fallback.calls)
			}
		}
	}
}

func TestModelTrialRefusalIsNotRetryable(t *testing.T) {
	r := billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialRateLimited, "m", 0, time.Second, time.Time{})
	wrapped := errors.Join(errors.New("ctx"), r)
	if retrypolicy.IsRetryable(wrapped) || retrypolicy.IsAuthenticationError(wrapped) {
		t.Fatal("a model trial refusal must never be retried or rotated")
	}
	if !gw.IsNonRetriableError(wrapped) {
		t.Fatal("a model trial refusal must skip the fallback plan")
	}
}

func trialRow() *provider_config.Configuration {
	return &provider_config.Configuration{ID: "trial", ProviderName: "openai", IsTrial: true, IsActive: true,
		APIKeyEncrypted: "sk-trial", EnabledModels: []string{"gpt-5.4-mini"}}
}

// The trial row joins a bundle only when nothing else serves openai or the
// trial model: a tenant's own openai row (active or not, keyed or not)
// removes it, as does any row listing the trial model.
func TestMergeTrialConfig(t *testing.T) {
	org := "org-a"
	own := func(provider string, models ...string) *provider_config.Configuration {
		return &provider_config.Configuration{ProviderName: provider, OrganizationID: &org, EnabledModels: models}
	}
	cases := map[string]struct {
		configs []*provider_config.Configuration
		want    bool
	}{
		"empty bundle":                   {nil, true},
		"other provider":                 {[]*provider_config.Configuration{own("anthropic", "claude-x")}, true},
		"tenant openai row":              {[]*provider_config.Configuration{own("openai", "gpt-4o")}, false},
		"inactive keyless openai row":    {[]*provider_config.Configuration{own("OpenAI")}, false},
		"platform openai row":            {[]*provider_config.Configuration{{ProviderName: "openai", EnabledModels: []string{"gpt-4o"}}}, false},
		"another row lists trial model":  {[]*provider_config.Configuration{own("openrouter", "GPT-5.4-mini")}, false},
		"another row lists other models": {[]*provider_config.Configuration{own("openrouter", "gpt-5.4")}, true},
	}
	for name, tc := range cases {
		got := mergeTrialConfig(tc.configs, trialRow())
		has := len(got) == len(tc.configs)+1 && got[len(got)-1].IsTrial
		if has != tc.want {
			t.Fatalf("%s: merged = %v, want %v", name, has, tc.want)
		}
	}
	if got := mergeTrialConfig(nil, nil); len(got) != 0 {
		t.Fatal("nil trial merged")
	}
	notTrial := trialRow()
	notTrial.IsTrial = false
	if got := mergeTrialConfig(nil, notTrial); len(got) != 0 {
		t.Fatal("a row without the trial flag was merged as the trial")
	}
}

// Off a shared gateway the trial row is never merged, and EVS_MODEL_TRIAL=off
// keeps it out of every bundle.
func TestTrialProviderConfigGates(t *testing.T) {
	repo := provider_config.NewRepository(nil)
	dedicated := &Server{ctx: context.Background()}
	if c := dedicated.trialProviderConfig(context.Background(), repo, nil); c != nil {
		t.Fatal("trial merged on a dedicated gateway")
	}
	shared := &Server{ctx: context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)}
	t.Setenv(modelTrialEnv, "off")
	if c := shared.trialProviderConfig(context.Background(), repo, nil); c != nil {
		t.Fatal("trial merged with EVS_MODEL_TRIAL=off")
	}
	t.Setenv(modelTrialEnv, "")
	// No billing DB: no active meter, so no trial.
	if c := shared.trialProviderConfig(context.Background(), repo, nil); c != nil {
		t.Fatal("trial merged without an active meter")
	}
}

func TestModelTrialHeaderValue(t *testing.T) {
	ctx, note := billingmeter.WithModelTrialNote(context.Background())
	if _, ok := modelTrialHeader(note); ok {
		t.Fatal("header set before any take")
	}
	billingmeter.NoteModelTrialRemaining(ctx, 7)
	if v, ok := modelTrialHeader(note); !ok || v != "remaining=7" {
		t.Fatalf("header = %q, %v", v, ok)
	}
}
