package openaiapi

import (
	"context"
	"errors"
	"testing"
	"time"

	upstreampkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/upstream"
)

type provisionBindingStore struct {
	*restartStore
	bindErr, updateErr error
}

func (s *provisionBindingStore) BindEnvironment(_ context.Context, _ Ownership, _ string, sandboxID, readiness string) error {
	if s.bindErr != nil {
		return s.bindErr
	}
	s.session.SandboxID = sandboxID
	s.session.Readiness = readiness
	return nil
}

func (s *provisionBindingStore) UpdateSessionState(_ context.Context, _ Ownership, _ string, status, readiness, lastErr string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.session.Status = status
	s.session.Readiness = readiness
	s.session.LastError = lastErr
	return nil
}

type createdThenFailedEnvironment struct {
	deleteCalls  int
	calls        *[]string
	deleteErr    error
	provisionErr error
}

func (*createdThenFailedEnvironment) ValidateConfiguration(context.Context) error { return nil }
func (e *createdThenFailedEnvironment) Provision(context.Context, EnvironmentProvisionRequest) (*EnvironmentState, error) {
	provisionErr := e.provisionErr
	if provisionErr == nil {
		provisionErr = errors.New("executor failed after sandbox creation")
	}
	return &EnvironmentState{SandboxID: "sandbox-created", Status: "pending"}, provisionErr
}

func TestCreateSessionCompensatesWhenSandboxBindingFails(t *testing.T) {
	bindErr := errors.New("persist sandbox binding")
	cleanupErr := errors.New("destroy sandbox")
	stateErr := errors.New("persist failed session state")
	for _, test := range []struct {
		name                  string
		cleanupErr, updateErr error
	}{
		{name: "cleanup succeeds"},
		{name: "cleanup failure is joined", cleanupErr: cleanupErr},
		{name: "state failure is joined", updateErr: stateErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
			store := &provisionBindingStore{restartStore: &restartStore{}, bindErr: bindErr, updateErr: test.updateErr}
			environment := &createdThenFailedEnvironment{provisionErr: errors.New("provision completed"), deleteErr: test.cleanupErr}
			service, err := NewService(store, testCipher{}, &successfulCreateUpstream{}, environment, nil, Config{EnvironmentImage: "trusted"})
			if err != nil {
				t.Fatal(err)
			}
			connection, err := service.CreateConnection(context.Background(), o, CreateConnectionRequest{Name: "connection", APIKey: "application", ExecutorKey: "executor"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.CreateSession(context.Background(), o, CreateSessionRequest{ConnectionID: connection.ID, IdempotencyKey: "create", Model: "gpt", Environment: EnvironmentSpec{Type: "self_hosted"}})
			if !errors.Is(err, bindErr) {
				t.Fatalf("error=%v, want binding error", err)
			}
			if test.cleanupErr != nil && !errors.Is(err, test.cleanupErr) {
				t.Fatalf("error=%v, want cleanup error", err)
			}
			if test.updateErr != nil && !errors.Is(err, test.updateErr) {
				t.Fatalf("error=%v, want state persistence error", err)
			}
			if environment.deleteCalls != 1 {
				t.Fatalf("delete calls=%d, want 1", environment.deleteCalls)
			}
			if test.updateErr == nil && (store.session.Status != "failed" || store.session.Readiness != "failed") {
				t.Fatalf("session=%+v, want durable failed state", store.session)
			}
		})
	}
}

func TestCreateSessionBindFailureWithCanceledRequestRemainsFailedOnSameKeyRecovery(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &provisionBindingStore{restartStore: &restartStore{}, bindErr: context.Canceled}
	environment := &createdThenFailedEnvironment{provisionErr: errors.New("executor setup failed")}
	service, err := NewService(store, testCipher{}, &successfulCreateUpstream{}, environment, nil, Config{EnvironmentImage: "trusted"})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.CreateConnection(context.Background(), o, CreateConnectionRequest{Name: "connection", APIKey: "application", ExecutorKey: "executor"})
	if err != nil {
		t.Fatal(err)
	}
	request := CreateSessionRequest{ConnectionID: connection.ID, IdempotencyKey: "create", Model: "gpt", Environment: EnvironmentSpec{Type: "self_hosted"}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = service.CreateSession(canceled, o, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("create error=%v, want canceled binding error", err)
	}
	if store.session.Status != "failed" || store.session.Readiness != "failed" {
		t.Fatalf("session after compensation=%+v", store.session)
	}
	recovered, err := service.CreateSession(context.Background(), o, request)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "failed" || recovered.Readiness != "failed" {
		t.Fatalf("recovered session=%+v", recovered)
	}
}
func (*createdThenFailedEnvironment) Reconcile(context.Context, EnvironmentReconcileRequest) (*EnvironmentState, error) {
	return nil, ErrUnavailable
}
func (*createdThenFailedEnvironment) Observe(context.Context, EnvironmentObserveRequest) (*EnvironmentState, error) {
	return nil, ErrUnavailable
}
func (*createdThenFailedEnvironment) Relinquish(context.Context, Ownership, string) error { return nil }
func (e *createdThenFailedEnvironment) DeleteReference(context.Context, Ownership, string, string) error {
	e.deleteCalls++
	if e.calls != nil {
		*e.calls = append(*e.calls, "environment-delete")
	}
	return e.deleteErr
}

type cleanupPhaseStore struct{ *restartStore }

func (s *cleanupPhaseStore) MarkCleanup(_ context.Context, _ Ownership, _ string, status, lastErr string) error {
	s.session.CleanupStatus = status
	s.session.LastError = lastErr
	return nil
}

type failUpstreamDeletedMarkStore struct {
	*cleanupPhaseStore
	failOnce bool
}

func (s *failUpstreamDeletedMarkStore) MarkCleanup(ctx context.Context, o Ownership, id, status, lastErr string) error {
	if status == "upstream_deleted" && s.failOnce {
		s.failOnce = false
		return errors.New("persist upstream deletion phase")
	}
	return s.cleanupPhaseStore.MarkCleanup(ctx, o, id, status, lastErr)
}

type successfulCreateUpstream struct{ ambiguousUpstream }

func (*successfulCreateUpstream) CreateSession(context.Context, ConnectionSecret, CreateSessionRequest) (*UpstreamSession, error) {
	return &UpstreamSession{ID: "upstream", EnvironmentID: "environment", Status: "requires_action", Readiness: "pending"}, nil
}

func TestCreateSessionPersistsSandboxReferenceReturnedWithProvisionFailure(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &provisionBindingStore{restartStore: &restartStore{}}
	environment := &createdThenFailedEnvironment{}
	service, err := NewService(store, testCipher{}, &successfulCreateUpstream{}, environment, nil, Config{EnvironmentImage: "trusted"})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.CreateConnection(context.Background(), o, CreateConnectionRequest{Name: "connection", APIKey: "application", ExecutorKey: "executor"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateSession(context.Background(), o, CreateSessionRequest{ConnectionID: connection.ID, IdempotencyKey: "create", Model: "gpt", Environment: EnvironmentSpec{Type: "self_hosted"}})
	if err == nil {
		t.Fatal("expected provision failure")
	}
	got, getErr := store.GetSession(context.Background(), o, store.session.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.SandboxID != "sandbox-created" || got.Status != "failed" || got.Readiness != "failed" {
		t.Fatalf("session=%+v", got)
	}
}

type cleanupOrderUpstream struct {
	ambiguousUpstream
	calls     *[]string
	deleteErr error
	deleted   bool
}

type sequencedCleanupUpstream struct {
	ambiguousUpstream
	sessions []UpstreamSession
	gets     int
	cancels  int
	deletes  int
}

func (u *sequencedCleanupUpstream) GetSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	index := u.gets
	u.gets++
	if index >= len(u.sessions) {
		index = len(u.sessions) - 1
	}
	session := u.sessions[index]
	return &session, nil
}
func (u *sequencedCleanupUpstream) CancelSession(context.Context, ConnectionSecret, string) error {
	u.cancels++
	return nil
}
func (u *sequencedCleanupUpstream) DeleteSession(context.Context, ConnectionSecret, string) error {
	u.deletes++
	return nil
}

func (u *cleanupOrderUpstream) GetSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	if u.deleted {
		return nil, wrappedUpstreamError{&upstreampkg.Error{StatusCode: 404, Operation: "get session"}}
	}
	return &UpstreamSession{ID: "upstream", Status: "idle"}, nil
}
func (u *cleanupOrderUpstream) DeleteSession(context.Context, ConnectionSecret, string) error {
	*u.calls = append(*u.calls, "upstream-delete")
	if u.deleteErr == nil {
		u.deleted = true
	}
	return u.deleteErr
}

func TestDeleteSessionKeepsSandboxWhenUpstreamDeletionFails(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	calls := []string{}
	store := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", SandboxID: "sandbox-created", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "idle", Readiness: "failed"}}
	environment := &createdThenFailedEnvironment{calls: &calls}
	upstream := &cleanupOrderUpstream{calls: &calls, deleteErr: errors.New("required actions still prevent deletion")}
	service, err := NewService(store, testCipher{}, upstream, environment, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteSession(context.Background(), o, "session"); err == nil {
		t.Fatal("expected upstream deletion failure")
	}
	if environment.deleteCalls != 0 {
		t.Fatalf("environment delete calls=%d, want 0", environment.deleteCalls)
	}
	if len(calls) != 1 || calls[0] != "upstream-delete" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestDeleteSessionDeletesSandboxOnlyAfterUpstreamDeletion(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	calls := []string{}
	store := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", SandboxID: "sandbox-created", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "idle", Readiness: "failed"}}
	environment := &createdThenFailedEnvironment{calls: &calls}
	upstream := &cleanupOrderUpstream{calls: &calls}
	service, err := NewService(store, testCipher{}, upstream, environment, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteSession(context.Background(), o, "session"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "upstream-delete" || calls[1] != "environment-delete" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestDeleteSessionRetryAfterSandboxFailureDoesNotReplayUpstreamDelete(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	calls := []string{}
	base := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", SandboxID: "sandbox-created", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "idle", Readiness: "failed"}}
	store := &cleanupPhaseStore{restartStore: base}
	environment := &createdThenFailedEnvironment{calls: &calls, deleteErr: errors.New("sandbox destroy temporarily failed")}
	upstream := &cleanupOrderUpstream{calls: &calls}
	service, err := NewService(store, testCipher{}, upstream, environment, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteSession(context.Background(), o, "session"); err == nil {
		t.Fatal("expected sandbox deletion failure")
	}
	if store.session.CleanupStatus != "upstream_deleted" {
		t.Fatalf("cleanup status=%q", store.session.CleanupStatus)
	}
	environment.deleteErr = nil
	if err = service.DeleteSession(context.Background(), o, "session"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0] != "upstream-delete" || calls[1] != "environment-delete" || calls[2] != "environment-delete" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestDeleteSessionRetryUsesTypedUpstreamNotFoundAfterDeletionPhaseWriteFails(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	calls := []string{}
	base := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", SandboxID: "sandbox-created", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "idle", Readiness: "failed"}}
	store := &failUpstreamDeletedMarkStore{cleanupPhaseStore: &cleanupPhaseStore{restartStore: base}, failOnce: true}
	environment := &createdThenFailedEnvironment{calls: &calls}
	upstream := &cleanupOrderUpstream{calls: &calls}
	service, err := NewService(store, testCipher{}, upstream, environment, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteSession(context.Background(), o, "session"); err == nil {
		t.Fatal("expected deletion phase persistence failure")
	}
	if environment.deleteCalls != 0 {
		t.Fatalf("environment delete calls=%d before durable upstream reconciliation", environment.deleteCalls)
	}
	if err = service.DeleteSession(context.Background(), o, "session"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "upstream-delete" || calls[1] != "environment-delete" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestDeleteSessionFailedWithRequiredActionsDoesNotDeleteUntilActionsClear(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	base := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", SandboxID: "sandbox-created", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "failed", Readiness: "failed"}}
	store := &cleanupPhaseStore{restartStore: base}
	environment := &createdThenFailedEnvironment{}
	upstream := &sequencedCleanupUpstream{sessions: []UpstreamSession{{ID: "upstream", Status: "failed", HasRequiredActions: true}}}
	service, err := NewService(store, testCipher{}, upstream, environment, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err = service.DeleteSession(ctx, o, "session"); err == nil {
		t.Fatal("expected required actions to block deletion")
	}
	if upstream.cancels != 1 || upstream.deletes != 0 || environment.deleteCalls != 0 {
		t.Fatalf("cancels=%d upstream deletes=%d environment deletes=%d", upstream.cancels, upstream.deletes, environment.deleteCalls)
	}
	if store.session.CleanupStatus != "retry_pending" {
		t.Fatalf("cleanup status=%q", store.session.CleanupStatus)
	}
}

func TestDeleteSessionInProgressCancelsThenDeletesOnlyAfterIdleWithoutActions(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	base := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", SandboxID: "sandbox-created", Environment: EnvironmentSpec{Type: "self_hosted"}, Status: "in_progress", Readiness: "ready"}}
	store := &cleanupPhaseStore{restartStore: base}
	environment := &createdThenFailedEnvironment{}
	upstream := &sequencedCleanupUpstream{sessions: []UpstreamSession{
		{ID: "upstream", Status: "in_progress"},
		{ID: "upstream", Status: "idle"},
	}}
	service, err := NewService(store, testCipher{}, upstream, environment, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteSession(context.Background(), o, "session"); err != nil {
		t.Fatal(err)
	}
	if upstream.cancels != 1 || upstream.deletes != 1 || environment.deleteCalls != 1 {
		t.Fatalf("cancels=%d upstream deletes=%d environment deletes=%d", upstream.cancels, upstream.deletes, environment.deleteCalls)
	}
}

func TestFromUpstreamReportsRequiredActions(t *testing.T) {
	got := fromUpstream(&upstreampkg.Session{ID: "upstream", Status: "failed", RequiredActions: []upstreampkg.RequiredAction{{Type: "function_call"}}})
	if !got.HasRequiredActions {
		t.Fatal("required actions were lost while adapting upstream session")
	}
	if fromUpstream(&upstreampkg.Session{ID: "upstream", Status: "idle"}).HasRequiredActions {
		t.Fatal("empty required actions reported as present")
	}
}
