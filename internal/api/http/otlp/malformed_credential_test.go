package otlp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/mcp/serverauth"
	"github.com/jmoiron/sqlx"
)

func TestMalformedOrUnknownCredentialNeverUsesUpstreamTenant(t *testing.T) {
	for _, headers := range []http.Header{
		{"Authorization": {"\u00a0Bearer x"}}, {"Authorization": {"\tBearer\tx"}}, {"Authorization": {"bearer x"}},
		{"Authorization": {"Bearer \u00a0\t"}}, {"Authorization": {"\t"}}, {"X-Evs-Api-Key": {" \u00a0\t"}},
		{"X-Mf-Api-Key": {" "}, "X-Everstack-Api-Key": {"x"}}, {"Authorization": {"Bearer x", "Bearer y"}},
	} {
		raw, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		auth := serverauth.NewAPIKeyAuthenticator(sqlx.NewDb(raw, "sqlmock"), nil).ForSurface(apikey.SurfaceTelemetry)
		r := httptest.NewRequest("POST", "/v1/traces", nil)
		r.Header = headers
		_, _, malformed := serverauth.ExtractPresentedKey(r)
		if !malformed {
			mock.ExpectQuery("SELECT .* FROM api_keys WHERE hash").WillReturnRows(sqlmock.NewRows([]string{"id"}))
		}
		ctx := contextkeys.WithTenantID(r.Context(), "upstream-host-tenant")
		ctx = contextkeys.WithAPIKeyHashSecret(ctx, "test-secret")
		w := httptest.NewRecorder()
		called := false
		WithTenantAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }), auth).ServeHTTP(w, r.WithContext(ctx))
		if called || w.Code != 401 {
			t.Fatalf("credential reached anonymous fallback: %d", w.Code)
		}
		if err = mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		raw.Close()
	}
}
