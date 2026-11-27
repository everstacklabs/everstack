package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	sandboxmetrics "github.com/everstacklabs/everstack/internal/sandbox/metrics"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshots"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/jmoiron/sqlx"
)

func TestSandboxMetricsBatchOmitsMissingAndForeignSandboxes(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	db := sqlx.NewDb(rawDB, "sqlmock")
	s := &Server{sandboxMgr: ownershipTestManager(), metricsRepo: sandboxmetrics.NewRepository(db)}
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = ? AND sandbox_id IN (?)")).
		WithArgs("tenant-a", "sbx-a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "sandbox_id", "tenant_id", "cpu_percent", "memory_usage", "memory_limit", "disk_used_mb", "collected_at"}).
			AddRow(1, "sbx-a", "tenant-a", 1.5, 10, 20, 3, now))

	ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")
	resp, err := s.SandboxMetricsBatch(ctx, connect.NewRequest(&agentspb.SandboxMetricsBatchRequest{Ids: []string{"sbx-a", "sbx-b", "missing"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.GetMetrics()) != 1 || resp.Msg.GetMetrics()["sbx-a"] == nil {
		t.Fatalf("metrics = %#v, want only sbx-a", resp.Msg.GetMetrics())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotForeignSourceMatchesMissingSource(t *testing.T) {
	rawDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{sandboxMgr: ownershipTestManager(), snapshotRepo: snapshots.NewRepository(sqlx.NewDb(rawDB, "sqlmock"))}

	call := func(source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/snapshots", strings.NewReader(`{"name":"copy","from_sandbox_id":"`+source+`"}`))
		req = req.WithContext(verifiedTenantCtx("tenant-a"))
		rec := httptest.NewRecorder()
		s.HandleCreateSnapshot(rec, req)
		return rec
	}
	foreign := call("sbx-b")
	missing := call("missing")
	if foreign.Code != http.StatusNotFound || foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign = %d %q, missing = %d %q", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
	}
}

func TestEgressPolicyRequiresOwnedSandbox(t *testing.T) {
	s := &Server{sandboxMgr: collisionTestManager()}
	call := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/sandbox/egress/policy?sandbox_id="+id, nil)
		req = req.WithContext(verifiedTenantCtx("tenant-a"))
		rec := httptest.NewRecorder()
		s.handleGetEgressPolicy(rec, req, nil)
		return rec
	}
	if owner := call("sbx-b"); owner.Code != http.StatusOK {
		t.Fatalf("colliding owner status = %d, body %s", owner.Code, owner.Body.String())
	}
	foreign := call("victim")
	missing := call("missing")
	if foreign.Code != http.StatusNotFound || foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign = %d %q, missing = %d %q", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
	}
}
