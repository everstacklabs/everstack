package apikey

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/jmoiron/sqlx"
)

func TestLookupDefaultDeny(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	handler := NewApiKeyByHashQueryHandler(sqlx.NewDb(raw, "sqlmock"))
	for _, surfaces := range []interface{}{nil, "{}", "{mcp}", "{inference,mcp,telemetry,a2a}"} {
		for _, surface := range []apikey.Surface{"", apikey.SurfaceMCP, apikey.SurfaceInference, apikey.SurfaceTelemetry, apikey.SurfaceA2A, "admin"} {
			for _, enforces := range []bool{false, true} {
				q := NewGetApiKeyByHashQuery("digest", "", "")
				q.Surface = surface
				q.CallerEnforces = enforces
				// A surface-limited key is stored under the prefixed hash.
				stored := "digest"
				if surfaces != nil {
					stored = apikey.StoredHashPrefix + "digest"
				}
				mock.ExpectQuery("SELECT .* surfaces, .* FROM api_keys WHERE hash IN").WithArgs("digest", apikey.StoredHashPrefix+"digest").WillReturnRows(sqlmock.NewRows([]string{"id", "hash", "surfaces"}).AddRow("key", stored, surfaces))
				result, err := handler.Handle(context.Background(), q)
				if err != nil {
					t.Fatal(err)
				}
				allowed := surfaces == nil || enforces || surfaces == "{mcp}" && surface == apikey.SurfaceMCP || surfaces == "{inference,mcp,telemetry,a2a}" && apikey.ValidSurface(surface)
				if (result != nil) != allowed {
					t.Fatalf("set=%v surface=%q callerEnforces=%v got=%v", surfaces, surface, enforces, result != nil)
				}
			}
		}
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A row whose stored hash form disagrees with its surfaces was not written by
// this code. Both shapes are refused: a bare hash with surfaces (which an older
// build would read as an ordinary key) and a prefixed hash without surfaces.
func TestLookupRefusesInconsistentHashForm(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	handler := NewApiKeyByHashQueryHandler(sqlx.NewDb(raw, "sqlmock"))
	for _, row := range []struct {
		hash     string
		surfaces interface{}
		found    bool
	}{
		{"digest", nil, true},
		{apikey.StoredHashPrefix + "digest", "{mcp}", true},
		{"digest", "{mcp}", false},
		{apikey.StoredHashPrefix + "digest", nil, false},
	} {
		q := NewGetApiKeyByHashQuery("digest", "", "")
		q.CallerEnforces = true
		mock.ExpectQuery("FROM api_keys WHERE hash IN").WithArgs("digest", apikey.StoredHashPrefix+"digest").WillReturnRows(sqlmock.NewRows([]string{"id", "hash", "surfaces"}).AddRow("key", row.hash, row.surfaces))
		result, err := handler.Handle(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if (result != nil) != row.found {
			t.Fatalf("hash=%q surfaces=%v found=%v, want %v", row.hash, row.surfaces, result != nil, row.found)
		}
		if rm, ok := result.(APIKeyReadModel); ok && rm.Hash != "digest" {
			t.Fatalf("returned hash %q still carries the stored prefix", rm.Hash)
		}
	}
}

func TestListExcludesBotKeysInSQL(t *testing.T) {
	raw, mock, _ := sqlmock.New()
	defer raw.Close()
	db := sqlx.NewDb(raw, "sqlmock")
	q := NewListApiKeysQuery("", "tenant", "", "", "")
	q.ExcludeSurfaceLimited = true
	mock.ExpectQuery(regexp.QuoteMeta("FROM api_keys WHERE COALESCE(revoked, FALSE) = FALSE AND surfaces IS NULL AND org_id = $1 ORDER BY created_at DESC")).WithArgs("tenant").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow("ordinary", time.Now()))
	if _, err := NewListApiKeysQueryHandler(db).Handle(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
