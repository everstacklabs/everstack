package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type testCipher struct{}

func (testCipher) Encrypt(v string) (string, error) { return "encrypted:" + v, nil }
func (testCipher) Decrypt(v string) (string, error) { return v[len("encrypted:"):], nil }

type ambiguousUpstream struct{ creates, inputs int }
type ambiguous struct{}
type recoveryUpstream struct{ ambiguousUpstream }

func (*recoveryUpstream) GetSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	return &UpstreamSession{ID: "upstream-1", EnvironmentID: "env-1", RemoteURL: "https://api.openai.com/v1/agents/environments/env-1", Status: "idle"}, nil
}

type recoveryStore struct {
	*restartStore
	records []RecoveryRecord
}

func (s *recoveryStore) ListRecoverable(context.Context) ([]RecoveryRecord, error) {
	return s.records, nil
}

type recoveryEnvironment struct {
	got            EnvironmentReconcileRequest
	deletedSession string
}

func (*recoveryEnvironment) ValidateConfiguration(context.Context) error { return nil }

type allowRecovery struct{}

func (allowRecovery) AuthorizeRecovery(_ context.Context, o Ownership) (Ownership, error) {
	return o, nil
}

type viewerRecovery struct{}

func (viewerRecovery) AuthorizeRecovery(_ context.Context, o Ownership) (Ownership, error) {
	o.Role = "viewer"
	return o, nil
}

func (*recoveryEnvironment) Provision(context.Context, EnvironmentProvisionRequest) (*EnvironmentState, error) {
	return nil, ErrUnavailable
}
func (e *recoveryEnvironment) Reconcile(_ context.Context, r EnvironmentReconcileRequest) (*EnvironmentState, error) {
	e.got = r
	return &EnvironmentState{SandboxID: r.SandboxID, Status: "pending"}, nil
}
func (*recoveryEnvironment) Observe(context.Context, EnvironmentObserveRequest) (*EnvironmentState, error) {
	return nil, ErrUnavailable
}
func (*recoveryEnvironment) Relinquish(context.Context, Ownership, string) error { return nil }
func (e *recoveryEnvironment) DeleteReference(_ context.Context, _ Ownership, id, _ string) error {
	e.deletedSession = id
	return nil
}

func TestDeleteSessionUsesLocalExecutorBinding(t *testing.T) {
	owner := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &restartStore{}
	environments := &recoveryEnvironment{}
	service, err := NewService(store, testCipher{}, &ambiguousUpstream{}, environments, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.CreateConnection(context.Background(), owner, CreateConnectionRequest{Name: "test", APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	store.session = &Session{ID: "local-session", ConnectionID: connection.ID, SandboxID: "different-sandbox", Status: "completed"}
	if err := service.DeleteSession(context.Background(), owner, store.session.ID); err != nil {
		t.Fatal(err)
	}
	if environments.deletedSession != store.session.ID {
		t.Fatalf("deleted executor binding %q, want local session %q", environments.deletedSession, store.session.ID)
	}
}

type unsupportedEnvironment struct{}

func (unsupportedEnvironment) ValidateConfiguration(context.Context) error { return ErrUnavailable }
func (unsupportedEnvironment) Provision(context.Context, EnvironmentProvisionRequest) (*EnvironmentState, error) {
	panic("provision must not run")
}
func (unsupportedEnvironment) Reconcile(context.Context, EnvironmentReconcileRequest) (*EnvironmentState, error) {
	return nil, ErrUnavailable
}
func (unsupportedEnvironment) Observe(context.Context, EnvironmentObserveRequest) (*EnvironmentState, error) {
	return nil, ErrUnavailable
}
func (unsupportedEnvironment) Relinquish(context.Context, Ownership, string) error { return nil }
func (unsupportedEnvironment) DeleteReference(context.Context, Ownership, string, string) error {
	return nil
}

func (ambiguous) Error() string         { return "request outcome unknown for sk-secret" }
func (ambiguous) AmbiguousResult() bool { return true }
func (u *ambiguousUpstream) CreateSession(context.Context, ConnectionSecret, CreateSessionRequest) (*UpstreamSession, error) {
	u.creates++
	return nil, ambiguous{}
}
func (*ambiguousUpstream) ValidateConnection(context.Context, ConnectionSecret) error { return nil }
func (*ambiguousUpstream) RecoverSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	return nil, ErrNotFound
}
func (*ambiguousUpstream) GetSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	return nil, ErrNotFound
}
func (u *ambiguousUpstream) SendInput(context.Context, ConnectionSecret, string, InputRequest) error {
	u.inputs++
	return nil
}
func (*ambiguousUpstream) CancelSession(context.Context, ConnectionSecret, string) error { return nil }
func (*ambiguousUpstream) DeleteSession(context.Context, ConnectionSecret, string) error { return nil }

type provisionFailureUpstream struct {
	ambiguousUpstream
	gets, cancels, deletes int
	cancelErr              error
	status                 string
}

func (u *provisionFailureUpstream) GetSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	u.gets++
	status := u.status
	if status == "" {
		status = "requires_action"
	}
	return &UpstreamSession{ID: "upstream", Status: status, Readiness: "pending"}, nil
}
func (u *provisionFailureUpstream) CancelSession(context.Context, ConnectionSecret, string) error {
	u.cancels++
	return u.cancelErr
}
func (u *provisionFailureUpstream) DeleteSession(context.Context, ConnectionSecret, string) error {
	u.deletes++
	return nil
}

type restartStore struct {
	mu         sync.Mutex
	owner      Ownership
	connection Connection
	session    *Session
	events     []Event
}

func (s *restartStore) CreateConnection(_ context.Context, o Ownership, c Connection, app, exec string) (*Connection, error) {
	s.owner = o
	c.credentialRef = app
	c.executorRef = exec
	s.connection = c
	return &c, nil
}
func (s *restartStore) ListConnections(context.Context, Ownership) ([]Connection, error) {
	return []Connection{s.connection}, nil
}
func (s *restartStore) GetConnection(_ context.Context, o Ownership, id string) (*Connection, error) {
	if o.OrganizationID != s.owner.OrganizationID || o.InstanceID != s.owner.InstanceID || id != s.connection.ID {
		return nil, ErrNotFound
	}
	c := s.connection
	return &c, nil
}
func (s *restartStore) UpdateConnection(_ context.Context, o Ownership, c Connection, app, exec string) (*Connection, error) {
	if o.OrganizationID != s.owner.OrganizationID || o.InstanceID != s.owner.InstanceID {
		return nil, ErrNotFound
	}
	c.credentialRef = app
	c.executorRef = exec
	s.connection = c
	return &c, nil
}
func (*restartStore) UpdateConnectionStatus(context.Context, Ownership, string, string, string) error {
	return nil
}
func (*restartStore) DeleteConnection(context.Context, Ownership, string) error { return nil }
func (s *restartStore) BeginSessionCreate(_ context.Context, o Ownership, x Session) (*Session, bool, error) {
	if s.session != nil {
		copy := *s.session
		return &copy, false, nil
	}
	s.owner = o
	x.Status = "creating"
	x.Readiness = "pending"
	s.session = &x
	copy := x
	return &copy, true, nil
}
func (s *restartStore) CompleteSessionCreate(_ context.Context, _ Ownership, _ string, u UpstreamSession) (*Session, error) {
	s.session.UpstreamSessionID = u.ID
	return s.session, nil
}
func (*restartStore) BindEnvironment(context.Context, Ownership, string, string, string) error {
	return nil
}
func (s *restartStore) MarkSessionCreateAmbiguous(_ context.Context, _ Ownership, _ string, msg string) error {
	s.session.Status = "recovery_pending"
	s.session.LastError = sanitizeError(msg)
	return nil
}
func (s *restartStore) ListSessions(context.Context, Ownership) ([]Session, error) {
	return []Session{*s.session}, nil
}
func (s *restartStore) GetSession(_ context.Context, o Ownership, id string) (*Session, error) {
	if s.session == nil || id != s.session.ID || o.OrganizationID != s.owner.OrganizationID || o.InstanceID != s.owner.InstanceID {
		return nil, ErrNotFound
	}
	copy := *s.session
	return &copy, nil
}
func (*restartStore) UpdateSessionState(context.Context, Ownership, string, string, string, string) error {
	return nil
}
func (s *restartStore) UpdateSessionStatus(_ context.Context, _ Ownership, _ string, status, lastErr string) error {
	s.session.Status = status
	s.session.LastError = lastErr
	return nil
}
func (*restartStore) MarkCleanup(context.Context, Ownership, string, string, string) error {
	return nil
}
func (s *restartStore) PutEvent(_ context.Context, _ Ownership, _ string, u UpstreamEvent) (*Event, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.events {
		if s.events[i].EventID == u.ID {
			e := s.events[i]
			return &e, false, nil
		}
	}
	e := Event{Cursor: int64(len(s.events) + 1), EventID: u.ID, Type: u.Type, Payload: sanitizeJSON(u.Payload), ToolFailed: u.Failed}
	s.events = append(s.events, e)
	created := e
	return &created, true, nil
}
func (s *restartStore) ListEvents(context.Context, Ownership, string, int64, int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...), nil
}
func (*restartStore) ClaimInput(context.Context, Ownership, string, string, string) (bool, error) {
	return true, nil
}
func (*restartStore) InputStatus(context.Context, Ownership, string, string) (string, string, error) {
	return "recovery_pending", "", nil
}
func (*restartStore) CompleteInput(context.Context, Ownership, string, string, string) error {
	return nil
}

func TestSendInputRejectsTimeoutTerminalEnvironmentBeforeUpstreamMutation(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", EnvironmentCleanupStatus: "timeout_terminal"}}
	upstream := &ambiguousUpstream{}
	service, err := NewService(store, testCipher{}, upstream, nil, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = service.SendInput(context.Background(), o, "session", InputRequest{IdempotencyKey: "input", Input: json.RawMessage(`"hello"`)})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SendInput error=%v", err)
	}
	if upstream.inputs != 0 {
		t.Fatalf("upstream inputs=%d, want 0", upstream.inputs)
	}
}

func TestGetSessionPreservesLocalProvisionFailure(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "failed", Readiness: "failed", LastError: "managed profile rejected"}}
	upstream := &provisionFailureUpstream{}
	service, err := NewService(store, testCipher{}, upstream, nil, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.GetSession(context.Background(), o, "session")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.LastError != "managed profile rejected" {
		t.Fatalf("session=%+v", got)
	}
	if upstream.gets != 0 {
		t.Fatalf("upstream refreshes=%d, want 0", upstream.gets)
	}
}

func TestDeleteProvisionFailureChecksUpstreamAndStopsOnCancelFailure(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "failed", Readiness: "failed", LastError: "provision failed"}}
	upstream := &provisionFailureUpstream{cancelErr: errors.New("upstream cannot cancel unbound environment")}
	service, err := NewService(store, testCipher{}, upstream, nil, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = service.DeleteSession(context.Background(), o, "session")
	if err == nil || upstream.gets != 1 || upstream.cancels != 1 {
		t.Fatalf("error=%v gets=%d cancels=%d", err, upstream.gets, upstream.cancels)
	}
	if upstream.deletes != 0 {
		t.Fatalf("upstream deletes=%d, want 0 after cancellation failure", upstream.deletes)
	}
}

func TestDeleteProvisionFailureCancelsDespiteIdleUpstreamStatus(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "failed", Readiness: "failed", LastError: "provision failed"}}
	upstream := &provisionFailureUpstream{status: "idle", cancelErr: errors.New("required action still blocks deletion")}
	service, err := NewService(store, testCipher{}, upstream, nil, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = service.DeleteSession(context.Background(), o, "session")
	if err == nil || upstream.gets != 1 || upstream.cancels != 1 {
		t.Fatalf("error=%v gets=%d cancels=%d", err, upstream.gets, upstream.cancels)
	}
	if upstream.deletes != 0 {
		t.Fatalf("upstream deletes=%d, want 0 after cancellation failure", upstream.deletes)
	}
}

func TestAmbiguousCreateIsNotRetriedAfterRestart(t *testing.T) {
	o := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "admin"}
	store := &restartStore{}
	up := &ambiguousUpstream{}
	svc, _ := NewService(store, testCipher{}, up, nil, nil, Config{AllowedModels: []string{"gpt-6"}})
	c, err := svc.CreateConnection(context.Background(), o, CreateConnectionRequest{Name: "primary", APIKey: "sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	req := CreateSessionRequest{ConnectionID: c.ID, IdempotencyKey: "intent-1", Model: "gpt-6", Environment: EnvironmentSpec{Type: "none"}}
	first, err := svc.CreateSession(context.Background(), o, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "recovery_pending" {
		t.Fatalf("status=%q", first.Status)
	}
	if first.LastError == "" || contains(first.LastError, "sk-secret") {
		t.Fatalf("secret leaked in error: %q", first.LastError)
	}
	restarted, _ := NewService(store, testCipher{}, up, nil, nil, Config{AllowedModels: []string{"gpt-6"}})
	_, err = restarted.CreateSession(context.Background(), o, req)
	if !errors.Is(err, ErrCreatePending) {
		t.Fatalf("expected recovery pending, got %v", err)
	}
	if up.creates != 1 {
		t.Fatalf("ambiguous create retried %d times", up.creates)
	}
}
func TestEventDedupPreservesIndividualToolFailureAndRedactsSecrets(t *testing.T) {
	s := &restartStore{}
	raw := json.RawMessage(`{"api_key":"sk-live","result":"failed","text":"visible answer","error":"request failed for sk-hidden at https://private.example","usage":{"input_tokens":0,"output_tokens":12,"total_tokens":12}}`)
	e, created, err := s.PutEvent(context.Background(), Ownership{}, "session", UpstreamEvent{ID: "evt-1", Type: "tool.completed", Payload: raw, Failed: true})
	if err != nil || !created || !e.ToolFailed {
		t.Fatalf("event=%+v created=%v err=%v", e, created, err)
	}
	if contains(string(e.Payload), "sk-live") {
		t.Fatalf("payload leaked key: %s", e.Payload)
	}
	if contains(string(e.Payload), "sk-hidden") || contains(string(e.Payload), "private.example") {
		t.Fatalf("nested string secret leaked: %s", e.Payload)
	}
	if !contains(string(e.Payload), "visible answer") || !contains(string(e.Payload), `"output_tokens":12`) {
		t.Fatalf("transcript or usage was incorrectly redacted: %s", e.Payload)
	}
	_, created, _ = s.PutEvent(context.Background(), Ownership{}, "session", UpstreamEvent{ID: "evt-1"})
	if created || len(s.events) != 1 {
		t.Fatal("duplicate event was inserted")
	}
}
func TestForeignInstanceCannotReadConnection(t *testing.T) {
	o := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "admin"}
	s := &restartStore{}
	svc, _ := NewService(s, testCipher{}, &ambiguousUpstream{}, nil, nil, Config{})
	c, _ := svc.CreateConnection(context.Background(), o, CreateConnectionRequest{Name: "x", APIKey: "secret"})
	foreign := o
	foreign.InstanceID = "instance-b"
	if _, err := svc.GetConnection(context.Background(), foreign, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign read error=%v", err)
	}
}
func TestConnectionRejectsReusedApplicationCredentialAsExecutorCredential(t *testing.T) {
	o := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "admin"}
	svc, _ := NewService(&restartStore{}, testCipher{}, &ambiguousUpstream{}, nil, nil, Config{})
	if _, err := svc.CreateConnection(context.Background(), o, CreateConnectionRequest{Name: "unsafe", APIKey: "same-secret", ExecutorKey: " same-secret "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid split credential, got %v", err)
	}
}
func TestUnsupportedSelfHostedBackendFailsBeforePaidUpstreamCreate(t *testing.T) {
	o := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "admin"}
	store := &restartStore{}
	up := &ambiguousUpstream{}
	svc, _ := NewService(store, testCipher{}, up, unsupportedEnvironment{}, nil, Config{EnvironmentImage: "trusted"})
	c, _ := svc.CreateConnection(context.Background(), o, CreateConnectionRequest{Name: "c", APIKey: "app", ExecutorKey: "executor"})
	_, err := svc.CreateSession(context.Background(), o, CreateSessionRequest{ConnectionID: c.ID, IdempotencyKey: "idem", Model: "gpt", Environment: EnvironmentSpec{Type: "self_hosted"}})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error=%v", err)
	}
	if up.creates != 0 {
		t.Fatalf("paid creates=%d", up.creates)
	}
}
func TestAPIKeyCannotEscalateToCredentialOrSessionWrite(t *testing.T) {
	o := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "apikey:verified-hash"}
	if err := authorize(o, "connection:write"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("connection write=%v", err)
	}
	if err := authorize(o, "session:write"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("session write=%v", err)
	}
	if err := authorize(o, "connection:read"); err != nil {
		t.Fatalf("scoped read denied: %v", err)
	}
}
func TestStartRecoveryRestoresPersistedExecutorBindingWithFreshSecret(t *testing.T) {
	o := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "admin"}
	base := &restartStore{owner: o, connection: Connection{ID: "conn-1", credentialRef: `encrypted:{"api_key":"app-key"}`, executorRef: `encrypted:{"executor_key":"executor-key"}`}, session: &Session{ID: "session-1", ConnectionID: "conn-1", UpstreamSessionID: "upstream-1", UpstreamEnvironmentID: "env-1", SandboxID: "sandbox-1", Environment: EnvironmentSpec{Type: "self_hosted"}, Readiness: "pending", Status: "active"}}
	store := &recoveryStore{restartStore: base, records: []RecoveryRecord{{Ownership: o, Session: *base.session}}}
	environment := &recoveryEnvironment{}
	svc, _ := NewService(store, testCipher{}, &recoveryUpstream{}, environment, nil, Config{EnvironmentImage: "trusted"})
	svc.SetRecoveryAuthorizer(allowRecovery{})
	if err := svc.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer svc.Close(context.Background())
	got := environment.got
	if got.OrganizationID != o.OrganizationID || got.InstanceID != o.InstanceID || got.LocalSessionID != "session-1" || got.SandboxID != "sandbox-1" || got.UpstreamEnvironmentID != "env-1" || got.ExecutorAPIKey != "executor-key" || got.RemoteURL == "" {
		t.Fatalf("reconcile=%+v", got)
	}
}
func TestStartRecoveryStopsAfterOwnerDowngradedToViewer(t *testing.T) {
	o := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "admin"}
	base := &restartStore{owner: o, connection: Connection{ID: "conn-1", credentialRef: `encrypted:{"api_key":"app-key"}`}, session: &Session{ID: "session-1", ConnectionID: "conn-1", UpstreamSessionID: "upstream-1", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "active"}}
	store := &recoveryStore{restartStore: base, records: []RecoveryRecord{{Ownership: o, Session: *base.session}}}
	env := &recoveryEnvironment{}
	svc, _ := NewService(store, testCipher{}, &recoveryUpstream{}, env, nil, Config{EnvironmentImage: "trusted"})
	svc.SetRecoveryAuthorizer(viewerRecovery{})
	if err := svc.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if env.got.LocalSessionID != "" {
		t.Fatalf("viewer recovery invoked executor: %+v", env.got)
	}
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
