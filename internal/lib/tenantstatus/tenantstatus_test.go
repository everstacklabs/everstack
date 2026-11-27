package tenantstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
)

const (
	pausedID = "11111111-2222-3333-4444-555555a1b2c3" // host dev-a1b2c3
	activeID = "11111111-2222-3333-4444-666666d4e5f6" // host prod-d4e5f6
	loadSQL  = `FROM everstack\.tenant_config`
)

func newChecker(t *testing.T) (*Checker, sqlmock.Sqlmock, *time.Time) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	c := New(sqlx.NewDb(raw, "sqlmock"), 30*time.Second)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	c.SetClock(func() time.Time { return now })
	return c, mock, &now
}

func table(pausedStatus, pausedReason string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"instance_id", "slug", "org_slug", "status", "paused_reason", "custom_domain", "custom_domain_status"}).
		AddRow(pausedID, "dev", "acme", pausedStatus, pausedReason, "api.acme.com", "active").
		AddRow(activeID, "prod", "acme", "active", "", "", "")
}

func TestActive_OneSnapshotPerTTL(t *testing.T) {
	c, mock, now := newChecker(t)
	ctx := context.Background()
	mock.ExpectQuery(loadSQL).WillReturnRows(table("paused", ReasonOverLimitAuto))
	for i := 0; i < 5; i++ {
		if c.Active(ctx, pausedID) {
			t.Fatal("a paused tenant must not be active")
		}
		if !c.Active(ctx, activeID) || !c.Active(ctx, "org-alias-or-self-hosted") {
			t.Fatal("active and unknown tenants are active")
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err) // one load for all of it
	}
	*now = now.Add(31 * time.Second)
	mock.ExpectQuery(loadSQL).WillReturnRows(table("active", ""))
	if !c.Active(ctx, pausedID) {
		t.Fatal("a resumed tenant must be active after the TTL")
	}
}

func TestReloadErrorKeepsLastKnownState(t *testing.T) {
	c, mock, now := newChecker(t)
	ctx := context.Background()
	mock.ExpectQuery(loadSQL).WillReturnError(errors.New("db down"))
	if !c.Active(ctx, pausedID) {
		t.Fatal("never loaded: fail open")
	}
	*now = now.Add(31 * time.Second)
	mock.ExpectQuery(loadSQL).WillReturnRows(table("paused", ""))
	if c.Active(ctx, pausedID) {
		t.Fatal("loaded paused")
	}
	*now = now.Add(31 * time.Second)
	mock.ExpectQuery(loadSQL).WillReturnError(errors.New("db down"))
	if c.Active(ctx, pausedID) {
		t.Fatal("a reload error must keep the last known state")
	}
}

func TestRefreshAndListPaused(t *testing.T) {
	c, mock, _ := newChecker(t)
	ctx := context.Background()
	mock.ExpectQuery(loadSQL).WillReturnRows(table("paused", ""))
	ids, err := c.ListPaused(ctx)
	if err != nil || len(ids) != 1 || ids[0] != pausedID {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	// Refresh reloads even inside the TTL.
	mock.ExpectQuery(loadSQL).WillReturnRows(table("active", ""))
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if ids, _ := c.ListPaused(ctx); len(ids) != 0 {
		t.Fatalf("after refresh nothing is paused: %v", ids)
	}
}

func TestNilCheckerIsAlwaysActive(t *testing.T) {
	var c *Checker
	if !c.Active(context.Background(), "x") {
		t.Fatal("nil checker must report active")
	}
	h := HostGuard(nil, "", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != 204 {
		t.Fatalf("nil guard must pass through, got %d", rec.Code)
	}
}

func serve(h http.Handler, method, path, host string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	h.ServeHTTP(rec, req)
	return rec
}

func TestHostGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("over-limit pause gets 403 JSON with the billing link for a member, also on the custom domain", func(t *testing.T) {
		c, mock, _ := newChecker(t)
		mock.ExpectQuery(loadSQL).WillReturnRows(table("paused", ReasonOverLimitChoice))
		c.SetViewerCheck(func(*http.Request, Tenant) bool { return true })
		h := HostGuard(c, "https://app.everstack.ai", ok)
		for _, host := range []string{"Dev-A1B2C3.eu.everstack.ai:443", "api.acme.com"} {
			rec := serve(h, http.MethodPost, "/v1/chat/completions", host)
			if rec.Code != http.StatusForbidden || rec.Header().Get("X-EVS-Instance-Paused") != "over_plan_instance_limit" {
				t.Fatalf("%s: code=%d header=%q", host, rec.Code, rec.Header().Get("X-EVS-Instance-Paused"))
			}
			var body ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "instance_paused" || body.Error.BillingURL != "https://app.everstack.ai/acme/settings/billing" {
				t.Fatalf("body = %+v", body)
			}
		}
	})

	t.Run("user stop gets 503", func(t *testing.T) {
		c, mock, _ := newChecker(t)
		mock.ExpectQuery(loadSQL).WillReturnRows(table("paused", ""))
		rec := serve(HostGuard(c, "", ok), http.MethodGet, "/v1/models", "dev-a1b2c3.eu.everstack.ai")
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-EVS-Instance-Paused") != "stopped" {
			t.Fatalf("code=%d", rec.Code)
		}
	})

	t.Run("random hosts cost no queries and no memory", func(t *testing.T) {
		c, mock, _ := newChecker(t)
		mock.ExpectQuery(loadSQL).WillReturnRows(table("paused", ""))
		h := HostGuard(c, "", ok)
		for i := 0; i < 1000; i++ {
			if rec := serve(h, http.MethodGet, "/v1/models", fmt.Sprintf("x%d-abcdef.eu.everstack.ai", i)); rec.Code != 200 {
				t.Fatalf("non-tenant host must pass, got %d", rec.Code)
			}
		}
		if rec := serve(h, http.MethodGet, "/__ready", "dev-a1b2c3.eu.everstack.ai"); rec.Code != 200 {
			t.Fatal("probe paths must pass")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err) // exactly one load
		}
		if n := len(c.snap.Load().byLabel); n != 2 {
			t.Fatalf("snapshot holds only tenant rows, got %d labels", n)
		}
	})

	t.Run("a resume is honoured within seconds, not a TTL", func(t *testing.T) {
		c, mock, now := newChecker(t)
		mock.ExpectQuery(loadSQL).WillReturnRows(table("paused", ReasonOverLimitAuto))
		h := HostGuard(c, "", ok)
		if rec := serve(h, http.MethodGet, "/v1/models", "dev-a1b2c3.x"); rec.Code != http.StatusForbidden {
			t.Fatalf("paused: %d", rec.Code)
		}
		// Resumed in the controlplane; 6s later, well inside the 30s TTL.
		*now = now.Add(6 * time.Second)
		mock.ExpectQuery(loadSQL).WillReturnRows(table("active", ""))
		if rec := serve(h, http.MethodGet, "/v1/models", "dev-a1b2c3.x"); rec.Code != http.StatusOK {
			t.Fatalf("resumed instance still refused: %d", rec.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("last use is recorded only for accepted API requests", func(t *testing.T) {
		c, mock, _ := newChecker(t)
		mock.ExpectQuery(loadSQL).WillReturnRows(table("active", ""))
		deny := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
		serve(HostGuard(c, "", deny), http.MethodGet, "/v1/models", "dev-a1b2c3.x")
		serve(HostGuard(c, "", ok), http.MethodGet, "/assets/app.js", "dev-a1b2c3.x")
		c.touchMu.Lock()
		n := len(c.touched)
		c.touchMu.Unlock()
		if n != 0 {
			t.Fatal("an unauthenticated or static request must not record use")
		}
		done := make(chan struct{})
		mock.ExpectExec(`UPDATE everstack\.managed_instances SET last_used_at`).
			WithArgs(sqlmock.AnyArg(), pausedID).WillReturnResult(sqlmock.NewResult(0, 1))
		serve(HostGuard(c, "", ok), http.MethodGet, "/v1/models", "dev-a1b2c3.x")
		go func() {
			for mock.ExpectationsWereMet() != nil {
				time.Sleep(5 * time.Millisecond)
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("an accepted API request must record use")
		}
	})
}

// HostGuard hands the tenant a resolved Host names to the credential
// validators, so an API key bound to another tenant can be refused there. It
// is stamped only for a resolved, serving tenant: an unknown host (localhost,
// the shared apex, the grpc-gateway's loopback hop) carries no host scope.
func TestHostGuardStampsTheHostSelectedTenant(t *testing.T) {
	c, mock, _ := newChecker(t)
	mock.ExpectQuery(loadSQL).WillReturnRows(
		sqlmock.NewRows([]string{"instance_id", "organization_id", "slug", "org_slug", "status", "paused_reason", "custom_domain", "custom_domain_status", "gateway_url"}).
			AddRow(activeID, "org-2", "prod", "acme", "active", "", "api.acme.com", "active", "https://prod-d4e5f6.eu-gra-1.everstack.ai"))
	var got contextkeys.RequestInstanceScope
	var stamped bool
	h := HostGuard(c, "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, stamped = contextkeys.HostSelectedTenantFromContext(r.Context())
		if _, verified := contextkeys.RequestInstanceScopeFromContext(r.Context()); verified {
			t.Error("HostGuard must not stamp a verified RequestInstanceScope")
		}
		w.WriteHeader(http.StatusUnauthorized) // keep TouchLastUsed out of the way
	}))

	for _, host := range []string{"prod-d4e5f6.eu-gra-1.everstack.ai", "PROD-D4E5F6.eu-gra-1.everstack.ai:443", "api.acme.com"} {
		stamped = false
		serve(h, http.MethodPost, "/v1/chat/completions", host)
		if !stamped || got.InstanceID != activeID || got.OrganizationID != "org-2" {
			t.Fatalf("%s: host-selected tenant = %+v (stamped %t), want %s/org-2", host, got, stamped, activeID)
		}
	}
	for _, host := range []string{"localhost:8089", "gateway.eu-gra-1.everstack.ai", "prod-d4e5f6.evil.example"} {
		stamped = false
		serve(h, http.MethodPost, "/v1/chat/completions", host)
		if stamped {
			t.Fatalf("%s: an unresolved host must carry no host scope, got %+v", host, got)
		}
	}
}

// The managed host label is only the first DNS label; the rest of the Host
// must be the tenant's canonical host (its stored gateway_url), or any
// domain that routes here could name a tenant by label alone.
func TestResolveHostRequiresCanonicalHost(t *testing.T) {
	c, mock, _ := newChecker(t)
	mock.ExpectQuery(loadSQL).WillReturnRows(
		sqlmock.NewRows([]string{"instance_id", "organization_id", "slug", "org_slug", "status", "paused_reason", "custom_domain", "custom_domain_status", "gateway_url"}).
			AddRow(pausedID, "org-1", "dev", "acme", "paused", "", "", "", "https://dev-a1b2c3.eu-gra-1.everstack.ai").
			AddRow(activeID, "org-2", "prod", "acme", "active", "", "", "", ""))
	ctx := context.Background()

	for _, host := range []string{"dev-a1b2c3.eu-gra-1.everstack.ai", "DEV-A1B2C3.eu-gra-1.everstack.ai.", "dev-a1b2c3.eu-gra-1.everstack.ai:443"} {
		if tn, found, _ := c.ResolveHost(ctx, host); !found || tn.InstanceID != pausedID || tn.OrganizationID != "org-1" {
			t.Fatalf("%s: canonical spelling must resolve, got found=%t %+v", host, found, tn)
		}
	}
	for _, host := range []string{"dev-a1b2c3.evil.example", "dev-a1b2c3.dev.eu-gra-1.everstack.ai", "dev-a1b2c3"} {
		if _, found, _ := c.ResolveHost(ctx, host); found {
			t.Fatalf("%s: label match on a non-canonical host must not resolve", host)
		}
	}
	// A row with no recorded gateway URL keeps label matching.
	if _, found, _ := c.ResolveHost(ctx, "prod-d4e5f6.anything.example"); !found {
		t.Fatal("row without gateway_url must still resolve by label")
	}
}

func TestHostMatchesCanonical(t *testing.T) {
	cases := []struct {
		host, gatewayURL string
		want             bool
	}{
		{"a-123456.eu.everstack.ai", "https://a-123456.eu.everstack.ai", true},
		{"A-123456.EU.everstack.ai:8443", "https://a-123456.eu.everstack.ai/", true},
		{"a-123456.eu.everstack.ai", "http://a-123456.eu.everstack.ai:8089", true},
		{"a-123456.other.example", "https://a-123456.eu.everstack.ai", false},
		{"a-123456.eu.everstack.ai", "not a url", false},
		{"a-123456.eu.everstack.ai", "", true},
	}
	for _, tc := range cases {
		if got := HostMatchesCanonical(tc.host, tc.gatewayURL); got != tc.want {
			t.Errorf("HostMatchesCanonical(%q, %q) = %t, want %t", tc.host, tc.gatewayURL, got, tc.want)
		}
	}
}

// A paused instance's refusal names the org's billing page, which carries
// the org slug. Only a caller the viewer check verifies as a member may see
// it; everyone else gets the refusal without the link.
func TestHostGuardBillingLinkOnlyForVerifiedMembers(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	c, mock, _ := newChecker(t)
	mock.ExpectQuery(loadSQL).WillReturnRows(table("paused", ReasonOverLimitChoice))
	member := false
	c.SetViewerCheck(func(r *http.Request, tn Tenant) bool { return member && tn.InstanceID == pausedID })
	h := HostGuard(c, "https://app.everstack.ai", ok)

	rec := serve(h, http.MethodPost, "/v1/chat/completions", "dev-a1b2c3.eu.everstack.ai")
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusForbidden || body.Error.BillingURL != "" || strings.Contains(rec.Body.String(), "acme") {
		t.Fatalf("anonymous caller: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// A browser navigation is not redirected to the org's billing page.
	if rec := serve(h, http.MethodGet, "/dashboard", "dev-a1b2c3.eu.everstack.ai"); rec.Code == http.StatusFound || strings.Contains(rec.Header().Get("Location"), "acme") {
		t.Fatalf("anonymous navigation redirected to %q", rec.Header().Get("Location"))
	}

	member = true
	rec = serve(h, http.MethodPost, "/v1/chat/completions", "dev-a1b2c3.eu.everstack.ai")
	body = ErrorBody{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.BillingURL != "https://app.everstack.ai/acme/settings/billing" {
		t.Fatalf("member: billing_url = %q", body.Error.BillingURL)
	}
}

// A deleted managed instance is inactive whether its tenant_config row is
// gone or was left behind as active: the background loops must not run it.
func TestDeletedInstanceIsInactive(t *testing.T) {
	tenantlive.SetDeletedCheck(func(id string) bool { return id == activeID || id == "gone" })
	t.Cleanup(func() { tenantlive.SetDeletedCheck(nil) })
	c, mock, _ := newChecker(t)
	mock.ExpectQuery(loadSQL).WillReturnRows(table("active", ""))

	if c.Active(context.Background(), activeID) {
		t.Fatal("a deleted instance with a leftover active row reported active")
	}
	if c.Active(context.Background(), "gone") {
		t.Fatal("a deleted instance with no row reported active")
	}
	if !c.Active(context.Background(), "unknown-self-hosted") {
		t.Fatal("an unknown, undeleted tenant must stay active")
	}
}
