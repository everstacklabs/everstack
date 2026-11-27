package sandbox

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/robfig/cron/v3"
)

func pausedFn(paused ...string) (func(context.Context, string) bool, *atomic.Int32) {
	var calls atomic.Int32
	set := make(map[string]bool, len(paused))
	for _, id := range paused {
		set[id] = true
	}
	return func(_ context.Context, tenantID string) bool {
		calls.Add(1)
		return !set[tenantID]
	}, &calls
}

func TestRequireSandboxBillingRefusesPausedTenant(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{}
	fn, _ := pausedFn("inst-paused")
	m.SetTenantActiveFn(fn)

	// No billing resolver configured: the pause gate must still apply.
	if err := m.RequireSandboxBilling("inst-paused"); !errors.Is(err, ErrTenantPaused) {
		t.Fatalf("RequireSandboxBilling(paused) = %v, want ErrTenantPaused", err)
	}
	if errors.Is(ErrTenantPaused, ErrSandboxBillingRequired) {
		t.Fatal("a paused instance must not be reported as a billing failure")
	}
	if err := m.RequireSandboxBilling("inst-active"); err != nil {
		t.Fatalf("active tenant refused: %v", err)
	}

	// A real allocation path is refused before the (nil) backend is touched.
	if _, err := m.ExecuteCreate(context.Background(), "sbx-1", InstanceConfig{TenantID: "inst-paused"}); !errors.Is(err, ErrTenantPaused) {
		t.Fatalf("ExecuteCreate(paused) = %v, want ErrTenantPaused", err)
	}

	// Pause takes precedence over billing, and nil clears the gate.
	m.SetSandboxBillingResolver(func(string) bool { return true })
	if err := m.RequireSandboxBilling("inst-paused"); !errors.Is(err, ErrTenantPaused) {
		t.Fatalf("with billing allowed, paused = %v, want ErrTenantPaused", err)
	}
	m.SetTenantActiveFn(nil)
	if err := m.RequireSandboxBilling("inst-paused"); err != nil {
		t.Fatalf("nil active fn must treat every tenant as active: %v", err)
	}
}

func TestResumeTenantAgentsLeavesPausedTenantParked(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()

	m := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	fn, calls := pausedFn("inst-paused")
	m.SetTenantActiveFn(fn)

	resumed, err := m.ResumeTenantAgents(context.Background(), "inst-paused")
	if err != nil || resumed != 0 {
		t.Fatalf("ResumeTenantAgents(paused) = (%d, %v), want (0, nil)", resumed, err)
	}
	if calls.Load() == 0 {
		t.Fatal("pause gate was not consulted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err) // no UPDATE may be issued
	}
}

func TestCronSkipsPausedTenantButAdvancesNextRun(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()

	m := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	s := &CronScheduler{
		manager: m,
		parser:  cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
	fn, calls := pausedFn("inst-paused")
	s.SetTenantActiveFn(fn)

	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs("cron_7").
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	// Only next_run_at moves: no run_count/error_count bump and no trigger row.
	mock.ExpectExec(`UPDATE sandbox_crons SET\s+next_run_at = \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2`).
		WithArgs(sqlmock.AnyArg(), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`SELECT pg_advisory_unlock`).WithArgs("cron_7").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// AutoRecreate with a config would call GetOrRecreate on a manager with no
	// backend; reaching it fails the test via unexpected queries or a panic.
	s.executeCron(context.Background(), SandboxCron{
		ID:            7,
		TenantID:      "inst-paused",
		SandboxID:     "sbx-1",
		SessionID:     "sess-1",
		Schedule:      "*/5 * * * *",
		Command:       "echo hi",
		AutoRecreate:  true,
		SandboxConfig: json.RawMessage(`{"image":"alpine"}`),
	})

	if calls.Load() == 0 {
		t.Fatal("pause gate was not consulted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCronFallsBackToManagerPauseGate(t *testing.T) {
	m := &SandboxManager{}
	fn, _ := pausedFn("inst-paused")
	m.SetTenantActiveFn(fn)
	s := &CronScheduler{manager: m}
	if s.tenantActive(context.Background(), "inst-paused") {
		t.Fatal("scheduler without its own gate must use the manager's")
	}
	if !s.tenantActive(context.Background(), "inst-active") {
		t.Fatal("active tenant reported paused")
	}
}

func TestWebhookRefusesPausedTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()

	const secret = "whsec"
	mock.ExpectQuery(`SELECT \* FROM sandbox_webhooks WHERE path = \$1 AND enabled = true`).
		WithArgs("hook-a").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "sandbox_id", "session_id", "path", "secret", "command",
			"enabled", "rate_limit_rpm", "auto_recreate", "sandbox_config",
		}).AddRow(int64(3), "inst-paused", "sbx-1", "sess-1", "hook-a", secret, "echo hi",
			true, 60, true, []byte(`{"image":"alpine"}`)))

	m := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	wr := NewWebhookRouter(m)
	fn, _ := pausedFn("inst-paused")
	wr.SetTenantActiveFn(fn)

	body := `{"x":1}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	req := httptest.NewRequest(http.MethodPost, "/wh/hook-a", strings.NewReader(body))
	req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	wr.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Error struct {
			Type, Code, Message string
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if got.Error.Type != "instance_paused" || got.Error.Code != "instance_paused" || got.Error.Message != "This instance is paused." {
		t.Fatalf("body = %+v", got)
	}
	// No trigger row, no compute: every further query would be unexpected.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
