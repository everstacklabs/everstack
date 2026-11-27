package middleware

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/cqrs"
	licpkg "github.com/everstacklabs/everstack/internal/license"
	"github.com/everstacklabs/everstack/internal/query"
	license "github.com/everstacklabs/everstack/internal/query/handlers/license"
)

// credentialLookups stands in for the instance credentials query and counts
// how often it is asked. It finds nothing: the instance has no license.
type credentialLookups struct {
	calls atomic.Int64
}

func (c *credentialLookups) QueryType() string { return license.GetInstanceCredentialsType }
func (c *credentialLookups) Handle(context.Context, query.Query) (interface{}, error) {
	c.calls.Add(1)
	return nil, nil
}

// unlicensedEnforcerWithVerifier is an instance that could hold a license (it
// has a pinned vendor key, as release builds do) but has none stored.
func unlicensedEnforcerWithVerifier(t *testing.T) (*LicenseEnforcer, *credentialLookups) {
	t.Helper()
	lookups := &credentialLookups{}
	bus := query.NewQueryBus()
	bus.RegisterHandler(lookups)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := licpkg.NewVerifier(base64.StdEncoding.EncodeToString(pub))
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	l := NewLicenseEnforcer(&cqrs.System{QueryBus: bus})
	l.verifier = verifier
	return l, lookups
}

// An unlicensed instance stays unlicensed, so a database read per request to
// look for a license is a permanent tax on every request it serves.
func TestUnlicensedInstanceDoesNotQueryTheDatabasePerRequest(t *testing.T) {
	l, lookups := unlicensedEnforcerWithVerifier(t)

	for i := 0; i < 200; i++ {
		if code, ran, _ := serve(t, l, http.MethodPost); !ran || code != http.StatusOK {
			t.Fatalf("request %d: code %d, next ran %v; an unlicensed instance must still serve", i, code, ran)
		}
	}
	if got := lookups.calls.Load(); got != 1 {
		t.Fatalf("200 requests looked for a stored license %d times, want 1", got)
	}
}

// The safety net still works: once the interval has passed, the next request
// looks again, so a license written without a refresh is picked up.
func TestUnlicensedInstanceLooksAgainAfterTheInterval(t *testing.T) {
	l, lookups := unlicensedEnforcerWithVerifier(t)

	serve(t, l, http.MethodPost)
	if got := lookups.calls.Load(); got != 1 {
		t.Fatalf("first request made %d lookups, want 1", got)
	}

	l.lastStoredLicenseCheck.Store(time.Now().Add(-2 * storedLicenseCheckInterval).UnixNano())
	serve(t, l, http.MethodPost)
	serve(t, l, http.MethodPost)
	if got := lookups.calls.Load(); got != 2 {
		t.Fatalf("lookups after the interval = %d, want 2", got)
	}
}

// The interval is measured on the wall clock, which can be set back (an NTP
// correction, a VM resume). A check claimed "in the future" must not pause
// the next one until the clock catches up.
func TestStoredLicenseCheckSurvivesClockSetBack(t *testing.T) {
	l := &LicenseEnforcer{}
	now := time.Now()

	if !l.storedLicenseCheckDue(now) {
		t.Fatal("the first check is always due")
	}
	if l.storedLicenseCheckDue(now.Add(time.Second)) {
		t.Fatal("a second check inside the interval must wait")
	}
	// The clock steps back an hour.
	if !l.storedLicenseCheckDue(now.Add(-time.Hour)) {
		t.Fatal("after the clock is set back the check must be due, not paused for an hour")
	}
	if l.storedLicenseCheckDue(now.Add(-time.Hour + time.Second)) {
		t.Fatal("the interval applies again from the new clock reading")
	}
}
