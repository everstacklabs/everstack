package serve

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	billingv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/billing/v1"
)

// The billing proxy forwards the console's GetModelTrial read with the
// gateway's SERVICE credentials. The cloud gateway is one process shared by
// every tenant, so the organization it asks about must be the one the
// request's verified session was scoped to for its Host (RequestInstanceScope),
// whatever the browser put in the body, and only such a session may trigger
// it: no API keys, no unscoped sessions, nothing process-global.

const (
	tenantAOrg  = "aaaaaaaa-0000-4000-8000-00000000000a"
	tenantAInst = "aaaaaaaa-1111-4000-8000-00000000000a"
	tenantBOrg  = "bbbbbbbb-0000-4000-8000-00000000000b"
	tenantBInst = "bbbbbbbb-1111-4000-8000-00000000000b"
)

// scopedSessionCtx mimics cookie_session_auth.go after it verified the
// session's user is a member of the Host instance's organization.
func scopedSessionCtx(org, inst string) context.Context {
	ctx := contextkeys.WithCloudUserID(context.Background(), "55555555-5555-5555-5555-555555555555")
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	ctx = contextkeys.WithTenantID(ctx, inst)
	return contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{InstanceID: inst, OrganizationID: org})
}

// unscopedSessionCtx is a verified user session the host middleware did not
// scope: a non-member of the Host's organization, or a host it could not
// resolve. The tenant id is the user's own first tenant, not a verified org.
func unscopedSessionCtx() context.Context {
	ctx := contextkeys.WithCloudUserID(context.Background(), "55555555-5555-5555-5555-555555555555")
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	return contextkeys.WithTenantID(ctx, tenantAInst)
}

// apiKeyCtx is an API-key principal on the same host, scope and all.
func apiKeyCtx() context.Context {
	ctx := contextkeys.WithTenantAuthenticated(context.Background())
	ctx = contextkeys.WithTenantID(ctx, tenantAInst)
	ctx = contextkeys.WithAPIKeyHash(ctx, "hash-of-some-key")
	return contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{InstanceID: tenantAInst, OrganizationID: tenantAOrg})
}

type recordingBillingBackend struct {
	mu     sync.Mutex
	paths  []string
	auths  []string
	cookie []string
	orgs   []string
}

func (b *recordingBillingBackend) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		org := ""
		switch {
		case strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc-web"):
			var msg billingv1.GetModelTrialRequest
			if len(raw) >= 5 && proto.Unmarshal(raw[5:], &msg) == nil {
				org = msg.GetOrganizationId()
			}
		default:
			var body struct {
				OrganizationID string `json:"organizationId"`
			}
			_ = json.Unmarshal(raw, &body)
			org = body.OrganizationID
		}
		b.mu.Lock()
		b.paths = append(b.paths, r.URL.Path)
		b.auths = append(b.auths, r.Header.Get("Authorization"))
		b.cookie = append(b.cookie, r.Header.Get("Cookie"))
		b.orgs = append(b.orgs, org)
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"organizationId":"` + org + `","state":"MODEL_TRIAL_STATE_ACTIVE","remaining":20}`))
	})
}

func newBillingProxyUnderTest(t *testing.T) (http.Handler, *recordingBillingBackend) {
	t.Helper()
	backend := &recordingBillingBackend{}
	server := httptest.NewServer(backend.handler())
	t.Cleanup(server.Close)
	h, err := newBillingProxyHandler(server.URL, staticAuthTransport{base: http.DefaultTransport})
	require.NoError(t, err)
	return h, backend
}

func billingProxyCall(h http.Handler, ctx context.Context, proc, contentType, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/everstack.billing.v1.BillingService/"+proc, strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Cookie", "es_session=console-session-cookie")
	h.ServeHTTP(rec, req)
	return rec
}

func TestBillingProxyPinsOrganizationToTheRequestScope(t *testing.T) {
	h, backend := newBillingProxyUnderTest(t)

	// Two tenants on the same shared proxy. A session scoped to B asking
	// for A, or for nothing, reaches billing as B; a session scoped to A as A.
	cases := []struct {
		ctx  context.Context
		body string
		want string
	}{
		{scopedSessionCtx(tenantBOrg, tenantBInst), `{"organizationId":"` + tenantAOrg + `"}`, tenantBOrg},
		{scopedSessionCtx(tenantBOrg, tenantBInst), `{}`, tenantBOrg},
		{scopedSessionCtx(tenantAOrg, tenantAInst), `{"organizationId":"` + tenantBOrg + `"}`, tenantAOrg},
	}
	for _, c := range cases {
		rec := billingProxyCall(h, c.ctx, "GetModelTrial", "application/json", c.body)
		require.Equal(t, http.StatusOK, rec.Code, c.body)
		require.Contains(t, rec.Body.String(), `"organizationId":"`+c.want+`"`)
	}
	require.Equal(t, []string{tenantBOrg, tenantBOrg, tenantAOrg}, backend.orgs)
	for _, a := range backend.auths {
		require.Equal(t, "Bearer portal-service-token", a, "forwarded with the service credentials")
	}
	for _, c := range backend.cookie {
		require.Empty(t, c, "the console session cookie must not reach billing")
	}
}

func TestBillingProxyPinsOrganizationOnGRPCWeb(t *testing.T) {
	h, backend := newBillingProxyUnderTest(t)

	payload, err := proto.Marshal(&billingv1.GetModelTrialRequest{OrganizationId: tenantAOrg})
	require.NoError(t, err)
	frame := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)

	rec := billingProxyCall(h, scopedSessionCtx(tenantBOrg, tenantBInst), "GetModelTrial", "application/grpc-web+proto", string(frame))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{tenantBOrg}, backend.orgs)
}

func TestBillingProxyRefusesEverythingButAScopedUserSession(t *testing.T) {
	h, backend := newBillingProxyUnderTest(t)
	body := `{"organizationId":"` + tenantAOrg + `"}`

	cases := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"no session", context.Background(), http.StatusUnauthorized},
		{"tenant authenticated, no user", contextkeys.WithTenantID(contextkeys.WithTenantAuthenticated(context.Background()), tenantAInst), http.StatusUnauthorized},
		{"user session without an instance scope (non-member or unresolved host)", unscopedSessionCtx(), http.StatusForbidden},
		{"API-key principal, even with a scope", apiKeyCtx(), http.StatusForbidden},
		{"scope with an empty organization", contextkeys.WithRequestInstanceScope(unscopedSessionCtx(), contextkeys.RequestInstanceScope{InstanceID: tenantAInst}), http.StatusForbidden},
	}
	for _, c := range cases {
		rec := billingProxyCall(h, c.ctx, "GetModelTrial", "application/json", body)
		require.Equal(t, c.want, rec.Code, c.name)
	}
	require.Empty(t, backend.paths, "refused calls must never reach billing")
}

func TestBillingProxyRefusesWrongMethodAndBadURL(t *testing.T) {
	h, backend := newBillingProxyUnderTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/everstack.billing.v1.BillingService/GetModelTrial", nil)
	req = req.WithContext(scopedSessionCtx(tenantAOrg, tenantAInst))
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, backend.paths)

	for _, bad := range []string{"", "   ", "not a url", "/relative/only", "billing.internal:8080"} {
		_, err := newBillingProxyHandler(bad, http.DefaultTransport)
		require.Error(t, err, bad)
	}
}

func TestBillingProxyForwardsOnlyGetModelTrial(t *testing.T) {
	h, backend := newBillingProxyUnderTest(t)

	for _, proc := range []string{
		"RegisterApiKey", "UnregisterApiKey", "ReportUsage", "GetSpendLimits",
		"SetSpendLimit", "GetCreditBalance", "PurchaseCredits", "CreateInstanceMapping",
	} {
		rec := billingProxyCall(h, scopedSessionCtx(tenantAOrg, tenantAInst), proc, "application/json", `{"organizationId":"`+tenantAOrg+`"}`)
		require.Equal(t, http.StatusForbidden, rec.Code, proc)
	}
	require.Empty(t, backend.paths, "denied procedures must never reach billing")
}
