package moderation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/hosting/moderation"
)

func TestPostgresStoreDurablyRecordsDesiredTakedownState(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	store := moderation.NewPostgresStore(sqlx.NewDb(db, "sqlmock"))
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, moderation_generation, status[[:space:]]+FROM sites").
		WithArgs("release-notes").
		WillReturnRows(sqlmock.NewRows([]string{"id", "moderation_generation", "status"}).AddRow("site-1", 0, "active"))
	mock.ExpectQuery("INSERT INTO site_moderation_actions").
		WithArgs(
			"site-1", "release-notes", int64(1), string(moderation.ActionKindTakedown),
			string(moderation.ReasonPhishing), "Confirmed credential collection.",
			"operator-1", "request-1",
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "site_id", "slug", "generation", "action", "status", "reason", "note", "requested_by",
			"idempotency_key", "attempt_count", "last_error", "lease_token", "created_at", "applied_at",
		}).AddRow(
			"action-1", "site-1", "release-notes", int64(1), "takedown", "pending", "phishing",
			"Confirmed credential collection.", "operator-1", "request-1", 0, nil, "", now, nil,
		))
	mock.ExpectExec("UPDATE site_moderation_actions").
		WithArgs("release-notes", "action-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE sites").
		WithArgs("site-1", string(moderation.ReasonPhishing), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	action, err := store.BeginAction(context.Background(), moderation.Command{
		Slug: "release-notes", Kind: moderation.ActionKindTakedown, Reason: moderation.ReasonPhishing,
		Note: "Confirmed credential collection.", ActorID: "operator-1", IdempotencyKey: "request-1",
	})
	if err != nil {
		t.Fatalf("begin action: %v", err)
	}
	if action.ID != "action-1" || action.Status != moderation.ActionStatusPending {
		t.Fatalf("action = %+v", action)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestPostgresStoreTakedownPreservesDeletingState(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	store := moderation.NewPostgresStore(sqlx.NewDb(db, "sqlmock"))
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, moderation_generation, status[[:space:]]+FROM sites").
		WithArgs("release-notes").
		WillReturnRows(sqlmock.NewRows([]string{"id", "moderation_generation", "status"}).AddRow("site-1", 4, "deleting"))
	mock.ExpectQuery("INSERT INTO site_moderation_actions").
		WithArgs(
			"site-1", "release-notes", int64(5), string(moderation.ActionKindTakedown),
			string(moderation.ReasonPhishing), "Deletion cleanup is stuck.",
			"operator-1", "request-deleting-1",
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "site_id", "slug", "generation", "action", "status", "reason", "note", "requested_by",
			"idempotency_key", "attempt_count", "last_error", "lease_token", "created_at", "applied_at",
		}).AddRow(
			"action-2", "site-1", "release-notes", int64(5), "takedown", "pending", "phishing",
			"Deletion cleanup is stuck.", "operator-1", "request-deleting-1", 0, nil, "", now, nil,
		))
	mock.ExpectExec("UPDATE site_moderation_actions").
		WithArgs("release-notes", "action-2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET status = CASE WHEN status = 'deleting' THEN 'deleting' ELSE 'disabled' END").
		WithArgs("site-1", string(moderation.ReasonPhishing), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if _, err := store.BeginAction(context.Background(), moderation.Command{
		Slug: "release-notes", Kind: moderation.ActionKindTakedown, Reason: moderation.ReasonPhishing,
		Note: "Deletion cleanup is stuck.", ActorID: "operator-1", IdempotencyKey: "request-deleting-1",
	}); err != nil {
		t.Fatalf("takedown deleting site: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestPostgresStoreRejectsRestoreWhileSiteIsDeleting(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	store := moderation.NewPostgresStore(sqlx.NewDb(db, "sqlmock"))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, moderation_generation, status[[:space:]]+FROM sites").
		WithArgs("release-notes").
		WillReturnRows(sqlmock.NewRows([]string{"id", "moderation_generation", "status"}).AddRow("site-1", 4, "deleting"))
	mock.ExpectRollback()

	_, err = store.BeginAction(context.Background(), moderation.Command{
		Slug: "release-notes", Kind: moderation.ActionKindRestore,
		ActorID: "operator-1", IdempotencyKey: "restore-deleting-1",
	})
	if !errors.Is(err, moderation.ErrInvalidAction) {
		t.Fatalf("restore error = %v, want invalid action", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestPostgresStoreReadsAnInstanceHostingOverview(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	store := moderation.NewPostgresStore(sqlx.NewDb(db, "sqlmock"))

	mock.ExpectQuery("SELECT[[:space:]]+COUNT\\(\\*\\)::bigint AS total_sites").
		WillReturnRows(sqlmock.NewRows([]string{
			"total_sites", "active_sites", "anonymous_sites", "expiring_sites",
			"disabled_sites", "total_bytes", "open_reports", "pending_actions",
		}).AddRow(128, 115, 20, 9, 3, 1048576, 4, 1))

	overview, err := store.Overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.TotalSites != 128 || overview.OpenReports != 4 || overview.PendingActions != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestPostgresStoreClaimsOnlyCurrentPendingGenerations(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	store := moderation.NewPostgresStore(sqlx.NewDb(db, "sqlmock"))
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("a.generation = s.moderation_generation[\\s\\S]+s.status <> 'deleted'[\\s\\S]+FOR UPDATE OF a SKIP LOCKED").
		WithArgs(50).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "site_id", "slug", "generation", "action", "status", "reason", "note", "requested_by",
			"idempotency_key", "attempt_count", "last_error", "lease_token", "created_at", "applied_at",
		}).AddRow(
			"action-1", "site-1", "release-notes", int64(3), "takedown", "pending", "phishing", "",
			"operator-1", "request-1", 1, "retry", "lease-1", now, nil,
		))

	actions, err := store.ListPending(context.Background(), 50)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(actions) != 1 || actions[0].Generation != 3 || actions[0].LeaseToken != "lease-1" {
		t.Fatalf("claimed actions = %+v", actions)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestPostgresStoreCompletesAnAttemptWithGenerationAndLeaseCAS(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	store := moderation.NewPostgresStore(sqlx.NewDb(db, "sqlmock"))
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	action := moderation.Action{
		ID: "action-1", SiteID: "site-1", Slug: "release-notes", Generation: 3,
		Kind: moderation.ActionKindTakedown, Status: moderation.ActionStatusPending,
		ActorID: "operator-1", IdempotencyKey: "request-1", LeaseToken: "lease-1", CreatedAt: now,
	}

	mock.ExpectQuery("WHERE id = \\$1 AND generation = \\$5 AND status = 'pending'").
		WithArgs("action-1", "applied", "", sqlmock.AnyArg(), int64(3), "lease-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "site_id", "slug", "generation", "action", "status", "reason", "note", "requested_by",
			"idempotency_key", "attempt_count", "last_error", "lease_token", "created_at", "applied_at",
		}).AddRow(
			"action-1", "site-1", "release-notes", int64(3), "takedown", "applied", "", "",
			"operator-1", "request-1", 1, "", "", now, now,
		))

	completed, err := store.CompleteAttempt(context.Background(), action, moderation.AttemptOutcome{Applied: true})
	if err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	if completed.Status != moderation.ActionStatusApplied || completed.AttemptCount != 1 {
		t.Fatalf("completed action = %+v", completed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestPostgresStoreListsSitesAcrossTenantsForOperators(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	store := moderation.NewPostgresStore(sqlx.NewDb(db, "sqlmock"))
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	tenantID := "tenant-1"

	mock.ExpectQuery("FROM sites s").WithArgs("", "", 51, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "access", "spa_fallback",
			"current_version", "total_bytes", "file_count", "kill_switch", "takedown_reason",
			"expires_at", "created_at", "last_published_at", "open_report_count", "enforcement_status",
		}).AddRow(
			"site-1", "release-notes", tenantID, "user-1", "active", "public", true,
			2, 4096, 3, false, "", nil, now, now, 2, "pending",
		))

	page, err := store.ListSites(context.Background(), moderation.ListOptions{Limit: 50})
	if err != nil {
		t.Fatalf("list sites: %v", err)
	}
	if len(page.Sites) != 1 || page.Sites[0].Slug != "release-notes" {
		t.Fatalf("sites = %+v", page.Sites)
	}
	if page.Sites[0].TenantID == nil || *page.Sites[0].TenantID != tenantID || page.Sites[0].OpenReportCount != 2 {
		t.Fatalf("operator site = %+v", page.Sites[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestPostgresStoreListsTheAbuseQueue(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	store := moderation.NewPostgresStore(sqlx.NewDb(db, "sqlmock"))
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("FROM site_abuse_reports r").WithArgs("open", "", 51, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "reason", "details", "page_path", "status", "created_at", "updated_at", "resolved_at",
		}).AddRow(
			"report-1", "release-notes", "phishing", "Credential collection", "/login", "open", now, now, nil,
		))

	page, err := store.ListReports(context.Background(), moderation.ReportListOptions{Status: "open", Limit: 50})
	if err != nil {
		t.Fatalf("list reports: %v", err)
	}
	if len(page.Reports) != 1 || page.Reports[0].Reason != moderation.ReasonPhishing {
		t.Fatalf("reports = %+v", page.Reports)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
