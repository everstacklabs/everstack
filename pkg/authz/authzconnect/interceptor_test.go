package authzconnect

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/pkg/authz"
)

// fakeReq is a minimal connect.AnyRequest for unit-testing the PEP without a
// real RPC transport.
type fakeReq struct {
	connect.AnyRequest
	msg  any
	proc string
}

func (f fakeReq) Any() any              { return f.msg }
func (f fakeReq) Spec() connect.Spec    { return connect.Spec{Procedure: f.proc} }

type deleteDatasetMsg struct{ ID string }

func newTestEngine(t *testing.T) *authz.Engine {
	t.Helper()
	store := authz.NewMemStore()
	e := authz.NewEngine(store, authz.EverstackSchema().WithResourceTypes("dataset"))
	ctx := context.Background()
	_ = store.Write(ctx,
		authz.OrgMembership("acme", "alice", authz.RoleOwner),
		authz.OrgMembership("acme", "carol", authz.RoleViewer),
		authz.WorkspaceParent("prod", "acme"),
		authz.InstanceParent("inst1", "prod"),
		authz.ResourceParent("dataset", "ds1", "inst1"),
	)
	return e
}

func datasetRegistry() Registry {
	return Registry{
		"/everstack.datasets.v1.DatasetsService/DeleteDataset": {
			Permission: authz.PermResourceDelete,
			Object: func(msg any) (authz.Object, bool) {
				m, ok := msg.(*deleteDatasetMsg)
				if !ok || m.ID == "" {
					return authz.Object{}, false
				}
				return authz.Resource("dataset", m.ID), true
			},
		},
	}
}

func runUnary(i *Interceptor, ctx context.Context, req connect.AnyRequest) error {
	next := func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) { return nil, nil }
	_, err := i.WrapUnary(next)(ctx, req)
	return err
}

func TestPEPEnforce(t *testing.T) {
	e := newTestEngine(t)
	i := NewInterceptor(e, datasetRegistry(), Options{
		UserID:      func(ctx context.Context) string { return ctx.Value("uid").(string) },
		Mode:        ModeEnforce,
		RequireRule: false,
	})

	// Owner may delete.
	err := runUnary(i, context.WithValue(context.Background(), "uid", "alice"),
		fakeReq{msg: &deleteDatasetMsg{ID: "ds1"}, proc: "/everstack.datasets.v1.DatasetsService/DeleteDataset"})
	if err != nil {
		t.Fatalf("owner should be allowed to delete, got %v", err)
	}

	// Viewer denied.
	err = runUnary(i, context.WithValue(context.Background(), "uid", "carol"),
		fakeReq{msg: &deleteDatasetMsg{ID: "ds1"}, proc: "/everstack.datasets.v1.DatasetsService/DeleteDataset"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("viewer should be permission-denied, got %v", err)
	}

	// Unauthenticated denied.
	err = runUnary(i, context.WithValue(context.Background(), "uid", ""),
		fakeReq{msg: &deleteDatasetMsg{ID: "ds1"}, proc: "/everstack.datasets.v1.DatasetsService/DeleteDataset"})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("empty user should be unauthenticated, got %v", err)
	}
}

func TestPEPShadowAllows(t *testing.T) {
	e := newTestEngine(t)
	i := NewInterceptor(e, datasetRegistry(), Options{
		UserID: func(ctx context.Context) string { return "carol" },
		Mode:   ModeShadow,
	})
	// Viewer would be denied, but shadow mode lets it through.
	err := runUnary(i, context.Background(),
		fakeReq{msg: &deleteDatasetMsg{ID: "ds1"}, proc: "/everstack.datasets.v1.DatasetsService/DeleteDataset"})
	if err != nil {
		t.Fatalf("shadow mode must allow, got %v", err)
	}
}

func TestPEPShadowAllowsEmptyUser(t *testing.T) {
	e := newTestEngine(t)
	// Shadow mode must never block, including a userless-but-authenticated caller
	// (e.g. an API-key request that carries a tenant but no user id). Enabling
	// EVS_AUTHZ_ENABLED in shadow must be safe for these requests.
	i := NewInterceptor(e, datasetRegistry(), Options{
		UserID: func(context.Context) string { return "" },
		Mode:   ModeShadow,
	})
	err := runUnary(i, context.Background(),
		fakeReq{msg: &deleteDatasetMsg{ID: "ds1"}, proc: "/everstack.datasets.v1.DatasetsService/DeleteDataset"})
	if err != nil {
		t.Fatalf("shadow mode must allow an empty-user call, got %v", err)
	}
}

func TestPEPUnmappedProcedure(t *testing.T) {
	e := newTestEngine(t)
	// requireRule=false: unmapped procedure passes through.
	i := NewInterceptor(e, datasetRegistry(), Options{UserID: func(context.Context) string { return "alice" }})
	if err := runUnary(i, context.Background(), fakeReq{msg: nil, proc: "/some.other.Service/Method"}); err != nil {
		t.Fatalf("unmapped procedure should pass through, got %v", err)
	}

	// requireRule=true: unmapped procedure denied.
	i2 := NewInterceptor(e, datasetRegistry(), Options{
		UserID:      func(context.Context) string { return "alice" },
		RequireRule: true,
	})
	if err := runUnary(i2, context.Background(), fakeReq{msg: nil, proc: "/some.other.Service/Method"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("requireRule should deny unmapped, got %v", err)
	}
}
