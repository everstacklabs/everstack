package v1

import (
	"context"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/jmoiron/sqlx"
)

func newSandboxTriggerTestServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	return &Server{
		db:         sqlx.NewDb(rawDB, "sqlmock"),
		sandboxMgr: &sandbox.SandboxManager{},
	}, mock
}

func cronRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "sandbox_id", "session_id", "name", "schedule", "command",
		"work_dir", "timeout_seconds", "enabled", "last_run_at", "next_run_at",
		"run_count", "error_count", "last_error", "auto_recreate", "sandbox_config",
		"created_at", "updated_at",
	})
}

func TestUpdateCronStaysInsideTheOwningTenant(t *testing.T) {
	const cronID int64 = 7
	name := "mine"
	query := regexp.QuoteMeta("UPDATE sandbox_crons SET updated_at = NOW(), name = $2 WHERE id = $1 AND tenant_id = $3 RETURNING *")

	t.Run("another tenant gets not found and no row changes", func(t *testing.T) {
		s, mock := newSandboxTriggerTestServer(t)
		mock.ExpectQuery(query).
			WithArgs(cronID, name, otherTenant).
			WillReturnRows(cronRows())

		_, err := s.UpdateCron(sessionPrincipal(otherTenant), connect.NewRequest(&agentspb.UpdateCronRequest{Id: cronID, Name: &name}))
		wantNotFound(t, "UpdateCron", err)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("owner updates the cron", func(t *testing.T) {
		s, mock := newSandboxTriggerTestServer(t)
		mock.ExpectQuery(query).
			WithArgs(cronID, name, ownerTenant).
			WillReturnRows(cronRows().AddRow(
				cronID, ownerTenant, "sbx-1", "sess-1", name, "0 * * * *", "echo ok",
				"/workspace", 300, true, nil, nil, 0, 0, nil, false, []byte("{}"), time.Now(), time.Now(),
			))

		resp, err := s.UpdateCron(sessionPrincipal(ownerTenant), connect.NewRequest(&agentspb.UpdateCronRequest{Id: cronID, Name: &name}))
		if err != nil {
			t.Fatalf("owner UpdateCron: %v", err)
		}
		if resp.Msg.GetCron().GetTenantId() != ownerTenant {
			t.Fatalf("updated cron tenant = %q", resp.Msg.GetCron().GetTenantId())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDeleteCronAndWebhookStayInsideTheOwningTenant(t *testing.T) {
	tests := []struct {
		name  string
		query string
		call  func(*Server, string) error
	}{
		{
			name:  "cron",
			query: "DELETE FROM sandbox_crons WHERE id = $1 AND tenant_id = $2",
			call: func(s *Server, tenantID string) error {
				_, err := s.DeleteCron(sessionPrincipal(tenantID), connect.NewRequest(&agentspb.DeleteCronRequest{Id: 7}))
				return err
			},
		},
		{
			name:  "webhook",
			query: "DELETE FROM sandbox_webhooks WHERE id = $1 AND tenant_id = $2",
			call: func(s *Server, tenantID string) error {
				_, err := s.DeleteWebhook(sessionPrincipal(tenantID), connect.NewRequest(&agentspb.DeleteWebhookRequest{Id: 7}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" another tenant gets not found", func(t *testing.T) {
			s, mock := newSandboxTriggerTestServer(t)
			mock.ExpectExec(regexp.QuoteMeta(tt.query)).
				WithArgs(int64(7), otherTenant).
				WillReturnResult(sqlmock.NewResult(0, 0))
			wantNotFound(t, tt.name, tt.call(s, otherTenant))
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})

		t.Run(tt.name+" owner deletes the row", func(t *testing.T) {
			s, mock := newSandboxTriggerTestServer(t)
			mock.ExpectExec(regexp.QuoteMeta(tt.query)).
				WithArgs(int64(7), ownerTenant).
				WillReturnResult(sqlmock.NewResult(0, 1))
			if err := tt.call(s, ownerTenant); err != nil {
				t.Fatalf("owner delete: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCronWebhookAndTriggerListsUseTheAuthenticatedTenant(t *testing.T) {
	tests := []struct {
		name       string
		countQuery string
		listQuery  string
		call       func(*Server) error
	}{
		{
			name:       "crons",
			countQuery: "SELECT COUNT(*) FROM sandbox_crons WHERE tenant_id = $1",
			listQuery:  "SELECT * FROM sandbox_crons WHERE tenant_id = $1 ORDER BY created_at DESC",
			call: func(s *Server) error {
				_, err := s.ListCrons(sessionPrincipal(ownerTenant), connect.NewRequest(&agentspb.ListCronsRequest{}))
				return err
			},
		},
		{
			name:       "webhooks",
			countQuery: "SELECT COUNT(*) FROM sandbox_webhooks WHERE tenant_id = $1",
			listQuery:  "SELECT * FROM sandbox_webhooks WHERE tenant_id = $1 ORDER BY created_at DESC",
			call: func(s *Server) error {
				_, err := s.ListWebhooks(sessionPrincipal(ownerTenant), connect.NewRequest(&agentspb.ListWebhooksRequest{}))
				return err
			},
		},
		{
			name:       "trigger history",
			countQuery: "SELECT COUNT(*) FROM sandbox_triggers st LEFT JOIN sandbox_crons sc ON st.trigger_type = 'cron' AND sc.id = st.trigger_id LEFT JOIN sandbox_webhooks sw ON st.trigger_type = 'webhook' AND sw.id = st.trigger_id WHERE COALESCE(sc.tenant_id, sw.tenant_id) = $1",
			listQuery:  "SELECT st.* FROM sandbox_triggers st LEFT JOIN sandbox_crons sc ON st.trigger_type = 'cron' AND sc.id = st.trigger_id LEFT JOIN sandbox_webhooks sw ON st.trigger_type = 'webhook' AND sw.id = st.trigger_id WHERE COALESCE(sc.tenant_id, sw.tenant_id) = $1 ORDER BY st.created_at DESC",
			call: func(s *Server) error {
				_, err := s.ListTriggers(sessionPrincipal(ownerTenant), connect.NewRequest(&agentspb.ListTriggersRequest{}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, mock := newSandboxTriggerTestServer(t)
			mock.ExpectQuery(regexp.QuoteMeta(tt.countQuery)).
				WithArgs(ownerTenant).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			mock.ExpectQuery(regexp.QuoteMeta(tt.listQuery)).
				WithArgs(ownerTenant).
				WillReturnRows(sqlmock.NewRows([]string{"id"}))
			if err := tt.call(s); err != nil {
				t.Fatalf("list: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunCronNowStaysInsideTheOwningTenant(t *testing.T) {
	const cronID int64 = 7
	lookup := regexp.QuoteMeta("SELECT 1 FROM sandbox_crons WHERE id = $1 AND tenant_id = $2")

	t.Run("another tenant gets not found and the cron does not run", func(t *testing.T) {
		s, mock := newSandboxTriggerTestServer(t)
		runs := 0
		s.runCronNow = func(_ context.Context, _ int64, _ string) error {
			runs++
			return nil
		}
		mock.ExpectQuery(lookup).
			WithArgs(cronID, otherTenant).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}))
		req := httptest.NewRequest("POST", "/v1/agents/crons/7/run", nil).WithContext(sessionPrincipal(otherTenant))
		rec := httptest.NewRecorder()
		s.handleRunCronNow(rec, req, map[string]string{"cron_id": "7"})
		if rec.Code != 404 || runs != 0 {
			t.Fatalf("status = %d, runs = %d", rec.Code, runs)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("owner runs the cron", func(t *testing.T) {
		s, mock := newSandboxTriggerTestServer(t)
		runs := 0
		s.runCronNow = func(_ context.Context, gotID int64, gotTenant string) error {
			if gotID != cronID {
				t.Fatalf("cron id = %d", gotID)
			}
			if gotTenant != ownerTenant {
				t.Fatalf("tenant id = %q", gotTenant)
			}
			runs++
			return nil
		}
		mock.ExpectQuery(lookup).
			WithArgs(cronID, ownerTenant).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
		req := httptest.NewRequest("POST", "/v1/agents/crons/7/run", nil).WithContext(sessionPrincipal(ownerTenant))
		rec := httptest.NewRecorder()
		s.handleRunCronNow(rec, req, map[string]string{"cron_id": "7"})
		if rec.Code != 200 || runs != 1 {
			t.Fatalf("status = %d, runs = %d", rec.Code, runs)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
