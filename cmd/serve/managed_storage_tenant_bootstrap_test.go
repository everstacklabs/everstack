package serve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
)

type bootstrapManagedDefaultEnsurer struct {
	mu      sync.Mutex
	calls   []string
	failFor int
	started chan<- struct{}
	block   <-chan struct{}
}

func (e *bootstrapManagedDefaultEnsurer) EnsureDefault(_ context.Context, tenantID string) (*storagepkg.ManagedConnection, error) {
	e.mu.Lock()
	e.calls = append(e.calls, tenantID)
	attempt := len(e.calls)
	e.mu.Unlock()

	if e.started != nil {
		e.started <- struct{}{}
	}
	if e.block != nil {
		<-e.block
	}
	if attempt <= e.failFor {
		return nil, errors.New("database unavailable")
	}
	return &storagepkg.ManagedConnection{TenantID: tenantID}, nil
}

func TestManagedStorageTenantBootstrapCoalescesConcurrentFirstRequests(t *testing.T) {
	const requestCount = 12
	started := make(chan struct{}, requestCount)
	release := make(chan struct{})
	ensurer := &bootstrapManagedDefaultEnsurer{started: started, block: release}
	handler := newManagedStorageTenantBootstrap(ensurer).Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx := contextkeys.WithTenantID(context.Background(), "instance-1")
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	start := make(chan struct{})
	results := make(chan int, requestCount)
	for range requestCount {
		go func() {
			<-start
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			results <- res.Code
		}()
	}
	close(start)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("managed default provisioning did not start")
	}
	time.Sleep(50 * time.Millisecond)
	close(release)

	for range requestCount {
		if status := <-results; status != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", status, http.StatusNoContent)
		}
	}
	if calls := ensurer.tenantCalls(); len(calls) != 1 {
		t.Fatalf("EnsureDefault calls = %d, want one coalesced call", len(calls))
	}
}

func (e *bootstrapManagedDefaultEnsurer) tenantCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func TestManagedStorageTenantBootstrapProvisionsBeforeServingAndCachesSuccess(t *testing.T) {
	ensurer := &bootstrapManagedDefaultEnsurer{}
	served := 0
	handler := newManagedStorageTenantBootstrap(ensurer).Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		w.WriteHeader(http.StatusNoContent)
	}))

	for range 2 {
		ctx := contextkeys.WithTenantID(context.Background(), "instance-1")
		ctx = contextkeys.WithTenantAuthenticated(ctx)
		req := httptest.NewRequest(http.MethodGet, "/everstack.storage.v1.ObjectStorageService/ListStorageConfigs", nil).WithContext(ctx)
		res := httptest.NewRecorder()

		handler.ServeHTTP(res, req)

		if res.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
		}
	}

	if served != 2 {
		t.Fatalf("downstream calls = %d, want 2", served)
	}
	if calls := ensurer.tenantCalls(); len(calls) != 1 || calls[0] != "instance-1" {
		t.Fatalf("EnsureDefault calls = %#v, want [instance-1]", calls)
	}
}

func TestGatewayAPIRuntimeBootstrapRunsAfterUpstreamTenantAuthentication(t *testing.T) {
	ensurer := &bootstrapManagedDefaultEnsurer{}
	runtime := &GatewayAPIRuntime{managedStorageDefaults: ensurer}
	downstreamCalls := 0
	gatewayHandler := runtime.WrapManagedStorageTenantBootstrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downstreamCalls++
		w.WriteHeader(http.StatusNoContent)
	}))

	upstreamTenantAuth := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := contextkeys.WithTenantID(r.Context(), "shared-instance-1")
		ctx = contextkeys.WithTenantAuthenticated(ctx)
		gatewayHandler.ServeHTTP(w, r.WithContext(ctx))
	})
	res := httptest.NewRecorder()
	upstreamTenantAuth.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))

	if res.Code != http.StatusNoContent || downstreamCalls != 1 {
		t.Fatalf("status = %d, downstream calls = %d", res.Code, downstreamCalls)
	}
	if calls := ensurer.tenantCalls(); len(calls) != 1 || calls[0] != "shared-instance-1" {
		t.Fatalf("EnsureDefault calls = %#v, want [shared-instance-1]", calls)
	}
}

func TestManagedStorageTenantBootstrapFailsClosedAndRetries(t *testing.T) {
	ensurer := &bootstrapManagedDefaultEnsurer{failFor: 1}
	served := 0
	handler := newManagedStorageTenantBootstrap(ensurer).Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx := contextkeys.WithTenantID(context.Background(), "instance-1")
	ctx = contextkeys.WithTenantAuthenticated(ctx)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusServiceUnavailable)
	}
	if served != 0 {
		t.Fatalf("downstream calls after failed provisioning = %d, want 0", served)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	if second.Code != http.StatusNoContent {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusNoContent)
	}
	if served != 1 {
		t.Fatalf("downstream calls after retry = %d, want 1", served)
	}
	if calls := ensurer.tenantCalls(); len(calls) != 2 {
		t.Fatalf("EnsureDefault calls = %#v, want two attempts", calls)
	}
}

func TestManagedStorageTenantBootstrapSkipsUnverifiedTenantContext(t *testing.T) {
	ensurer := &bootstrapManagedDefaultEnsurer{}
	handler := newManagedStorageTenantBootstrap(ensurer).Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	contexts := []context.Context{
		context.Background(),
		contextkeys.WithTenantID(context.Background(), "unverified-instance"),
		contextkeys.WithTenantAuthenticated(context.Background()),
	}
	for _, ctx := range contexts {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
		if res.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
		}
	}

	if calls := ensurer.tenantCalls(); len(calls) != 0 {
		t.Fatalf("EnsureDefault calls = %#v, want none", calls)
	}
}
