package channels

import (
	"context"
	"math"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ReconnectingConnector wraps a Connector with automatic reconnection
// using exponential backoff.
type ReconnectingConnector struct {
	inner   Connector
	factory func() (Connector, error) // re-creates the connector on reconnect
	config  ReconnectConfig

	status ConnectorStatus
}

// ReconnectConfig configures reconnection behavior.
type ReconnectConfig struct {
	InitialDelay time.Duration // Default: 1s
	MaxDelay     time.Duration // Default: 5m
	MaxRetries   int           // 0 = unlimited
	Multiplier   float64       // Default: 2.0
}

// DefaultReconnectConfig returns sensible defaults.
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     5 * time.Minute,
		MaxRetries:   0, // unlimited
		Multiplier:   2.0,
	}
}

// NewReconnectingConnector wraps a connector with auto-reconnect.
func NewReconnectingConnector(inner Connector, factory func() (Connector, error), config ReconnectConfig) *ReconnectingConnector {
	if config.InitialDelay <= 0 {
		config.InitialDelay = 1 * time.Second
	}
	if config.MaxDelay <= 0 {
		config.MaxDelay = 5 * time.Minute
	}
	if config.Multiplier <= 0 {
		config.Multiplier = 2.0
	}

	return &ReconnectingConnector{
		inner:   inner,
		factory: factory,
		config:  config,
		status:  StatusDisconnected,
	}
}

// minStableDuration is the minimum time Start must run before we consider
// the connection to have been successfully established. If Start returns
// faster than this, we treat it as an immediate failure and apply backoff.
const minStableDuration = 30 * time.Second

func (rc *ReconnectingConnector) Start(ctx context.Context) error {
	attempt := 0
	delay := rc.config.InitialDelay

	for {
		rc.status = StatusConnecting

		startedAt := time.Now()
		err := rc.inner.Start(ctx)
		uptime := time.Since(startedAt)

		// Check if context was cancelled (graceful shutdown)
		if ctx.Err() != nil {
			rc.status = StatusDisconnected
			return ctx.Err()
		}

		// Connection dropped
		rc.status = StatusError

		// If the connection was stable (ran for a meaningful duration),
		// reset backoff — this was a real disconnect, not a startup failure.
		if uptime >= minStableDuration {
			attempt = 0
			delay = rc.config.InitialDelay
		}

		attempt++

		if rc.config.MaxRetries > 0 && attempt >= rc.config.MaxRetries {
			logger.WithFields("attempts", attempt, "error", err).
				Error("channels: max reconnect attempts reached, giving up")
			return err
		}

		logger.WithFields("attempt", attempt, "delay_s", delay.Seconds(), "uptime_s", uptime.Seconds(), "error", err).
			Warn("channels: connector disconnected, reconnecting")

		// Wait with exponential backoff
		select {
		case <-ctx.Done():
			rc.status = StatusDisconnected
			return ctx.Err()
		case <-time.After(delay):
		}

		// Exponential backoff for next attempt
		delay = time.Duration(float64(delay) * rc.config.Multiplier)
		if delay > rc.config.MaxDelay {
			delay = rc.config.MaxDelay
		}

		// Try to re-create the connector
		if rc.factory != nil {
			newConn, err := rc.factory()
			if err != nil {
				logger.WithError(err).Warn("channels: failed to recreate connector")
				continue
			}
			rc.inner = newConn
		}
	}
}

func (rc *ReconnectingConnector) Stop(ctx context.Context) error {
	rc.status = StatusDisconnected
	return rc.inner.Stop(ctx)
}

func (rc *ReconnectingConnector) Send(ctx context.Context, channelRef string, threadRef string, msg OutboundMessage) (string, error) {
	return rc.inner.Send(ctx, channelRef, threadRef, msg)
}

func (rc *ReconnectingConnector) SendTyping(ctx context.Context, channelRef string) error {
	return rc.inner.SendTyping(ctx, channelRef)
}

func (rc *ReconnectingConnector) EditMessage(ctx context.Context, channelRef string, messageRef string, msg OutboundMessage) error {
	return rc.inner.EditMessage(ctx, channelRef, messageRef, msg)
}

func (rc *ReconnectingConnector) Status() ConnectorStatus {
	return rc.inner.Status()
}

func (rc *ReconnectingConnector) Platform() Platform {
	return rc.inner.Platform()
}

// backoffDelay calculates the backoff delay for a given attempt.
func backoffDelay(attempt int, initial, max time.Duration, multiplier float64) time.Duration {
	delay := time.Duration(float64(initial) * math.Pow(multiplier, float64(attempt)))
	if delay > max {
		delay = max
	}
	return delay
}
