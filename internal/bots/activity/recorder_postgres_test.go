package activity

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

func activityDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("EVS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_TEST_POSTGRES_DSN for activity integration tests")
	}
	base, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "activity_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); base.Exec("DROP SCHEMA " + schema + " CASCADE"); base.Close() })
	_, err = db.Exec(`CREATE TABLE api_keys(id TEXT PRIMARY KEY,org_id TEXT,instance_id TEXT,last_used_at TIMESTAMPTZ)`)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := filepath.Glob("../../database/migrations/sql/postgres/bot_activity_*/up.sql")
	if err != nil || len(migrations) != 1 {
		t.Fatal(migrations, err)
	}
	data, err := os.ReadFile(migrations[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO api_keys(id,org_id) VALUES ('key','tenant')`); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestPostgresActivityThrottleAndAdditiveReplicas(t *testing.T) {
	db := activityDB(t)
	ctx := context.Background()
	// Two workers flush concurrently and must add rather than replace counts.
	a, b := New(db), New(db)
	for range 7 {
		a.Record("key", "tenant", apikey.SurfaceMCP)
	}
	for range 11 {
		b.Record("key", "tenant", apikey.SurfaceMCP)
	}
	done := make(chan error, 2)
	go func() { done <- a.Stop(ctx) }()
	go func() { done <- b.Stop(ctx) }()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var calls int64
	if err := db.Get(&calls, `SELECT calls FROM api_key_activity_daily WHERE tenant_id='tenant' AND api_key_id='key' AND surface='mcp'`); err != nil || calls != 18 {
		t.Fatalf("additive calls=%d err=%v", calls, err)
	}
	var first time.Time
	if err := db.Get(&first, `SELECT last_used_at FROM api_keys WHERE id='key' AND org_id='tenant'`); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{db: db}
	day := first.UTC().Format("2006-01-02")
	flush := func(tenant string, at time.Time) {
		t.Helper()
		if err := r.flush(ctx, map[rowKey]row{{tenant, "key", day, apikey.SurfaceMCP}: {calls: 1, latest: at}}); err != nil {
			t.Fatal(err)
		}
	}
	flush("tenant", first.Add(30*time.Second))
	var got time.Time
	if err := db.Get(&got, `SELECT last_used_at FROM api_keys WHERE id='key' AND org_id='tenant'`); err != nil || !got.Equal(first) {
		t.Fatalf("throttle: %v %v", got, err)
	}
	later := first.Add(61 * time.Second)
	flush("tenant", later)
	if err := db.Get(&got, `SELECT last_used_at FROM api_keys WHERE id='key' AND org_id='tenant'`); err != nil || !got.Equal(later) {
		t.Fatalf("minute refresh: %v %v", got, err)
	}
	flush("foreign", later.Add(time.Hour))
	if err := db.Get(&got, `SELECT last_used_at FROM api_keys WHERE id='key' AND org_id='tenant'`); err != nil || !got.Equal(later) {
		t.Fatal("foreign tenant updated key")
	}
	if err := db.Get(&calls, `SELECT calls FROM api_key_activity_daily WHERE tenant_id='tenant' AND api_key_id='key' AND surface='mcp'`); err != nil || calls != 20 {
		t.Fatalf("tenant isolation: %d %v", calls, err)
	}
}
