package httpclient

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaxConnsPerHostOverride(t *testing.T) {
	cases := []struct {
		env  string
		want int
	}{
		{"", defaultMaxConnsPerHost},
		{"8192", 8192},
		{" 64 ", 64},
		{"0", 0}, // explicit: no limit
		{"-1", defaultMaxConnsPerHost},
		{"lots", defaultMaxConnsPerHost},
	}
	for _, tc := range cases {
		t.Setenv(maxConnsPerHostEnv, tc.env)
		if got := maxConnsPerHost(); got != tc.want {
			t.Errorf("%s=%q: maxConnsPerHost() = %d, want %d", maxConnsPerHostEnv, tc.env, got, tc.want)
		}
	}
}

// Every connection that may be open must also be allowed to sit idle,
// otherwise steady traffic above the idle limit redials on every request.
func TestTransportKeepsAsManyIdleConnsAsItMayOpen(t *testing.T) {
	for _, env := range []string{"", "300", "0"} {
		t.Setenv(maxConnsPerHostEnv, env)
		tr := newTransport()
		if tr.MaxConnsPerHost != 0 && tr.MaxIdleConnsPerHost < tr.MaxConnsPerHost {
			t.Errorf("%q: idle per host %d < max per host %d", env, tr.MaxIdleConnsPerHost, tr.MaxConnsPerHost)
		}
		if tr.MaxIdleConnsPerHost <= 0 {
			t.Errorf("%q: idle per host is %d; Go would fall back to 2", env, tr.MaxIdleConnsPerHost)
		}
		if tr.MaxIdleConns < tr.MaxIdleConnsPerHost {
			t.Errorf("%q: total idle %d < idle per host %d", env, tr.MaxIdleConns, tr.MaxIdleConnsPerHost)
		}
	}
}

// The per-host limit is, for an HTTP/1.1 upstream, the number of requests in
// flight: the rest wait inside the transport. This is why the default has to
// sit above the concurrency the gateway is expected to carry.
func TestMaxConnsPerHostBoundsInFlightHTTP1Requests(t *testing.T) {
	const limit, offered = 4, 12
	t.Setenv(maxConnsPerHostEnv, "4")

	var inFlight, peak atomic.Int32
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	client := &http.Client{Transport: newTransport()}
	var wg sync.WaitGroup
	for i := 0; i < offered; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(upstream.URL)
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			_ = resp.Body.Close()
		}()
	}

	// Wait until the upstream is holding as many requests as it will get.
	deadline := time.Now().Add(5 * time.Second)
	for inFlight.Load() < limit && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Give any request beyond the limit time to arrive if it were going to.
	time.Sleep(100 * time.Millisecond)
	if got := inFlight.Load(); got != limit {
		t.Fatalf("upstream is holding %d requests, want exactly the limit of %d (offered %d)", got, limit, offered)
	}

	close(release)
	wg.Wait()
	if got := peak.Load(); got != limit {
		t.Fatalf("peak concurrent upstream requests = %d, want %d", got, limit)
	}
}

func TestUnaryTransportHasNoResponseHeaderTimeout(t *testing.T) {
	if tr := Transport(); tr.ResponseHeaderTimeout == 0 {
		t.Fatalf("streaming transport lost its response header timeout")
	}
	unary, ok := Unary().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unary transport is %T, want *http.Transport", Unary().Transport)
	}
	if unary.ResponseHeaderTimeout != 0 {
		t.Fatalf("unary transport has a response header timeout of %s; it would cap generation time", unary.ResponseHeaderTimeout)
	}
}
