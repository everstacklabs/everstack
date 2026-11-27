package v1

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestResolveOwnedSandboxFallsBackWhenMemoryTenantIsUnknown(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	mgr := &sandbox.SandboxManager{}
	mgr.SetDB(sqlx.NewDb(rawDB, "sqlmock"))
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
		"sess-a": {ID: "sbx-a", Config: sandbox.InstanceConfig{SessionID: "sess-a"}},
	})

	query := regexp.QuoteMeta(`
		SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, config, image, agent_target
		FROM sandbox_instances
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT 1`)
	mock.ExpectQuery(query).WithArgs("sess-a").WillReturnRows(sqlmock.NewRows([]string{
		"id", "instance_id", "container_id", "backend", "status", "lifecycle_state", "created_at",
		"billing_started_at", "billing_ended_at", "name", "agent_id", "persistent", "config", "image", "agent_target",
	}).AddRow(
		"sbx-a", nil, nil, "docker", "running", "running", time.Now(), nil, nil, "sandbox-a", nil, false,
		[]byte(`{"tenant_id":"tenant-a","session_id":"sess-a"}`), "alpine", nil,
	))

	s := &Server{sandboxMgr: mgr}
	inst, ok := s.resolveOwnedSandbox(context.Background(), map[string]string{"session_id": "sess-a"}, "tenant-a")
	if !ok || inst == nil || inst.ID != "sbx-a" || inst.Config.TenantID != "tenant-a" {
		t.Fatalf("database fallback = ok %v, instance %+v", ok, inst)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
