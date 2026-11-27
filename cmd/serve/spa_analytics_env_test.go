package serve

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	authdomain "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/ui"
	"github.com/jmoiron/sqlx"
)

// The instance console's analytics key reaches the SPA through the runtime
// env the gateway injects into index.html. This drives the production chain
// (session middleware, then the SPA handler) for the cookie a managed
// instance actually receives. It previously checked for `es_tenant_session`,
// a cookie instance auth stopped setting in May 2026, so no real user ever
// received the key and instances recorded no analytics.
func TestSPAShell_AnalyticsEnvFollowsVerifiedSession(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html><head></head><body></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EVS_UI_DIST", dist)
	t.Setenv("EVS_UI_PROXY_URL", "")
	t.Setenv("EVS_POSTHOG_KEY", "phc_cloud_project")
	t.Setenv("EVS_POSTHOG_HOST", "https://ph.everstack.ai")

	const host = "prod-a1b2c3.eu-gra-1.everstack.ai"
	const userID = "00000000-0000-0000-0000-000000000009"

	serveHost := func(t *testing.T, reqHost string, cookies []*http.Cookie, expect func(sqlmock.Sqlmock)) string {
		t.Helper()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		expect(mock)
		h := cookieSessionAuthMiddleware(func() *sqlx.DB { return sqlx.NewDb(db, "sqlmock") })(ui.NewSPAHandler())
		req := httptest.NewRequest(http.MethodGet, "https://"+reqHost+"/login", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rec.Body)
		return string(body)
	}
	serve := func(t *testing.T, cookie *http.Cookie, expect func(sqlmock.Sqlmock)) string {
		t.Helper()
		var cookies []*http.Cookie
		if cookie != nil {
			cookies = append(cookies, cookie)
		}
		return serveHost(t, host, cookies, expect)
	}
	hostRow := func(mock sqlmock.Sqlmock) {
		mock.ExpectQuery(`FROM everstack\.tenant_config AS tc`).
			WithArgs("prod-a1b2c3").
			WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
				AddRow("instance-1", "org-1", "everstack"))
	}

	t.Run("member with the cloud session cookie", func(t *testing.T) {
		got := serve(t, &http.Cookie{Name: "es_everstack_session", Value: "cookie-token"}, func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(sessionlivetest.QueryPattern).
				WithArgs("cookie-token").
				WillReturnRows(sessionlivetest.LegacyRows(userID, time.Now().Add(time.Hour)))
			hostRow(mock)
			mock.ExpectQuery(`SELECT COALESCE\(role, ''\) FROM everstack\.organization_members`).
				WithArgs(userID, "org-1").
				WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("member"))
		})
		for _, want := range []string{`"POSTHOG_KEY":"phc_cloud_project"`, `"ORGANIZATION_ID":"org-1"`, `"INSTANCE_ID":"instance-1"`} {
			if !strings.Contains(got, want) {
				t.Errorf("shell missing %s: %s", want, got)
			}
		}
	})

	// Signed-out visitors of a managed instance are recorded anonymously:
	// the key, but never the tenant ids. This covers the bounce cases the
	// owner wants to see: no session, an expired or foreign session, and a
	// browser that signed out of this instance.
	expectAnonymous := func(t *testing.T, got string) {
		t.Helper()
		if !strings.Contains(got, `"POSTHOG_KEY":"phc_cloud_project"`) {
			t.Errorf("signed-out visitor of a managed instance should get the key: %s", got)
		}
		for _, leaked := range []string{"ORGANIZATION_ID", "INSTANCE_ID", "ORGANIZATION_SLUG", "org-1", "instance-1"} {
			if strings.Contains(got, leaked) {
				t.Errorf("signed-out shell leaked %s: %s", leaked, got)
			}
		}
	}

	t.Run("anonymous visitor", func(t *testing.T) {
		expectAnonymous(t, serve(t, nil, hostRow))
	})

	t.Run("expired or forged session cookie", func(t *testing.T) {
		got := serve(t, &http.Cookie{Name: "es_everstack_session", Value: "stale"}, func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(sessionlivetest.QueryPattern).WithArgs("stale").WillReturnError(sql.ErrNoRows)
			hostRow(mock)
		})
		expectAnonymous(t, got)
	})

	t.Run("signed out of this instance", func(t *testing.T) {
		got := serveHost(t, host, []*http.Cookie{
			{Name: authdomain.InstanceSignedOutCookie, Value: "1"},
			{Name: "es_everstack_session", Value: "cloud-token"},
		}, hostRow)
		expectAnonymous(t, got)
	})

	t.Run("member of another organization", func(t *testing.T) {
		got := serve(t, &http.Cookie{Name: "es_everstack_session", Value: "cookie-token"}, func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(sessionlivetest.QueryPattern).
				WithArgs("cookie-token").
				WillReturnRows(sessionlivetest.LegacyRows(userID, time.Now().Add(time.Hour)))
			hostRow(mock)
			mock.ExpectQuery(`SELECT COALESCE\(role, ''\) FROM everstack\.organization_members`).
				WithArgs(userID, "org-1").
				WillReturnError(sql.ErrNoRows)
		})
		expectAnonymous(t, got)
	})

	// A Host that resolves to no instance gets nothing, signed in or not.
	t.Run("unknown host", func(t *testing.T) {
		got := serveHost(t, "nope-000000.eu-gra-1.everstack.ai", nil, func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(`FROM everstack\.tenant_config AS tc`).
				WithArgs("nope-000000").
				WillReturnError(sql.ErrNoRows)
		})
		if strings.Contains(got, "POSTHOG_KEY") {
			t.Errorf("unknown host must not get the key: %s", got)
		}
	})

	// The production gateway runs managed: a valid session on a host that
	// resolves to nothing fails closed before any scope is attached.
	t.Run("managed gateway, valid session on an unknown host", func(t *testing.T) {
		enterprise.SetManagedGateway(true)
		t.Cleanup(func() { enterprise.SetManagedGateway(false) })
		got := serveHost(t, "nope-000000.eu-gra-1.everstack.ai", []*http.Cookie{{Name: "es_everstack_session", Value: "cookie-token"}}, func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(sessionlivetest.QueryPattern).
				WithArgs("cookie-token").
				WillReturnRows(sessionlivetest.LegacyRows(userID, time.Now().Add(time.Hour)))
			mock.ExpectQuery(`FROM everstack\.tenant_config AS tc`).
				WithArgs("nope-000000").
				WillReturnError(sql.ErrNoRows)
		})
		if strings.Contains(got, "POSTHOG_KEY") {
			t.Errorf("unknown host must not get the key: %s", got)
		}
	})

	t.Run("unknown host with a session cookie", func(t *testing.T) {
		got := serveHost(t, "nope-000000.eu-gra-1.everstack.ai", []*http.Cookie{{Name: "es_everstack_session", Value: "stale"}}, func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(sessionlivetest.QueryPattern).WithArgs("stale").WillReturnError(sql.ErrNoRows)
			mock.ExpectQuery(`FROM everstack\.tenant_config AS tc`).
				WithArgs("nope-000000").
				WillReturnError(sql.ErrNoRows)
		})
		if strings.Contains(got, "POSTHOG_KEY") {
			t.Errorf("unknown host must not get the key: %s", got)
		}
	})
}
