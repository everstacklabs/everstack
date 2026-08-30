package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/edition"
)

// The tests below pin the D6/D9 state machine from
// docs/design/editions-and-billing.md: unlicensed and expired states never
// wall requests; only an explicitly disabled license blocks writes.

func newStateTestEnforcer() *LicenseEnforcer {
	// A non-nil cqrs.System is required or WithLicenseEnforcement becomes a
	// pass-through and the state machine is never exercised.
	return NewLicenseEnforcer(&cqrs.System{})
}

// serve runs one request through the enforcement middleware and reports the
// response code, whether next ran, and the license status header.
func serve(t *testing.T, l *LicenseEnforcer, method string) (code int, nextRan bool, status string) {
	t.Helper()
	var ran bool
	h := l.WithLicenseEnforcement(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/v1/agents", nil)
	h.ServeHTTP(rec, req)
	return rec.Code, ran, rec.Header().Get("X-Everstack-License-Status")
}

func TestUnlicensedIsTerminalNonBlocking(t *testing.T) {
	l := newStateTestEnforcer()
	// No cached state, no trial manager, no verifier: a fresh unlicensed CE
	// instance. Reads AND writes must pass (D9: unlicensed is CE, not a wall).
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		code, ran, status := serve(t, l, method)
		if !ran || code != http.StatusOK {
			t.Fatalf("%s: unlicensed request blocked (code=%d ran=%v); D9 violated", method, code, ran)
		}
		if status != "unlicensed" {
			t.Fatalf("%s: expected X-Everstack-License-Status=unlicensed, got %q", method, status)
		}
	}
}

func TestDisabledLicenseBlocksWritesAllowsReads(t *testing.T) {
	l := newStateTestEnforcer()
	l.SetCachedState(&LicenseState{Active: false, Status: "disabled", Tier: "pro"})

	code, ran, status := serve(t, l, http.MethodGet)
	if !ran || code != http.StatusOK || status != "disabled" {
		t.Fatalf("disabled license must allow reads (code=%d ran=%v status=%q)", code, ran, status)
	}

	code, ran, _ = serve(t, l, http.MethodPost)
	if ran || code == http.StatusOK {
		t.Fatalf("disabled license must block writes (code=%d ran=%v)", code, ran)
	}
}

func TestExpiredLicenseInGracePasses(t *testing.T) {
	l := newStateTestEnforcer()
	expired := time.Now().UTC().Add(-24 * time.Hour) // 1 day into the 14-day grace
	l.SetCachedState(&LicenseState{Active: true, Status: "active", Tier: "pro", IsPaid: true, ExpiresAt: &expired})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		code, ran, status := serve(t, l, method)
		if !ran || code != http.StatusOK {
			t.Fatalf("%s: in-grace request blocked (code=%d ran=%v); D6 violated", method, code, ran)
		}
		if status != "grace" {
			t.Fatalf("%s: expected status header grace, got %q", method, status)
		}
	}
}

func TestExpiredLicenseBeyondGracePassesAsDegraded(t *testing.T) {
	l := newStateTestEnforcer()
	expired := time.Now().UTC().Add(-15 * 24 * time.Hour) // past the 14-day grace
	l.SetCachedState(&LicenseState{Active: true, Status: "active", Tier: "pro", IsPaid: true, ExpiresAt: &expired})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		code, ran, status := serve(t, l, method)
		if !ran || code != http.StatusOK {
			t.Fatalf("%s: degraded request blocked (code=%d ran=%v); reads/CE-writes must pass", method, code, ran)
		}
		if status != "degraded-ce" {
			t.Fatalf("%s: expected status header degraded-ce, got %q", method, status)
		}
	}
}

func TestSuspendedLicensePasses(t *testing.T) {
	l := newStateTestEnforcer()
	l.SetCachedState(&LicenseState{Active: true, Status: "suspended", Tier: "pro"})

	code, ran, status := serve(t, l, http.MethodPost)
	if !ran || code != http.StatusOK || status != "suspended" {
		t.Fatalf("suspended license must pass admin traffic (code=%d ran=%v status=%q)", code, ran, status)
	}
}

func TestInGracePeriodHelper(t *testing.T) {
	l := newStateTestEnforcer()
	if l.InGracePeriod() {
		t.Fatal("no state must not report grace")
	}
	in := time.Now().UTC().Add(-24 * time.Hour)
	l.SetCachedState(&LicenseState{Active: true, Status: "active", ExpiresAt: &in})
	if !l.InGracePeriod() {
		t.Fatal("expired 1d ago must be in grace")
	}
	out := time.Now().UTC().Add(-15 * 24 * time.Hour)
	l.SetCachedState(&LicenseState{Active: true, Status: "active", ExpiresAt: &out})
	if l.InGracePeriod() {
		t.Fatal("expired 15d ago must be past grace")
	}
}

func TestSetEnabledCannotDisableInShippedBuilds(t *testing.T) {
	if edition.IsDev() {
		t.Skip("dev builds keep the config switch")
	}
	l := newStateTestEnforcer()
	if !l.IsEnabled() {
		t.Fatal("enforcer must start enabled")
	}
	l.SetEnabled(false) // D10: config cannot switch enforcement off in ce/ee
	if !l.IsEnabled() {
		t.Fatal("SetEnabled(false) must be ignored outside dev builds (D10)")
	}
	l.SetEnabled(true)
	if !l.IsEnabled() {
		t.Fatal("SetEnabled(true) must remain allowed")
	}
}
