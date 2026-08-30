package fcagent

import (
	"fmt"
	"testing"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func baseReq() isolation.ExecutionRequest {
	return isolation.ExecutionRequest{
		FunctionID:  "fn-1",
		TenantID:    "tenant-1",
		Runtime:     isolation.RuntimeNodeJS20,
		Code:        "export default () => 1",
		Packages:    []string{"lodash"},
		MemoryMB:    512,
		VCPUs:       1,
		NetworkMode: isolation.NetworkDeny,
	}
}

func TestComputeEnvKey_Deterministic(t *testing.T) {
	a := computeEnvKey(baseReq())
	b := computeEnvKey(baseReq())
	if a != b {
		t.Fatalf("env key not deterministic: %s != %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected sha256 hex (64 chars), got %d", len(a))
	}
}

func TestComputeEnvKey_SensitiveToCode(t *testing.T) {
	r := baseReq()
	r.Code = "export default () => 2"
	if computeEnvKey(baseReq()) == computeEnvKey(r) {
		t.Fatalf("env key should change when code changes")
	}
}

func TestComputeEnvKey_SensitiveToSizing(t *testing.T) {
	r := baseReq()
	r.MemoryMB = 1024
	if computeEnvKey(baseReq()) == computeEnvKey(r) {
		t.Fatalf("env key should change when memory changes (VM is sized once)")
	}
}

func TestComputeEnvKey_SensitiveToFunctionID(t *testing.T) {
	r := baseReq()
	r.FunctionID = "fn-2"
	if computeEnvKey(baseReq()) == computeEnvKey(r) {
		t.Fatalf("distinct functions must not share an env even with identical code")
	}
}

func TestComputeEnvKey_SensitiveToNetwork(t *testing.T) {
	r := baseReq()
	r.NetworkMode = isolation.NetworkAllow
	if computeEnvKey(baseReq()) == computeEnvKey(r) {
		t.Fatalf("env key should change with network mode")
	}
}

func TestResultFromProto(t *testing.T) {
	out := resultFromProto(&fcpb.InvokeFunctionResult{
		Success:    true,
		ResultJson: `{"n":42}`,
		Stdout:     "log",
		DurationMs: 12,
	})
	if !out.Success {
		t.Fatalf("expected success")
	}
	obj, ok := out.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", out.Result)
	}
	if obj["n"].(float64) != 42 {
		t.Fatalf("expected n=42, got %#v", obj["n"])
	}
	if out.DurationMS != 12 {
		t.Fatalf("expected duration 12, got %d", out.DurationMS)
	}
}

func TestResultFromProto_ErrorPassthrough(t *testing.T) {
	out := resultFromProto(&fcpb.InvokeFunctionResult{
		Success:   false,
		Error:     "boom",
		ErrorType: string(isolation.ErrorTypeTimeout),
	})
	if out.Success {
		t.Fatalf("expected failure")
	}
	if out.Error != "boom" || out.ErrorType != isolation.ErrorTypeTimeout {
		t.Fatalf("error not passed through: %+v", out)
	}
}

func TestResultFromProto_Nil(t *testing.T) {
	out := resultFromProto(nil)
	if out.Success {
		t.Fatalf("nil proto should map to failure")
	}
}

func TestIsRetryableInvokeError(t *testing.T) {
	if isRetryableInvokeError(nil) {
		t.Fatalf("nil must not be retryable")
	}
	if !isRetryableInvokeError(status.Error(codes.Unavailable, "agent down")) {
		t.Fatalf("Unavailable must fail over to another agent")
	}
	if !isRetryableInvokeError(fmt.Errorf("VM pool exhausted")) {
		t.Fatalf("capacity error must fail over")
	}
	// Post-dispatch errors must NOT retry — a side-effecting function
	// would double-execute on another agent.
	if isRetryableInvokeError(status.Error(codes.InvalidArgument, "bad request")) {
		t.Fatalf("InvalidArgument must not retry")
	}
	if isRetryableInvokeError(status.Error(codes.Internal, "handler panicked")) {
		t.Fatalf("Internal must not retry (may have already executed)")
	}
}
