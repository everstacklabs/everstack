package main

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"sync/atomic"
)

// agentToken holds the per-VM bearer token the host injects over vsock
// (MethodSetAgentToken) shortly after boot. It is nil until the host pushes it.
//
// SECURITY INVARIANT — the token is PER-VM and DISTINCT. It authenticates the
// host to THIS guest only. A guest process can read its own token, but that is
// harmless precisely because no two VMs share one: a peer guest never learns
// this VM's token. NEVER consolidate to a shared/global token — that would let
// any guest which learns the shared secret authenticate to every peer, exactly
// recreating the unauthenticated cross-tenant RCE this defends against.
//
// Note this is defense in depth beneath the host FORWARD drop of guest->guest
// traffic (internal/sandbox/firecracker/network.go). The token rides plaintext
// over the point-to-point TAP, so it defends against a FORWARD-rule regression
// (a peer can connect but has no token) but NOT a sniffing-capable attacker on
// a shared L2 / multi-host topology — that requires TLS/mTLS on :8080 (v2).
var agentToken atomic.Pointer[string]

// setAgentToken records the per-VM token pushed by the host over vsock.
func setAgentToken(tok string) {
	t := tok
	agentToken.Store(&t)
}

// authMiddleware enforces the per-VM bearer token on every :8080 request except
// /health (liveness only, returns 204, carries no sensitive data — the host's
// readiness/health probes are unauthenticated by design).
//
// Default-deny: if the host has not pushed a token yet (nil) or it is empty,
// reject every non-/health request. This closes the boot window where the HTTP
// listener is bound (main.go binds it first, before the vsock token push lands)
// but no token is set — during that gap a co-resident guest must NOT be able to
// call /toolbox/exec.
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		want := agentToken.Load()
		if want == nil || *want == "" {
			http.Error(w, "agent token not initialized", http.StatusUnauthorized)
			return
		}
		const prefix = "Bearer "
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(authz, prefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		got := authz[len(prefix):]
		// subtle.ConstantTimeCompare returns 0 on a length mismatch (leaking
		// only length, never content) and is constant-time for equal lengths.
		// Both sides are fixed-length 64-char hex tokens.
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(*want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
