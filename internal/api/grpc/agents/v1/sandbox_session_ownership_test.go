package v1

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"

	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/jmoiron/sqlx"
)

// A caller may choose the session id when creating a sandbox. Until
// 2026-11-11 a session id that already belonged to another tenant's sandbox
// returned that sandbox.
func TestSessionIDAvailableToTenant(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
		"sess-a": {ID: "sbx-a", Config: sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "sess-a"}},
	})
	s := &Server{sandboxMgr: mgr}
	ctx := context.Background()

	if err := s.sessionIDAvailableToTenant(ctx, "sess-a", "tenant-b"); !errors.Is(err, errSessionIDUnavailable) {
		t.Fatalf("tenant-b must not create under tenant-a's session id, got %v", err)
	}
	if err := s.sessionIDAvailableToTenant(ctx, "sess-a", "tenant-a"); !errors.Is(err, errSessionIDUnavailable) {
		t.Fatalf("an assigned session id must be refused identically for its owner, got %v", err)
	}
	if err := s.sessionIDAvailableToTenant(ctx, "sess-new", "tenant-b"); err != nil {
		t.Fatalf("an unused session id is available, got %v", err)
	}
	if err := s.sessionIDAvailableToTenant(ctx, "sess-new", ""); !errors.Is(err, errSessionIDUnavailable) {
		t.Fatalf("a create with no tenant must be refused, got %v", err)
	}
	// The message must not say whose it is or that it exists elsewhere.
	if got := errSessionIDUnavailable.Error(); got != "session_id is not available" {
		t.Fatalf("unexpected message %q", got)
	}
}

func TestCreateSandboxChecksLimitBeforeSessionAvailability(t *testing.T) {
	mgr := sandboxManagerAtLimit("tenant-a")
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
		"shared": {ID: "sbx-b", Config: sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "shared"}},
	})
	s := &Server{sandboxMgr: mgr}
	_, err := s.CreateSandbox(verifiedTenantCtx("tenant-a"), connect.NewRequest(&agentspb.CreateSandboxRequest{
		SessionId: "shared",
		Image:     "alpine",
	}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted || !strings.Contains(err.Error(), sandbox.ErrConcurrentSandboxLimit.Error()) {
		t.Fatalf("CreateSandbox error = %v, want sandbox limit", err)
	}
}

func TestRecreateSandboxChecksLimitBeforeSessionAvailability(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	instances := make(map[string]*sandbox.Instance, sandbox.FreeConcurrentSandboxLimit)
	for i := 0; i < sandbox.FreeConcurrentSandboxLimit-1; i++ {
		id := fmt.Sprintf("sbx-running-%d", i)
		instances[id] = &sandbox.Instance{ID: id, LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "session-" + id}}
	}
	instances["sbx-source"] = &sandbox.Instance{ID: "sbx-source", Name: "notebook", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "source"}}
	mgr.SeedLiveInstancesForTest(instances)
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
		"shared": {ID: "sbx-b", Config: sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "shared"}},
	})
	s := &Server{sandboxMgr: mgr}
	_, err := s.RecreateSandbox(verifiedTenantCtx("tenant-a"), connect.NewRequest(&agentspb.RecreateSandboxRequest{
		SandboxId: "sbx-source",
		SessionId: "shared",
	}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted || !strings.Contains(err.Error(), sandbox.ErrConcurrentSandboxLimit.Error()) {
		t.Fatalf("RecreateSandbox error = %v, want sandbox limit", err)
	}
}

func TestSessionIDAvailableToTenantUsesUniformDatabaseRefusal(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	mgr := &sandbox.SandboxManager{}
	mgr.SetDBForTest(sqlx.NewDb(rawDB, "sqlmock"))
	query := `SELECT EXISTS\s*\(\s*SELECT 1 FROM sandbox_instances\s*WHERE session_id = \$1 AND tenant_id <> ''\s*\)`
	for range 2 {
		mock.ExpectQuery(query).
			WithArgs("assigned-session").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	}
	s := &Server{sandboxMgr: mgr}
	ownerErr := s.sessionIDAvailableToTenant(context.Background(), "assigned-session", "tenant-owner")
	foreignErr := s.sessionIDAvailableToTenant(context.Background(), "assigned-session", "tenant-other")
	if !errors.Is(ownerErr, errSessionIDUnavailable) || !errors.Is(foreignErr, errSessionIDUnavailable) || ownerErr.Error() != foreignErr.Error() {
		t.Fatalf("owner = %v, foreign = %v", ownerErr, foreignErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func sandboxManagerAtLimit(tenantID string) *sandbox.SandboxManager {
	mgr := &sandbox.SandboxManager{}
	instances := make(map[string]*sandbox.Instance, sandbox.FreeConcurrentSandboxLimit)
	for i := 0; i < sandbox.FreeConcurrentSandboxLimit; i++ {
		id := fmt.Sprintf("sbx-running-%d", i)
		instances[id] = &sandbox.Instance{ID: id, LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: tenantID, SessionID: "session-" + id}}
	}
	mgr.SeedLiveInstancesForTest(instances)
	return mgr
}

func TestCreateSandboxRejectsAnotherTenantsSessionID(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
		"shared": {ID: "sbx-b", Config: sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "shared"}},
	})
	s := &Server{sandboxMgr: mgr}
	_, err := s.CreateSandbox(verifiedTenantCtx("tenant-a"), connect.NewRequest(&agentspb.CreateSandboxRequest{
		SessionId: "shared",
		Image:     "alpine",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || err.Error() != "invalid_argument: session_id is not available" {
		t.Fatalf("CreateSandbox error = %v", err)
	}
}

func TestCreateSandboxUsesSameRefusalForAssignedSessions(t *testing.T) {
	call := func(owner string) error {
		mgr := &sandbox.SandboxManager{}
		mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
			"shared": {ID: "sbx-shared", Config: sandbox.InstanceConfig{TenantID: owner, SessionID: "shared"}},
		})
		_, err := (&Server{sandboxMgr: mgr}).CreateSandbox(verifiedTenantCtx("tenant-a"), connect.NewRequest(&agentspb.CreateSandboxRequest{
			SessionId: "shared",
			Image:     "alpine",
		}))
		return err
	}
	own := call("tenant-a")
	foreign := call("tenant-b")
	if connect.CodeOf(own) != connect.CodeInvalidArgument || connect.CodeOf(foreign) != connect.CodeInvalidArgument || own.Error() != foreign.Error() {
		t.Fatalf("own = %v, foreign = %v", own, foreign)
	}
}

func TestRecreateSandboxRejectsAnotherTenantsSessionID(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	mgr.SeedLiveInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {ID: "sbx-a", Name: "notebook", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "old"}},
	})
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
		"shared": {ID: "sbx-b", Config: sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "shared"}},
	})
	s := &Server{sandboxMgr: mgr}
	_, err := s.RecreateSandbox(verifiedTenantCtx("tenant-a"), connect.NewRequest(&agentspb.RecreateSandboxRequest{
		SandboxId: "sbx-a",
		SessionId: "shared",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || err.Error() != "invalid_argument: session_id is not available" {
		t.Fatalf("RecreateSandbox error = %v", err)
	}
}
