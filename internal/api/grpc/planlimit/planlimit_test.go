package planlimit

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/everstacklabs/everstack/internal/enterprise"
)

func TestPlanCapBecomesResourceExhaustedWithErrorInfo(t *testing.T) {
	limitErr := &enterprise.LimitError{Type: enterprise.UsageTypeAgents, Resource: "agent", Limit: 3, Used: 3, Requested: 1, Tier: "free"}
	cErr := ConnectError(limitErr, "agent")
	if cErr.Code() != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", cErr.Code())
	}
	if cErr.Message() != limitErr.Error() {
		t.Fatalf("message = %q, want the customer-facing text", cErr.Message())
	}
	var found bool
	for _, d := range cErr.Details() {
		v, err := d.Value()
		if err != nil {
			t.Fatal(err)
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetReason() == enterprise.PlanLimitReason {
			found = info.GetMetadata()["limit_type"] == "AGENTS"
		}
	}
	if !found {
		t.Fatal("missing plan_limit_reached ErrorInfo detail")
	}
}

func TestInternalCheckFailureNeverLeaksDriverText(t *testing.T) {
	cErr := ConnectError(errors.New("failed to check agent limit: dial tcp 10.0.3.7:5432: connection refused"), "agent")
	if cErr.Code() != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", cErr.Code())
	}
	if strings.Contains(cErr.Message(), "10.0.3.7") {
		t.Fatalf("message leaks driver text: %q", cErr.Message())
	}
	if ConnectError(nil, "agent") != nil {
		t.Fatal("nil error must map to nil")
	}
}

func TestUnavailableLimitCheckIsRetryable(t *testing.T) {
	cErr := ConnectError(fmt.Errorf("check: %w", enterprise.ErrLimitCheckUnavailable), "agent")
	if cErr.Code() != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable (retryable)", cErr.Code())
	}
}
