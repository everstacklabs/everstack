// Package trial provides anonymous trial mode functionality for the Everstack gateway.
// It allows users to test the gateway without requiring license activation,
// with restrictive usage limits.
package trial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Default trial limits - aligned with Free tier in plans.json
const (
	DefaultTrialDuration   = 14 * 24 * time.Hour // 14 days trial
	DefaultDailyLimit      = 10000               // 10K requests total (monthly, but shown as daily for trial simplicity)
	DefaultRPM             = 60                  // 60 requests per minute (matches Free tier)
	DefaultTotalTraceLimit = 100                 // 100 traces total
	DefaultTokenLimit      = 1000000             // 1M tokens (matches Free tier)
)

// Errors
var (
	ErrTrialExpired      = errors.New("trial period has expired")
	ErrDailyLimitReached = errors.New("daily request limit reached")
	ErrRPMLimitReached   = errors.New("requests per minute limit reached")
	ErrTokenLimitReached = errors.New("token limit reached")
	ErrTrialNotActive    = errors.New("trial mode is not active")
)

// State represents the current trial state
type State struct {
	Fingerprint    string    `json:"fingerprint"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	DailyLimit     int64     `json:"daily_limit"`
	DailyUsed      int64     `json:"daily_used"`
	DayStart       time.Time `json:"day_start"`
	TotalRequests  int64     `json:"total_requests"`
	RPMCount       int64     `json:"rpm_count"`
	RPMWindowStart time.Time `json:"rpm_window_start"`
	// Token tracking
	TokenLimit int64 `json:"token_limit"`
	TokensUsed int64 `json:"tokens_used"`
}

// Config holds trial configuration
type Config struct {
	Duration   time.Duration
	DailyLimit int64
	RPMLimit   int64
	TokenLimit int64
}

// DefaultConfig returns the default trial configuration
func DefaultConfig() Config {
	return Config{
		Duration:   DefaultTrialDuration,
		DailyLimit: DefaultDailyLimit,
		RPMLimit:   DefaultRPM,
		TokenLimit: DefaultTokenLimit,
	}
}

// Storage interface for persisting trial state
type Storage interface {
	Load(ctx context.Context, fingerprint string) (*State, error)
	Save(ctx context.Context, state *State) error
}

// Manager manages trial mode state and enforcement
type Manager struct {
	config  Config
	storage Storage
	state   *State
	mu      sync.RWMutex
	active  bool
}

// NewManager creates a new trial manager
func NewManager(cfg Config, storage Storage) *Manager {
	if cfg.Duration == 0 {
		cfg.Duration = DefaultTrialDuration
	}
	if cfg.DailyLimit == 0 {
		cfg.DailyLimit = DefaultDailyLimit
	}
	if cfg.RPMLimit == 0 {
		cfg.RPMLimit = DefaultRPM
	}

	return &Manager{
		config:  cfg,
		storage: storage,
		active:  false,
	}
}

// Initialize sets up trial mode for the current instance.
// Should be called at gateway startup when no license is configured.
func (m *Manager) Initialize(ctx context.Context) error {
	fingerprint := GenerateFingerprint()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Try to load existing trial state
	if m.storage != nil {
		state, err := m.storage.Load(ctx, fingerprint)
		if err == nil && state != nil {
			m.state = state
			m.active = true
			logger.Infof("trial: loaded existing trial state (expires: %s, daily used: %d/%d)",
				state.ExpiresAt.Format(time.RFC3339), state.DailyUsed, state.DailyLimit)
			return nil
		}
	}

	// Create new trial state
	now := time.Now().UTC()
	m.state = &State{
		Fingerprint:    fingerprint,
		CreatedAt:      now,
		ExpiresAt:      now.Add(m.config.Duration),
		DailyLimit:     m.config.DailyLimit,
		DailyUsed:      0,
		DayStart:       startOfDay(now),
		TotalRequests:  0,
		RPMCount:       0,
		RPMWindowStart: now,
		TokenLimit:     m.config.TokenLimit,
		TokensUsed:     0,
	}
	m.active = true

	// Persist the new state
	if m.storage != nil {
		if err := m.storage.Save(ctx, m.state); err != nil {
			logger.Warnf("trial: failed to persist trial state: %v", err)
		}
	}

	logger.Infof("trial: initialized new trial (expires: %s, daily limit: %d, token limit: %d)",
		m.state.ExpiresAt.Format(time.RFC3339), m.state.DailyLimit, m.state.TokenLimit)

	return nil
}

// IsActive returns whether trial mode is active
func (m *Manager) IsActive() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active && m.state != nil
}

// IsExpired checks if the trial period has expired
func (m *Manager) IsExpired() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state == nil {
		return true
	}
	return time.Now().UTC().After(m.state.ExpiresAt)
}

// RecordRequest records a request and enforces trial limits.
// Returns an error if any limit is exceeded.
func (m *Manager) RecordRequest(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == nil || !m.active {
		return ErrTrialNotActive
	}

	now := time.Now().UTC()

	// Check if trial has expired
	if now.After(m.state.ExpiresAt) {
		return ErrTrialExpired
	}

	// Reset daily counter if new day
	todayStart := startOfDay(now)
	if todayStart.After(m.state.DayStart) {
		m.state.DailyUsed = 0
		m.state.DayStart = todayStart
	}

	// Check daily limit
	if m.state.DailyUsed >= m.state.DailyLimit {
		return ErrDailyLimitReached
	}

	// Reset RPM counter if new minute window
	if now.Sub(m.state.RPMWindowStart) >= time.Minute {
		m.state.RPMCount = 0
		m.state.RPMWindowStart = now
	}

	// Check RPM limit
	if m.state.RPMCount >= m.config.RPMLimit {
		return ErrRPMLimitReached
	}

	// Increment counters
	m.state.DailyUsed++
	m.state.TotalRequests++
	m.state.RPMCount++

	// Persist state asynchronously (don't block request)
	if m.storage != nil {
		go func() {
			if err := m.storage.Save(context.Background(), m.state); err != nil {
				logger.Debugf("trial: failed to persist state: %v", err)
			}
		}()
	}

	return nil
}

// RecordTokens records token usage and enforces token limits.
// Should be called after a successful request with the number of tokens used.
// Returns an error if the token limit would be exceeded.
func (m *Manager) RecordTokens(ctx context.Context, tokens int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == nil || !m.active {
		return ErrTrialNotActive
	}

	now := time.Now().UTC()

	// Check if trial has expired
	if now.After(m.state.ExpiresAt) {
		return ErrTrialExpired
	}

	// Check if adding these tokens would exceed the limit
	if m.state.TokenLimit > 0 && m.state.TokensUsed+tokens > m.state.TokenLimit {
		return ErrTokenLimitReached
	}

	// Increment token counter
	m.state.TokensUsed += tokens

	// Persist state asynchronously (don't block request)
	if m.storage != nil {
		go func() {
			if err := m.storage.Save(context.Background(), m.state); err != nil {
				logger.Debugf("trial: failed to persist state after token record: %v", err)
			}
		}()
	}

	return nil
}

// CheckTokenLimit checks if the token limit would be exceeded without recording.
// Useful for pre-flight checks before making API calls.
func (m *Manager) CheckTokenLimit(tokens int64) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state == nil || !m.active {
		return ErrTrialNotActive
	}

	if time.Now().UTC().After(m.state.ExpiresAt) {
		return ErrTrialExpired
	}

	if m.state.TokenLimit > 0 && m.state.TokensUsed+tokens > m.state.TokenLimit {
		return ErrTokenLimitReached
	}

	return nil
}

// GetState returns a copy of the current trial state
func (m *Manager) GetState() *State {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state == nil {
		return nil
	}

	// Return a copy
	stateCopy := *m.state
	return &stateCopy
}

// UpdateLimitsFromService updates the trial limits based on License Service response
// This allows the License Service to be the authoritative source of trial limits
func (m *Manager) UpdateLimitsFromService(dailyLimit, monthlyLimit, rpm, tokenLimit int64, expiresAt time.Time, limitsExceeded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == nil {
		return
	}

	// Update configuration (authoritative from License Service)
	if dailyLimit > 0 {
		m.state.DailyLimit = dailyLimit
	}
	if rpm > 0 {
		m.config.RPMLimit = rpm
	}
	if tokenLimit > 0 {
		m.state.TokenLimit = tokenLimit
		m.config.TokenLimit = tokenLimit
	}
	if !expiresAt.IsZero() {
		m.state.ExpiresAt = expiresAt
	}

	// If limits exceeded from License Service, mark trial as expired
	// This is the authoritative signal from the License Service
	if limitsExceeded {
		// Set expiration to now to block further requests
		m.state.ExpiresAt = time.Now().UTC()
		logger.Warn("trial: limits exceeded per License Service - trial blocked")
	}

	logger.WithFields(
		"daily_limit", m.state.DailyLimit,
		"rpm_limit", m.config.RPMLimit,
		"token_limit", m.state.TokenLimit,
		"expires_at", m.state.ExpiresAt.Format(time.RFC3339),
	).Debug("trial: limits updated from License Service")
}

// SetLimitsExceeded immediately blocks the trial due to License Service limit exceeded signal
func (m *Manager) SetLimitsExceeded() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == nil {
		return
	}

	// Set expiration to now to block further requests
	m.state.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	logger.Warn("trial: limits exceeded - trial immediately blocked")
}

// GetStatus returns a summary of the trial status for API responses
func (m *Manager) GetStatus() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state == nil {
		return map[string]interface{}{
			"active":  false,
			"message": "Trial mode is not initialized",
		}
	}

	now := time.Now().UTC()
	expired := now.After(m.state.ExpiresAt)
	daysRemaining := int(m.state.ExpiresAt.Sub(now).Hours() / 24)
	if daysRemaining < 0 {
		daysRemaining = 0
	}

	return map[string]interface{}{
		"active":           m.active && !expired,
		"expired":          expired,
		"created_at":       m.state.CreatedAt.Format(time.RFC3339),
		"expires_at":       m.state.ExpiresAt.Format(time.RFC3339),
		"days_remaining":   daysRemaining,
		"daily_used":       m.state.DailyUsed,
		"daily_limit":      m.state.DailyLimit,
		"total_requests":   m.state.TotalRequests,
		"tokens_used":      m.state.TokensUsed,
		"token_limit":      m.state.TokenLimit,
		"rpm_limit":        m.config.RPMLimit,
		"fingerprint_hash": m.state.Fingerprint[:8] + "...", // Partial for privacy
	}
}

// Deactivate disables trial mode (e.g., when a license is activated)
func (m *Manager) Deactivate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = false
	logger.Info("trial: deactivated trial mode")
}

// startOfDay returns the start of the day (midnight UTC) for the given time
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// MarshalJSON implements json.Marshaler for State
func (s *State) MarshalJSON() ([]byte, error) {
	type Alias State
	return json.Marshal(&struct {
		*Alias
		CreatedAt      string `json:"created_at"`
		ExpiresAt      string `json:"expires_at"`
		DayStart       string `json:"day_start"`
		RPMWindowStart string `json:"rpm_window_start"`
	}{
		Alias:          (*Alias)(s),
		CreatedAt:      s.CreatedAt.Format(time.RFC3339),
		ExpiresAt:      s.ExpiresAt.Format(time.RFC3339),
		DayStart:       s.DayStart.Format(time.RFC3339),
		RPMWindowStart: s.RPMWindowStart.Format(time.RFC3339),
	})
}

// UnmarshalJSON implements json.Unmarshaler for State
func (s *State) UnmarshalJSON(data []byte) error {
	type Alias State
	aux := &struct {
		*Alias
		CreatedAt      string `json:"created_at"`
		ExpiresAt      string `json:"expires_at"`
		DayStart       string `json:"day_start"`
		RPMWindowStart string `json:"rpm_window_start"`
	}{
		Alias: (*Alias)(s),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	var err error
	if aux.CreatedAt != "" {
		s.CreatedAt, err = time.Parse(time.RFC3339, aux.CreatedAt)
		if err != nil {
			return fmt.Errorf("parse created_at: %w", err)
		}
	}
	if aux.ExpiresAt != "" {
		s.ExpiresAt, err = time.Parse(time.RFC3339, aux.ExpiresAt)
		if err != nil {
			return fmt.Errorf("parse expires_at: %w", err)
		}
	}
	if aux.DayStart != "" {
		s.DayStart, err = time.Parse(time.RFC3339, aux.DayStart)
		if err != nil {
			return fmt.Errorf("parse day_start: %w", err)
		}
	}
	if aux.RPMWindowStart != "" {
		s.RPMWindowStart, err = time.Parse(time.RFC3339, aux.RPMWindowStart)
		if err != nil {
			return fmt.Errorf("parse rpm_window_start: %w", err)
		}
	}

	return nil
}
