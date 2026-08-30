package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := authMiddleware(ok)

	// Reset to the "no token pushed yet" state, then restore after.
	agentToken.Store(nil)
	t.Cleanup(func() { agentToken.Store(nil) })

	call := func(path, authHeader string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// /health is always exempt, even before any token is set.
	if got := call("/health", ""); got != http.StatusOK {
		t.Fatalf("/health before token = %d, want 200", got)
	}

	// Default-deny: no token pushed yet -> sensitive endpoints 401.
	if got := call("/toolbox/exec", ""); got != http.StatusUnauthorized {
		t.Fatalf("/toolbox/exec with no token set = %d, want 401", got)
	}
	if got := call("/toolbox/exec", "Bearer anything"); got != http.StatusUnauthorized {
		t.Fatalf("/toolbox/exec with token unset but header present = %d, want 401", got)
	}

	// Exact-match invariant: the ONLY exempt path is the literal "/health".
	// Locks the security guarantee against a future refactor to a prefix/subtree
	// match (which would reopen the bypass). Every normalization trick and every
	// non-exact variant must require auth (401 here, since no token is set).
	for _, p := range []string{
		"/health/../toolbox/exec",
		"/toolbox/exec/../../health",
		"//health",
		"/HEALTH",
		"/health/",
		"/health/foo",
		"/healthx",
	} {
		if got := call(p, ""); got != http.StatusUnauthorized {
			t.Fatalf("exempt-path invariant broken: %q = %d, want 401 (only literal /health may be exempt)", p, got)
		}
	}
	// The literal /health is exempt even with no token.
	if got := call("/health", ""); got != http.StatusOK {
		t.Fatalf("literal /health = %d, want 200", got)
	}

	// Token pushed.
	setAgentToken("s3cr3t-token-abc123")

	// /health stays exempt.
	if got := call("/health", ""); got != http.StatusOK {
		t.Fatalf("/health after token = %d, want 200", got)
	}
	// Correct token passes.
	if got := call("/toolbox/exec", "Bearer s3cr3t-token-abc123"); got != http.StatusOK {
		t.Fatalf("/toolbox/exec with correct token = %d, want 200", got)
	}
	// Wrong token, missing header, empty bearer, and no-Bearer-prefix all 401.
	for _, h := range []string{"Bearer wrong", "", "Bearer ", "s3cr3t-token-abc123"} {
		if got := call("/toolbox/exec", h); got != http.StatusUnauthorized {
			t.Fatalf("/toolbox/exec with header %q = %d, want 401", h, got)
		}
	}

	// Empty-string token must never authorize (guard against a failed push
	// leaving "" and a caller sending "").
	setAgentToken("")
	if got := call("/toolbox/exec", "Bearer "); got != http.StatusUnauthorized {
		t.Fatalf("/toolbox/exec with empty token both sides = %d, want 401", got)
	}
}
