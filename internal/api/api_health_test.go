package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type healthCheckFunc func(context.Context) error

func (f healthCheckFunc) Health(ctx context.Context) error { return f(ctx) }

func TestHealthHandlerUsesHealthSetAfterHandlerCreation(t *testing.T) {
	t.Parallel()

	a := &API{}
	handler := a.healthHandler()
	a.SetHealth(healthCheckFunc(func(context.Context) error {
		return errors.New("primary unavailable")
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("readiness status = %d, want %d", recorder.Code, http.StatusPreconditionFailed)
	}
}

func TestOptionalDependencyDegradesWithoutFailingReadiness(t *testing.T) {
	t.Parallel()

	a := &API{}
	a.SetDependencies([]DependencyCheck{
		{Name: "postgres", Required: true, Check: func(context.Context) error { return nil }},
		{Name: "clickhouse", Required: false, Check: func(context.Context) error { return errors.New("down") }},
	})
	handler := a.healthHandler()

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want %d; body=%s", ready.Code, http.StatusOK, ready.Body.String())
	}

	dependencies := httptest.NewRecorder()
	handler.ServeHTTP(dependencies, httptest.NewRequest(http.MethodGet, "/health/dependencies", nil))
	if dependencies.Code != http.StatusOK {
		t.Fatalf("dependency status = %d, want %d", dependencies.Code, http.StatusOK)
	}
	if body := dependencies.Body.String(); !strings.Contains(body, `"status":"degraded"`) || !strings.Contains(body, `"name":"clickhouse"`) {
		t.Fatalf("dependency body = %s, want degraded ClickHouse", body)
	}
}

func TestRequiredDependencyFailsReadinessAndDependencyHealth(t *testing.T) {
	t.Parallel()

	a := &API{}
	a.SetDependencies([]DependencyCheck{
		{Name: "postgres", Required: true, Check: func(context.Context) error { return errors.New("down") }},
	})
	handler := a.healthHandler()

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusPreconditionFailed {
		t.Fatalf("readiness status = %d, want %d", ready.Code, http.StatusPreconditionFailed)
	}

	dependencies := httptest.NewRecorder()
	handler.ServeHTTP(dependencies, httptest.NewRequest(http.MethodGet, "/health/dependencies", nil))
	if dependencies.Code != http.StatusServiceUnavailable {
		t.Fatalf("dependency status = %d, want %d", dependencies.Code, http.StatusServiceUnavailable)
	}
}
