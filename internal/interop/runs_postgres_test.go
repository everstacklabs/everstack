package interop

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	a2a "github.com/everstacklabs/everstack/internal/a2a/client"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

func testRunStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("EVS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_TEST_POSTGRES_DSN for isolated PostgreSQL integration tests")
	}
	base, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "a2a_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(`CREATE SCHEMA ` + schema); err != nil {
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
	for _, migration := range []string{"interop_tables_20260615001907", externalRunsMigration(t)} {
		raw, err := os.ReadFile(filepath.Join("../database/migrations/sql/postgres", migration, "up.sql"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EVS_INTEROP_CREDENTIAL_MASTER_KEY", strings.Repeat("test-only-root-", 4))
	t.Setenv("EVS_INTEROP_CREDENTIAL_PREVIOUS_KEYS", "")
	t.Setenv("EVS_INTEROP_CREDENTIAL_KEY_ID", "v1")
	return NewStore(db)
}

type runClient struct {
	sends       atomic.Int32
	gets        atomic.Int32
	cancelState string
	failSend    bool
	sendErr     error
}

func (c *runClient) Send(context.Context, a2a.Connection, string, string) (a2a.Result, error) {
	c.sends.Add(1)
	if c.sendErr != nil {
		return a2a.Result{}, c.sendErr
	}
	if c.failSend {
		return a2a.Result{}, errors.New("timeout")
	}
	return a2a.Result{TaskID: "remote-task", ContextID: "remote-context", State: "working", Text: "partial"}, nil
}
func (c *runClient) Get(context.Context, a2a.Connection, string) (a2a.Result, error) {
	c.gets.Add(1)
	return a2a.Result{TaskID: "remote-task", ContextID: "remote-context", State: "completed", Text: "done"}, nil
}
func (c *runClient) Cancel(context.Context, a2a.Connection, string) (a2a.Result, error) {
	return a2a.Result{TaskID: "remote-task", ContextID: "remote-context", State: c.cancelState}, nil
}

func TestPostgresRunIdempotencyAndRestart(t *testing.T) {
	store := testRunStore(t)
	ctx := context.Background()
	if err := store.SaveRemote(ctx, "tenant-a", "bot", "https://agent.example/a2a", a2a.V1, "", nil); err != nil {
		t.Fatal(err)
	}
	client := &runClient{}
	service := &RunService{Repository: store, Client: client}
	var wg sync.WaitGroup
	results := make(chan ExternalRun, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := service.Start(ctx, "tenant-a", "bot", "do work", "same-key-123")
			if err != nil {
				t.Error(err)
			}
			results <- run
		}()
	}
	wg.Wait()
	close(results)
	id := ""
	for run := range results {
		if id == "" {
			id = run.ID
		}
		if run.ID != id {
			t.Error("duplicate run")
		}
	}
	if client.sends.Load() != 1 {
		t.Fatalf("duplicate network dispatch: %d", client.sends.Load())
	}
	if _, err := service.Start(ctx, "tenant-a", "bot", "different work", "same-key-123"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	// A new service/store has no in-memory state from the original process.
	restarted := &RunService{Repository: NewStore(store.db), Client: client}
	run, err := restarted.Refresh(ctx, "tenant-a", id, false)
	if err != nil || run.State != "completed" || run.Output != "done" {
		t.Fatalf("restart lost task: %+v %v", run, err)
	}
	if client.sends.Load() != 1 || client.gets.Load() != 1 {
		t.Fatal("restart resent work")
	}
	if _, err := restarted.Refresh(ctx, "tenant-b", id, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant leak: %v", err)
	}
	if client.gets.Load() != 1 {
		t.Fatal("foreign run made network call")
	}
	if _, err := service.Start(ctx, "tenant-b", "bot", "do work", "same-key-123"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign remote: %v", err)
	}
	// Terminal runs are not queried or canceled again.
	run, err = restarted.Refresh(ctx, "tenant-a", id, true)
	if err != nil || run.State != "completed" {
		t.Fatal(err)
	}
	if client.gets.Load() != 1 {
		t.Fatal("queried terminal run")
	}
}

func TestPostgresUnknownOutcomeAndFencing(t *testing.T) {
	store := testRunStore(t)
	ctx := context.Background()
	if err := store.SaveRemote(ctx, "t", "bot", "https://agent.example", a2a.Legacy, "", nil); err != nil {
		t.Fatal(err)
	}
	client := &runClient{failSend: true}
	service := &RunService{Repository: store, Client: client}
	run, err := service.Start(ctx, "t", "bot", "do work", "unknown-key")
	if err != nil || run.State != "unknown" {
		t.Fatalf("%+v %v", run, err)
	}
	duplicate, err := service.Start(ctx, "t", "bot", "do work", "unknown-key")
	if err != nil || duplicate.ID != run.ID || client.sends.Load() != 1 {
		t.Fatal("unknown send retried")
	}
	if _, err := service.Refresh(ctx, "t", run.ID, false); !errors.Is(err, ErrNoTask) {
		t.Fatalf("invented task handle: %v", err)
	}
	// Simulate death after request dispatch but before the outcome was saved.
	_, err = store.db.Exec(`UPDATE a2a_external_runs SET state='dispatching',lease_token='dead-worker',lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err = service.Read(ctx, "t", run.ID)
	if err != nil || run.State != "unknown" || run.LastError != "dispatch_interrupted" {
		t.Fatalf("crash not marked unknown: %+v %v", run, err)
	}
	duplicate, err = service.Start(ctx, "t", "bot", "do work", "unknown-key")
	if err != nil || duplicate.ID != run.ID || client.sends.Load() != 1 {
		t.Fatal("crashed dispatch resent")
	}
	client.failSend = false
	run, err = service.Start(ctx, "t", "bot", "next", "lease-key-123")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := store.ClaimRun(ctx, "t", run.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimRun(ctx, "t", run.ID, true); !errors.Is(err, ErrBusy) {
		t.Fatal("two workers acquired same run")
	}
	if _, err = store.db.Exec(`UPDATE a2a_external_runs SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	current, err := store.ClaimRun(ctx, "t", run.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	stale.State = "completed"
	if _, err := store.FinishRun(ctx, stale); !errors.Is(err, ErrBusy) {
		t.Fatal("stale worker overwrote current owner")
	}
	current.State = "working"
	if _, err := store.FinishRun(ctx, current); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCancellationAndConnectionRevocation(t *testing.T) {
	store := testRunStore(t)
	ctx := context.Background()
	_ = store.SaveRemote(ctx, "t", "bot", "https://agent.example", a2a.V1, "", nil)
	client := &runClient{cancelState: "working"}
	service := &RunService{Repository: store, Client: client}
	run, err := service.Start(ctx, "t", "bot", "hello", "cancel-key")
	if err != nil {
		t.Fatal(err)
	}
	run, err = service.Refresh(ctx, "t", run.ID, true)
	if err != nil || run.State != "working" || run.CancelRequestedAt == nil || run.Output != "partial" {
		t.Fatalf("cancel falsely confirmed/lost partial: %+v %v", run, err)
	}
	_ = store.SaveRemote(ctx, "t", "bot", "https://changed.example", a2a.V1, "", nil)
	if _, err := service.Refresh(ctx, "t", run.ID, false); !errors.Is(err, ErrRemoteChanged) {
		t.Fatalf("rebound old run: %v", err)
	}
	if err := store.DeleteRemoteAgent(ctx, "t", run.RemoteID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(ctx, "t", run.ID); err != nil {
		t.Fatal("remote deletion erased run")
	}
	if _, err := service.Refresh(ctx, "t", run.ID, true); !errors.Is(err, ErrRemoteChanged) {
		t.Fatal("deleted remote still callable")
	}
}

func TestPostgresCredentialIsolationAndMigration(t *testing.T) {
	store := testRunStore(t)
	ctx := context.Background()
	token := "private-bearer"
	if err := store.SaveRemote(ctx, "a", "bot", "https://agent.example", a2a.V1, "", &token); err != nil {
		t.Fatal(err)
	}
	var plain *string
	var ciphertext []byte
	if err := store.db.QueryRow(`SELECT auth_token,auth_ciphertext FROM a2a_remote_agents WHERE tenant_id='a'`).Scan(&plain, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if plain != nil || len(ciphertext) == 0 || strings.Contains(string(ciphertext), token) {
		t.Fatal("credential stored in plaintext")
	}
	list, err := store.ListRemoteAgents(ctx, "a")
	if err != nil || len(list) != 1 || !list[0].HasCredential || list[0].AuthToken != "" {
		t.Fatal("listing leaked/lost credential")
	}
	remote, err := store.GetRemoteAgentByName(ctx, "a", "bot")
	if err != nil || remote.AuthToken != token {
		t.Fatal("credential resolution failed")
	}
	if err = store.SaveRemote(ctx, "a", "bot", "https://changed.example", a2a.V1, "", nil); !errors.Is(err, ErrCredentialReplacement) {
		t.Fatalf("silent credential rebound: %v", err)
	}
	// Re-saving without a token preserves the encrypted credential.
	if err = store.SaveRemote(ctx, "a", "bot", "https://agent.example", a2a.V1, "", nil); err != nil {
		t.Fatal(err)
	}
	_ = store.SaveRemote(ctx, "b", "bot", "https://agent.example", a2a.V1, "", nil)
	_, err = store.db.Exec(`UPDATE a2a_remote_agents SET auth_ciphertext=$1,auth_key_id='v1' WHERE tenant_id='b'`, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRemoteAgentByName(ctx, "b", "bot"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatal("cross-tenant ciphertext resolved")
	}
	// Existing plaintext can be read and atomically migrated once configured.
	_, err = store.db.Exec(`INSERT INTO a2a_remote_agents(tenant_id,name,endpoint,auth_token) VALUES('a','legacy','https://legacy.example','legacy-secret')`)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := store.GetRemoteAgentByName(ctx, "a", "legacy")
	if err != nil || legacy.AuthToken != "legacy-secret" {
		t.Fatal(err)
	}
	var migrated bool
	_ = store.db.Get(&migrated, `SELECT auth_token IS NULL AND auth_ciphertext IS NOT NULL FROM a2a_remote_agents WHERE name='legacy'`)
	if !migrated {
		t.Fatal("legacy credential not migrated")
	}
	t.Setenv("EVS_INTEROP_CREDENTIAL_MASTER_KEY", "")
	unavailable := NewStore(store.db)
	if _, err := unavailable.GetRemoteAgentByName(ctx, "a", "bot"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatal("missing key did not fail closed")
	}
	if err := unavailable.SaveRemote(ctx, "a", "new", "https://agent.example", a2a.V1, "", &token); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatal("plaintext fallback on new credential")
	}
	// Downgrade must fail before discarding encrypted secrets or run history.
	raw, _ := os.ReadFile("../database/migrations/sql/postgres/" + externalRunsMigration(t) + "/down.sql")
	if _, err := store.db.Exec(string(raw)); err == nil {
		t.Fatal("destructive credential downgrade allowed")
	}
}

func TestPostgresRunHTTPUsesTenantAndIdempotency(t *testing.T) {
	store := testRunStore(t)
	ctx := context.Background()
	_ = store.SaveRemote(ctx, "tenant-A", "bot", "https://agent.example", a2a.V1, "", nil)
	client := &runClient{}
	h := NewHandlers(store, false, false)
	h.runs = &RunService{Repository: store, Client: client}
	req := httptest.NewRequest(http.MethodPost, "/api/interop/runs", strings.NewReader(`{"remote":"bot","message":"hello","idempotency_key":"http-key-123","tenant_id":"another-tenant"}`))
	req = req.WithContext(withTestTenant(req))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router := mux.NewRouter()
	h.Mount(muxAdapter{router})
	router.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"working"`) {
		t.Fatalf("HTTP start: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "endpoint") || strings.Contains(w.Body.String(), "tenant_id") {
		t.Fatal("internal run data exposed")
	}
	foreign := httptest.NewRequest(http.MethodPost, "/api/interop/runs", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, foreign)
	if w.Code != 401 {
		t.Fatal("unauthenticated run accepted")
	}
}

// externalRunsMigration finds the migration folder by prefix so a timestamp
// rename cannot silently break these tests.
func externalRunsMigration(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("../database/migrations/sql/postgres/external_agent_runs_*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one external_agent_runs migration, got %v (%v)", matches, err)
	}
	return filepath.Base(matches[0])
}

// A definite refusal (the remote answered, but declined before doing work)
// is terminal; anything ambiguous stays unknown and is never resent.
func TestPostgresDefiniteRejectionIsTerminal(t *testing.T) {
	store := testRunStore(t)
	ctx := context.Background()
	if err := store.SaveRemote(ctx, "t", "bot", "https://agent.example", a2a.Legacy, "", nil); err != nil {
		t.Fatal(err)
	}
	for i, c := range []struct{ code, state string }{
		{"rpc_-32601", "rejected"},
		{"http_401", "rejected"},
		{"rpc_-32603", "unknown"},
		{"rpc_-32006", "unknown"},
		{"http_503", "unknown"},
		{"transport_error", "unknown"},
	} {
		client := &runClient{sendErr: &a2a.Error{Code: c.code}}
		service := &RunService{Repository: store, Client: client}
		run, err := service.Start(ctx, "t", "bot", "do work", "reject-key-"+string(rune('a'+i)))
		if err != nil || run.State != c.state || run.LastError != c.code {
			t.Errorf("%s: got state=%q last_error=%q err=%v, want %q", c.code, run.State, run.LastError, err, c.state)
		}
	}
}
