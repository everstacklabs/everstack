// Package metricsserver serves the process's Prometheus default registry on a
// dedicated listener.
//
// Every billing loss counter (internal/billingmeter, internal/providers/billing,
// the services billing debit loop) is registered on the default registry via
// promauto, but neither the gateway nor the services bundle exposed it: the
// API mux on :8089 answers /metrics with the SPA. A separate listener keeps the
// metrics off the public API port and its ingress, so the only way to reach
// them is the in-cluster Service the ServiceMonitor scrapes.
package metricsserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// EnvAddr names the listen address, for example ":9464". Unset or empty keeps
// the listener off, so self-hosted installs open no new port by default.
const EnvAddr = "EVS_METRICS_ADDR"

// AddrFromEnv returns the configured listen address, or "" when disabled.
func AddrFromEnv() string {
	return strings.TrimSpace(os.Getenv(EnvAddr))
}

// Handler serves /metrics from the default registry and 404s everything else.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

// Serve serves Handler on ln until ctx ends. It returns nil on a clean
// shutdown.
func Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// StartFromEnv starts the listener in the background when EnvAddr is set. A
// bind failure is logged, never fatal: losing metrics must not take the
// process down with it.
func StartFromEnv(ctx context.Context, component string) {
	startFromEnv(ctx, component, net.Listen)
}

// started records the one listener this process serves. The services binary
// runs both its own entry point and the gateway's startAPI, and each calls
// StartFromEnv; both would serve the same default registry, so the first call
// binds and later calls reuse it instead of failing on the taken port.
var started struct {
	sync.Mutex
	addr      string
	component string
}

// startFromEnv reports whether the process serves /metrics after the call.
func startFromEnv(ctx context.Context, component string, listen func(network, address string) (net.Listener, error)) bool {
	addr := AddrFromEnv()
	if addr == "" {
		logger.WithFields("component", component).Debug("metrics: listener disabled (" + EnvAddr + " unset)")
		return false
	}
	started.Lock()
	defer started.Unlock()
	if started.addr != "" {
		logger.WithFields("component", component, "addr", started.addr, "started_by", started.component).
			Debug("metrics: process already serves /metrics; reusing it")
		return true
	}
	ln, err := listen("tcp", addr)
	if err != nil {
		logger.WithFields("component", component, "addr", addr, "error", err.Error()).
			Error("metrics: listener failed to bind; Prometheus metrics are unavailable")
		return false
	}
	started.addr, started.component = ln.Addr().String(), component
	logger.WithFields("component", component, "addr", started.addr).Info("metrics: serving /metrics")
	go func() {
		if err := Serve(ctx, ln); err != nil {
			logger.WithFields("component", component, "error", err.Error()).Error("metrics: listener stopped")
		}
	}()
	return true
}
