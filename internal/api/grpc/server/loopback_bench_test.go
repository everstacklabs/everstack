package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// BenchmarkLoopbackCallBesideParkedStreams measures one in-process call while
// other calls on the same connection are open and idle, which is the gateway's
// steady state: at 1000 requests per second against a provider that answers in
// 0.9 s, about 900 chat calls are parked at any moment.
//
// "default" is the HTTP/2 library's own write scheduler, kept here only as the
// reference the loopback's scheduler is compared with.
func BenchmarkLoopbackCallBesideParkedStreams(b *testing.B) {
	schedulers := []struct {
		name string
		h2   func() *http2.Server
	}{
		{"default", func() *http2.Server { return &http2.Server{MaxConcurrentStreams: loopbackMaxStreams} }},
		{"loopback", newLoopbackHTTP2Server},
	}
	for _, parked := range []int{0, 1000} {
		for _, sched := range schedulers {
			b.Run(fmt.Sprintf("parked=%d/scheduler=%s", parked, sched.name), func(b *testing.B) {
				grpcSrv := grpc.NewServer()
				healthpb.RegisterHealthServer(grpcSrv, health.NewServer())
				loopback := NewLoopback()
				stop := loopback.serve(http.HandlerFunc(grpcSrv.ServeHTTP), sched.h2())
				conn, err := grpc.NewClient(loopback.Target("localhost:8089"), loopback.DialOptions()...)
				if err != nil {
					b.Fatalf("new client: %v", err)
				}
				defer func() {
					_ = conn.Close()
					stop()
					grpcSrv.Stop()
				}()
				client := healthpb.NewHealthClient(conn)

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				// Watch stays open after its first message, so each call
				// occupies a stream for the rest of the benchmark.
				for i := 0; i < parked; i++ {
					stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
					if err != nil {
						b.Fatalf("park stream %d: %v", i, err)
					}
					if _, err := stream.Recv(); err != nil {
						b.Fatalf("park stream %d: %v", i, err)
					}
				}

				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
						b.Fatalf("check: %v", err)
					}
				}
			})
		}
	}
}
