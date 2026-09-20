package openaiapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	executorpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/executor"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

func openIsolatedHADB(t *testing.T, dsn string) *sqlx.DB {
	t.Helper()
	schema := "openai_ha_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("pgx", u.String())
	if err != nil {
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); admin.Close() })
	return db
}

type replicaExecutorBackend struct {
	mu                       sync.Mutex
	owner                    executorpkg.Ownership
	instance                 *sandbox.Instance
	executorStarts, destroys int
	files                    map[string][]byte
}

func (b *replicaExecutorBackend) Provision(context.Context, executorpkg.Ownership, string, sandbox.SandboxConfig) (*sandbox.Instance, error) {
	return b.instance, nil
}
func (b *replicaExecutorBackend) VerifyOwnership(_ context.Context, o executorpkg.Ownership, id string) error {
	if o != b.owner || id != b.instance.ID {
		return errors.New("foreign sandbox")
	}
	return nil
}
func (b *replicaExecutorBackend) Exec(ctx context.Context, o executorpkg.Ownership, id string, c executorpkg.Command) (*sandbox.ExecResult, error) {
	if err := b.VerifyOwnership(ctx, o, id); err != nil {
		return nil, err
	}
	if len(c.Argv) > 1 && c.Argv[1] == "--version" {
		return &sandbox.ExecResult{Stdout: "codex-cli " + executorpkg.ExecutorVersion, ExitCode: 0}, nil
	}
	b.mu.Lock()
	b.executorStarts++
	b.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}
func (b *replicaExecutorBackend) ReadFile(ctx context.Context, o executorpkg.Ownership, id, name string) ([]byte, error) {
	if err := b.VerifyOwnership(ctx, o, id); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.files[name]...), nil
}
func (b *replicaExecutorBackend) WriteFile(ctx context.Context, o executorpkg.Ownership, id, name string, data []byte) error {
	if err := b.VerifyOwnership(ctx, o, id); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.files[name] = append([]byte(nil), data...)
	return nil
}
func (b *replicaExecutorBackend) ListFiles(context.Context, executorpkg.Ownership, string, string) ([]sandbox.FileInfo, error) {
	return nil, nil
}
func (b *replicaExecutorBackend) Destroy(ctx context.Context, o executorpkg.Ownership, id, _ string) error {
	if err := b.VerifyOwnership(ctx, o, id); err != nil {
		return err
	}
	b.mu.Lock()
	b.destroys++
	b.mu.Unlock()
	return nil
}

type replicaArtifactBridge struct{ input, exported []byte }

func (b *replicaArtifactBridge) ResolveInput(_ context.Context, r executorpkg.InputRequest) (*executorpkg.InputFile, error) {
	return &executorpkg.InputFile{ReferenceID: r.ReferenceID, Filename: "input.txt", Content: b.input}, nil
}
func (b *replicaArtifactBridge) Export(_ context.Context, r executorpkg.ExportRequest) (*executorpkg.Artifact, error) {
	b.exported = append([]byte(nil), r.Content...)
	return &executorpkg.Artifact{ObjectID: "output", Filename: r.Filename}, nil
}

type adminRecoveryAuthorizer struct{}

func (adminRecoveryAuthorizer) AuthorizeRecovery(_ context.Context, o Ownership) (Ownership, error) {
	o.Role = "admin"
	return o, nil
}

func TestPostgresStoreDurabilityAndOwnership(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN for disposable Postgres integration")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migration, err := os.ReadFile("../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	fenceMigration, err := os.ReadFile("../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(fenceMigration)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`TRUNCATE openai_agent_input_intents,openai_agent_events,openai_agent_sessions,openai_agent_connections CASCADE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TABLE IF EXISTS openai_agent_input_intents,openai_agent_events,openai_agent_sessions,openai_agent_connections CASCADE`)
	})
	store, _ := NewPostgresStore(db)
	ctx := context.Background()
	owner := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "admin"}
	conn, err := store.CreateConnection(ctx, owner, Connection{ID: "conn-a", Name: "primary", Provider: Provider, AllowedModels: []string{"gpt-6"}}, "ciphertext-app", "")
	if err != nil {
		t.Fatal(err)
	}
	if conn.credentialRef != "ciphertext-app" {
		t.Fatal("credential ciphertext was not retained internally")
	}
	request := Session{ID: "local-a", Provider: Provider, ConnectionID: conn.ID, Model: "gpt-6", Environment: EnvironmentSpec{Type: "none"}, IdempotencyKey: "create-once"}
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	created := make(chan bool, workers)
	errs := make(chan error, workers)
	for range workers {
		go func() {
			defer wg.Done()
			_, fresh, e := store.BeginSessionCreate(ctx, owner, request)
			created <- fresh
			errs <- e
		}()
	}
	wg.Wait()
	close(created)
	close(errs)
	freshCount := 0
	for fresh := range created {
		if fresh {
			freshCount++
		}
	}
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if freshCount != 1 {
		t.Fatalf("fresh creates=%d, want 1", freshCount)
	}
	if _, err = store.CompleteSessionCreate(ctx, owner, "local-a", UpstreamSession{ID: "upstream-a", Status: "idle", Readiness: "ready"}); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewPostgresStore(db)
	got, err := restarted.GetSession(ctx, owner, "local-a")
	if err != nil || got.UpstreamSessionID != "upstream-a" {
		t.Fatalf("restart recovery got=%+v err=%v", got, err)
	}
	foreign := owner
	foreign.InstanceID = "instance-b"
	if _, err = restarted.GetSession(ctx, foreign, "local-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign session read=%v", err)
	}
	event := UpstreamEvent{ID: "event-a", Type: "tool.completed", Payload: []byte(`{"status":"failed"}`), Failed: true}
	first, inserted, err := restarted.PutEvent(ctx, owner, "local-a", event)
	if err != nil || !inserted || !first.ToolFailed {
		t.Fatalf("first event=%+v inserted=%v err=%v", first, inserted, err)
	}
	second, inserted, err := restarted.PutEvent(ctx, owner, "local-a", event)
	if err != nil || inserted || second.Cursor != first.Cursor {
		t.Fatalf("duplicate event=%+v inserted=%v err=%v", second, inserted, err)
	}
	// A connection that still backs a live session refuses deletion, because
	// the delete clears the credentials its teardown needs. Retire the session
	// first; this assertion is about delete-then-read, not about that guard,
	// which TestPostgresDeleteConnectionRefusesWhileSessionsHoldResources owns.
	if err = restarted.MarkCleanup(ctx, owner, "local-a", "deleted", ""); err != nil {
		t.Fatal(err)
	}
	if err = restarted.DeleteConnection(ctx, owner, "conn-a"); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.GetConnection(ctx, owner, "conn-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted connection read=%v", err)
	}
}

func TestPostgresSessionLeaseHasOneOwnerAndExpires(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN for disposable Postgres integration")
	}
	db := openIsolatedHADB(t, dsn)
	var err error
	for _, name := range []string{"../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql", "../../database/migrations/sql/postgres/openai_agent_session_leases_20260911170000/up.sql", "../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql"} {
		migration, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := NewPostgresStore(db)
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	if _, err = store.CreateConnection(context.Background(), o, Connection{ID: "connection", Name: "primary", Provider: Provider}, "ciphertext", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.BeginSessionCreate(context.Background(), o, Session{ID: "session", ConnectionID: "connection", Provider: Provider, Model: "gpt-6", Environment: EnvironmentSpec{Type: "none"}, IdempotencyKey: "create", RequestFingerprint: "fingerprint"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteSessionCreate(context.Background(), o, "session", UpstreamSession{ID: "upstream", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if acquired, err := store.AcquireSessionLease(context.Background(), o, "session", "replica-a", 50*time.Millisecond); err != nil || !acquired {
		t.Fatalf("first acquire=%v err=%v", acquired, err)
	}
	if acquired, err := store.AcquireSessionLease(context.Background(), o, "session", "replica-b", time.Second); err != nil || acquired {
		t.Fatalf("contended acquire=%v err=%v", acquired, err)
	}
	time.Sleep(75 * time.Millisecond)
	if acquired, err := store.AcquireSessionLease(context.Background(), o, "session", "replica-b", time.Second); err != nil || !acquired {
		t.Fatalf("expired takeover=%v err=%v", acquired, err)
	}
	if err = store.MarkCleanup(context.Background(), o, "session", "deleting", ""); err != nil {
		t.Fatal(err)
	}
	if renewed, err := store.RenewSessionLease(context.Background(), o, "session", "replica-b", time.Second); err != nil || renewed {
		t.Fatalf("renew while deleting=%v err=%v", renewed, err)
	}
}

func TestPostgresEnvironmentTimeoutFenceIsOwnedAndMonotonic(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	}
	db := openIsolatedHADB(t, dsn)
	for _, name := range []string{"../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql", "../../database/migrations/sql/postgres/openai_agent_session_leases_20260911170000/up.sql", "../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql"} {
		migration, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	storeA, _ := NewPostgresStore(db)
	storeB, _ := NewPostgresStore(db)
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	if _, err := storeA.CreateConnection(context.Background(), o, Connection{ID: "connection", Name: "primary", Provider: Provider}, "ciphertext", "executor"); err != nil {
		t.Fatal(err)
	}
	createSession := func(id string) {
		if _, _, err := storeA.BeginSessionCreate(context.Background(), o, Session{ID: id, ConnectionID: "connection", Provider: Provider, Model: "gpt-6", Environment: EnvironmentSpec{Type: "self_hosted"}, IdempotencyKey: id, RequestFingerprint: id}); err != nil {
			t.Fatal(err)
		}
		if _, err := storeA.CompleteSessionCreate(context.Background(), o, id, UpstreamSession{ID: "upstream-" + id, EnvironmentID: "environment-" + id, Status: "active", Readiness: "pending"}); err != nil {
			t.Fatal(err)
		}
		if err := storeA.BindEnvironment(context.Background(), o, id, "sandbox-"+id, "pending"); err != nil {
			t.Fatal(err)
		}
	}

	createSession("fenced")
	if acquired, err := storeA.AcquireSessionLease(context.Background(), o, "fenced", "replica-a", 40*time.Millisecond); err != nil || !acquired {
		t.Fatalf("A acquire=%v err=%v", acquired, err)
	}
	if fenced, err := storeA.FenceEnvironmentTimeout(context.Background(), o, "fenced", "replica-a", "executor connection timed out"); err != nil || !fenced {
		t.Fatalf("A fence=%v err=%v", fenced, err)
	}
	time.Sleep(60 * time.Millisecond)
	if acquired, err := storeB.AcquireSessionLease(context.Background(), o, "fenced", "replica-b", time.Second); err != nil || acquired {
		t.Fatalf("B acquired terminal environment=%v err=%v", acquired, err)
	}
	if err := storeB.UpdateSessionState(context.Background(), o, "fenced", "idle", "ready", ""); err != nil {
		t.Fatal(err)
	}
	fencedSession, err := storeB.GetSession(context.Background(), o, "fenced")
	if err != nil || fencedSession.Status != "failed" || fencedSession.Readiness != "failed" || fencedSession.EnvironmentCleanupStatus != "timeout_terminal" {
		t.Fatalf("monotonic fence session=%+v err=%v", fencedSession, err)
	}
	if claimed, err := storeB.ClaimInput(context.Background(), o, "fenced", "late-input", "fingerprint"); err != nil || claimed {
		t.Fatalf("terminal input claim=%v err=%v", claimed, err)
	}
	if err := storeB.MarkCleanup(context.Background(), o, "fenced", "deleting", ""); err != nil {
		t.Fatal(err)
	}
	fencedSession, err = storeB.GetSession(context.Background(), o, "fenced")
	if err != nil || fencedSession.LastError != "executor connection timed out" {
		t.Fatalf("cleanup erased timeout diagnosis=%+v err=%v", fencedSession, err)
	}

	// Acquire must evaluate eligibility after a concurrently committing fence,
	// rather than using a statement snapshot taken before waiting for ownership.
	createSession("concurrent")
	lockTx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lockTx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "concurrent"); err != nil {
		t.Fatal(err)
	}
	acquiredAfterFence := make(chan bool, 1)
	acquireErr := make(chan error, 1)
	go func() {
		acquired, acquireError := storeB.AcquireSessionLease(context.Background(), o, "concurrent", "replica-b", time.Second)
		acquiredAfterFence <- acquired
		acquireErr <- acquireError
	}()
	select {
	case <-acquiredAfterFence:
		t.Fatal("acquire did not serialize behind the environment fence")
	case <-time.After(30 * time.Millisecond):
	}
	if _, err = lockTx.Exec(`UPDATE openai_agent_sessions SET environment_cleanup_status='timeout_terminal',status='failed',readiness='failed' WHERE id=$1`, "concurrent"); err != nil {
		t.Fatal(err)
	}
	if err = lockTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if acquired := <-acquiredAfterFence; acquired {
		t.Fatal("acquire slipped through committed environment fence")
	}
	if err = <-acquireErr; err != nil {
		t.Fatal(err)
	}

	createSession("lost")
	if acquired, err := storeA.AcquireSessionLease(context.Background(), o, "lost", "replica-a", 30*time.Millisecond); err != nil || !acquired {
		t.Fatalf("A acquire lost=%v err=%v", acquired, err)
	}
	time.Sleep(50 * time.Millisecond)
	if acquired, err := storeB.AcquireSessionLease(context.Background(), o, "lost", "replica-b", time.Second); err != nil || !acquired {
		t.Fatalf("B takeover=%v err=%v", acquired, err)
	}
	if fenced, err := storeA.FenceEnvironmentTimeout(context.Background(), o, "lost", "replica-a", "late timeout"); err != nil || fenced {
		t.Fatalf("stale A fence=%v err=%v", fenced, err)
	}
	lostSession, err := storeB.GetSession(context.Background(), o, "lost")
	if err != nil || lostSession.Status != "active" || lostSession.EnvironmentCleanupStatus != "active" {
		t.Fatalf("stale fence mutated replacement=%+v err=%v", lostSession, err)
	}
	if renewed, err := storeB.RenewSessionLease(context.Background(), o, "lost", "replica-b", time.Second); err != nil || !renewed {
		t.Fatalf("B renew=%v err=%v", renewed, err)
	}
}

func TestPostgresUpdateSessionStatusPreservesReadinessAndOwnership(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	}
	db := openIsolatedHADB(t, dsn)
	for _, name := range []string{"../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql", "../../database/migrations/sql/postgres/openai_agent_session_leases_20260911170000/up.sql", "../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql"} {
		migration, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := NewPostgresStore(db)
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	if _, err := store.CreateConnection(context.Background(), o, Connection{ID: "connection", Name: "primary", Provider: Provider}, "ciphertext", "executor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginSessionCreate(context.Background(), o, Session{ID: "session", ConnectionID: "connection", Provider: Provider, Model: "gpt-6", Environment: EnvironmentSpec{Type: "self_hosted"}, IdempotencyKey: "intent", RequestFingerprint: "fingerprint"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteSessionCreate(context.Background(), o, "session", UpstreamSession{ID: "upstream", EnvironmentID: "environment", Status: "active", Readiness: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindEnvironment(context.Background(), o, "session", "sandbox", "connected"); err != nil {
		t.Fatal(err)
	}

	foreign := o
	foreign.InstanceID = "other-instance"
	if err := store.UpdateSessionStatus(context.Background(), foreign, "session", "idle", "foreign error"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign update error = %v, want ErrNotFound", err)
	}
	if err := store.UpdateSessionStatus(context.Background(), o, "session", "idle", ""); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(context.Background(), o, "session")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "idle" || got.Readiness != "connected" || got.LastError != "" {
		t.Fatalf("session = %+v, want idle status with connected readiness", got)
	}
}

func TestPostgresTwoReplicaRecoveryArtifactsAndDelete(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	}
	db := openIsolatedHADB(t, dsn)
	var err error
	for _, name := range []string{"../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql", "../../database/migrations/sql/postgres/openai_agent_session_leases_20260911170000/up.sql", "../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql"} {
		migration, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(migration)); e != nil {
			t.Fatal(e)
		}
	}
	store1, _ := NewPostgresStore(db)
	store2, _ := NewPostgresStore(db)
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	conn, err := store1.CreateConnection(context.Background(), o, Connection{ID: "connection", Name: "primary", Provider: Provider}, `encrypted:{"api_key":"app"}`, `encrypted:{"executor_key":"executor"}`)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store1.BeginSessionCreate(context.Background(), o, Session{ID: "session", ConnectionID: conn.ID, Provider: Provider, Model: "gpt-6", Environment: EnvironmentSpec{Type: "self_hosted"}, IdempotencyKey: "create", RequestFingerprint: "fingerprint"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store1.CompleteSessionCreate(context.Background(), o, "session", UpstreamSession{ID: "upstream", EnvironmentID: "environment", Status: "active", Readiness: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store1.BindEnvironment(context.Background(), o, "session", "sbx-openai", "pending"); err != nil {
		t.Fatal(err)
	}
	backend := &replicaExecutorBackend{owner: executorpkg.Ownership{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID}, instance: &sandbox.Instance{ID: "sbx-openai", Status: sandbox.StatusRunning}, files: map[string][]byte{executorpkg.WorkspaceDirectory + "/result.txt": []byte("result")}}
	m1, m2 := executorpkg.New(backend), executorpkg.New(backend)
	upstream := &countingRecoveryUpstream{}
	s1, _ := NewService(store1, testCipher{}, upstream, ExecutorAdapter{Service: m1, BackendName: "firecracker"}, nil, Config{LeaseDuration: 300 * time.Millisecond})
	s2, _ := NewService(store2, testCipher{}, upstream, ExecutorAdapter{Service: m2, BackendName: "firecracker"}, nil, Config{LeaseDuration: 300 * time.Millisecond})
	s1.SetRecoveryAuthorizer(adminRecoveryAuthorizer{})
	s2.SetRecoveryAuthorizer(adminRecoveryAuthorizer{})
	if err = s1.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s2.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	backend.mu.Lock()
	starts := backend.executorStarts
	backend.mu.Unlock()
	if starts != 1 {
		t.Fatalf("executor starts=%d, want 1", starts)
	}
	bridge := &replicaArtifactBridge{input: []byte("input")}
	if _, err = m2.AttachInputReference(context.Background(), backend.owner, "session", "sbx-openai", "input-object", "inputs/a.txt", bridge); err != nil {
		t.Fatal(err)
	}
	if _, err = m2.ExportWorkspaceFileReference(context.Background(), backend.owner, "session", "sbx-openai", "result.txt", bridge); err != nil {
		t.Fatal(err)
	}
	if string(bridge.exported) != "result" {
		t.Fatalf("export=%q", bridge.exported)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s1.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err = s2.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	backend.mu.Lock()
	starts = backend.executorStarts
	backend.mu.Unlock()
	if starts != 2 {
		t.Fatalf("takeover executor starts=%d, want 2 total", starts)
	}
	if err = s2.DeleteSession(context.Background(), o, "session"); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	destroys := backend.destroys
	backend.mu.Unlock()
	if destroys != 1 {
		t.Fatalf("destroys=%d, want 1", destroys)
	}
	_ = s2.Close(closeCtx)
}

// This fixture needs CREATE DATABASE on a disposable PostgreSQL server. The
// middleware uses qualified identity tables, so a search_path-only schema does
// not isolate it. Never truncate shared identity tables for a test.
func TestDeviceBearerRejectsForeignHostInstanceAndRevocation(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	}
	admin, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "openai_auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sqlx.Connect("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE SCHEMA everstack;
 CREATE TABLE everstack.organization_members(user_id text,organization_id uuid,role text);
 CREATE TABLE everstack.tenant_config(instance_id text,organization_id uuid,status text);
 INSERT INTO everstack.organization_members VALUES
 ('user-a','11111111-1111-1111-1111-111111111111','admin'),
 ('user-b','22222222-2222-2222-2222-222222222222','admin');
 INSERT INTO everstack.tenant_config VALUES
 ('instance-a','11111111-1111-1111-1111-111111111111','active'),
 ('instance-a2','11111111-1111-1111-1111-111111111111','active'),
 ('instance-b','22222222-2222-2222-2222-222222222222','active')`); err != nil {
		t.Fatal(err)
	}
	tokens, err := deviceauth.NewTokenManager([]byte("01234567890123456789012345678901"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	orgA, orgB := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	issue := func(user, org, instance string) string {
		token, err := tokens.Issue(deviceauth.Identity{UserID: user, OrganizationID: org, InstanceID: instance, ClientID: "cli"})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	tokenA, tokenB, orgToken := issue("user-a", orgA, "instance-a"), issue("user-b", orgB, "instance-b"), issue("user-a", orgA, "")
	reached := ""
	handler := WithDeviceTokenAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, err := ownershipFromContext(r)
		if err != nil {
			writeError(w, err)
			return
		}
		if err = authorize(owner, "session:write"); err != nil {
			writeError(w, err)
			return
		}
		reached = owner.PrincipalID
		w.WriteHeader(http.StatusNoContent)
	}), tokens, db)
	check := func(label, token, org, instance string, want int, user string) {
		t.Helper()
		reached = ""
		req := httptest.NewRequest(http.MethodPost, "http://fixture.test/v1/openai-agents/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if instance != "" {
			req = req.WithContext(contextkeys.WithRequestInstanceScope(req.Context(), contextkeys.RequestInstanceScope{OrganizationID: org, InstanceID: instance}))
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != want || reached != user {
			t.Fatalf("%s: status=%d user=%q want=%d/%q", label, rr.Code, reached, want, user)
		}
	}
	check("A on A", tokenA, orgA, "instance-a", http.StatusNoContent, "user-a")
	check("B on B", tokenB, orgB, "instance-b", http.StatusNoContent, "user-b")
	check("foreign organization", tokenA, orgB, "instance-b", http.StatusForbidden, "")
	check("foreign instance in same organization", tokenA, orgA, "instance-a2", http.StatusForbidden, "")
	check("unscoped organization token", orgToken, "", "", http.StatusForbidden, "")
	if _, err = db.Exec(`UPDATE everstack.organization_members SET role='viewer' WHERE user_id='user-a'`); err != nil {
		t.Fatal(err)
	}
	check("same unexpired token after downgrade", tokenA, orgA, "instance-a", http.StatusForbidden, "")
	if _, err = db.Exec(`DELETE FROM everstack.organization_members WHERE user_id='user-a'`); err != nil {
		t.Fatal(err)
	}
	check("same unexpired token after membership removal", tokenA, orgA, "instance-a", http.StatusForbidden, "")
	if _, err = db.Exec(`UPDATE everstack.tenant_config SET status='deleted' WHERE instance_id='instance-b'`); err != nil {
		t.Fatal(err)
	}
	check("same unexpired token after instance removal", tokenB, orgB, "instance-b", http.StatusForbidden, "")
}
