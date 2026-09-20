package openaiapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type sharedLeaseState struct {
	mu      sync.Mutex
	holder  string
	expires time.Time
}

type stalledLeaseStore struct{ *restartStore }

func (*stalledLeaseStore) AcquireSessionLease(context.Context, Ownership, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (*stalledLeaseStore) RenewSessionLease(ctx context.Context, _ Ownership, _, _ string, _ time.Duration) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}
func (*stalledLeaseStore) ReleaseSessionLease(context.Context, Ownership, string, string) error {
	return nil
}

type delayedAcquireLeaseStore struct {
	*stalledLeaseStore
	delay time.Duration
}

func (s *delayedAcquireLeaseStore) AcquireSessionLease(context.Context, Ownership, string, string, time.Duration) (bool, error) {
	time.Sleep(s.delay)
	return true, nil
}

type lateRenewLeaseStore struct {
	*stalledLeaseStore
	delay time.Duration
}

func (s *lateRenewLeaseStore) RenewSessionLease(context.Context, Ownership, string, string, time.Duration) (bool, error) {
	time.Sleep(s.delay)
	return true, nil
}

type leasedRecoveryStore struct {
	*recoveryStore
	leases *sharedLeaseState
}

func (s *leasedRecoveryStore) AcquireSessionLease(_ context.Context, _ Ownership, _, holder string, ttl time.Duration) (bool, error) {
	s.leases.mu.Lock()
	defer s.leases.mu.Unlock()
	if s.leases.holder != "" && s.leases.holder != holder && time.Now().Before(s.leases.expires) {
		return false, nil
	}
	s.leases.holder, s.leases.expires = holder, time.Now().Add(ttl)
	return true, nil
}
func (s *leasedRecoveryStore) RenewSessionLease(_ context.Context, _ Ownership, _, holder string, ttl time.Duration) (bool, error) {
	s.leases.mu.Lock()
	defer s.leases.mu.Unlock()
	if s.leases.holder != holder || time.Now().After(s.leases.expires) {
		return false, nil
	}
	s.leases.expires = time.Now().Add(ttl)
	return true, nil
}
func (s *leasedRecoveryStore) ReleaseSessionLease(_ context.Context, _ Ownership, _, holder string) error {
	s.leases.mu.Lock()
	defer s.leases.mu.Unlock()
	if s.leases.holder == holder {
		s.leases.holder = ""
	}
	return nil
}

type countingRecoveryEnvironment struct {
	recoveryEnvironment
	mu           sync.Mutex
	reconciles   int
	relinquishes int
}

type countingRecoveryUpstream struct {
	recoveryUpstream
	mu      sync.Mutex
	streams int
}

type mutableRecoveryAuthorizer struct {
	mu      sync.Mutex
	allowed bool
}

type stalledRecoveryAuthorizer struct {
	started chan struct{}
	release chan struct{}
}

func (a *stalledRecoveryAuthorizer) AuthorizeRecovery(context.Context, Ownership) (Ownership, error) {
	select {
	case <-a.started:
	default:
		close(a.started)
	}
	<-a.release
	return Ownership{}, ErrForbidden
}

func (a *mutableRecoveryAuthorizer) AuthorizeRecovery(_ context.Context, o Ownership) (Ownership, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.allowed {
		return Ownership{}, ErrForbidden
	}
	o.Role = "admin"
	return o, nil
}

func (a *mutableRecoveryAuthorizer) revoke() {
	a.mu.Lock()
	a.allowed = false
	a.mu.Unlock()
}

type revocationStream struct {
	recoveryUpstream
	started  chan struct{}
	canceled chan struct{}
	lateErr  chan error
}

func (u *revocationStream) StreamEvents(ctx context.Context, _ ConnectionSecret, _ string, emit func(UpstreamEvent) error) error {
	close(u.started)
	<-ctx.Done()
	close(u.canceled)
	u.lateErr <- emit(UpstreamEvent{ID: "after-revocation", Type: "agent.session.turn.completed"})
	return ctx.Err()
}

func (u *countingRecoveryUpstream) StreamEvents(ctx context.Context, _ ConnectionSecret, _ string, _ func(UpstreamEvent) error) error {
	u.mu.Lock()
	u.streams++
	u.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}
func (e *countingRecoveryEnvironment) Reconcile(_ context.Context, r EnvironmentReconcileRequest) (*EnvironmentState, error) {
	e.mu.Lock()
	e.reconciles++
	e.mu.Unlock()
	return &EnvironmentState{SandboxID: r.SandboxID, Status: "pending"}, nil
}
func (e *countingRecoveryEnvironment) Relinquish(context.Context, Ownership, string) error {
	e.mu.Lock()
	e.relinquishes++
	e.mu.Unlock()
	return nil
}

func TestTwoServicesLeaseOneRecoveryOwnerAndTakeOverAfterShutdown(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	base := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`, executorRef: `encrypted:{"executor_key":"executor"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", UpstreamEnvironmentID: "environment", SandboxID: "sandbox", Status: "active", Readiness: "pending", CleanupStatus: "active", Environment: EnvironmentSpec{Type: "self_hosted"}}}
	record := RecoveryRecord{Ownership: o, Session: *base.session}
	leasing := &sharedLeaseState{}
	env1, env2 := &countingRecoveryEnvironment{}, &countingRecoveryEnvironment{}
	upstream := &countingRecoveryUpstream{}
	s1, err := NewService(&leasedRecoveryStore{recoveryStore: &recoveryStore{restartStore: base, records: []RecoveryRecord{record}}, leases: leasing}, testCipher{}, upstream, env1, nil, Config{LeaseDuration: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewService(&leasedRecoveryStore{recoveryStore: &recoveryStore{restartStore: base, records: []RecoveryRecord{record}}, leases: leasing}, testCipher{}, upstream, env2, nil, Config{LeaseDuration: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	s1.SetRecoveryAuthorizer(allowRecovery{})
	s2.SetRecoveryAuthorizer(allowRecovery{})
	if err = s1.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s2.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if env1.reconciles+env2.reconciles != 1 {
		t.Fatalf("reconciles=%d, want one owner", env1.reconciles+env2.reconciles)
	}
	deadline := time.Now().Add(time.Second)
	for {
		upstream.mu.Lock()
		streams := upstream.streams
		upstream.mu.Unlock()
		if streams == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("streams=%d, want one owner", streams)
		}
		time.Sleep(time.Millisecond)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s1.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err = s2.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if env2.reconciles != 1 {
		t.Fatalf("takeover reconciles=%d, want 1", env2.reconciles)
	}
	_ = s2.Close(closeCtx)
}

func TestLocalSessionOwnershipIsScopedToOrganizationAndInstance(t *testing.T) {
	owner := Ownership{OrganizationID: "org-a", InstanceID: "instance-a", PrincipalID: "owner", Role: "admin"}
	leasing := &sharedLeaseState{}
	store := &leasedRecoveryStore{recoveryStore: &recoveryStore{restartStore: &restartStore{}}, leases: leasing}
	service, err := NewService(store, testCipher{}, &ambiguousUpstream{}, nil, nil, Config{LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	owned, newlyOwned, err := service.acquireSessionOwnership(context.Background(), owner, "session")
	if err != nil || !owned || !newlyOwned {
		t.Fatalf("initial ownership owned=%v new=%v err=%v", owned, newlyOwned, err)
	}
	originalContext := service.sessionOwnershipContext("session")
	collaborator := owner
	collaborator.PrincipalID = "collaborator"
	collaborator.Role = "member"
	owned, newlyOwned, err = service.acquireSessionOwnership(context.Background(), collaborator, "session")
	if err != nil || !owned || newlyOwned {
		t.Fatalf("collaborator ownership owned=%v new=%v err=%v", owned, newlyOwned, err)
	}
	for _, foreign := range []Ownership{
		{OrganizationID: "org-b", InstanceID: owner.InstanceID, PrincipalID: "foreign", Role: "admin"},
		{OrganizationID: owner.OrganizationID, InstanceID: "instance-b", PrincipalID: "foreign", Role: "admin"},
	} {
		owned, newlyOwned, err = service.acquireSessionOwnership(context.Background(), foreign, "session")
		if !errors.Is(err, ErrNotFound) || owned || newlyOwned {
			t.Fatalf("foreign %+v owned=%v new=%v err=%v", foreign, owned, newlyOwned, err)
		}
		select {
		case <-originalContext.Done():
			t.Fatal("foreign request canceled original ownership")
		default:
		}
		leasing.mu.Lock()
		holder := leasing.holder
		leasing.mu.Unlock()
		if holder != service.holderID {
			t.Fatalf("foreign request changed lease holder to %q", holder)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseDeadlineRelinquishesExecutorWhenRenewalStalls(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	env := &countingRecoveryEnvironment{}
	s, err := NewService(&stalledLeaseStore{restartStore: &restartStore{}}, testCipher{}, &ambiguousUpstream{}, env, nil, Config{LeaseDuration: 60 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if owned, _, err := s.acquireSessionOwnership(context.Background(), o, "session"); err != nil || !owned {
		t.Fatalf("owned=%v err=%v", owned, err)
	}
	deadline := time.Now().Add(150 * time.Millisecond)
	for {
		env.mu.Lock()
		relinquished := env.relinquishes
		env.mu.Unlock()
		if relinquished == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("executor ownership remained active past lease deadline")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOngoingAuthorizationRevocationCancelsStreamAndRelinquishesOwnership(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	base := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", Status: "active", Readiness: "connected", CleanupStatus: "active"}}
	leasing := &sharedLeaseState{}
	store := &leasedRecoveryStore{recoveryStore: &recoveryStore{restartStore: base}, leases: leasing}
	environment := &countingRecoveryEnvironment{}
	stream := &revocationStream{started: make(chan struct{}), canceled: make(chan struct{}), lateErr: make(chan error, 1)}
	authorizer := &mutableRecoveryAuthorizer{allowed: true}
	service, err := NewService(store, testCipher{}, stream, environment, nil, Config{LeaseDuration: 60 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRecoveryAuthorizer(authorizer)
	service.startSupervisor(o, "session", "upstream")
	select {
	case <-stream.started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	authorizer.revoke()
	select {
	case <-stream.canceled:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("revocation did not cancel stream within one ownership interval")
	}
	if lateErr := <-stream.lateErr; !errors.Is(lateErr, context.Canceled) {
		t.Fatalf("late event error=%v, want canceled", lateErr)
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		environment.mu.Lock()
		relinquished := environment.relinquishes
		environment.mu.Unlock()
		leasing.mu.Lock()
		holder := leasing.holder
		leasing.mu.Unlock()
		if relinquished == 1 && holder == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relinquishes=%d holder=%q", relinquished, holder)
		}
		time.Sleep(time.Millisecond)
	}
	if len(base.events) != 0 {
		t.Fatalf("events after revocation=%d, want 0", len(base.events))
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestStalledAuthorizationCannotExtendLeasePastDeadline(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	environment := &countingRecoveryEnvironment{}
	leasing := &sharedLeaseState{}
	store := &leasedRecoveryStore{recoveryStore: &recoveryStore{restartStore: &restartStore{}}, leases: leasing}
	authorizer := &stalledRecoveryAuthorizer{started: make(chan struct{}), release: make(chan struct{})}
	service, err := NewService(store, testCipher{}, &ambiguousUpstream{}, environment, nil, Config{LeaseDuration: 60 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRecoveryAuthorizer(authorizer)
	if owned, _, acquireErr := service.acquireSessionOwnership(context.Background(), o, "session"); acquireErr != nil || !owned {
		t.Fatalf("owned=%v err=%v", owned, acquireErr)
	}
	select {
	case <-authorizer.started:
	case <-time.After(time.Second):
		t.Fatal("authorization lookup did not start")
	}
	deadline := time.Now().Add(150 * time.Millisecond)
	for {
		environment.mu.Lock()
		relinquished := environment.relinquishes
		environment.mu.Unlock()
		leasing.mu.Lock()
		holder := leasing.holder
		leasing.mu.Unlock()
		if relinquished == 1 && holder == "" {
			break
		}
		if time.Now().After(deadline) {
			close(authorizer.release)
			t.Fatalf("stalled authorization extended ownership: relinquishes=%d holder=%q", relinquished, holder)
		}
		time.Sleep(time.Millisecond)
	}
	close(authorizer.release)
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestDelayedLeaseResponseDoesNotCreateAlreadyExpiredLocalOwner(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance"}
	store := &delayedAcquireLeaseStore{stalledLeaseStore: &stalledLeaseStore{restartStore: &restartStore{}}, delay: 50 * time.Millisecond}
	s, err := NewService(store, testCipher{}, &ambiguousUpstream{}, nil, nil, Config{LeaseDuration: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	owned, _, err := s.acquireSessionOwnership(context.Background(), o, "session")
	if owned || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("owned=%v err=%v", owned, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.Close(ctx)
}

func TestLateRenewalResponseCannotExtendLocalOwnership(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance"}
	env := &countingRecoveryEnvironment{}
	store := &lateRenewLeaseStore{stalledLeaseStore: &stalledLeaseStore{restartStore: &restartStore{}}, delay: 100 * time.Millisecond}
	s, err := NewService(store, testCipher{}, &ambiguousUpstream{}, env, nil, Config{LeaseDuration: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if owned, _, err := s.acquireSessionOwnership(context.Background(), o, "session"); err != nil || !owned {
		t.Fatalf("owned=%v err=%v", owned, err)
	}
	deadline := time.Now().Add(70 * time.Millisecond)
	for {
		env.mu.Lock()
		n := env.relinquishes
		env.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late renewal response extended expired ownership")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.Close(ctx)
}
