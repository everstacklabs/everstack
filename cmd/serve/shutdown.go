package serve

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	// defaultShutdownDrainTimeout bounds how long in-flight HTTP requests
	// (LLM streams, agent turns) get to finish after SIGTERM. It is the default
	// for server.config.shutdown_drain_timeout / EVS_SERVER_SHUTDOWN_DRAIN_TIMEOUT.
	//
	// A long stream never finishes on its own, so every deploy with one in
	// flight spends this whole budget. The gateway chart gives the process
	// terminationGracePeriodSeconds (45) minus preStopSleepSeconds (15) = 30s
	// before SIGKILL, and the wallet settlement and writer flushes that follow
	// the drain must still fit inside it: a drain that used all 30s would get
	// the settlement killed. Raise this only together with the chart's grace
	// period.
	defaultShutdownDrainTimeout = 10 * time.Second

	// Budgets for the stages that run after the HTTP drain. Each stage gets its
	// own fresh deadline so a slow stage never starves the next one.
	componentShutdownTimeout = 5 * time.Second  // sandbox proxy, agent sessions
	activityFlushTimeout     = 15 * time.Second // bot-activity recorder flush
	telemetryShutdownTimeout = 15 * time.Second // OTEL providers (own 10s inside), collector
)

// globalShutdownDrainTimeout is the resolved HTTP drain budget, set once config
// is loaded. Zero falls back to defaultShutdownDrainTimeout.
var globalShutdownDrainTimeout time.Duration

// shutdownDrainTimeoutFromConfig reads server.config.shutdown_drain_timeout.
// The validator rejects bad values at load; an unset or unusable value here
// (programmatic config) falls back to the default.
func shutdownDrainTimeoutFromConfig(config *validator.Config) time.Duration {
	if config == nil || config.Server == nil {
		return defaultShutdownDrainTimeout
	}
	d, err := validator.ParseShutdownDrainTimeout(config.Server.Config.ShutdownDrainTimeout)
	if err != nil {
		return defaultShutdownDrainTimeout
	}
	return d
}

// shutdownStep is one unit of the shutdown sequence.
type shutdownStep struct {
	name string
	// budget is the step's own deadline, applied to a context that is
	// detached from the parent's cancellation (a signalled parent is already
	// dead on arrival). Zero means the step bounds itself.
	budget time.Duration
	run    func(ctx context.Context) error
	// report marks a step whose error is returned to the caller; other
	// errors are logged and the sequence continues.
	report bool
}

// runShutdownPhases runs the phases in order. Steps inside one phase run
// concurrently, and a phase starts only after the previous one has finished,
// so an earlier phase can never be cut short by a later one.
func runShutdownPhases(parent context.Context, phases [][]shutdownStep) error {
	base := context.WithoutCancel(parent)
	var reported error
	var mu sync.Mutex
	for _, phase := range phases {
		var wg sync.WaitGroup
		for _, step := range phase {
			wg.Add(1)
			go func(s shutdownStep) {
				defer wg.Done()
				ctx := base
				if s.budget > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(base, s.budget)
					defer cancel()
				}
				if err := s.run(ctx); err != nil {
					if s.report {
						mu.Lock()
						if reported == nil {
							reported = err
						}
						mu.Unlock()
						return
					}
					logger.WithFields("step", s.name, "error", err.Error()).Warn("shutdown step failed")
				}
			}(step)
		}
		wg.Wait()
	}
	return reported
}

// bounded runs a context-unaware shutdown func so that it cannot outlive ctx.
func bounded(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// gatewayShutdownPhases builds the production order:
//
//  1. sandbox proxy, then agent sessions (checkpoint in-flight turns while
//     their connections are still open, as before)
//  2. stop accepting and drain in-flight HTTP (its own, configurable budget)
//  3. wallet-debit settlement
//  4. batched writers (bot activity, events), which cannot get more input now
//  5. telemetry last, so the spans of the requests drained in step 2 are
//     exported before the exporters stop
func gatewayShutdownPhases(server *http.Server, drainTimeout time.Duration) [][]shutdownStep {
	if drainTimeout <= 0 {
		drainTimeout = defaultShutdownDrainTimeout
	}
	var phases [][]shutdownStep
	if globalSandboxProxyShutdown != nil {
		phases = append(phases, []shutdownStep{{name: "sandbox proxy", budget: componentShutdownTimeout, run: globalSandboxProxyShutdown}})
	}
	if globalAgentsShutdown != nil {
		phases = append(phases, []shutdownStep{{name: "agent sessions", budget: componentShutdownTimeout, run: globalAgentsShutdown}})
	}
	phases = append(phases, []shutdownStep{{
		name: "http drain", budget: drainTimeout, report: true,
		run: func(ctx context.Context) error {
			err := server.Shutdown(ctx)
			if err != nil {
				// Budget spent: cut the stragglers so their handlers return and
				// their spans end before telemetry flushes.
				_ = server.Close()
			}
			return err
		},
	}})
	// ShutdownAll drains on its own detached budget (see billingmeter), so it
	// is not given one here.
	phases = append(phases, []shutdownStep{{name: "billing settlement", run: func(ctx context.Context) error {
		billingmeter.ShutdownAll(ctx)
		return nil
	}}})
	var flushes []shutdownStep
	if globalActivityRecorder != nil {
		flushes = append(flushes, shutdownStep{name: "bot activity", budget: activityFlushTimeout, run: globalActivityRecorder.Stop})
	}
	if globalEventsShutdown != nil {
		flushes = append(flushes, shutdownStep{name: "event writer", budget: eventsDrainTimeout, run: func(ctx context.Context) error {
			if err := globalEventsShutdown(ctx); err != nil {
				return fmt.Errorf("event writer drain failed; buffered events lost: %w", err)
			}
			return nil
		}})
	}
	if len(flushes) > 0 {
		phases = append(phases, flushes)
	}
	if globalOTELShutdown != nil {
		phases = append(phases, []shutdownStep{{name: "opentelemetry", budget: telemetryShutdownTimeout, run: func(ctx context.Context) error {
			return bounded(ctx, func() error { globalOTELShutdown(); return nil })
		}}})
	}
	// The embedded collector receives the exporters' final flush, so it stops
	// only after OpenTelemetry has.
	if globalCollectorShutdown != nil {
		phases = append(phases, []shutdownStep{{name: "embedded collector", budget: telemetryShutdownTimeout, run: func(ctx context.Context) error {
			return bounded(ctx, globalCollectorShutdown)
		}}})
	}
	return phases
}
