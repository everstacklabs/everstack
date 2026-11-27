package license_monitor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// On a managed (Cloud) gateway the invoice-fed spend limit never blocks: Cloud
// sells prepaid credit and the wallet admission replaces it. Every enforcement
// point (gRPC interceptor, metered REST, openai-agents, WithLicenseMonitoring)
// asks IsSpendBlocked, so the decision lives here.
func TestIsSpendBlocked_ManagedGatewayNeverBlocks(t *testing.T) {
	SetManagedGateway(true)
	t.Cleanup(func() { SetManagedGateway(false) })

	flagged := newTestMonitor()
	flagged.spendBlocked, flagged.spendBlockedReason = true, "Spend limit exceeded (flag)"
	blocked, reason := flagged.IsSpendBlocked()
	assert.False(t, blocked, "explicit spend-blocked flag must not block on a managed gateway")
	assert.Empty(t, reason)

	// Prod shape: $50 block limit. Even once local spend reaches it, nothing blocks.
	local := newTestMonitor()
	local.SetSpendLimitConfig(50.00, "block", true)
	local.mu.Lock()
	local.localCurrentSpend = 50.00
	local.licenseState = &LicenseState{SpendLimits: []SpendLimit{{
		LimitType: SpendLimitTypeEstimatedCost, LimitAmount: 50, ActionOnExceed: SpendLimitActionBlock, Enabled: true,
	}}}
	local.mu.Unlock()
	blocked, _ = local.IsSpendBlocked()
	assert.False(t, blocked, "local block limit must not block on a managed gateway")

	allowed, _ := local.CheckSpendLimitBeforeRequest(1)
	assert.True(t, allowed, "pre-request spend check must not block on a managed gateway")
}

// Self-hosted gateways keep enforcing their spend limits.
func TestIsSpendBlocked_SelfHostedStillBlocks(t *testing.T) {
	SetManagedGateway(false)

	m := newTestMonitor()
	m.spendBlocked, m.spendBlockedReason = true, "Spend limit exceeded (flag)"
	blocked, reason := m.IsSpendBlocked()
	assert.True(t, blocked)
	assert.NotEmpty(t, reason)
}
