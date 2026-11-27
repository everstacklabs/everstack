package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// pausedTestStore records every call that would mean work ran or a failure
// was counted. It embeds a nil Store, so any unexpected call panics.
type pausedTestStore struct {
	Store
	mu          sync.Mutex
	byPath      map[string]*Trigger
	countCalls  int
	recordCalls int
	failures    int
	opened      int
}

func (s *pausedTestStore) GetTriggerByWebhookPath(_ context.Context, path string) (*Trigger, error) {
	if t, ok := s.byPath[path]; ok {
		return t, nil
	}
	return nil, errors.New("not found")
}

func (s *pausedTestStore) CountRunningExecutions(context.Context, string) (int, error) {
	s.mu.Lock()
	s.countCalls++
	s.mu.Unlock()
	return 0, nil
}

func (s *pausedTestStore) RecordExecution(_ context.Context, e *Execution) error {
	s.mu.Lock()
	s.recordCalls++
	s.mu.Unlock()
	e.ID = "execution-1"
	return nil
}

func (s *pausedTestStore) CompleteExecution(context.Context, string, ExecutionStatus, string, string, int) error {
	return nil
}

func (s *pausedTestStore) IncrementFailures(context.Context, string) (int, error) {
	s.mu.Lock()
	s.failures++
	s.mu.Unlock()
	return 1, nil
}

func (s *pausedTestStore) OpenCircuit(context.Context, string) error {
	s.mu.Lock()
	s.opened++
	s.mu.Unlock()
	return nil
}

func pausedSet(paused ...string) func(context.Context, string) bool {
	set := map[string]bool{}
	for _, p := range paused {
		set[p] = true
	}
	return func(_ context.Context, tenantID string) bool { return !set[tenantID] }
}

func TestExecuteSkipsPausedTenant(t *testing.T) {
	store := &pausedTestStore{}
	executor := NewExecutor(store, nil, nil, nil)
	executor.SetTenantActiveFn(pausedSet("inst-paused"))

	// A half-open circuit would normally take a probe and record the outcome;
	// a paused skip must leave it exactly as it was.
	trig := &Trigger{ID: "trigger-1", TenantID: "inst-paused", Type: TriggerCron, MaxConcurrent: 1, CircuitState: CircuitHalfOpen}
	err := executor.Execute(context.Background(), trig, nil)

	if !errors.Is(err, ErrTenantPaused) {
		t.Fatalf("Execute() error = %v, want ErrTenantPaused", err)
	}
	if store.countCalls != 0 || store.recordCalls != 0 {
		t.Fatalf("work started for a paused tenant: count=%d record=%d", store.countCalls, store.recordCalls)
	}
	if store.failures != 0 || store.opened != 0 || trig.CircuitState != CircuitHalfOpen {
		t.Fatalf("skip touched the circuit breaker: failures=%d opened=%d state=%s", store.failures, store.opened, trig.CircuitState)
	}
}

func TestExecuteNilTenantActiveFnRunsEveryone(t *testing.T) {
	store := &pausedTestStore{}
	executor := NewExecutor(store, nil, nil, nil)
	executor.SetTenantActiveFn(nil)

	// MaxConcurrent 0 stops right after the concurrency count, so reaching
	// CountRunningExecutions proves the tenant was treated as active.
	err := executor.Execute(context.Background(), &Trigger{ID: "trigger-1", TenantID: "inst-a"}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if store.countCalls != 1 {
		t.Fatalf("CountRunningExecutions() calls = %d, want 1", store.countCalls)
	}
}

func TestSchedulerSkipsPausedTenants(t *testing.T) {
	executor := NewExecutor(&pausedTestStore{}, nil, nil, nil)
	executor.SetTenantActiveFn(pausedSet("inst-paused"))
	scheduler := NewScheduler(nil, executor, nil)

	now := time.Date(2026, 9, 30, 12, 0, 5, 0, time.UTC)
	triggers := []*Trigger{
		{ID: "paused", TenantID: "inst-paused", CronExpression: "* * * * *"},
		{ID: "active", TenantID: "inst-active", CronExpression: "* * * * *"},
		{ID: "not-due", TenantID: "inst-active", CronExpression: "30 3 * * *"},
		{ID: "active-2", TenantID: "inst-other", CronExpression: "0 12 * * *"},
	}

	due := scheduler.dueTriggers(context.Background(), triggers, now)
	var ids []string
	for _, tr := range due {
		ids = append(ids, tr.ID)
	}
	if got := strings.Join(ids, ","); got != "active,active-2" {
		t.Fatalf("due triggers = %q, want active,active-2", got)
	}

	// Once resumed, the same trigger fires in its next window.
	executor.SetTenantActiveFn(nil)
	if got := len(scheduler.dueTriggers(context.Background(), triggers[:1], now)); got != 1 {
		t.Fatalf("resumed tenant due triggers = %d, want 1", got)
	}
}

func newWebhookFixture(paused ...string) (*WebhookHandler, *pausedTestStore) {
	store := &pausedTestStore{byPath: map[string]*Trigger{
		"hook-a": {ID: "trigger-a", TenantID: "inst-a", Type: TriggerWebhook, MaxConcurrent: 0},
	}}
	executor := NewExecutor(store, nil, nil, nil)
	executor.SetTenantActiveFn(pausedSet(paused...))
	wh := NewWebhookHandler(store, executor)
	wh.SetHostTenantResolver(func(_ context.Context, host string) (string, bool) {
		switch host {
		case "a.eu.everstack.ai":
			return "inst-a", true
		case "b.eu.everstack.ai":
			return "inst-b", true
		}
		return "", false
	})
	return wh, store
}

func serveWebhook(wh *WebhookHandler, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/"+path, strings.NewReader(`{"x":1}`))
	req.Host = host
	req = mux.SetURLVars(req, map[string]string{"path": path})
	rec := httptest.NewRecorder()
	wh.Handle(rec, req)
	return rec
}

func TestWebhookHostTenantMismatchIsNotFound(t *testing.T) {
	wh, _ := newWebhookFixture()

	mismatch := serveWebhook(wh, "b.eu.everstack.ai", "hook-a")
	unknown := serveWebhook(wh, "b.eu.everstack.ai", "no-such-hook")
	if mismatch.Code != http.StatusNotFound {
		t.Fatalf("mismatch code = %d, want 404", mismatch.Code)
	}
	if mismatch.Body.String() != unknown.Body.String() || mismatch.Header().Get("Content-Type") != unknown.Header().Get("Content-Type") {
		t.Fatalf("mismatch response %q differs from unknown path %q", mismatch.Body.String(), unknown.Body.String())
	}
}

func TestWebhookHostTenantMatchAndPlatformHostProceed(t *testing.T) {
	for _, host := range []string{"a.eu.everstack.ai", "api.everstack.ai"} {
		wh, _ := newWebhookFixture()
		rec := serveWebhook(wh, host, "hook-a")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("host %s: code = %d, want 202 (body %s)", host, rec.Code, rec.Body.String())
		}
	}
}

func TestWebhookPausedTenantIsForbidden(t *testing.T) {
	wh, store := newWebhookFixture("inst-a")

	rec := serveWebhook(wh, "a.eu.everstack.ai", "hook-a")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Type != "instance_paused" || body.Error.Code != "instance_paused" || body.Error.Message != "This instance is paused." {
		t.Fatalf("body = %+v", body)
	}
	time.Sleep(20 * time.Millisecond) // any stray background Execute would have run by now
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.countCalls != 0 || store.recordCalls != 0 || store.failures != 0 {
		t.Fatalf("paused webhook reached the executor: count=%d record=%d failures=%d", store.countCalls, store.recordCalls, store.failures)
	}
}
