package telemetry

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// DefaultHeartbeatInterval keeps one span per minute flowing into
// otel_traces even when the gateway serves no traffic. Pipeline-freshness
// alerts (e.g. telemetry.stale, threshold 900s) then detect real ingestion
// breakage — exporter down, collector wedged, ClickHouse rejecting writes —
// instead of firing on an idle environment.
const DefaultHeartbeatInterval = time.Minute

// heartbeatSpanName is the span name emitted by the heartbeat. Dashboards
// and staleness rules can filter on it to separate liveness traffic from
// customer traffic.
const heartbeatSpanName = "telemetry.heartbeat"

// StartHeartbeat emits a telemetry.heartbeat span every interval until the
// returned stop func is called. It only runs when the tracer provider is a
// real sdktrace.TracerProvider — with tracing disabled (noop provider) there
// is nothing to keep alive and this returns immediately without spawning a
// goroutine.
//
// The first beat fires synchronously so a freshly booted pipeline proves
// itself within seconds rather than after one full interval.
func StartHeartbeat(t *Telemetry, interval time.Duration) (stop func()) {
	if t == nil || t.TracerProvider == nil || interval <= 0 {
		return func() {}
	}
	tp, ok := t.TracerProvider.(*sdktrace.TracerProvider)
	if !ok {
		// Noop or foreign provider: tracing is disabled, nothing to do.
		return func() {}
	}

	tracer := tp.Tracer("github.com/everstacklabs/everstack/internal/telemetry")
	started := time.Now()

	emitHeartbeat(tracer, started, interval)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				emitHeartbeat(tracer, started, interval)
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		wg.Wait()
	}
}

// emitHeartbeat records and immediately ends one heartbeat span. The batch
// span processor flushes it asynchronously; a short timeout bounds the work
// in case context decoration ever grows expensive.
func emitHeartbeat(tracer trace.Tracer, started time.Time, interval time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, span := tracer.Start(ctx, heartbeatSpanName,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.Bool("telemetry.heartbeat", true),
			attribute.Int64("telemetry.heartbeat.interval_ms", interval.Milliseconds()),
			attribute.Float64("process.uptime_seconds", time.Since(started).Seconds()),
		),
	)
	span.End()
}
