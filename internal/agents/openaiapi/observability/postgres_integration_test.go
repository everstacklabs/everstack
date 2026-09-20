package observability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

func TestPostgresUsageMigrationAndRepository(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN for disposable Postgres integration")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("openai_usage_test_%d", time.Now().UnixNano())
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	if _, err = db.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	for _, migrationPath := range []string{
		"../../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql",
		"../../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql",
		"../../../database/migrations/sql/postgres/openai_agents_usage_20260911160000/up.sql",
	} {
		migration, readErr := os.ReadFile(filepath.Clean(migrationPath))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatalf("apply %s: %v", migrationPath, err)
		}
	}
	_, err = db.Exec(`INSERT INTO openai_agent_connections
 (id,organization_id,instance_id,name,provider,credential_ref) VALUES
 ('conn','org-a','instance-a','primary','openai_agents','encrypted');
 INSERT INTO openai_agent_sessions
 (id,organization_id,instance_id,provider,connection_id,upstream_session_id,model,environment_type,idempotency_key,request_fingerprint,created_by)
 VALUES ('local','org-a','instance-a','openai_agents','conn','upstream','gpt-6','none','idem','fingerprint','user-a')`)
	if err != nil {
		t.Fatal(err)
	}
	repository, _ := NewPostgresRepository(db)
	ctx := context.Background()
	base := TurnUsage{
		Scope:             Scope{OrganizationID: "org-a", InstanceID: "instance-a", LocalSessionID: "local"},
		UpstreamSessionID: "upstream", UpstreamTurnID: "turn-1", EventID: "event-1", Cursor: 5,
		InputTokens: int64Pointer(4), TotalTokens: int64Pointer(4), ObservedAt: time.Now().UTC(),
	}
	changed, err := repository.RecordTurnUsage(ctx, base)
	if err != nil || !changed {
		t.Fatalf("first usage changed=%v err=%v", changed, err)
	}
	changed, err = repository.RecordTurnUsage(ctx, base)
	if err != nil || changed {
		t.Fatalf("replayed event changed=%v err=%v", changed, err)
	}
	stale := base
	stale.EventID, stale.Cursor, stale.InputTokens = "event-older", 4, int64Pointer(99)
	changed, err = repository.RecordTurnUsage(ctx, stale)
	if err != nil || changed {
		t.Fatalf("out-of-order event changed=%v err=%v", changed, err)
	}
	second := base
	second.UpstreamTurnID, second.EventID, second.Cursor = "turn-2", "event-2", 6
	second.InputTokens, second.OutputTokens, second.TotalTokens = nil, int64Pointer(0), int64Pointer(0)
	if changed, err = repository.RecordTurnUsage(ctx, second); err != nil || !changed {
		t.Fatalf("second turn changed=%v err=%v", changed, err)
	}
	snapshot, err := repository.SessionUsage(ctx, base.Scope)
	if err != nil || snapshot.Status != UsageReported || snapshot.InputTokens == nil || *snapshot.InputTokens != 4 || snapshot.OutputTokens == nil || *snapshot.OutputTokens != 0 || snapshot.TotalTokens == nil || *snapshot.TotalTokens != 4 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	foreign := base.Scope
	foreign.InstanceID = "instance-b"
	if _, err = repository.SessionUsage(ctx, foreign); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign read err=%v", err)
	}
	foreignUsage := base
	foreignUsage.InstanceID, foreignUsage.EventID, foreignUsage.Cursor = "instance-b", "event-foreign", 7
	if _, err = repository.RecordTurnUsage(ctx, foreignUsage); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign write err=%v", err)
	}
}
