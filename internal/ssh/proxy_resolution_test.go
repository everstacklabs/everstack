package ssh

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/jmoiron/sqlx"
)

type resolutionKey struct {
	id       int64
	userID   string
	tenantID string
}

func newResolutionProxy(t *testing.T, keys []resolutionKey, instances map[string]*sandbox.Instance) (*Proxy, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	rows := sqlmock.NewRows([]string{
		"id", "user_id", "tenant_id", "name", "public_key", "fingerprint", "key_type", "last_used_at", "created_at",
	})
	for i, key := range keys {
		rows.AddRow(key.id, key.userID, key.tenantID, "laptop", "ssh-ed25519 AAAA", "SHA256:test", "ssh-ed25519", nil, time.Date(2026, 11, 17, 12, i, 0, 0, time.UTC).Format(time.RFC3339))
	}
	mock.ExpectQuery(`FROM user_ssh_keys`).WillReturnRows(rows)
	mgr := &sandbox.SandboxManager{}
	mgr.SeedInstancesForTest(instances)
	mgr.SetDBForTest(sqlx.NewDb(rawDB, "sqlmock"))
	return NewProxy(ProxyConfig{
		KeyStore:       NewKeyStore(sqlx.NewDb(rawDB, "sqlmock")),
		SandboxManager: mgr,
	}), mock
}

func expectIdentifierMiss(mock sqlmock.Sqlmock, identifier, tenantID string) {
	mock.ExpectQuery(`FROM sandbox_instances\s+WHERE \(id = \$1 OR short_code = \$1\) AND tenant_id = \$2`).
		WithArgs(identifier, tenantID).
		WillReturnError(sql.ErrNoRows)
}

func expectNameMiss(mock sqlmock.Sqlmock, name, tenantID string) {
	mock.ExpectQuery(`FROM sandbox_instances\s+WHERE name = \$1 AND tenant_id = \$2`).
		WithArgs(name, tenantID).
		WillReturnError(sql.ErrNoRows)
}

func runningSSHInstance(id, tenantID, name, shortCode string) *sandbox.Instance {
	return &sandbox.Instance{
		ID:             id,
		Name:           name,
		ShortCode:      shortCode,
		Status:         sandbox.StatusRunning,
		LifecycleState: sandbox.LifecycleRunning,
		Config:         sandbox.InstanceConfig{TenantID: tenantID},
	}
}

func expectSSHAccess(mock sqlmock.Sqlmock, sandboxID, userID, tenantID string, keyID int64) {
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM sandbox_ssh_access`).
		WithArgs(sandboxID, userID, tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`UPDATE user_ssh_keys SET last_used_at`).
		WithArgs(keyID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func waitForSSHExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if err := mock.ExpectationsWereMet(); err == nil {
			return
		} else if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPublicKeyAuthShortCodeWinsAcrossTenantKeyOrder(t *testing.T) {
	orders := []struct {
		name string
		keys []resolutionKey
	}{
		{name: "name row newer", keys: []resolutionKey{{1, "attacker-user", "tenant-attacker"}, {2, "victim-user", "tenant-victim"}}},
		{name: "short code row newer", keys: []resolutionKey{{2, "victim-user", "tenant-victim"}, {1, "attacker-user", "tenant-attacker"}}},
	}
	for _, test := range orders {
		t.Run(test.name, func(t *testing.T) {
			p, mock := newResolutionProxy(t, test.keys, map[string]*sandbox.Instance{
				"sbx-attacker": runningSSHInstance("sbx-attacker", "tenant-attacker", "v1ct1m", "attacker1"),
				"sbx-victim":   runningSSHInstance("sbx-victim", "tenant-victim", "victim", "v1ct1m"),
			})
			if test.keys[0].tenantID == "tenant-attacker" {
				expectIdentifierMiss(mock, "v1ct1m", "tenant-attacker")
			}
			expectSSHAccess(mock, "sbx-victim", "victim-user", "tenant-victim", 2)
			permissions, err := p.publicKeyCallback(fakeConnMeta{user: "v1ct1m"}, testPublicKey(t))
			if err != nil {
				t.Fatal(err)
			}
			if got := permissions.Extensions["sandbox_id"]; got != "sbx-victim" {
				t.Fatalf("sandbox_id = %q, want sbx-victim", got)
			}
			waitForSSHExpectations(t, mock)
		})
	}
}

func TestPublicKeyAuthExactIDWinsOverAnotherTenantName(t *testing.T) {
	p, mock := newResolutionProxy(t, []resolutionKey{
		{1, "attacker-user", "tenant-attacker"},
		{2, "victim-user", "tenant-victim"},
	}, map[string]*sandbox.Instance{
		"sbx-attacker": runningSSHInstance("sbx-attacker", "tenant-attacker", "sbx-victim", "attacker1"),
		"sbx-victim":   runningSSHInstance("sbx-victim", "tenant-victim", "victim", "victim1"),
	})
	mock.MatchExpectationsInOrder(false)
	expectIdentifierMiss(mock, "sbx-victim", "tenant-attacker")
	// Both candidate tenants have a valid grant. Identifier ordering alone
	// must select the requested sandbox.
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM sandbox_ssh_access`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`UPDATE user_ssh_keys SET last_used_at`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	permissions, err := p.publicKeyCallback(fakeConnMeta{user: "sbx-victim"}, testPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := permissions.Extensions["sandbox_id"]; got != "sbx-victim" {
		t.Fatalf("sandbox_id = %q, want sbx-victim", got)
	}
	waitForSSHExpectations(t, mock)
}

func TestPublicKeyAuthRefusesAmbiguousSandboxName(t *testing.T) {
	p, mock := newResolutionProxy(t, []resolutionKey{
		{1, "user-a", "tenant-a"},
		{2, "user-b", "tenant-b"},
	}, map[string]*sandbox.Instance{
		"sbx-a": runningSSHInstance("sbx-a", "tenant-a", "shared", "code-a"),
		"sbx-b": runningSSHInstance("sbx-b", "tenant-b", "shared", "code-b"),
	})
	expectIdentifierMiss(mock, "shared", "tenant-a")
	expectIdentifierMiss(mock, "shared", "tenant-b")
	_, err := p.publicKeyCallback(fakeConnMeta{user: "shared"}, testPublicKey(t))
	if err == nil || !strings.Contains(err.Error(), "connect with the sandbox id or short code") {
		t.Fatalf("error = %v, want an id-or-short-code instruction", err)
	}
	if errors.Is(err, errInstancePaused) {
		t.Fatalf("error = %v, want ambiguity refusal", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicKeyAuthAllowsUniqueSandboxNameAcrossCandidateTenants(t *testing.T) {
	p, mock := newResolutionProxy(t, []resolutionKey{
		{1, "user-a", "tenant-a"},
		{2, "user-b", "tenant-b"},
	}, map[string]*sandbox.Instance{
		"sbx-a": runningSSHInstance("sbx-a", "tenant-a", "unique", "code-a"),
		"sbx-b": runningSSHInstance("sbx-b", "tenant-b", "other", "code-b"),
	})
	expectIdentifierMiss(mock, "unique", "tenant-a")
	expectIdentifierMiss(mock, "unique", "tenant-b")
	expectNameMiss(mock, "unique", "tenant-b")
	expectSSHAccess(mock, "sbx-a", "user-a", "tenant-a", 1)
	permissions, err := p.publicKeyCallback(fakeConnMeta{user: "unique"}, testPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := permissions.Extensions["sandbox_id"]; got != "sbx-a" {
		t.Fatalf("sandbox_id = %q, want sbx-a", got)
	}
	waitForSSHExpectations(t, mock)
}

func TestPublicKeyAuthRefusesIdentifierLookupDatabaseErrorBeforeNamePass(t *testing.T) {
	p, mock := newResolutionProxy(t, []resolutionKey{
		{1, "user-a", "tenant-a"},
		{2, "user-b", "tenant-b"},
	}, map[string]*sandbox.Instance{
		"sbx-a": runningSSHInstance("sbx-a", "tenant-a", "shared", "code-a"),
		"sbx-b": runningSSHInstance("sbx-b", "tenant-b", "shared", "code-b"),
	})
	mock.ExpectQuery(`FROM sandbox_instances\s+WHERE \(id = \$1 OR short_code = \$1\) AND tenant_id = \$2`).
		WithArgs("shared", "tenant-a").
		WillReturnError(errors.New("database temporarily unavailable"))

	_, err := p.publicKeyCallback(fakeConnMeta{user: "shared"}, testPublicKey(t))
	if err == nil || err.Error() != "auth failed" {
		t.Fatalf("error = %v, want fail-closed authentication error", err)
	}
	if strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %v, name pass ran after identifier lookup failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicKeyAuthRefusesNameLookupDatabaseError(t *testing.T) {
	p, mock := newResolutionProxy(t, []resolutionKey{{1, "user-a", "tenant-a"}}, nil)
	p.SetTenantActiveFn(func(context.Context, string) bool { return false })
	expectIdentifierMiss(mock, "shared", "tenant-a")
	mock.ExpectQuery(`FROM sandbox_instances\s+WHERE name = \$1 AND tenant_id = \$2`).
		WithArgs("shared", "tenant-a").
		WillReturnError(errors.New("database temporarily unavailable"))

	_, err := p.publicKeyCallback(fakeConnMeta{user: "shared"}, testPublicKey(t))
	if err == nil || err.Error() != "auth failed" {
		t.Fatalf("error = %v, want fail-closed authentication error", err)
	}
	if errors.Is(err, errInstancePaused) {
		t.Fatalf("error = %v, paused fallback ran after name lookup failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type fixedSandboxLookup struct {
	identifierMatches map[string]*sandbox.Instance
	nameMatches       map[string]*sandbox.Instance
}

func (fixedSandboxLookup) GetBySandboxIDOrNameInScope(string, sandbox.TenantInstanceScope) (*sandbox.Instance, bool) {
	return nil, false
}

func (fixedSandboxLookup) LookupInstanceByIDFromDBInScope(context.Context, string, sandbox.TenantInstanceScope) (*sandbox.Instance, error) {
	return nil, sql.ErrNoRows
}

func (f fixedSandboxLookup) GetBySandboxIDOrShortCodeInScope(_ string, scope sandbox.TenantInstanceScope) (*sandbox.Instance, bool) {
	inst := f.identifierMatches[scope.SandboxTenantID()]
	return inst, inst != nil
}

func (fixedSandboxLookup) LookupInstanceByIDOrShortCodeFromDBInScope(context.Context, string, sandbox.TenantInstanceScope) (*sandbox.Instance, error) {
	return nil, sql.ErrNoRows
}

func (f fixedSandboxLookup) GetBySandboxNameInScope(_ string, scope sandbox.TenantInstanceScope) (*sandbox.Instance, bool) {
	inst := f.nameMatches[scope.SandboxTenantID()]
	return inst, inst != nil
}

func (fixedSandboxLookup) LookupInstanceByNameFromDBInScope(context.Context, string, sandbox.TenantInstanceScope) (*sandbox.Instance, error) {
	return nil, sql.ErrNoRows
}

func TestPublicKeyAuthStoppedShortCodeDoesNotFallThroughToForeignNames(t *testing.T) {
	p, mock := newResolutionProxy(t, []resolutionKey{
		{1, "stopped-user", "tenant-stopped"},
		{2, "foreign-a-user", "tenant-foreign-a"},
		{3, "foreign-b-user", "tenant-foreign-b"},
	}, nil)
	stopped := runningSSHInstance("sbx-stopped", "tenant-stopped", "stopped", "shared")
	stopped.Status = sandbox.StatusStopped
	stopped.LifecycleState = sandbox.LifecycleStopped
	p.sandboxLookup = fixedSandboxLookup{
		identifierMatches: map[string]*sandbox.Instance{"tenant-stopped": stopped},
		nameMatches: map[string]*sandbox.Instance{
			"tenant-foreign-a": runningSSHInstance("sbx-foreign-a", "tenant-foreign-a", "shared", "code-a"),
			"tenant-foreign-b": runningSSHInstance("sbx-foreign-b", "tenant-foreign-b", "shared", "code-b"),
		},
	}

	_, err := p.publicKeyCallback(fakeConnMeta{user: "shared"}, testPublicKey(t))
	if err == nil || err.Error() != "auth failed" {
		t.Fatalf("error = %v, want stopped identifier refusal", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicKeyAuthNamePassRequiresRunningSandbox(t *testing.T) {
	p, mock := newResolutionProxy(t, []resolutionKey{
		{1, "stopped-user", "tenant-stopped"},
		{2, "running-user", "tenant-running"},
	}, nil)
	stopped := runningSSHInstance("sbx-stopped", "tenant-stopped", "shared", "stopped-code")
	stopped.Status = sandbox.StatusStopped
	stopped.LifecycleState = sandbox.LifecycleStopped
	p.sandboxLookup = fixedSandboxLookup{nameMatches: map[string]*sandbox.Instance{
		"tenant-stopped": stopped,
		"tenant-running": runningSSHInstance("sbx-running", "tenant-running", "shared", "running-code"),
	}}
	expectSSHAccess(mock, "sbx-running", "running-user", "tenant-running", 2)

	permissions, err := p.publicKeyCallback(fakeConnMeta{user: "shared"}, testPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := permissions.Extensions["sandbox_id"]; got != "sbx-running" {
		t.Fatalf("sandbox_id = %q, want sbx-running", got)
	}
	waitForSSHExpectations(t, mock)
}
