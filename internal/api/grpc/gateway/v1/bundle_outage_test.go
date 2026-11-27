package v1

// A tenant bundle reload that fails (database outage longer than the bundle
// TTL) answers 503 Unavailable, or serves that same tenant's last loaded
// bundle; never a nil-router panic and never another tenant's bundle.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// outageServer is a shared-mode gateway whose provider database fails every
// read.
func outageServer(t *testing.T) *Server {
	t.Helper()
	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 50; i++ {
		mock.ExpectQuery(`.`).WillReturnError(errors.New("connection refused: database is down"))
	}
	s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{}, &validator.FeaturesConfig{})
	s.feat.Gateway.EnableStreaming = true
	sctx := context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)
	sctx = context.WithValue(sctx, contextkeys.ProviderRepo, provider_config.NewRepository(sqlx.NewDb(raw, "postgres")))
	sctx = context.WithValue(sctx, contextkeys.APIKeyRepo, provider_api_keys.Repository(fakeKeyRepo{}))
	s.ctx = sctx
	return s
}

func bundleServing(p gw.Provider) *tenantBundle {
	reg := gw.NewRegistry()
	reg.Register(p)
	return &tenantBundle{reg: reg, router: gw.NewRouter(reg, nil)}
}

// call runs one request and turns a panic into an error so a nil-router
// panic fails the test instead of the binary.
func call(s *Server, ctx context.Context, kind string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panicked: %v", r)
		}
	}()
	var got collected
	switch kind {
	case "unary":
		return processChatCompletion(ctx, s, chatReq("m-primary", false), got.send)
	case "stream":
		return processChatCompletion(ctx, s, chatReq("m-primary", true), got.send)
	default:
		return processEmbeddings(ctx, s, &pb.EmbeddingsRequest{Model: "m-primary", Input: "hi"}, func(*pb.EmbeddingsResponse) error { return nil })
	}
}

func tenantOf(id string) context.Context {
	return contextkeys.WithTenantID(context.Background(), id)
}

func TestBundleReloadFailureIsUnavailable(t *testing.T) {
	for _, kind := range []string{"unary", "stream", "embeddings"} {
		s := outageServer(t)
		// Another tenant's fresh bundle must never serve tenant-1.
		s.tenantBundles.store("tenant-2", bundleServing(&routeModel{name: "pp", model: "m-primary"}))
		err := call(s, tenantOf("tenant-1"), kind)
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("%s: err = %v (code %v), want Unavailable", kind, err, connect.CodeOf(err))
		}
	}
}

func TestBundleReloadFailureServesSameTenantStaleBundle(t *testing.T) {
	for _, kind := range []string{"unary", "stream", "embeddings"} {
		s := outageServer(t)
		own := &routeModel{name: "pp", model: "m-primary"}
		b := bundleServing(own)
		s.tenantBundles.store("tenant-1", b)
		b.loadedAt = time.Now().Add(-2 * providerRefreshStaleness) // expired, reload due
		if err := call(s, tenantOf("tenant-1"), kind); err != nil {
			t.Fatalf("%s: stale bundle not served: %v", kind, err)
		}
	}
}

func TestBundleReloadFailureNeverResurrectsAnInvalidatedOrAncientBundle(t *testing.T) {
	s := outageServer(t)
	s.tenantBundles.store("tenant-1", bundleServing(&routeModel{name: "pp", model: "m-primary"}))
	s.invalidateBundle("tenant-1")
	if err := call(s, tenantOf("tenant-1"), "unary"); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("invalidated bundle: err = %v, want Unavailable", err)
	}

	b := bundleServing(&routeModel{name: "pp", model: "m-primary"})
	s.tenantBundles.store("tenant-1", b)
	b.loadedAt = time.Now().Add(-2 * providerStaleServeMax)
	if err := call(s, tenantOf("tenant-1"), "unary"); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("bundle older than providerStaleServeMax: err = %v, want Unavailable", err)
	}

	// No tenant identity in shared mode: nothing, not the default bundle.
	if err := call(s, context.Background(), "unary"); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("no tenant: err = %v, want Unavailable", err)
	}
}

// While a stale bundle serves, a failed reload is not retried by every
// request: the next attempt waits providerReloadRetryAfter.
func TestFailedReloadIsNotRetriedPerRequest(t *testing.T) {
	var queries atomic.Int32
	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(string, string) error {
		queries.Add(1)
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for i := 0; i < 10; i++ {
		mock.ExpectQuery(`.`).WillReturnError(errors.New("database is down"))
	}
	s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{}, &validator.FeaturesConfig{})
	sctx := context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)
	sctx = context.WithValue(sctx, contextkeys.ProviderRepo, provider_config.NewRepository(sqlx.NewDb(raw, "postgres")))
	sctx = context.WithValue(sctx, contextkeys.APIKeyRepo, provider_api_keys.Repository(fakeKeyRepo{}))
	s.ctx = sctx
	b := bundleServing(&routeModel{name: "pp", model: "m-primary"})
	s.tenantBundles.store("tenant-1", b)
	b.loadedAt = time.Now().Add(-2 * providerRefreshStaleness)
	for i := 0; i < 5; i++ {
		if err := call(s, tenantOf("tenant-1"), "unary"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if n := queries.Load(); n != 1 {
		t.Fatalf("provider reload queried the database %d times in 5 requests, want 1", n)
	}
	if !s.tenantBundles.reloadBackoff("tenant-1") {
		t.Fatal("the failed reload was not recorded")
	}
}
