package bots

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	keyserver "github.com/everstacklabs/everstack/internal/api/grpc/api_key/v1"
	"github.com/everstacklabs/everstack/internal/commands"
	keycmd "github.com/everstacklabs/everstack/internal/commands/handlers/api_key"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/interop"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	keyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Like interop's testRunStore, use an isolated schema and locate new migrations
// by glob. Both deployed api_keys.id types must support bot associations.
func testBotStore(t *testing.T, idType string) (*Store, *keyserver.Server, context.Context) {
	t.Helper()
	dsn := os.Getenv("EVS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_TEST_POSTGRES_DSN for isolated PostgreSQL integration tests")
	}
	base, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "bots_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(`CREATE SCHEMA ` + schema); err != nil {
		base.Close()
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
	t.Cleanup(func() { db.Close(); base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); base.Close() })
	patterns := []string{"api_keys_ensure_*"}
	if idType == "uuid" {
		patterns = []string{"api_keys_init_*", "alter_api_key_with_revoke_*"}
	}
	patterns = append(patterns, "api_keys_add_user_org_*", "add_sensitive_id_api_keys_*", "api_keys_add_instance_id_*", "interop_tables_*", "external_agent_runs_*", "bot_connections_*", "bot_activity_*")
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join("../database/migrations/sql/postgres", pattern, "up.sql"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration %s: %v %v", pattern, matches, err)
		}
		data, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(data)); err != nil {
			t.Fatalf("migration %s: %v", pattern, err)
		}
	}
	// A one-connection pool must work: lifecycle serialization must not hold a
	// database connection while asking the command projections for another.
	db.SetMaxOpenConns(1)
	bus := database.NewInMemoryEventBus()
	pm := database.NewProjectionManager(db, bus)
	if err = pm.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	cb := commands.NewCommandBus(nil, bus)
	cb.RegisterHandler(keycmd.NewApiKeyCommandHandler())
	qb := query.NewQueryBus()
	qb.RegisterHandler(keyq.NewApiKeyByHashQueryHandler(db))
	qb.RegisterHandler(keyq.NewApiKeyQueryHandler(db))
	qb.RegisterHandler(keyq.NewListApiKeysQueryHandler(db))
	ctx := cqrs.WithSystem(session("tenant-a"), &cqrs.System{CommandBus: cb, QueryBus: qb})
	ctx = contextkeys.WithAPIKeyHashSecret(ctx, "bots-postgres-test-secret")
	t.Setenv("EVS_INSTANCE_ID", "")
	return NewStore(db), keyserver.CreateServerWithContext(ctx), ctx
}
func insertTestBot(t *testing.T, store *Store, keys KeyService, ctx context.Context, name string, surfaces []string) *Bot {
	t.Helper()
	id, _, hint, err := keys.CreateBotKey(ctx, name, surfaces)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{TenantID: contextkeys.GetTenantID(ctx), Name: name, Kind: "custom", APIKeyID: id, KeyHint: hint, Surfaces: surfaces}
	if err = store.Insert(ctx, b); err != nil {
		t.Fatal(err)
	}
	return b
}
func TestPostgresBotsLifecycleAndSurfaceProjection(t *testing.T) {
	for _, idType := range []string{"uuid", "text"} {
		t.Run(idType, func(t *testing.T) {
			store, keys, ctx := testBotStore(t, idType)
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			real := newRealHarness(store, keys)
			r := real.request(ctx, "POST", "/api/bots", `{"name":"  Real bot  ","kind":"custom"}`, "application/json")
			checkCode(t, r, 201, "")
			list, err := store.List(ctx, "tenant-a")
			if err != nil || len(list) != 1 {
				t.Fatalf("list: %v %v", list, err)
			}
			bot := list[0]
			if bot.Name != "Real bot" || len(bot.Surfaces) != 3 || bot.CreatedBy == nil || *bot.CreatedBy != "session-user" {
				t.Fatalf("stored bot: %+v", bot)
			}
			var exists bool
			if err = store.db.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id::text=$1 AND org_id=$2 AND surfaces IS NOT NULL AND NOT revoked)`, bot.APIKeyID, "tenant-a"); err != nil || !exists {
				t.Fatalf("key projection not synchronous: %v", err)
			}
			oldID := bot.APIKeyID
			checkCode(t, real.request(ctx, "PATCH", "/api/bots/"+bot.ID, `{"surfaces":[]}`, "application/json"), 200, "")
			botPtr, err := store.Get(ctx, "tenant-a", bot.ID)
			if err != nil {
				t.Fatal(err)
			}
			bot = *botPtr
			if bot.Surfaces == nil || len(bot.Surfaces) != 0 {
				t.Fatal("empty surfaces became SQL NULL")
			}
			var raw pq.StringArray
			if err = store.db.GetContext(ctx, &raw, `SELECT surfaces FROM api_keys WHERE org_id=$1 AND id::text=$2`, "tenant-a", oldID); err != nil || raw == nil {
				t.Fatalf("empty projection: %v", err)
			}
			checkCode(t, real.request(ctx, "POST", "/api/bots/"+bot.ID+"/rotate-key", "", "application/json"), 200, "")
			botPtr, err = store.Get(ctx, "tenant-a", bot.ID)
			if err != nil {
				t.Fatal(err)
			}
			if botPtr.APIKeyID == oldID || botPtr.Surfaces == nil || len(botPtr.Surfaces) != 0 {
				t.Fatal("rotation lost surface set")
			}
			if err = store.db.GetContext(ctx, &exists, `SELECT revoked FROM api_keys WHERE org_id=$1 AND id::text=$2`, "tenant-a", oldID); err != nil || !exists {
				t.Fatalf("old key not revoked: %v", err)
			}
			// Default-deny lookup with a live MCP-only key and the actual SQL handler.
			id, plain, _, err := keys.CreateBotKey(ctx, "MCP", []string{"mcp"})
			if err != nil {
				t.Fatal(err)
			}
			digest, _ := apikey.HashFromContext(ctx, plain)
			lookup := keyq.NewApiKeyByHashQueryHandler(store.db)
			for _, surface := range []apikey.Surface{"", apikey.SurfaceMCP, apikey.SurfaceA2A, apikey.SurfaceTelemetry} {
				q := keyq.NewGetApiKeyByHashQuery(digest, "", "")
				q.Surface = surface
				res, err := lookup.Handle(ctx, q)
				if err != nil || (res != nil) != (surface == apikey.SurfaceMCP) {
					t.Fatalf("lookup %s: %v %v", surface, res, err)
				}
			}
			if err = keys.RevokeBotKey(ctx, id); err != nil {
				t.Fatal(err)
			}
			// The public list only returns ordinary keys; public delete refuses bots.
			if _, err = keys.CreateApiKey(ctx, connect.NewRequest(&pb.CreateApiKeyRequest{Name: "ordinary"})); err != nil {
				t.Fatal(err)
			}
			publicList, err := keys.ListApiKeys(ctx, connect.NewRequest(&pb.ListApiKeysRequest{}))
			if err != nil || len(publicList.Msg.ApiKeys) != 1 {
				t.Fatalf("public list: %v %v", publicList, err)
			}
			if _, err = keys.DeleteApiKey(ctx, connect.NewRequest(&pb.DeleteApiKeyRequest{Id: botPtr.APIKeyID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("public bot delete: %v", err)
			}
			// Revocation is idempotent, including deletion of an already dead key.
			if err = keys.RevokeBotKey(ctx, botPtr.APIKeyID); err != nil {
				t.Fatal(err)
			}
			checkCode(t, real.request(ctx, "DELETE", "/api/bots/"+bot.ID, "", "application/json"), 204, "")
			if _, err = store.Get(ctx, "tenant-a", bot.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("deleted bot: %v", err)
			}
		})
	}
}
func newRealHarness(store *Store, keys KeyService) *harness {
	r := mux.NewRouter()
	NewHandlers(store, keys, interop.NewStore(store.db)).Mount(botRouter{r})
	return &harness{router: r}
}
func TestPostgresBotsTenantIsolationNamesAndCAS(t *testing.T) {
	store, keys, ctx := testBotStore(t, "text")
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	bot := insertTestBot(t, store, keys, ctx, "Same", []string{"mcp"})
	otherCtx := contextkeys.WithTenantID(ctx, "tenant-b")
	other := insertTestBot(t, store, keys, otherCtx, "Same", []string{"mcp"})
	if _, err := store.Get(ctx, "tenant-a", other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign read")
	}
	foreign := *other
	foreign.TenantID = "tenant-a"
	foreign.Name = "stolen"
	if err := store.Update(ctx, &foreign); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign patch: %v", err)
	}
	if err := store.Delete(ctx, "tenant-a", other.ID, other.UpdatedAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	duplicate := *bot
	duplicate.Name = "sAME"
	duplicate.APIKeyID = uuid.NewString()
	if err := store.Insert(ctx, &duplicate); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("case insensitive duplicate: %v", err)
	}
	first, _ := store.Get(ctx, "tenant-a", bot.ID)
	stale := *first
	first.Name = "new"
	if err := store.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	stale.Surfaces = []string{"inference"}
	if err := store.Update(ctx, &stale); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale patch: %v", err)
	}
	if err := store.Delete(ctx, "tenant-a", bot.ID, stale.UpdatedAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale delete: %v", err)
	}
	current, _ := store.Get(ctx, "tenant-a", bot.ID)
	if len(current.Surfaces) != 1 || current.Surfaces[0] != "mcp" {
		t.Fatal("stale key update committed")
	}
	// Actual interop remote validation stays tenant-scoped and permits null clear.
	remote := interop.NewStore(store.db)
	if err := remote.SaveRemote(ctx, "tenant-a", "remote", "https://agent.example", "a2a-1.0", "", nil); err != nil {
		t.Fatal(err)
	}
	h := newRealHarness(store, keys)
	checkCode(t, h.request(ctx, "PATCH", "/api/bots/"+bot.ID, `{"remote_agent":"remote"}`, "application/json"), 200, "")
	checkCode(t, h.request(otherCtx, "PATCH", "/api/bots/"+other.ID, `{"remote_agent":"remote"}`, "application/json"), 400, "remote_not_found")
	checkCode(t, h.request(ctx, "PATCH", "/api/bots/"+bot.ID, `{"remote_agent":null}`, "application/json"), 200, "")
	current, _ = store.Get(ctx, "tenant-a", bot.ID)
	if current.RemoteAgent != nil {
		t.Fatal("remote not cleared")
	}
	// Case-insensitive conflict must roll back the key's surfaces too.
	second := insertTestBot(t, store, keys, ctx, "Second", []string{"mcp"})
	second.Name = current.Name
	second.Surfaces = []string{"inference"}
	if err := store.Update(ctx, second); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename conflict: %v", err)
	}
	second, _ = store.Get(ctx, "tenant-a", second.ID)
	if len(second.Surfaces) != 1 || second.Surfaces[0] != "mcp" {
		t.Fatal("conflicting rename changed key")
	}
}

func TestPostgresRollbackRevokesRestrictedKeysPermanently(t *testing.T) {
	store, keys, ctx := testBotStore(t, "text")
	ids := []string{}
	for _, surfaces := range [][]string{{}, {"mcp"}} {
		id, _, _, err := keys.CreateBotKey(ctx, "rollback", surfaces)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	ordinary, err := keys.CreateApiKey(ctx, connect.NewRequest(&pb.CreateApiKeyRequest{Name: "ordinary"}))
	if err != nil {
		t.Fatal(err)
	}
	ordinaryID := ordinary.Msg.ApiKey.Id
	for _, direction := range []string{"down", "up"} {
		matches, err := filepath.Glob(filepath.Join("../database/migrations/sql/postgres", "bot_connections_*", direction+".sql"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration: %v %v", matches, err)
		}
		sql, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.ExecContext(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			var revoked bool
			if err = store.db.GetContext(ctx, &revoked, `SELECT revoked FROM api_keys WHERE org_id=$1 AND id::text=$2`, "tenant-a", id); err != nil || !revoked {
				t.Fatalf("%s unrevoked restricted key: %v", direction, err)
			}
		}
		var revoked bool
		if err = store.db.GetContext(ctx, &revoked, `SELECT revoked FROM api_keys WHERE org_id=$1 AND id::text=$2`, "tenant-a", ordinaryID); err != nil || revoked {
			t.Fatalf("%s changed ordinary key: %v", direction, err)
		}
	}
}

func TestPostgresBotUsageAndRateLimitRoundTrip(t *testing.T) {
	store, keys, ctx := testBotStore(t, "text")
	real := newRealHarness(store, keys)
	created := real.request(ctx, "POST", "/api/bots", `{"name":"Metered","kind":"custom","rate_limit_rpm":12}`, "application/json")
	checkCode(t, created, 201, "")
	list, err := store.List(ctx, "tenant-a")
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	bot := list[0]
	if bot.RateLimitRPM == nil || *bot.RateLimitRPM != 12 || bot.Usage.Calls30d != 0 || len(bot.Usage.BySurface) != 4 {
		t.Fatalf("create: %+v", bot)
	}
	// Old/current keys, dates, and tenants must not contaminate this bot's usage.
	_, err = store.db.Exec(`INSERT INTO api_key_activity_daily(tenant_id,api_key_id,day,surface,calls) VALUES
 ('tenant-a',$1,(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date,'mcp',7),
 ('tenant-a',$1,(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date-29,'inference',3),
 ('tenant-a',$1,(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date-30,'telemetry',99),
 ('foreign',$1,(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date-1,'a2a',99),
 ('tenant-a','old-key',(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date,'mcp',99)`, bot.APIKeyID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`UPDATE api_keys SET last_used_at=NOW() WHERE id::text=$1 AND org_id='tenant-a'`, bot.APIKeyID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`UPDATE bot_connections SET last_seen_at='2000-01-01' WHERE id=$1 AND tenant_id='tenant-a'`, bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	list, err = store.List(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	got := list[0]
	if got.Usage.Calls30d != 10 || got.Usage.BySurface["mcp"] != 7 || got.Usage.BySurface["inference"] != 3 || got.LastSeenAt == nil || got.LastSeenAt.Year() == 2000 {
		t.Fatalf("list: %+v", got)
	}
	checkCode(t, real.request(ctx, "PATCH", "/api/bots/"+bot.ID, `{"rate_limit_rpm":23}`, "application/json"), 200, "")
	gotPtr, err := store.Get(ctx, "tenant-a", bot.ID)
	if err != nil || gotPtr.RateLimitRPM == nil || *gotPtr.RateLimitRPM != 23 || gotPtr.Usage.Calls30d != 10 {
		t.Fatal(gotPtr, err)
	}
	checkCode(t, real.request(ctx, "POST", "/api/bots/"+bot.ID+"/rotate-key", "", "application/json"), 200, "")
	gotPtr, err = store.Get(ctx, "tenant-a", bot.ID)
	if err != nil || gotPtr.RateLimitRPM == nil || *gotPtr.RateLimitRPM != 23 || gotPtr.Usage.Calls30d != 0 || gotPtr.LastSeenAt != nil {
		t.Fatal(gotPtr, err)
	}
	checkCode(t, real.request(ctx, "PATCH", "/api/bots/"+bot.ID, `{"rate_limit_rpm":null}`, "application/json"), 200, "")
	gotPtr, err = store.Get(ctx, "tenant-a", bot.ID)
	if err != nil || gotPtr.RateLimitRPM != nil {
		t.Fatal(gotPtr, err)
	}
}
