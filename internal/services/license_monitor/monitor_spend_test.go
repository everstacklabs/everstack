package license_monitor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMonitor() *Monitor {
	return NewMonitor(nil, Config{
		CheckInterval: 1 * time.Hour,
	})
}

func TestSetSpendLimitConfig_UpdatesLocalState(t *testing.T) {
	m := newTestMonitor()

	m.SetSpendLimitConfig(150.00, "warn", true)

	m.mu.RLock()
	defer m.mu.RUnlock()

	assert.Equal(t, 150.00, m.localLimitAmount)
	assert.Equal(t, SpendLimitActionWarn, m.localLimitAction)
	assert.True(t, m.localLimitEnabled)
}

func TestSetSpendLimitConfig_SetsJWTFlag(t *testing.T) {
	m := newTestMonitor()

	assert.False(t, m.spendLimitConfigFromJWT, "should be false before SetSpendLimitConfig")

	m.SetSpendLimitConfig(50.00, "block", true)

	m.mu.RLock()
	defer m.mu.RUnlock()

	assert.True(t, m.spendLimitConfigFromJWT)
}

func TestSetSpendLimitConfig_DefaultsToBlockOnInvalidAction(t *testing.T) {
	m := newTestMonitor()

	m.SetSpendLimitConfig(100.00, "invalid_action", true)

	m.mu.RLock()
	defer m.mu.RUnlock()

	assert.Equal(t, SpendLimitActionBlock, m.localLimitAction)
}

func TestSetSpendLimitConfig_DisabledUnblocksPreviouslyBlocked(t *testing.T) {
	m := newTestMonitor()

	// Simulate a blocked state
	m.mu.Lock()
	m.spendBlocked = true
	m.spendBlockedReason = "spend limit exceeded"
	m.mu.Unlock()

	// Disable spend limit via JWT
	m.SetSpendLimitConfig(0, "block", false)

	m.mu.RLock()
	defer m.mu.RUnlock()

	assert.False(t, m.spendBlocked, "should be unblocked after disabling spend limit")
	assert.Empty(t, m.spendBlockedReason)
}

func TestSetSpendLimitConfig_BlocksWhenSpendExceedsLimit(t *testing.T) {
	m := newTestMonitor()

	// Set current spend above the limit we're about to configure
	m.mu.Lock()
	m.localCurrentSpend = 200.00
	m.mu.Unlock()

	m.SetSpendLimitConfig(100.00, "block", true)

	m.mu.RLock()
	defer m.mu.RUnlock()

	assert.True(t, m.spendBlocked)
	assert.Contains(t, m.spendBlockedReason, "Spend limit exceeded")
}

func TestSetSpendLimitConfig_WarnActionDoesNotBlock(t *testing.T) {
	m := newTestMonitor()

	m.mu.Lock()
	m.localCurrentSpend = 200.00
	m.mu.Unlock()

	m.SetSpendLimitConfig(100.00, "warn", true)

	m.mu.RLock()
	defer m.mu.RUnlock()

	assert.False(t, m.spendBlocked, "warn action should not block even when spend exceeds limit")
}

func TestCalculateSpendSyncInterval_NoLimit(t *testing.T) {
	m := newTestMonitor()

	// No limit enabled (default state)
	interval := m.calculateSpendSyncInterval()
	assert.Equal(t, 5*time.Minute, interval)
}

func TestCalculateSpendSyncInterval_BelowFiftyPercent(t *testing.T) {
	m := newTestMonitor()

	m.mu.Lock()
	m.localLimitEnabled = true
	m.localLimitAmount = 100.00
	m.localCurrentSpend = 30.00 // 30%
	m.mu.Unlock()

	interval := m.calculateSpendSyncInterval()
	assert.Equal(t, 5*time.Minute, interval)
}

func TestCalculateSpendSyncInterval_FiftyToEightyPercent(t *testing.T) {
	m := newTestMonitor()

	m.mu.Lock()
	m.localLimitEnabled = true
	m.localLimitAmount = 100.00
	m.localCurrentSpend = 65.00 // 65%
	m.mu.Unlock()

	interval := m.calculateSpendSyncInterval()
	assert.Equal(t, 1*time.Minute, interval)
}

func TestCalculateSpendSyncInterval_AboveEightyPercent(t *testing.T) {
	m := newTestMonitor()

	m.mu.Lock()
	m.localLimitEnabled = true
	m.localLimitAmount = 100.00
	m.localCurrentSpend = 85.00 // 85%
	m.mu.Unlock()

	interval := m.calculateSpendSyncInterval()
	assert.Equal(t, 15*time.Second, interval)
}

func TestCalculateSpendSyncInterval_ExactBoundaries(t *testing.T) {
	m := newTestMonitor()

	m.mu.Lock()
	m.localLimitEnabled = true
	m.localLimitAmount = 100.00
	m.mu.Unlock()

	tests := []struct {
		name     string
		spend    float64
		expected time.Duration
	}{
		{"exactly 50%", 50.00, 1 * time.Minute},
		{"exactly 80%", 80.00, 15 * time.Second},
		{"exactly 0%", 0.00, 5 * time.Minute},
		{"exactly 100%", 100.00, 15 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.mu.Lock()
			m.localCurrentSpend = tt.spend
			m.mu.Unlock()

			interval := m.calculateSpendSyncInterval()
			assert.Equal(t, tt.expected, interval)
		})
	}
}

func TestCalculateSpendSyncInterval_ZeroLimitAmount(t *testing.T) {
	m := newTestMonitor()

	m.mu.Lock()
	m.localLimitEnabled = true
	m.localLimitAmount = 0 // zero limit amount
	m.localCurrentSpend = 50.00
	m.mu.Unlock()

	interval := m.calculateSpendSyncInterval()
	require.Equal(t, 5*time.Minute, interval, "zero limit amount should return default 5min interval")
}
