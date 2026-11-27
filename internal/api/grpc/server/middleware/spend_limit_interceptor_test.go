package middleware

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	licensemonitor "github.com/everstacklabs/everstack/internal/services/license_monitor"
)

// blockedMonitor builds a monitor whose local spend tracking reports a block.
func blockedMonitor(t *testing.T) *licensemonitor.Monitor {
	t.Helper()
	m := licensemonitor.NewMonitor(nil, licensemonitor.Config{})
	// A $0 block limit with $0 spent is "exceeded": the monitor blocks.
	m.SetSpendLimitConfig(0, string(licensemonitor.SpendLimitActionBlock), true)
	blocked, _ := m.IsSpendBlocked()
	if !blocked {
		t.Fatal("test monitor must report a spend block")
	}
	return m
}

func TestSpendLimitInterceptorBlocksSelfHostedGateway(t *testing.T) {
	licensemonitor.SetManagedGateway(false)
	i := NewSpendLimitInterceptor(blockedMonitor(t), nil)
	err := i.checkSpendLimit("/everstack.gateway.v1.GatewayService/ChatCompletion")
	if err == nil {
		t.Fatal("self-hosted gateway must block on an exceeded spend limit")
	}
	if code := connect.CodeOf(err); code != connect.CodeResourceExhausted {
		t.Fatalf("code = %s, want resource_exhausted", code)
	}
}

// Cloud sells prepaid credit. The invoice-fed spend limit never blocks on a
// managed gateway: it allows and logs, and the wallet admission decides.
func TestSpendLimitInterceptorAllowsManagedGateway(t *testing.T) {
	m := blockedMonitor(t)
	licensemonitor.SetManagedGateway(true)
	t.Cleanup(func() { licensemonitor.SetManagedGateway(false) })
	i := NewSpendLimitInterceptor(m, nil)
	if err := i.checkSpendLimit("/everstack.gateway.v1.GatewayService/ChatCompletion"); err != nil {
		t.Fatalf("managed gateway must allow, got %v", errors.Unwrap(err))
	}
}
