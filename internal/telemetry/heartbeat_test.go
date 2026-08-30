package telemetry

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestStartHeartbeatEmitsSpans(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(t.Context()) }()

	spans := make(chan string, 8)
	tp.RegisterSpanProcessor(simpleProcessorFunc(func(s sdktrace.ReadOnlySpan) {
		spans <- s.Name()
	}))

	tel := &Telemetry{TracerProvider: tp}
	stop := StartHeartbeat(tel, 20*time.Millisecond)
	defer stop()

	select {
	case name := <-spans:
		if name != heartbeatSpanName {
			t.Fatalf("first span = %q, want %q", name, heartbeatSpanName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not emit its first span")
	}

	select {
	case <-spans: // second tick proves the loop keeps running
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat stopped emitting after the first span")
	}
}

func TestStartHeartbeatNoopWhenTracingDisabled(t *testing.T) {
	tel := &Telemetry{TracerProvider: trace.NewNoopTracerProvider()}

	stop := StartHeartbeat(tel, 0)
	if stop == nil {
		t.Fatal("StartHeartbeat() stop = nil, want non-nil no-op func")
	}
	stop()
}

func TestStartHeartbeatNoopWithoutTracerProvider(t *testing.T) {
	stop := StartHeartbeat(&Telemetry{}, time.Minute)
	stop()
	stop = StartHeartbeat(nil, time.Minute)
	stop()
}

// simpleProcessorFunc adapts an OnEnd callback into a SpanProcessor.
type simpleProcessorFunc func(sdktrace.ReadOnlySpan)

func (f simpleProcessorFunc) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (f simpleProcessorFunc) OnEnd(s sdktrace.ReadOnlySpan)                   { f(s) }
func (simpleProcessorFunc) Shutdown(context.Context) error                    { return nil }
func (simpleProcessorFunc) ForceFlush(context.Context) error                  { return nil }
