package authratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The limiter is the only thing standing between the instance login endpoint
// and unbounded password guessing, so this checks it actually refuses rather
// than merely being mounted.
func TestWrapRejectsOnceTheBurstIsSpent(t *testing.T) {
	rl := NewAuthRateLimiter(60, 3)
	defer rl.Stop()

	var served int
	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		w.WriteHeader(http.StatusOK)
	}))

	call := func() int {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "198.51.100.10:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := range 3 {
		if code := call(); code != http.StatusOK {
			t.Fatalf("call %d within burst returned %d, want 200", i+1, code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "198.51.100.10:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("call past the burst returned %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 429 should tell the caller when to come back")
	}
	if served != 3 {
		t.Errorf("handler ran %d times, want 3: the rejected call must not reach it", served)
	}
}

// Buckets are per caller, so one abuser must not spend anybody else's budget.
func TestWrapKeepsCallersInSeparateBuckets(t *testing.T) {
	rl := NewAuthRateLimiter(60, 1)
	defer rl.Stop()

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func(addr string) int {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := call("198.51.100.10:1111"); code != http.StatusOK {
		t.Fatalf("first caller got %d, want 200", code)
	}
	if code := call("198.51.100.10:1111"); code != http.StatusTooManyRequests {
		t.Fatalf("first caller past burst got %d, want 429", code)
	}
	if code := call("198.51.100.11:2222"); code != http.StatusOK {
		t.Errorf("second caller got %d, want 200: it has its own budget", code)
	}
}
