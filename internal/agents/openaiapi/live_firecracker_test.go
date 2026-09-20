package openaiapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	executorpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/executor"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/fcagent"
	"github.com/everstacklabs/everstack/internal/services/encryption"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// TestLiveOpenAIAgentFirecrackerJourney is paid and explicitly opt-in. It
// exercises the real Firecracker Agent backend, SandboxManager, Codex executor,
// OpenAI service adapter and durable session store. Credentials are consumed
// only from the test process environment and never included in test output.
func TestLiveOpenAIAgentFirecrackerJourney(t *testing.T) {
	if os.Getenv("EVS_TEST_OPENAI_AGENTS_LIVE_FIRECRACKER") != "1" {
		t.Skip("explicit live Firecracker qualification opt-in required")
	}
	serviceTarget := strings.TrimSpace(os.Getenv("EVS_TEST_OPENAI_AGENTS_FIRECRACKER_SERVICE"))
	image := strings.TrimSpace(os.Getenv("EVS_TEST_OPENAI_AGENTS_FIRECRACKER_IMAGE"))
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if serviceTarget == "" || image == "" || dsn == "" || os.Getenv("OPENAI_API_KEY") == "" || os.Getenv("EVS_TEST_OPENAI_EXECUTOR_KEY") == "" {
		t.Skip("Firecracker service, trusted image, disposable PostgreSQL DSN and both process credentials are required")
	}
	port := 9090
	if raw := strings.TrimSpace(os.Getenv("EVS_TEST_OPENAI_AGENTS_FIRECRACKER_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			t.Fatal("invalid Firecracker Agent port")
		}
		port = parsed
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	db, err := sqlx.ConnectContext(ctx, "pgx", dsn)
	if err != nil {
		t.Fatal("connect disposable database failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := "openai_live_fc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("create isolated schema failed")
	}
	defer db.Exec("DROP SCHEMA " + schema + " CASCADE")
	if _, err = db.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal("select isolated schema failed")
	}
	for _, migrationPath := range []string{
		"../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql",
		"../../database/migrations/sql/postgres/openai_agent_session_leases_20260911170000/up.sql",
		"../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql",
	} {
		migration, readErr := os.ReadFile(migrationPath)
		if readErr != nil {
			t.Fatalf("read OpenAI Agents migration %s failed", filepath.Base(filepath.Dir(migrationPath)))
		}
		if _, err = db.ExecContext(ctx, string(migration)); err != nil {
			t.Fatalf("apply OpenAI Agents migration %s failed: %s", filepath.Base(filepath.Dir(migrationPath)), sanitizeError(err.Error()))
		}
	}

	backend, err := fcagent.New(fcagent.Config{
		Service: serviceTarget, Port: port, RefreshInterval: 2 * time.Second, DialTimeout: 10 * time.Second,
		TLS: fcagent.TLSConfig{
			ClientCert: os.Getenv("EVS_TEST_OPENAI_AGENTS_FIRECRACKER_TLS_CLIENT_CERT"),
			ClientKey:  os.Getenv("EVS_TEST_OPENAI_AGENTS_FIRECRACKER_TLS_CLIENT_KEY"),
			ServerCA:   os.Getenv("EVS_TEST_OPENAI_AGENTS_FIRECRACKER_TLS_SERVER_CA"),
			ServerName: os.Getenv("EVS_TEST_OPENAI_AGENTS_FIRECRACKER_TLS_SERVER_NAME"),
		},
	})
	if err != nil {
		t.Fatal("construct real Firecracker Agent backend failed")
	}
	global := sandbox.DefaultGlobalSandboxConfig()
	global.Enabled = true
	global.Backend = "firecracker-agent"
	global.DefaultImage = image
	global.AllowedImages = []string{image}
	sandboxManager := sandbox.NewManager(backend, global)
	managerBackend := executorpkg.NewManagerBackend(sandboxManager)
	executorManager := executorpkg.New(managerBackend)
	environmentAdapter := ExecutorAdapter{Service: executorManager, BackendName: "firecracker-agent"}
	if err = environmentAdapter.ValidateConfiguration(ctx); err != nil {
		t.Fatal("Firecracker executor configuration rejected")
	}

	store, _ := NewPostgresStore(db)
	cipher, _ := encryption.NewService("isolated-live-firecracker-encryption-key")
	service, err := NewService(store, cipher, UpstreamAdapter{Timeout: 2 * time.Minute}, environmentAdapter, nil, Config{EnvironmentImage: image, ExecutorVersion: executorpkg.ExecutorVersion})
	if err != nil {
		t.Fatal("construct live Firecracker service failed")
	}
	service.SetRecoveryAuthorizer(liveTestRecoveryAuthorizer{})
	defer func() {
		closeCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if closeErr := service.Close(closeCtx); closeErr != nil {
			t.Error("supervisor shutdown failed")
		}
	}()

	owner := Ownership{OrganizationID: uuid.NewString(), InstanceID: uuid.NewString(), PrincipalID: "live-firecracker-user", Role: "admin"}
	connection, err := service.CreateConnection(ctx, owner, CreateConnectionRequest{Name: "Firecracker qualification", APIKey: os.Getenv("OPENAI_API_KEY"), ExecutorKey: os.Getenv("EVS_TEST_OPENAI_EXECUTOR_KEY")})
	if err != nil {
		t.Fatal("validate live OpenAI connection failed")
	}
	defer service.DeleteConnection(context.Background(), owner, connection.ID)
	model := strings.TrimSpace(os.Getenv("EVS_TEST_OPENAI_AGENTS_MODEL"))
	if model == "" {
		model = "gpt-6-astra"
	}
	const first = "EVERSTACK_FIRECRACKER_INITIAL_91D7"
	createKey := uuid.NewString()
	var session *Session
	defer func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		sessionID := ""
		if session != nil {
			sessionID = session.ID
		} else {
			_ = db.GetContext(cleanupCtx, &sessionID, `SELECT id FROM openai_agent_sessions WHERE organization_id=$1 AND instance_id=$2 AND idempotency_key=$3 LIMIT 1`, owner.OrganizationID, owner.InstanceID, createKey)
		}
		if sessionID != "" {
			cleanupResult := make(chan error, 1)
			go func() { cleanupResult <- service.DeleteSession(cleanupCtx, owner, sessionID) }()
			select {
			case cleanupErr := <-cleanupResult:
				if cleanupErr != nil {
					t.Errorf("live Firecracker session cleanup failed: %s", sanitizeError(cleanupErr.Error()))
				}
			case <-cleanupCtx.Done():
				t.Errorf("live Firecracker session cleanup exceeded 30 seconds")
			}
		}
	}()
	session, err = service.CreateSession(ctx, owner, CreateSessionRequest{
		ConnectionID: connection.ID, IdempotencyKey: createKey, Model: model,
		Instructions: "Use the provided environment shell for every requested command. Perform the command exactly, then reply briefly.",
		Environment:  EnvironmentSpec{Type: "self_hosted", WorkspaceDirectory: executorpkg.WorkspaceDirectory},
		Input:        json.RawMessage(`"Write exactly EVERSTACK_FIRECRACKER_INITIAL_91D7 followed by a newline to /workspace/qualification.txt using a shell command."`),
	})
	if err != nil {
		t.Fatalf("create live Firecracker session failed: %s", sanitizeError(err.Error()))
	}
	if session.SandboxID == "" || session.UpstreamEnvironmentID == "" {
		t.Fatal("session did not retain sandbox and upstream environment bindings")
	}
	executorOwner := executorpkg.Ownership{OrganizationID: owner.OrganizationID, InstanceID: owner.InstanceID}
	waitForFirecracker(t, ctx, "connected environment event", func() (bool, error) {
		current, getErr := store.GetSession(ctx, owner, session.ID)
		return current != nil && current.Readiness == "connected", getErr
	})
	waitForFirecracker(t, ctx, "initial agent workspace write", func() (bool, error) {
		data, readErr := executorManager.ReadWorkspaceFile(ctx, executorOwner, session.ID, "qualification.txt")
		return readErr == nil && string(data) == first+"\n", nil
	})

	foreign := executorpkg.Ownership{OrganizationID: owner.OrganizationID, InstanceID: uuid.NewString()}
	logGuest := func(stage string) {
		r, e := managerBackend.Exec(ctx, executorOwner, session.SandboxID, executorpkg.Command{Argv: []string{"sh", "-c", "sha256sum /usr/local/bin/sandbox-agent; ps -eo pid,comm | grep -E 'codex|node'"}, Timeout: 5 * time.Second, SilentLog: true})
		if e == nil && r != nil {
			t.Logf("%s guest process inventory: %s", stage, r.Stdout)
		}
	}
	logGuest("before reconcile")
	if _, err = executorManager.ReadWorkspaceFile(ctx, foreign, session.ID, "qualification.txt"); err == nil {
		t.Fatal("foreign instance read Firecracker workspace")
	}
	if _, err = managerBackend.Exec(ctx, foreign, session.SandboxID, executorpkg.Command{Argv: []string{"true"}}); err == nil {
		t.Fatal("foreign instance executed in Firecracker sandbox")
	}

	_, secret, err := service.sessionSecret(ctx, owner, session.ID)
	if err != nil {
		t.Fatal("resolve application credential for restart failed")
	}
	remote, err := service.upstream.GetSession(ctx, secret, session.UpstreamSessionID)
	if err != nil || remote == nil || remote.RemoteURL == "" {
		t.Fatal("recover exact upstream environment endpoint failed")
	}
	beforeEvents, eventsErr := store.ListEvents(ctx, owner, session.ID, 0, 500)
	if eventsErr != nil {
		t.Fatalf("capture pre-reconcile event barrier failed: %s", sanitizeError(eventsErr.Error()))
	}
	var eventBarrier int64
	connectedBefore := 0
	for _, event := range beforeEvents {
		if event.Cursor > eventBarrier {
			eventBarrier = event.Cursor
		}
		if strings.Contains(strings.ToLower(event.Type), "environment") {
			t.Logf("pre-reconcile environment event cursor=%d type=%s event_id_fingerprint=%s observed_at=%s", event.Cursor, event.Type, safeLiveFingerprint(event.EventID), event.CreatedAt.UTC().Format(time.RFC3339Nano))
			if strings.EqualFold(event.Type, "agent.session.environment.connected") {
				connectedBefore++
			}
		}
	}
	t.Logf("pre-relinquish barrier cursor=%d prior_connected_events=%d upstream_environment=%s remote_url_fingerprint=%s", eventBarrier, connectedBefore, remote.EnvironmentID, safeLiveFingerprint(remote.RemoteURL))
	if err = executorManager.Relinquish(ctx, executorOwner, session.ID); err != nil {
		t.Fatalf("relinquish executor before failover failed: %s", sanitizeError(err.Error()))
	}
	waitForFirecracker(t, ctx, "documented upstream executor disconnect event", func() (bool, error) {
		events, listErr := store.ListEvents(ctx, owner, session.ID, eventBarrier, 500)
		for _, event := range events {
			if event.Cursor > eventBarrier && (strings.EqualFold(event.Type, "agent.session.environment.disconnected") || strings.EqualFold(event.Type, "agent.session.environment.pending")) {
				eventBarrier = event.Cursor
				t.Logf("disconnect barrier cursor=%d type=%s event_id_fingerprint=%s observed_at=%s", event.Cursor, event.Type, safeLiveFingerprint(event.EventID), event.CreatedAt.UTC().Format(time.RFC3339Nano))
				return true, nil
			}
		}
		return false, listErr
	})
	reconnected, err := executorManager.Reconcile(ctx, executorpkg.ReconcileRequest{
		Ownership: executorOwner, RemoteURL: remote.RemoteURL, ExecutorAPIKey: os.Getenv("EVS_TEST_OPENAI_EXECUTOR_KEY"),
		Environment: executorpkg.Environment{SandboxID: session.SandboxID, LocalSessionID: session.ID, UpstreamEnvironmentID: session.UpstreamEnvironmentID, Status: executorpkg.StatusConnected},
	})
	if err != nil || reconnected.SandboxID != session.SandboxID || reconnected.Status != executorpkg.StatusPending {
		t.Fatal("executor restart did not reconcile the exact sandbox binding")
	}
	time.Sleep(5 * time.Second)
	logGuest("after reconcile")
	localAfter, localErr := executorManager.Status(ctx, executorOwner, session.ID)
	remoteAfter, remoteErr := service.upstream.GetSession(ctx, secret, session.UpstreamSessionID)
	if localErr != nil {
		t.Logf("post-reconcile local readiness error=%s", sanitizeError(localErr.Error()))
	} else {
		t.Logf("post-reconcile local readiness=%s", localAfter.Status)
	}
	if remoteErr != nil {
		t.Logf("post-reconcile upstream snapshot error=%s", sanitizeError(remoteErr.Error()))
	} else if remoteAfter != nil {
		t.Logf("post-reconcile upstream environment=%s status=%s remote_url_fingerprint=%s", remoteAfter.EnvironmentID, remoteAfter.Readiness, safeLiveFingerprint(remoteAfter.RemoteURL))
	}
	if events, e := store.ListEvents(ctx, owner, session.ID, eventBarrier, 500); e == nil {
		for _, event := range events {
			if strings.Contains(strings.ToLower(event.Type), "environment") {
				t.Logf("post-reconcile environment event cursor=%d type=%s event_id_fingerprint=%s observed_at=%s", event.Cursor, event.Type, safeLiveFingerprint(event.EventID), event.CreatedAt.UTC().Format(time.RFC3339Nano))
			}
		}
	} else {
		t.Logf("post-reconcile event diagnostics unavailable: %s", sanitizeError(e.Error()))
	}
	const second = "EVERSTACK_FIRECRACKER_FOLLOWUP_4A63"
	if err = service.SendInput(ctx, owner, session.ID, InputRequest{IdempotencyKey: uuid.NewString(), Input: json.RawMessage(`"Append exactly EVERSTACK_FIRECRACKER_FOLLOWUP_4A63 followed by a newline to /workspace/qualification.txt using the environment shell."`)}); err != nil {
		t.Fatal("send Firecracker follow-up failed")
	}
	waitForFirecracker(t, ctx, "post-reconnect follow-up write", func() (bool, error) {
		data, readErr := executorManager.ReadWorkspaceFile(ctx, executorOwner, session.ID, "qualification.txt")
		return readErr == nil && string(data) == first+"\n"+second+"\n", nil
	})
	waitForFirecracker(t, ctx, "executor reconnect event", func() (bool, error) {
		current, statusErr := executorManager.Status(ctx, executorOwner, session.ID)
		return current != nil && current.Status == executorpkg.StatusConnected, statusErr
	})
	if events, e := store.ListEvents(ctx, owner, session.ID, eventBarrier, 500); e == nil {
		for _, event := range events {
			if strings.Contains(strings.ToLower(event.Type), "environment") {
				t.Logf("verified reconnect environment event cursor=%d type=%s event_id_fingerprint=%s observed_at=%s", event.Cursor, event.Type, safeLiveFingerprint(event.EventID), event.CreatedAt.UTC().Format(time.RFC3339Nano))
			}
		}
	}
	command, err := managerBackend.Exec(ctx, executorOwner, session.SandboxID, executorpkg.Command{Argv: []string{"sh", "-c", "test \"$(sed -n '1p' qualification.txt)\" = EVERSTACK_FIRECRACKER_INITIAL_91D7 && test \"$(sed -n '2p' qualification.txt)\" = EVERSTACK_FIRECRACKER_FOLLOWUP_4A63"}, WorkDir: executorpkg.WorkspaceDirectory, Timeout: 10 * time.Second, SilentLog: true})
	if err != nil || command == nil || command.ExitCode != 0 {
		t.Fatal("Firecracker workspace command verification failed")
	}

	bridge := &liveArtifactBridge{owner: executorOwner}
	artifact, err := executorManager.ExportWorkspaceFile(ctx, executorOwner, session.ID, "qualification.txt", bridge)
	if err != nil || artifact == nil || artifact.SizeBytes == 0 || artifact.SHA256 == "" {
		t.Fatal("export workspace artifact failed")
	}
	if !bridge.matches(artifact.ObjectID, []byte(first+"\n"+second+"\n")) {
		t.Fatal("exported artifact bytes or ownership were not retained")
	}
	if _, err = executorManager.ExportWorkspaceFile(ctx, foreign, session.ID, "qualification.txt", bridge); err == nil {
		t.Fatal("foreign instance exported Firecracker artifact")
	}
	t.Log("real Firecracker create/connect/write/command/follow-up/reconcile/export/scope/cleanup journey verified")
}

func safeLiveFingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

type liveArtifactBridge struct {
	mu      sync.Mutex
	owner   executorpkg.Ownership
	objects map[string][]byte
}

func (*liveArtifactBridge) ResolveInput(context.Context, executorpkg.InputRequest) (*executorpkg.InputFile, error) {
	return nil, errors.New("live artifact input lookup is unavailable")
}
func (b *liveArtifactBridge) Export(_ context.Context, r executorpkg.ExportRequest) (*executorpkg.Artifact, error) {
	if r.Ownership != b.owner {
		return nil, errors.New("artifact ownership mismatch")
	}
	sum := sha256.Sum256(r.Content)
	digest := hex.EncodeToString(sum[:])
	if digest != r.SHA256 {
		return nil, errors.New("artifact checksum mismatch")
	}
	id := uuid.NewString()
	b.mu.Lock()
	if b.objects == nil {
		b.objects = map[string][]byte{}
	}
	b.objects[id] = append([]byte(nil), r.Content...)
	b.mu.Unlock()
	return &executorpkg.Artifact{ObjectID: id, Filename: r.Filename, SizeBytes: int64(len(r.Content)), SHA256: digest}, nil
}
func (b *liveArtifactBridge) matches(id string, want []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.objects[id]) == string(want)
}

func waitForFirecracker(t *testing.T, ctx context.Context, operation string, check func() (bool, error)) {
	t.Helper()
	for {
		ok, err := check()
		if err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s failed", operation)
		}
		if ok {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", operation)
		case <-time.After(time.Second):
		}
	}
}
