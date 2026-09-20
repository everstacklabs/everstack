package sandbox

import (
	"context"
	"database/sql/driver"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/semaphore"
)

type openAIDestroyBackend struct{ destroys int }

func (*openAIDestroyBackend) Name() string { return "test" }
func (*openAIDestroyBackend) Create(_ context.Context, id string, cfg InstanceConfig) (*Instance, error) {
	return &Instance{ID: id, ContainerID: "vm", Backend: "test", Status: StatusRunning, Config: cfg, CreatedAt: time.Now()}, nil
}
func (*openAIDestroyBackend) Exec(context.Context, string, ExecRequest) (*ExecResult, error) {
	return nil, nil
}
func (*openAIDestroyBackend) WriteFile(context.Context, string, string, []byte) error { return nil }
func (*openAIDestroyBackend) ReadFile(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (*openAIDestroyBackend) ListFiles(context.Context, string, string) ([]FileInfo, error) {
	return nil, nil
}
func (b *openAIDestroyBackend) Destroy(context.Context, string) error { b.destroys++; return nil }
func (*openAIDestroyBackend) Status(context.Context, string) (*Instance, error) {
	return nil, ErrSandboxNotRunning
}
func (*openAIDestroyBackend) DescribePending(context.Context, string) string { return "" }
func (*openAIDestroyBackend) Healthy(context.Context) error                  { return nil }
func (*openAIDestroyBackend) Logs(context.Context, string, LogsOptions) (io.ReadCloser, error) {
	return nil, nil
}
func (*openAIDestroyBackend) Stats(context.Context, string) (*ContainerStats, error) { return nil, nil }
func (*openAIDestroyBackend) Shell(context.Context, string, []string) (*ShellSession, error) {
	return nil, nil
}
func (*openAIDestroyBackend) List(context.Context) ([]*Instance, error) { return nil, nil }

func TestBindOpenAIExecutorOwnershipRejectsForeignInstance(t *testing.T) {
	inst := &Instance{ID: "sbx_openai_a", InstanceID: "instance-a", Config: InstanceConfig{TenantID: "tenant-a", SessionID: "openai_a"}}
	m := &SandboxManager{
		instances:          map[string]*Instance{"openai_a": inst},
		instancesBySandbox: map[string]*Instance{"sbx_openai_a": inst},
	}
	if _, err := m.BindOpenAIExecutorOwnership(context.Background(), inst.ID, "tenant-a", "instance-b"); err == nil {
		t.Fatal("foreign instance rebound sandbox")
	}
	if _, err := m.LookupOpenAIExecutorSandbox(context.Background(), inst.ID, "tenant-a", "instance-b"); err == nil {
		t.Fatal("foreign instance looked up sandbox")
	}
}

func TestLookupOpenAIExecutorSandboxRehydratesOwnedSessionMapping(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	db := sqlx.NewDb(raw, "sqlmock")
	m := &SandboxManager{db: db, instances: map[string]*Instance{}, instancesBySandbox: map[string]*Instance{}}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT config, image FROM sandbox_instances WHERE id = $1`)).WithArgs("sbx_openai_a").WillReturnRows(sqlmock.NewRows([]string{"config", "image"}).AddRow([]byte(`{"session_id":"openai_a","tenant_id":"tenant-a"}`), "executor"))
	mock.ExpectQuery(`SELECT container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, instance_id, destroyed_at`).WithArgs("sbx_openai_a").WillReturnRows(sqlmock.NewRows([]string{"container_id", "backend", "status", "lifecycle_state", "created_at", "billing_started_at", "billing_ended_at", "name", "agent_id", "persistent", "instance_id", "destroyed_at"}).AddRow("vm", "firecracker-agent", "running", "running", time.Now(), nil, nil, "OpenAI agent", nil, false, "instance-a", nil))
	inst, err := m.LookupOpenAIExecutorSandbox(context.Background(), "sbx_openai_a", "tenant-a", "instance-a")
	if err != nil {
		t.Fatal(err)
	}
	if inst.InstanceID != "instance-a" {
		t.Fatalf("instance ID=%q", inst.InstanceID)
	}
	if m.instances["openai_a"] != inst || m.instancesBySandbox[inst.ID] != inst {
		t.Fatal("durable fallback was not registered for session-keyed cleanup")
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	backend := &openAIDestroyBackend{}
	m.backend = backend
	m.db = nil
	if err = m.DestroyWithReason(context.Background(), "openai_a", "test"); err != nil {
		t.Fatal(err)
	}
	if backend.destroys != 1 {
		t.Fatalf("backend destroys=%d, want 1", backend.destroys)
	}
}

func TestBindOpenAIExecutorOwnershipBindsUnscopedSandbox(t *testing.T) {
	inst := &Instance{ID: "sbx_openai_a", Config: InstanceConfig{TenantID: "tenant-a", SessionID: "openai_a"}}
	m := &SandboxManager{
		instances:          map[string]*Instance{"openai_a": inst},
		instancesBySandbox: map[string]*Instance{"sbx_openai_a": inst},
	}
	got, err := m.BindOpenAIExecutorOwnership(context.Background(), inst.ID, "tenant-a", "instance-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != "instance-a" || inst.InstanceID != "instance-a" {
		t.Fatalf("instance binding = %q", got.InstanceID)
	}
}

func TestPersistOpenAIExecutorUsesExplicitInstanceOwnership(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	m := &SandboxManager{db: sqlx.NewDb(raw, "sqlmock")}
	args := make([]driver.Value, 24)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	args[3] = "instance-a"
	mock.ExpectExec(`INSERT INTO sandbox_instances`).WithArgs(args...).WillReturnResult(sqlmock.NewResult(1, 1))
	m.persistInstance(&Instance{
		ID:         "sbx_openai_a",
		InstanceID: "instance-a",
		Status:     StatusRunning,
		CreatedAt:  time.Now(),
		Config: InstanceConfig{
			SessionID: "openai_a",
			TenantID:  "tenant-a",
		},
	})
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetOrCreateForInstancePropagatesOwnershipToCreatedInstance(t *testing.T) {
	m := &SandboxManager{
		backend:            &openAIDestroyBackend{},
		globalConfig:       GlobalSandboxConfig{MaxSandboxes: 1},
		instances:          map[string]*Instance{},
		instancesBySandbox: map[string]*Instance{},
		createSem:          semaphore.NewWeighted(1),
		registry:           NewLocalRegistry(),
	}
	inst, err := m.GetOrCreateForInstance(context.Background(), "openai_a", "tenant-a", "instance-a", SandboxConfig{Image: "executor"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.InstanceID != "instance-a" {
		t.Fatalf("instance ownership = %q, want instance-a", inst.InstanceID)
	}
	if _, err := m.BindOpenAIExecutorOwnership(context.Background(), inst.ID, "tenant-a", "instance-a"); err != nil {
		t.Fatalf("verify post-create ownership: %v", err)
	}
}

func TestGetOrCreateForInstanceRejectsForeignSandboxInEveryLifecycleState(t *testing.T) {
	tests := []struct {
		name       string
		status     Status
		tenantID   string
		instanceID string
	}{
		{name: "running foreign instance", status: StatusRunning, tenantID: "tenant-a", instanceID: "instance-b"},
		{name: "pending foreign instance", status: StatusPending, tenantID: "tenant-a", instanceID: "instance-b"},
		{name: "stopped foreign instance", status: StatusStopped, tenantID: "tenant-a", instanceID: "instance-b"},
		{name: "foreign tenant", status: StatusStopped, tenantID: "tenant-b", instanceID: "instance-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &openAIDestroyBackend{}
			foreign := &Instance{
				ID:         "sbx_openai_a",
				InstanceID: tt.instanceID,
				Status:     tt.status,
				Config:     InstanceConfig{SessionID: "openai_a", TenantID: tt.tenantID},
			}
			m := &SandboxManager{
				backend:            backend,
				globalConfig:       GlobalSandboxConfig{MaxSandboxes: 1},
				instances:          map[string]*Instance{"openai_a": foreign},
				instancesBySandbox: map[string]*Instance{foreign.ID: foreign},
			}
			inst, err := m.GetOrCreateForInstance(context.Background(), "openai_a", "tenant-a", "instance-a", SandboxConfig{Image: "executor"})
			if err == nil || inst != nil {
				t.Fatalf("result = %#v, %v; want no foreign instance", inst, err)
			}
			if backend.destroys != 0 || m.instances["openai_a"] != foreign {
				t.Fatal("foreign sandbox was altered")
			}
		})
	}
}

func TestGetOrCreateForInstanceRejectsForeignPersistedSandboxBeforeCreate(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	backend := &openAIDestroyBackend{}
	m := &SandboxManager{
		backend:            backend,
		db:                 sqlx.NewDb(raw, "sqlmock"),
		globalConfig:       GlobalSandboxConfig{MaxSandboxes: 1},
		instances:          map[string]*Instance{},
		instancesBySandbox: map[string]*Instance{},
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT config, image FROM sandbox_instances WHERE id = $1`)).
		WithArgs("sbx_openai_a").
		WillReturnRows(sqlmock.NewRows([]string{"config", "image"}).AddRow([]byte(`{"session_id":"openai_a","tenant_id":"instance-b"}`), "executor"))
	mock.ExpectQuery(`SELECT container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, instance_id, destroyed_at`).
		WithArgs("sbx_openai_a").
		WillReturnRows(sqlmock.NewRows([]string{"container_id", "backend", "status", "lifecycle_state", "created_at", "billing_started_at", "billing_ended_at", "name", "agent_id", "persistent", "instance_id", "destroyed_at"}).
			AddRow("vm", "firecracker-agent", "stopped", "stopped", time.Now(), nil, nil, "OpenAI agent", nil, false, "instance-b", nil))
	inst, err := m.GetOrCreateForInstance(context.Background(), "openai_a", "instance-a", "instance-a", SandboxConfig{Image: "executor"})
	if err == nil || inst != nil {
		t.Fatalf("result = %#v, %v; want persisted ownership rejection", inst, err)
	}
	if backend.destroys != 0 || len(m.instances) != 0 {
		t.Fatal("foreign persisted sandbox was adopted or destroyed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBindOpenAIExecutorOwnershipRejectsArbitrarySandbox(t *testing.T) {
	inst := &Instance{ID: "sbx_regular", Config: InstanceConfig{TenantID: "tenant-a", SessionID: "regular"}}
	m := &SandboxManager{
		instances:          map[string]*Instance{"regular": inst},
		instancesBySandbox: map[string]*Instance{"sbx_regular": inst},
	}
	if _, err := m.BindOpenAIExecutorOwnership(context.Background(), inst.ID, "tenant-a", "instance-a"); err == nil {
		t.Fatal("arbitrary tenant sandbox was reassociated as an OpenAI executor")
	}
}
