package openaiapi

import (
	"context"
	"errors"
	"testing"
)

type readinessStore struct {
	*restartStore
	updates   int
	readiness string
	updateErr error
}

func (s *readinessStore) UpdateSessionStatus(_ context.Context, _ Ownership, _ string, status, lastErr string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updates++
	s.session.Status = status
	s.session.LastError = lastErr
	return nil
}

func (s *readinessStore) UpdateSessionState(_ context.Context, _ Ownership, _ string, status, readiness, lastErr string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updates++
	s.session.Status = status
	s.session.Readiness = readiness
	s.session.LastError = lastErr
	s.readiness = readiness
	return nil
}

type readinessEnvironment struct {
	recoveryEnvironment
	requests   []EnvironmentObserveRequest
	missing    bool
	observeErr error
}

func (e *readinessEnvironment) Observe(_ context.Context, req EnvironmentObserveRequest) (*EnvironmentState, error) {
	e.requests = append(e.requests, req)
	if e.missing {
		return nil, errEnvironmentNotBound
	}
	if e.observeErr != nil {
		return nil, e.observeErr
	}
	return &EnvironmentState{Status: req.Status}, nil
}

func newReadinessService() (*Service, *readinessStore, *readinessEnvironment, Ownership) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance"}
	store := &readinessStore{restartStore: &restartStore{
		owner:   o,
		session: &Session{ID: "session", Status: "active", Readiness: "connected"},
	}}
	environment := &readinessEnvironment{}
	return &Service{store: store, environments: environment}, store, environment, o
}

func TestDisconnectedEventPersistsPendingWithoutLocalBinding(t *testing.T) {
	service, store, environment, owner := newReadinessService()
	environment.missing = true

	err := service.observeEnvironmentEvent(context.Background(), owner, "session", UpstreamEvent{
		ID: "disconnect-1", Type: "agent.session.environment.disconnected",
	}, true)
	if err != nil {
		t.Fatalf("disconnected event stopped durable consumption: %v", err)
	}
	if store.readiness != "pending" || store.session.Status != "active" {
		t.Fatalf("persisted session = %+v", store.session)
	}
	if len(environment.requests) != 1 || environment.requests[0].Status != "pending" {
		t.Fatalf("observe requests = %+v", environment.requests)
	}

	if err = service.observeEnvironmentEvent(context.Background(), owner, "session", UpstreamEvent{
		ID: "connected-without-binding", Type: "agent.session.environment.connected",
	}, true); err != nil {
		t.Fatalf("missing local binding stopped durable consumption: %v", err)
	}
	if store.readiness != "pending" {
		t.Fatalf("missing binding invented readiness %q", store.readiness)
	}
}

func TestReplayedConnectedEventCannotMarkReplacementReady(t *testing.T) {
	service, store, environment, owner := newReadinessService()
	store.session.Readiness = "pending"

	if err := service.observeEnvironmentEvent(context.Background(), owner, "session", UpstreamEvent{
		ID: "old-connected", Type: "agent.session.environment.connected",
	}, false); err != nil {
		t.Fatal(err)
	}
	if len(environment.requests) != 0 || store.updates != 0 || store.session.Readiness != "pending" {
		t.Fatalf("replayed event changed readiness: requests=%d updates=%d readiness=%q", len(environment.requests), store.updates, store.session.Readiness)
	}
}

func TestDisconnectedEventDoesNotHideStoreFailure(t *testing.T) {
	service, store, _, owner := newReadinessService()
	want := errors.New("database unavailable")
	store.updateErr = want

	err := service.observeEnvironmentEvent(context.Background(), owner, "session", UpstreamEvent{
		ID: "disconnect-1", Type: "agent.session.environment.disconnected",
	}, true)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want database failure", err)
	}
}

func TestEnvironmentObservationDoesNotHideOwnershipFailure(t *testing.T) {
	service, store, environment, owner := newReadinessService()
	want := errors.New("environment is not owned by authenticated instance")
	environment.observeErr = want
	err := service.observeEnvironmentEvent(context.Background(), owner, "session", UpstreamEvent{
		ID: "connected-foreign", Type: "agent.session.environment.connected",
	}, true)
	if !errors.Is(err, want) || store.updates != 0 {
		t.Fatalf("ownership failure hidden: err=%v updates=%d", err, store.updates)
	}
}

type blockingReadinessUpstream struct {
	ambiguousUpstream
	remote  UpstreamSession
	started chan struct{}
	release chan struct{}
}

func (u *blockingReadinessUpstream) GetSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	close(u.started)
	<-u.release
	remote := u.remote
	return &remote, nil
}

func newReadinessGetService(readiness string, remoteReadiness string) (*Service, *readinessStore, *blockingReadinessUpstream, Ownership) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &readinessStore{restartStore: &restartStore{
		owner:      o,
		connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`},
		session: &Session{
			ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream",
			SandboxID: "sandbox", Environment: EnvironmentSpec{Type: "self_hosted"},
			Status: "in_progress", Readiness: readiness,
		},
	}}
	upstream := &blockingReadinessUpstream{
		remote:  UpstreamSession{ID: "upstream", Status: "idle", Readiness: remoteReadiness},
		started: make(chan struct{}), release: make(chan struct{}),
	}
	return &Service{store: store, upstream: upstream, cipher: testCipher{}}, store, upstream, o
}

func TestGetSelfHostedSessionSnapshotCannotInventConnectedReadiness(t *testing.T) {
	service, store, upstream, owner := newReadinessGetService("pending", "connected")
	close(upstream.release)

	got, err := service.GetSession(context.Background(), owner, "session")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "idle" || got.Readiness != "pending" {
		t.Fatalf("session = %+v, want refreshed status with durable pending readiness", got)
	}
	if store.updates != 1 {
		t.Fatalf("status updates = %d, want 1", store.updates)
	}
}

func TestGetSelfHostedSessionCannotOverwriteReadinessEventDuringRefresh(t *testing.T) {
	service, _, upstream, owner := newReadinessGetService("pending", "pending")
	environment := &readinessEnvironment{}
	service.environments = environment

	result := make(chan *Session, 1)
	errs := make(chan error, 1)
	go func() {
		got, err := service.GetSession(context.Background(), owner, "session")
		result <- got
		errs <- err
	}()
	<-upstream.started

	if err := service.observeEnvironmentEvent(context.Background(), owner, "session", UpstreamEvent{
		ID: "connected-during-refresh", Type: "agent.session.environment.connected",
	}, true); err != nil {
		t.Fatal(err)
	}
	close(upstream.release)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.Status != "idle" || got.Readiness != "connected" {
		t.Fatalf("session = %+v, want refreshed status with event-derived connected readiness", got)
	}
	if len(environment.requests) != 1 || environment.requests[0].Status != "connected" {
		t.Fatalf("observe requests = %+v", environment.requests)
	}
}
