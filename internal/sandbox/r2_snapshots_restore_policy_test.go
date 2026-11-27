package sandbox

import (
	"context"
	"database/sql"
	"reflect"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// policyCapturingRestorer is a VMRestorer that records the policy the
// manager hands it and restores nothing.
type policyCapturingRestorer struct {
	Backend
	got   *RestoreNetworkPolicy
	calls int
}

func (b *policyCapturingRestorer) Name() string { return "restore-fake" }

func (b *policyCapturingRestorer) RestoreVMSnapshot(_ context.Context, _, _, _ string, policy RestoreNetworkPolicy, _ snapshot.Store) (*Instance, error) {
	b.calls++
	p := policy
	b.got = &p
	return nil, nil
}

// enabledStore is any store that is not *snapshot.Disabled.
type enabledStore struct{ snapshot.Disabled }

const restorePolicyQuery = `SELECT config FROM sandbox_instances WHERE id = $1 AND tenant_id = $2`

func restoreThroughManager(t *testing.T, db *sqlx.DB) RestoreNetworkPolicy {
	t.Helper()
	backend := &policyCapturingRestorer{}
	m := &SandboxManager{
		db:                 db,
		backend:            backend,
		r2Snapshots:        &enabledStore{},
		instances:          map[string]*Instance{},
		instancesBySandbox: map[string]*Instance{},
		troopers:           map[string]*Instance{},
	}
	if _, err := m.RestoreFromR2Snapshot(context.Background(), "sbx-1", "trp-agent", "tenant-a", "agent"); err != nil {
		t.Fatalf("RestoreFromR2Snapshot: %v", err)
	}
	if backend.calls != 1 || backend.got == nil {
		t.Fatalf("restorer calls = %d, want 1", backend.calls)
	}
	return *backend.got
}

func restoreWithRecord(t *testing.T, config string) RestoreNetworkPolicy {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	mock.ExpectQuery(regexp.QuoteMeta(restorePolicyQuery)).
		WithArgs("sbx-1", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow([]byte(config)))
	policy := restoreThroughManager(t, sqlx.NewDb(raw, "sqlmock"))
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestRestoreFromR2Snapshot_WhitelistRecordRestoresAsWhitelist(t *testing.T) {
	got := restoreWithRecord(t, `{"tenant_id":"tenant-a","network_mode":"whitelist","allowed_hosts":["pypi.org","*.github.com"]}`)
	want := RestoreNetworkPolicy{Mode: NetworkWhitelist, AllowedHosts: []string{"pypi.org", "*.github.com"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
}

func TestRestoreFromR2Snapshot_DenyRecordRestoresAsDeny(t *testing.T) {
	got := restoreWithRecord(t, `{"tenant_id":"tenant-a","network_mode":"deny"}`)
	if got.Mode != NetworkDeny {
		t.Fatalf("mode = %q, want deny", got.Mode)
	}
}

func TestRestoreFromR2Snapshot_UnsetModeIsTheAllowDefault(t *testing.T) {
	// The sandbox was created with "" and firecracker normalised that to
	// allow, so restoring as allow is faithful, not a widening.
	got := restoreWithRecord(t, `{"tenant_id":"tenant-a"}`)
	if got.Mode != NetworkAllow {
		t.Fatalf("mode = %q, want allow", got.Mode)
	}
}

func TestRestoreFromR2Snapshot_FailsClosed(t *testing.T) {
	failClosed := RestoreNetworkPolicy{Mode: NetworkWhitelist}

	t.Run("no database", func(t *testing.T) {
		if got := restoreThroughManager(t, nil); !reflect.DeepEqual(got, failClosed) {
			t.Fatalf("policy = %+v, want %+v", got, failClosed)
		}
	})

	t.Run("no record for this tenant", func(t *testing.T) {
		raw, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		mock.ExpectQuery(regexp.QuoteMeta(restorePolicyQuery)).
			WithArgs("sbx-1", "tenant-a").
			WillReturnError(sql.ErrNoRows)
		if got := restoreThroughManager(t, sqlx.NewDb(raw, "sqlmock")); !reflect.DeepEqual(got, failClosed) {
			t.Fatalf("policy = %+v, want %+v", got, failClosed)
		}
	})

	for name, config := range map[string]string{
		"unparseable config":  `not json`,
		"unknown mode":        `{"tenant_id":"tenant-a","network_mode":"open"}`,
		"config names tenant": `{"tenant_id":"tenant-b","network_mode":"allow"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := restoreWithRecord(t, config); !reflect.DeepEqual(got, failClosed) {
				t.Fatalf("policy = %+v, want %+v", got, failClosed)
			}
		})
	}
}
