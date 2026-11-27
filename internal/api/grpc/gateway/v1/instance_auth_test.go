package v1

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"

	"github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/services/trial"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
)

// Gateway-side auth for the activation, status, refresh and subscription
// callback procedures. All of them are reachable before login (policy
// bypass), so the handlers themselves must tell an anonymous caller from an
// authenticated admin of THIS gateway.

const (
	testLocalOrg      = "11111111-1111-1111-1111-111111111111"
	testLocalInstance = "22222222-2222-2222-2222-222222222222"
	testOtherOrg      = "33333333-3333-3333-3333-333333333333"
	testOtherInstance = "44444444-4444-4444-4444-444444444444"
)

// --- fakes -----------------------------------------------------------------

type fakeInstanceManager struct {
	enterprise.InstanceManager
	mu          sync.Mutex
	activated   bool
	activateErr error
	info        *enterprise.InstanceInfo
	localID     string
	stored      []string
}

func (f *fakeInstanceManager) IsActivated(context.Context) (bool, error) {
	return f.activated, f.activateErr
}

func (f *fakeInstanceManager) GetActiveInstance(context.Context) (*enterprise.InstanceInfo, error) {
	if f.info == nil {
		return nil, errors.New("not activated")
	}
	return f.info, nil
}

func (f *fakeInstanceManager) GetLocalInstanceId(context.Context) (string, error) {
	return f.localID, nil
}

func (f *fakeInstanceManager) StoreActivation(_ context.Context, _ string, instanceID, _ string, _ string, _ any, _ ...enterprise.StoreActivationOpts) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored = append(f.stored, instanceID)
	return nil
}

func (f *fakeInstanceManager) storedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stored)
}

type fakeMonitor struct {
	enterprise.LicenseMonitor
	org, inst string
	usage     enterprise.UsageStats
	state     *enterprise.LicenseState
	refreshes atomic.Int32
}

func (m *fakeMonitor) GetOrganizationID() string                   { return m.org }
func (m *fakeMonitor) GetInstanceID() string                       { return m.inst }
func (m *fakeMonitor) GetUsageStats() enterprise.UsageStats        { return m.usage }
func (m *fakeMonitor) GetLicenseState() *enterprise.LicenseState   { return m.state }
func (m *fakeMonitor) IsLocked() (bool, string)                    { return false, "" }
func (m *fakeMonitor) IsSpendBlocked() (bool, string)              { return false, "" }
func (m *fakeMonitor) Refresh()                                    { m.refreshes.Add(1) }
func (m *fakeMonitor) SetOrganizationAndInstanceID(string, string) {}
func (m *fakeMonitor) SetM2MCredentials([]byte)                    {}
func (m *fakeMonitor) SetAvailableFeatures(map[string]*enterprise.FeatureRelease) {
}
func (m *fakeMonitor) GetAvailableFeatures() map[string]*enterprise.FeatureRelease {
	return map[string]*enterprise.FeatureRelease{"agents": {Name: "Agents", Status: "ga"}}
}

type fakeEnforcer struct {
	enterprise.LicenseEnforcer
	cached    *enterprise.LicenseState
	trialMgr  *trial.Manager
	refreshes atomic.Int32
}

func (e *fakeEnforcer) GetCached() *enterprise.LicenseState     { return e.cached }
func (e *fakeEnforcer) IsInTrialMode() bool                     { return e.trialMgr != nil }
func (e *fakeEnforcer) GetTrialManager() any                    { return e.trialMgr }
func (e *fakeEnforcer) RefreshNow()                             { e.refreshes.Add(1) }
func (e *fakeEnforcer) SetCachedState(*enterprise.LicenseState) {}

type fakeLicenseInstanceService struct {
	licenseconnect.UnimplementedInstanceServiceHandler
	hits *atomic.Int32
}

func (f fakeLicenseInstanceService) ActivateInstance(_ context.Context, req *connect.Request[licv1.ActivateInstanceRequest]) (*connect.Response[licv1.ActivateInstanceResponse], error) {
	f.hits.Add(1)
	instanceID := req.Msg.GetInstanceId()
	if instanceID == "" {
		instanceID = testLocalInstance
	}
	return connect.NewResponse(&licv1.ActivateInstanceResponse{
		InstanceId: instanceID,
		LicenseState: &licv1.LicenseState{
			TenantId: testLocalOrg,
			PlanTier: licv1.LicenseType_LICENSE_TYPE_PRO,
			Status:   licv1.LicenseStatus_LICENSE_STATUS_ACTIVE,
		},
	}), nil
}

// --- helpers ---------------------------------------------------------------

type selfHostedHarness struct {
	server   *Server
	im       *fakeInstanceManager
	monitor  *fakeMonitor
	enforcer *fakeEnforcer
	hits     *atomic.Int32
}

func newSelfHostedHarness(t *testing.T, im *fakeInstanceManager) *selfHostedHarness {
	t.Helper()
	t.Setenv("EVS_AUTH_SERVICE_URL", "")
	hits := &atomic.Int32{}
	mux := http.NewServeMux()
	path, handler := licenseconnect.NewInstanceServiceHandler(fakeLicenseInstanceService{hits: hits})
	mux.Handle(path, handler)
	licenseSrv := httptest.NewServer(mux)
	t.Cleanup(licenseSrv.Close)

	monitor := &fakeMonitor{
		org: testLocalOrg, inst: testLocalInstance,
		usage: enterprise.UsageStats{TotalRequests: 42, TotalTokens: 9001, EstimatedCostUSD: 12.5},
		state: &enterprise.LicenseState{Active: true, Status: "active", Tier: "pro", IsPaid: true,
			SandboxBillingEnabled: true, UsageLimits: []enterprise.UsageLimit{{Type: "requests", Limit: 1000}}},
	}
	enforcer := &fakeEnforcer{}
	ctx := context.Background()
	ctx = context.WithValue(ctx, contextkeys.InstanceManager, enterprise.InstanceManager(im))
	ctx = context.WithValue(ctx, contextkeys.LicenseMonitor, enterprise.LicenseMonitor(monitor))
	ctx = context.WithValue(ctx, contextkeys.LicenseEnforcer, enterprise.LicenseEnforcer(enforcer))
	ctx = context.WithValue(ctx, contextkeys.LicenseServiceURL, licenseSrv.URL)
	return &selfHostedHarness{server: &Server{ctx: ctx}, im: im, monitor: monitor, enforcer: enforcer, hits: hits}
}

func activatedInstanceManager() *fakeInstanceManager {
	stateJSON, _ := json.Marshal(&licv1.LicenseState{TenantId: testLocalOrg, PlanTier: licv1.LicenseType_LICENSE_TYPE_PRO})
	return &fakeInstanceManager{
		activated: true,
		localID:   testLocalInstance,
		info:      &enterprise.InstanceInfo{InstanceID: testLocalInstance, LicenseState: stateJSON, ActivatedAt: time.Now()},
	}
}

// sessionCtx mimics what cookieSessionAuthMiddleware installs for a verified
// self-hosted session.
func sessionCtx(role string) context.Context {
	ctx := contextkeys.WithCloudUserID(context.Background(), "55555555-5555-5555-5555-555555555555")
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	ctx = contextkeys.WithTenantID(ctx, "66666666-6666-6666-6666-666666666666")
	if role != "" {
		ctx = contextkeys.WithUserRole(ctx, role)
	}
	return ctx
}

func activate(h *selfHostedHarness, ctx context.Context, instanceID string) error {
	_, err := h.server.ActivateGatewayInstance(ctx, connect.NewRequest(&gatewaypb.ActivateGatewayInstanceRequest{
		ActivationToken: "act_attacker_unused", InstanceId: instanceID,
	}))
	return err
}

// --- item 9: ActivateGatewayInstance ----------------------------------------

func TestActivate_AnonymousCannotReplaceActivatedInstanceCredentials(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())

	err := activate(h, context.Background(), testLocalInstance)
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Fatalf("anonymous re-activation: want Unauthenticated, got %v (%v)", code, err)
	}
	if h.hits.Load() != 0 || h.im.storedCount() != 0 {
		t.Fatalf("anonymous re-activation reached the license service (%d) or stored credentials (%d)", h.hits.Load(), h.im.storedCount())
	}
}

func TestActivate_AnonymousWithoutInstanceIDStillBlockedWhenActivated(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	if err := activate(h, context.Background(), ""); err == nil {
		t.Fatal("anonymous activation of an already-activated gateway succeeded")
	}
	if h.hits.Load() != 0 || h.im.storedCount() != 0 {
		t.Fatal("already-activated guard let the call through")
	}
}

func TestActivate_NonAdminMemberCannotReactivate(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	err := activate(h, sessionCtx("member"), testLocalInstance)
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("member re-activation: want PermissionDenied, got %v (%v)", code, err)
	}
	if h.hits.Load() != 0 || h.im.storedCount() != 0 {
		t.Fatal("member re-activation reached the license service")
	}
}

func TestActivate_AdminCannotRebindToForeignInstanceID(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	err := activate(h, sessionCtx("owner"), testOtherInstance)
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("foreign instance id: want PermissionDenied, got %v (%v)", code, err)
	}
	if h.hits.Load() != 0 {
		t.Fatal("foreign instance id reached the license service")
	}
}

func TestActivate_ActivationStatusErrorFailsClosedForAnonymous(t *testing.T) {
	im := &fakeInstanceManager{activateErr: errors.New("db down")}
	h := newSelfHostedHarness(t, im)
	if err := activate(h, context.Background(), ""); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("status error: want Unauthenticated, got %v", err)
	}
	if h.hits.Load() != 0 {
		t.Fatal("status error failed open")
	}
}

func TestActivate_ManagedGatewayRejectsAnonymous(t *testing.T) {
	h := newSelfHostedHarness(t, &fakeInstanceManager{})
	h.server.ctx = context.WithValue(h.server.ctx, contextkeys.SharedGatewayMode, true)
	if err := activate(h, context.Background(), testLocalInstance); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("managed anonymous activation: want Unauthenticated, got %v", err)
	}
	if h.hits.Load() != 0 {
		t.Fatal("managed anonymous activation reached the license service")
	}
}

// Regression: self-hosted first activation stays anonymous.
func TestActivate_FirstActivationWorksAnonymously(t *testing.T) {
	h := newSelfHostedHarness(t, &fakeInstanceManager{localID: testLocalInstance})
	if err := activate(h, context.Background(), ""); err != nil {
		t.Fatalf("first activation: %v", err)
	}
	if h.hits.Load() != 1 || h.im.storedCount() != 1 {
		t.Fatalf("first activation did not complete: hits=%d stored=%d", h.hits.Load(), h.im.storedCount())
	}
}

// Regression: the admin upgrade dialog re-activates with the gateway's own id.
func TestActivate_AdminUpgradeWithOwnInstanceIDWorks(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	if err := activate(h, sessionCtx("owner"), testLocalInstance); err != nil {
		t.Fatalf("admin upgrade: %v", err)
	}
	if h.hits.Load() != 1 || h.im.storedCount() != 1 {
		t.Fatal("admin upgrade did not reach the license service")
	}
}

// --- item 11: status procedures ---------------------------------------------

func TestGetLicenseMonitorStatus_SelfHostedAnonymousIsRedacted(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	resp, err := h.server.GetLicenseMonitorStatus(context.Background(), connect.NewRequest(&gatewaypb.GetLicenseMonitorStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Msg.GetUsage()
	if u.GetTotalTokens() != 0 || u.GetEstimatedCostUsd() != 0 || u.GetTotalRequests() != 0 {
		t.Fatalf("anonymous caller got usage/cost: %+v", u)
	}
	l := resp.Msg.GetLicense()
	if l.GetTenantId() != "" || l.GetInstanceId() != "" || l.GetSandboxBillingEnabled() || len(l.GetUsageLimits()) != 0 {
		t.Fatalf("anonymous caller got identity/entitlements: %+v", l)
	}
	if l.GetTier() != "pro" || !l.GetActive() || resp.Msg.GetGateway().GetEdition() == "" {
		t.Fatalf("pre-login fields must survive redaction: %+v / %+v", l, resp.Msg.GetGateway())
	}
}

func TestGetLicenseMonitorStatus_SelfHostedAuthenticatedIsFull(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	resp, err := h.server.GetLicenseMonitorStatus(sessionCtx("member"), connect.NewRequest(&gatewaypb.GetLicenseMonitorStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.GetUsage().GetTotalTokens() != 9001 || resp.Msg.GetLicense().GetTenantId() != testLocalOrg ||
		resp.Msg.GetLicense().GetInstanceId() != testLocalInstance {
		t.Fatalf("authenticated caller lost data the billing page needs: %+v", resp.Msg)
	}
}

func TestGetGatewayInstanceStatus_SelfHostedAnonymousHidesLicenseState(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	resp, err := h.server.GetGatewayInstanceStatus(context.Background(), connect.NewRequest(&gatewaypb.GetGatewayInstanceStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.GetInstanceId() != "" || resp.Msg.GetLicenseState() != nil {
		t.Fatalf("anonymous caller got instance id / license state: %+v", resp.Msg)
	}
	if !resp.Msg.GetActivated() {
		t.Fatal("anonymous caller must still learn the gateway is activated")
	}

	full, err := h.server.GetGatewayInstanceStatus(sessionCtx("owner"), connect.NewRequest(&gatewaypb.GetGatewayInstanceStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if full.Msg.GetInstanceId() != testLocalInstance || full.Msg.GetLicenseState() == nil {
		t.Fatalf("authenticated caller (upgrade dialog) lost instance id / license state: %+v", full.Msg)
	}
}

func TestGetTrialStatus_SelfHostedAnonymousHidesFingerprintAndUsage(t *testing.T) {
	t.Setenv("EVS_DATA_DIR", t.TempDir())
	h := newSelfHostedHarness(t, &fakeInstanceManager{})
	tm := trial.NewManager(trial.DefaultConfig(), trial.NewMemoryStorage())
	if err := tm.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = tm.RecordRequest(context.Background())
	h.enforcer.trialMgr = tm

	resp, err := h.server.GetTrialStatus(context.Background(), connect.NewRequest(&gatewaypb.GetTrialStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	m := resp.Msg
	if m.GetFingerprintHash() != "" || m.GetTotalRequests() != 0 || m.GetDailyUsed() != 0 || m.GetTokensUsed() != 0 {
		t.Fatalf("anonymous caller got fingerprint/usage: %+v", m)
	}
	if m.GetMode() != "trial" || !m.GetActive() {
		t.Fatalf("trial mode must still be visible: %+v", m)
	}

	full, err := h.server.GetTrialStatus(sessionCtx("member"), connect.NewRequest(&gatewaypb.GetTrialStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if full.Msg.GetFingerprintHash() == "" || full.Msg.GetTotalRequests() == 0 {
		t.Fatalf("authenticated caller lost trial usage: %+v", full.Msg)
	}
}

func TestRefreshLicenseMonitor_AnonymousRejected(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	_, err := h.server.RefreshLicenseMonitor(context.Background(), connect.NewRequest(&gatewaypb.RefreshLicenseMonitorRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous refresh: want Unauthenticated, got %v", err)
	}
	if h.monitor.refreshes.Load() != 0 || h.enforcer.refreshes.Load() != 0 {
		t.Fatal("anonymous caller triggered an upstream license refresh")
	}
}

func TestRefreshLicenseMonitor_AuthenticatedWorks(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	resp, err := h.server.RefreshLicenseMonitor(sessionCtx("member"), connect.NewRequest(&gatewaypb.RefreshLicenseMonitorRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if h.monitor.refreshes.Load() != 1 || resp.Msg.GetUsage().GetTotalTokens() != 9001 {
		t.Fatalf("authenticated refresh broken: refreshes=%d resp=%+v", h.monitor.refreshes.Load(), resp.Msg)
	}
}

func TestRefreshLicenseMonitor_ManagedDoesNotExposeSharedMonitor(t *testing.T) {
	h := newSelfHostedHarness(t, activatedInstanceManager())
	h.server.ctx = context.WithValue(h.server.ctx, contextkeys.SharedGatewayMode, true)
	resp, err := h.server.RefreshLicenseMonitor(sessionCtx("owner"), connect.NewRequest(&gatewaypb.RefreshLicenseMonitorRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.GetUsage().GetTotalTokens() != 0 || resp.Msg.GetLicense().GetTenantId() == testLocalOrg {
		t.Fatalf("managed refresh leaked the gateway-wide monitor: %+v", resp.Msg)
	}
	if h.monitor.refreshes.Load() != 0 || h.enforcer.refreshes.Load() != 0 {
		t.Fatal("managed tenant triggered a refresh of the shared monitor")
	}
}

func TestPolicy_RefreshLicenseMonitorIsNotPublic(t *testing.T) {
	if policy.NewDefaultPolicy().ShouldBypassProcedure("/everstack.gateway.v1.GatewayService/RefreshLicenseMonitor") {
		t.Fatal("RefreshLicenseMonitor must not bypass authentication")
	}
}

// --- item 12: SubscriptionStatusCallback --------------------------------------

type callbackKeys struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newCallbackKeys(t *testing.T) callbackKeys {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return callbackKeys{pub: pub, priv: priv}
}

func signCallback(t *testing.T, k callbackKeys, org string, audience ...string) string {
	t.Helper()
	claims := SubscriptionStatusCallbackClaims{
		OrganizationID: org, Status: "canceled", PlanTier: "free", EventType: "subscription.canceled",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			Issuer:    "everstack-billing",
		},
	}
	if len(audience) > 0 {
		claims.Audience = jwt.ClaimStrings(audience)
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(k.priv)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func callbackHarness(t *testing.T, k callbackKeys, im *fakeInstanceManager) *selfHostedHarness {
	t.Helper()
	h := newSelfHostedHarness(t, im)
	h.server.ctx = context.WithValue(h.server.ctx, contextkeys.CloudPublicKey, base64.StdEncoding.EncodeToString(k.pub))
	return h
}

func callback(h *selfHostedHarness, token string) error {
	_, err := h.server.SubscriptionStatusCallback(context.Background(), connect.NewRequest(&gatewaypb.SubscriptionStatusCallbackRequest{SignedPayload: token}))
	return err
}

func TestSubscriptionCallback_RejectsOtherOrganizationsPayload(t *testing.T) {
	k := newCallbackKeys(t)
	h := callbackHarness(t, k, activatedInstanceManager())
	if err := callback(h, signCallback(t, k, testOtherOrg)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("replayed foreign-org callback: want PermissionDenied, got %v", err)
	}
	if h.monitor.refreshes.Load() != 0 {
		t.Fatal("foreign-org callback triggered a refresh")
	}
}

func TestSubscriptionCallback_RejectsOtherInstanceAudience(t *testing.T) {
	k := newCallbackKeys(t)
	h := callbackHarness(t, k, activatedInstanceManager())
	if err := callback(h, signCallback(t, k, testLocalOrg, testOtherInstance)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("callback for another instance: want PermissionDenied, got %v", err)
	}
}

func TestSubscriptionCallback_RejectsWhenGatewayHasNoLicensedOrg(t *testing.T) {
	k := newCallbackKeys(t)
	h := callbackHarness(t, k, &fakeInstanceManager{})
	h.monitor.org, h.monitor.inst = "", ""
	if err := callback(h, signCallback(t, k, testLocalOrg)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("unactivated gateway: want PermissionDenied, got %v", err)
	}
}

// Regression: legitimate callbacks, with and without the new audience claim
// (services that are not yet upgraded omit it).
func TestSubscriptionCallback_AcceptsOwnOrganization(t *testing.T) {
	k := newCallbackKeys(t)
	h := callbackHarness(t, k, activatedInstanceManager())
	if err := callback(h, signCallback(t, k, testLocalOrg)); err != nil {
		t.Fatalf("legacy (no aud) callback: %v", err)
	}
	if err := callback(h, signCallback(t, k, testLocalOrg, testLocalInstance)); err != nil {
		t.Fatalf("audience-bound callback: %v", err)
	}
	if h.monitor.refreshes.Load() != 2 {
		t.Fatalf("legit callbacks must refresh: %d", h.monitor.refreshes.Load())
	}
}

// After a restart with the license service unreachable the monitor has no
// org yet; the stored activation still identifies this gateway.
func TestSubscriptionCallback_FallsBackToStoredActivation(t *testing.T) {
	k := newCallbackKeys(t)
	h := callbackHarness(t, k, activatedInstanceManager())
	h.monitor.org, h.monitor.inst = "", ""
	if err := callback(h, signCallback(t, k, testLocalOrg, testLocalInstance)); err != nil {
		t.Fatalf("stored-activation fallback: %v", err)
	}
}

func TestGetGatewayInstanceStatus_SelfHostedAnonymousInactiveHidesLocalID(t *testing.T) {
	h := newSelfHostedHarness(t, &fakeInstanceManager{localID: testLocalInstance})
	resp, err := h.server.GetGatewayInstanceStatus(context.Background(), connect.NewRequest(&gatewaypb.GetGatewayInstanceStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.GetActivated() || resp.Msg.GetInstanceId() != "" {
		t.Fatalf("anonymous caller got the local instance id: %+v", resp.Msg)
	}
	full, err := h.server.GetGatewayInstanceStatus(sessionCtx("owner"), connect.NewRequest(&gatewaypb.GetGatewayInstanceStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if full.Msg.GetInstanceId() != testLocalInstance {
		t.Fatalf("authenticated caller lost the local instance id: %+v", full.Msg)
	}
}

// Billing may still hold the gateway's pre-activation local id (retry and
// manual token paths did not update it), so a callback addressed to the
// local id is this gateway's too.
func TestSubscriptionCallback_AcceptsAudienceOfLocalInstanceID(t *testing.T) {
	k := newCallbackKeys(t)
	im := activatedInstanceManager()
	im.localID = testOtherInstance // local id differs from the activated id
	h := callbackHarness(t, k, im)
	if err := callback(h, signCallback(t, k, testLocalOrg, testOtherInstance)); err != nil {
		t.Fatalf("callback addressed to the local instance id: %v", err)
	}
	// A different gateway's id is still rejected.
	if err := callback(h, signCallback(t, k, testLocalOrg, "99999999-9999-9999-9999-999999999999")); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("foreign audience: want PermissionDenied, got %v", err)
	}
}
