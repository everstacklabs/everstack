package metricsserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var testCounter = promauto.NewCounter(prometheus.CounterOpts{
	Name: "metricsserver_test_probe_total",
	Help: "Probe counter registered on the default registry by the test.",
})

// The listener serves the process-wide default registry, which is where every
// billing counter is registered through promauto, and nothing else: the
// metrics port must never become a second way into the API.
func TestServeExposesDefaultRegistryOnly(t *testing.T) {
	testCounter.Add(3)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	body := get(t, base+"/metrics", http.StatusOK)
	if !strings.Contains(body, "metricsserver_test_probe_total 3") {
		t.Fatalf("default registry not exposed; body did not contain the probe counter")
	}
	get(t, base+"/", http.StatusNotFound)
	get(t, base+"/v1/models", http.StatusNotFound)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after shutdown, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop after context cancel")
	}
}

func TestAddrFromEnvIsOptIn(t *testing.T) {
	t.Setenv(EnvAddr, "")
	if got := AddrFromEnv(); got != "" {
		t.Fatalf("AddrFromEnv() = %q with the variable empty, want disabled", got)
	}
	t.Setenv(EnvAddr, " :9464 ")
	if got := AddrFromEnv(); got != ":9464" {
		t.Fatalf("AddrFromEnv() = %q, want :9464", got)
	}
}

func get(t *testing.T, url string, want int) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("GET %s = %d, want %d", url, resp.StatusCode, want)
	}
	return string(b)
}

// The services binary also runs the gateway's startAPI, so both call
// StartFromEnv in one process. Prod 2026-10-06: the second call tried to bind
// the same port and logged "listener failed to bind" at ERROR on every boot.
// Both serve the same default registry, so one listener per process is the
// whole job: later calls must not bind again.
func TestStartFromEnvBindsOncePerProcess(t *testing.T) {
	resetStartedForTest(t)
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := free.Addr().String()
	free.Close()
	t.Setenv(EnvAddr, addr)

	var binds int
	listen := func(network, address string) (net.Listener, error) {
		binds++
		return net.Listen(network, address)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !startFromEnv(ctx, "services", listen) {
		t.Fatal("first start did not serve")
	}
	if !startFromEnv(ctx, "gateway", listen) {
		t.Fatal("second start reported metrics unavailable, but the process already serves them")
	}
	if binds != 1 {
		t.Fatalf("bound %d listeners, want exactly one per process", binds)
	}
	get(t, "http://"+addr+"/metrics", http.StatusOK)
}

func resetStartedForTest(t *testing.T) {
	t.Helper()
	started.Lock()
	started.addr, started.component = "", ""
	started.Unlock()
	t.Cleanup(func() {
		started.Lock()
		started.addr, started.component = "", ""
		started.Unlock()
	})
}
