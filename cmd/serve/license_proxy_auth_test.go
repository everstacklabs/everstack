package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
)

// EVS-324 item 13: the license proxy forwards spend-limit calls with the
// portal SERVICE credentials, so the gateway must authenticate the caller
// and pin organization_id / instance_id to the license this gateway holds.

const (
	proxyTestOrg      = "11111111-1111-1111-1111-111111111111"
	proxyTestInstance = "22222222-2222-2222-2222-222222222222"
	proxyForeignOrg   = "33333333-3333-3333-3333-333333333333"
	proxyForeignInst  = "44444444-4444-4444-4444-444444444444"
	proxyOwnLimitID   = "limit-own"
)

func boundIdentity() (string, string) { return proxyTestOrg, proxyTestInstance }

// proxySessionCtx mimics cookieSessionAuthMiddleware for a verified session.
func proxySessionCtx(role string) context.Context {
	ctx := contextkeys.WithCloudUserID(context.Background(), "55555555-5555-5555-5555-555555555555")
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	ctx = contextkeys.WithTenantID(ctx, "66666666-6666-6666-6666-666666666666")
	if role != "" {
		ctx = contextkeys.WithUserRole(ctx, role)
	}
	return ctx
}

type recordingLicenseService struct {
	licenseconnect.UnimplementedLicenseServiceHandler
	mu      sync.Mutex
	sets    []*licv1.SetSpendLimitRequest
	deletes []string
	gets    []string
}

func (s *recordingLicenseService) GetPlans(context.Context, *connect.Request[licv1.GetPlansRequest]) (*connect.Response[licv1.GetPlansResponse], error) {
	return connect.NewResponse(&licv1.GetPlansResponse{}), nil
}

func (s *recordingLicenseService) GetSpendLimits(_ context.Context, req *connect.Request[licv1.GetSpendLimitsRequest]) (*connect.Response[licv1.GetSpendLimitsResponse], error) {
	s.mu.Lock()
	s.gets = append(s.gets, req.Msg.GetOrganizationId())
	s.mu.Unlock()
	if req.Msg.GetOrganizationId() == proxyTestOrg {
		return connect.NewResponse(&licv1.GetSpendLimitsResponse{SpendLimits: []*licv1.SpendLimit{{Id: proxyOwnLimitID, OrganizationId: proxyTestOrg}}}), nil
	}
	return connect.NewResponse(&licv1.GetSpendLimitsResponse{SpendLimits: []*licv1.SpendLimit{{Id: "limit-foreign", OrganizationId: proxyForeignOrg}}}), nil
}

func (s *recordingLicenseService) SetSpendLimit(_ context.Context, req *connect.Request[licv1.SetSpendLimitRequest]) (*connect.Response[licv1.SetSpendLimitResponse], error) {
	s.mu.Lock()
	s.sets = append(s.sets, req.Msg)
	s.mu.Unlock()
	return connect.NewResponse(&licv1.SetSpendLimitResponse{SpendLimit: &licv1.SpendLimit{Id: proxyOwnLimitID}}), nil
}

func (s *recordingLicenseService) DeleteSpendLimit(_ context.Context, req *connect.Request[licv1.DeleteSpendLimitRequest]) (*connect.Response[licv1.DeleteSpendLimitResponse], error) {
	s.mu.Lock()
	s.deletes = append(s.deletes, req.Msg.GetId())
	s.mu.Unlock()
	return connect.NewResponse(&licv1.DeleteSpendLimitResponse{Success: true}), nil
}

func (s *recordingLicenseService) CheckSpendLimit(context.Context, *connect.Request[licv1.CheckSpendLimitRequest]) (*connect.Response[licv1.CheckSpendLimitResponse], error) {
	return connect.NewResponse(&licv1.CheckSpendLimitResponse{Allowed: true}), nil
}

func (s *recordingLicenseService) counts() (sets, deletes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sets), len(s.deletes)
}

// proxyFixture serves the proxy behind a middleware that installs ctxFn's
// identity, the way cookieSessionAuthMiddleware does in production.
func proxyFixture(t *testing.T, ctxFn func() context.Context) (*recordingLicenseService, string) {
	t.Helper()
	svc := &recordingLicenseService{}
	mux := http.NewServeMux()
	path, handler := licenseconnect.NewLicenseServiceHandler(svc)
	mux.Handle(path, handler)
	backend := httptest.NewServer(mux)
	t.Cleanup(backend.Close)

	proxy := newLicenseProxyHandler(backend.URL, http.DefaultTransport, boundIdentity)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r.WithContext(ctxFn()))
	}))
	t.Cleanup(front.Close)
	return svc, front.URL
}

// The admin UI uses the gRPC-web transport (packages/client/src/web.ts).
func grpcWebClient(url string) licenseconnect.LicenseServiceClient {
	return licenseconnect.NewLicenseServiceClient(http.DefaultClient, url, connect.WithGRPCWeb())
}

func ownSet() *licv1.SetSpendLimitRequest {
	return &licv1.SetSpendLimitRequest{
		OrganizationId: proxyTestOrg, InstanceId: proxyTestInstance,
		LimitType: licv1.SpendLimitType_SPEND_LIMIT_TYPE_ESTIMATED_COST, LimitAmount: 50,
		Period: licv1.SpendLimitPeriod_SPEND_LIMIT_PERIOD_MONTHLY, Enabled: true,
	}
}

func TestLicenseProxy_AnonymousCannotWriteSpendLimits(t *testing.T) {
	svc, url := proxyFixture(t, context.Background)
	c := grpcWebClient(url)

	_, err := c.SetSpendLimit(context.Background(), connect.NewRequest(ownSet()))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "anonymous SetSpendLimit: %v", err)
	_, err = c.DeleteSpendLimit(context.Background(), connect.NewRequest(&licv1.DeleteSpendLimitRequest{Id: proxyOwnLimitID}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "anonymous DeleteSpendLimit: %v", err)
	_, err = c.GetSpendLimits(context.Background(), connect.NewRequest(&licv1.GetSpendLimitsRequest{OrganizationId: proxyTestOrg}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "anonymous GetSpendLimits: %v", err)

	sets, deletes := svc.counts()
	require.Zero(t, sets+deletes, "anonymous writes reached the license service")
}

func TestLicenseProxy_MemberCannotWriteSpendLimits(t *testing.T) {
	svc, url := proxyFixture(t, func() context.Context { return proxySessionCtx("member") })
	c := grpcWebClient(url)

	_, err := c.SetSpendLimit(context.Background(), connect.NewRequest(ownSet()))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "member SetSpendLimit: %v", err)
	sets, _ := svc.counts()
	require.Zero(t, sets)

	// Reads stay available to members for the billing page.
	_, err = c.GetSpendLimits(context.Background(), connect.NewRequest(&licv1.GetSpendLimitsRequest{OrganizationId: proxyTestOrg}))
	require.NoError(t, err)
}

func TestLicenseProxy_AdminCannotTouchAnotherOrganization(t *testing.T) {
	svc, url := proxyFixture(t, func() context.Context { return proxySessionCtx("owner") })
	c := grpcWebClient(url)

	foreign := ownSet()
	foreign.OrganizationId = proxyForeignOrg
	_, err := c.SetSpendLimit(context.Background(), connect.NewRequest(foreign))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "foreign-org SetSpendLimit: %v", err)

	foreignInstance := ownSet()
	foreignInstance.InstanceId = proxyForeignInst
	_, err = c.SetSpendLimit(context.Background(), connect.NewRequest(foreignInstance))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "foreign-instance SetSpendLimit: %v", err)

	_, err = c.GetSpendLimits(context.Background(), connect.NewRequest(&licv1.GetSpendLimitsRequest{OrganizationId: proxyForeignOrg}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "foreign-org GetSpendLimits: %v", err)

	_, err = c.DeleteSpendLimit(context.Background(), connect.NewRequest(&licv1.DeleteSpendLimitRequest{Id: "limit-foreign"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "foreign DeleteSpendLimit: %v", err)

	sets, deletes := svc.counts()
	require.Zero(t, sets+deletes, "cross-org writes reached the license service")
}

// Regression: the admin UI spend-limit page (gRPC-web, org/instance from
// GetLicenseMonitorStatus) keeps working for an owner.
func TestLicenseProxy_AdminSpendLimitPageWorks(t *testing.T) {
	svc, url := proxyFixture(t, func() context.Context { return proxySessionCtx("owner") })
	c := grpcWebClient(url)

	got, err := c.GetSpendLimits(context.Background(), connect.NewRequest(&licv1.GetSpendLimitsRequest{OrganizationId: proxyTestOrg}))
	require.NoError(t, err)
	require.Len(t, got.Msg.GetSpendLimits(), 1)

	_, err = c.SetSpendLimit(context.Background(), connect.NewRequest(ownSet()))
	require.NoError(t, err)
	orgWide := ownSet()
	orgWide.InstanceId = ""
	_, err = c.SetSpendLimit(context.Background(), connect.NewRequest(orgWide))
	require.NoError(t, err)

	_, err = c.DeleteSpendLimit(context.Background(), connect.NewRequest(&licv1.DeleteSpendLimitRequest{Id: proxyOwnLimitID}))
	require.NoError(t, err)

	sets, deletes := svc.counts()
	require.Equal(t, 2, sets)
	require.Equal(t, 1, deletes)
	svc.mu.Lock()
	require.Equal(t, 50.0, svc.sets[0].GetLimitAmount(), "forwarded message must be intact")
	svc.mu.Unlock()

	// Connect (non gRPC-web) clients keep working too.
	connectClient := licenseconnect.NewLicenseServiceClient(http.DefaultClient, url)
	_, err = connectClient.SetSpendLimit(context.Background(), connect.NewRequest(ownSet()))
	require.NoError(t, err)
	jsonClient := licenseconnect.NewLicenseServiceClient(http.DefaultClient, url, connect.WithProtoJSON())
	_, err = jsonClient.SetSpendLimit(context.Background(), connect.NewRequest(ownSet()))
	require.NoError(t, err)
}

func TestLicenseProxy_GetPlansStaysPublic(t *testing.T) {
	_, url := proxyFixture(t, context.Background)
	_, err := grpcWebClient(url).GetPlans(context.Background(), connect.NewRequest(&licv1.GetPlansRequest{}))
	require.NoError(t, err)
}

func TestLicenseProxy_RejectsUnparseableSpendLimitBodies(t *testing.T) {
	svc, url := proxyFixture(t, func() context.Context { return proxySessionCtx("owner") })
	for name, req := range map[string]*http.Request{
		"text/plain": mustReq(http.MethodPost, url+"/everstack.license.v1.LicenseService/SetSpendLimit", "text/plain", `{"organizationId":"`+proxyTestOrg+`"}`),
		"unknown field": mustReq(http.MethodPost, url+"/everstack.license.v1.LicenseService/SetSpendLimit", "application/json",
			`{"organizationId":"`+proxyTestOrg+`","organization_id_override":"x"}`),
		"GET": mustReq(http.MethodGet, url+"/everstack.license.v1.LicenseService/SetSpendLimit", "application/json", ""),
	} {
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.GreaterOrEqual(t, resp.StatusCode, 400, name)
	}
	sets, _ := svc.counts()
	require.Zero(t, sets)
}

func mustReq(method, url, contentType, body string) *http.Request {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", contentType)
	return req
}
