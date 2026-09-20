package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestPostgresRepositoryEnforcesBothTenantDimensions(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	repo, _ := NewPostgresRepository(sqlx.NewDb(raw, "sqlmock"))
	now := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	mock.ExpectQuery("WITH owned AS").
		WithArgs("local", "org-a", "instance-a", "upstream", "event", "turn", int64(7), int64Pointer(1), nil, int64Pointer(1), now).
		WillReturnRows(sqlmock.NewRows([]string{"owned", "accepted", "changed"}).AddRow(false, false, false))
	changed, err := repo.RecordTurnUsage(context.Background(), TurnUsage{
		Scope:             Scope{OrganizationID: "org-a", InstanceID: "instance-a", LocalSessionID: "local"},
		UpstreamSessionID: "upstream", UpstreamTurnID: "turn", EventID: "event", Cursor: 7,
		InputTokens: int64Pointer(1), TotalTokens: int64Pointer(1), ObservedAt: now,
	})
	if changed || !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign ownership changed=%v err=%v", changed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionUsageReportedSparseZero(t *testing.T) {
	raw, mock, _ := sqlmock.New()
	t.Cleanup(func() { raw.Close() })
	repo, _ := NewPostgresRepository(sqlx.NewDb(raw, "sqlmock"))
	now := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT COUNT").WithArgs("local", "org", "instance").
		WillReturnRows(sqlmock.NewRows([]string{"count", "input", "output", "total", "updated"}).AddRow(1, nil, int64(0), int64(0), now))
	got, err := repo.SessionUsage(context.Background(), Scope{OrganizationID: "org", InstanceID: "instance", LocalSessionID: "local"})
	if err != nil || got.Status != UsageReported || got.InputTokens != nil || got.OutputTokens == nil || *got.OutputTokens != 0 || got.TotalTokens == nil || got.UpdatedAt == nil {
		t.Fatalf("snapshot=%#v err=%v", got, err)
	}
}

func TestSessionUsageUnavailableAndForeign(t *testing.T) {
	raw, mock, _ := sqlmock.New()
	t.Cleanup(func() { raw.Close() })
	repo, _ := NewPostgresRepository(sqlx.NewDb(raw, "sqlmock"))
	scope := Scope{OrganizationID: "org-a", InstanceID: "instance-a", LocalSessionID: "local"}
	mock.ExpectQuery("SELECT COUNT").WithArgs("local", "org-a", "instance-a").
		WillReturnRows(sqlmock.NewRows([]string{"count", "input", "output", "total", "updated"}).AddRow(0, nil, nil, nil, nil))
	got, err := repo.SessionUsage(context.Background(), scope)
	if err != nil || got.Status != UsageUnavailable {
		t.Fatalf("snapshot=%#v err=%v", got, err)
	}
	mock.ExpectQuery("SELECT COUNT").WithArgs("local", "org-b", "instance-b").WillReturnError(sqlmock.ErrCancelled)
	_, err = repo.SessionUsage(context.Background(), Scope{OrganizationID: "org-b", InstanceID: "instance-b", LocalSessionID: "local"})
	if err == nil {
		t.Fatal("foreign scope unexpectedly succeeded")
	}
}

func int64Pointer(value int64) *int64 { return &value }
