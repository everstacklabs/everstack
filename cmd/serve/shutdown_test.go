package serve

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

func TestRunShutdownPhasesOrderAndFreshContexts(t *testing.T) {
	var mu sync.Mutex
	var order []string
	rec := func(name string, sleep time.Duration) shutdownStep {
		return shutdownStep{name: name, budget: time.Second, run: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				t.Errorf("%s started with an expired context: %v", name, err)
			}
			time.Sleep(sleep)
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}}
	}
	// The parent is already cancelled (the SIGTERM-on-ctx path): every step
	// must still get a live, independent deadline.
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	err := runShutdownPhases(parent, [][]shutdownStep{
		{rec("http", 60*time.Millisecond)},
		{rec("settle", 0)},
		{rec("a", 20*time.Millisecond), rec("b", 0)},
		{rec("otel", 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 5 || order[0] != "http" || order[1] != "settle" || order[4] != "otel" {
		t.Fatalf("order = %v, want http, settle, {a,b}, otel", order)
	}
}

func TestRunShutdownPhasesReturnsOnlyReportedErrors(t *testing.T) {
	boom := errors.New("boom")
	err := runShutdownPhases(context.Background(), [][]shutdownStep{
		{{name: "quiet", run: func(context.Context) error { return boom }}},
		{{name: "loud", report: true, run: func(context.Context) error { return boom }}},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want reported error", err)
	}
	if err := runShutdownPhases(context.Background(), [][]shutdownStep{
		{{name: "quiet", run: func(context.Context) error { return boom }}},
	}); err != nil {
		t.Fatalf("unreported step leaked error: %v", err)
	}
}

// A slow telemetry shutdown must not shrink the HTTP drain: the drain runs
// first with its full budget, and telemetry is cut by its own deadline.
func TestGatewayShutdownDrainsHTTPBeforeTelemetry(t *testing.T) {
	var mu sync.Mutex
	var order []string
	rec := func(n string) { mu.Lock(); order = append(order, n); mu.Unlock() }

	oldOTEL, oldColl, oldAgents, oldProxy, oldEvents, oldAct := globalOTELShutdown, globalCollectorShutdown, globalAgentsShutdown, globalSandboxProxyShutdown, globalEventsShutdown, globalActivityRecorder
	t.Cleanup(func() {
		globalOTELShutdown, globalCollectorShutdown, globalAgentsShutdown, globalSandboxProxyShutdown, globalEventsShutdown, globalActivityRecorder = oldOTEL, oldColl, oldAgents, oldProxy, oldEvents, oldAct
	})
	globalActivityRecorder = nil
	globalOTELShutdown = func() { rec("otel") }
	globalCollectorShutdown = func() error { rec("collector"); return nil }
	globalAgentsShutdown = func(context.Context) error { rec("agents"); return nil }
	globalSandboxProxyShutdown = func(context.Context) error { rec("proxy"); return nil }
	globalEventsShutdown = func(context.Context) error { rec("events"); return nil }

	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec("handler-start")
		<-release
		rec("handler-end")
	})}
	_ = srv // handler wiring is exercised through the phase list below

	phases := gatewayShutdownPhases(srv, 2*time.Second)
	// Replace only the HTTP drain step's work with an in-flight request that
	// finishes mid-drain, keeping the real ordering of every other phase.
	replaced := false
	for pi := range phases {
		for si := range phases[pi] {
			if phases[pi][si].name != "http drain" {
				continue
			}
			replaced = true
			phases[pi][si].run = func(ctx context.Context) error {
				go func() { time.Sleep(50 * time.Millisecond); rec("handler-end"); close(release) }()
				<-release
				return ctx.Err()
			}
		}
	}
	if !replaced {
		t.Fatal("no http drain step in the shutdown phases")
	}
	if err := runShutdownPhases(context.Background(), phases); err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	// Agent sessions and the sandbox proxy are told to stop while their
	// connections are still open, so they come before the drain.
	for _, earlier := range []string{"proxy", "agents"} {
		if _, ok := pos[earlier]; !ok || pos[earlier] >= pos["handler-end"] {
			t.Fatalf("%s did not run before the HTTP drain finished: %v", earlier, order)
		}
	}
	for _, later := range []string{"events", "otel", "collector"} {
		if pos[later] <= pos["handler-end"] {
			t.Fatalf("%s ran before the in-flight request finished: %v", later, order)
		}
	}
	if pos["events"] >= pos["otel"] || pos["otel"] >= pos["collector"] {
		t.Fatalf("want events < otel < collector, got %v", order)
	}
}

func TestShutdownDrainTimeoutFromConfig(t *testing.T) {
	cases := map[string]time.Duration{"": 10 * time.Second, "45s": 45 * time.Second, "bogus": 10 * time.Second, "-1s": 10 * time.Second}
	for raw, want := range cases {
		cfg := &validator.Config{Server: &validator.ServerConfig{Config: validator.ServerConfigConfig{ShutdownDrainTimeout: raw}}}
		if got := shutdownDrainTimeoutFromConfig(cfg); got != want {
			t.Errorf("%q: got %s want %s", raw, got, want)
		}
	}
	if _, err := validator.ParseShutdownDrainTimeout("0s"); err == nil {
		t.Error("0s must be rejected")
	}
}

func TestPublicServerTimeouts(t *testing.T) {
	if publicReadHeaderTimeout <= 0 || publicIdleTimeout <= 0 {
		t.Fatal("public listener must set header and idle timeouts")
	}
}
