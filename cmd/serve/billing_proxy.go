package serve

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	nethttputil "net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"connectrpc.com/connect"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	billingv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/billing/v1"
)

const (
	billingProcGetModelTrial = "/everstack.billing.v1.BillingService/GetModelTrial"

	// billingProxyMaxBody bounds the request bodies the proxy accepts. The
	// one forwarded message is a single id; the caller's body is discarded.
	billingProxyMaxBody = 16 << 10
)

// billingProxyAllowedProcedures are the BillingService procedures the
// instance console reaches through the gateway (apps/admin/src/server/billing.ts).
// The proxy forwards with the gateway's own SERVICE credentials, so it is an
// allow-list of read-only procedures whose organization the proxy pins
// itself; usage writers, key registration and every customer mutation are
// never forwarded.
var billingProxyAllowedProcedures = map[string]bool{
	billingProcGetModelTrial: true,
}

// newBillingProxyHandler builds the reverse proxy for the billing service
// using the given (credential-attaching) transport. The console is served
// from the instance's own origin, so there is no CORS handling here.
//
// The cloud gateway is one process shared by every tenant: the tenant of a
// request is chosen by its Host, and cookie_session_auth.go stamps a
// RequestInstanceScope{InstanceID, OrganizationID} only after verifying the
// session's user is a member of that instance's organization. That scope is
// the single source of the organization forwarded here. Whatever
// organization_id the browser sent is discarded, never read; nothing
// process-global (the gateway's own licence) is ever used, because it would
// name one organization for every tenant. Billing admits the gateway client
// for this read (services/billing customer_auth.go, gatewayRead) and scopes
// it to that organization.
func newBillingProxyHandler(billingURLStr string, transport http.RoundTripper) (http.Handler, error) {
	targetURL, err := url.Parse(strings.TrimSpace(billingURLStr))
	if err != nil || targetURL.Scheme == "" || targetURL.Host == "" {
		return nil, fmt.Errorf("invalid billing service URL %q", billingURLStr)
	}
	errWriter := connect.NewErrorWriter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		procedure := strings.TrimSuffix(r.URL.Path, "/")
		if !billingProxyAllowedProcedures[procedure] {
			logger.WithFields("path", r.URL.Path).Warn("billing_proxy: refusing to forward procedure with service credentials")
			http.Error(w, "procedure not available through the gateway", http.StatusForbidden)
			return
		}

		if err := authorizeBillingProxyRequest(r); err != nil {
			logger.WithFields("path", r.URL.Path, "reason", err.Error()).Warn("billing_proxy: request refused")
			_ = errWriter.Write(w, r, err)
			return
		}

		proxy := nethttputil.NewSingleHostReverseProxy(targetURL)
		proxy.Transport = transport
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = targetURL.Host
			req.Header.Del("Origin")
			req.Header.Del("Referer")
			// The console's own session cookie means nothing to billing and
			// must not travel with service credentials.
			req.Header.Del("Cookie")
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
			logger.WithFields("path", r.URL.Path, "error", e.Error()).Error("billing proxy error")
			http.Error(w, "Billing service unavailable", http.StatusBadGateway)
		}
		proxy.ServeHTTP(w, r)
	}), nil
}

// billingProxyOrganization returns the organization a request may read
// billing for: the one the verified cookie session was found to be a member
// of for this request's Host. It fails closed on anything else: no user
// session, an API-key principal (keys carry no membership-verified instance
// scope and have no business reading the console's billing views), or a
// session the host middleware did not scope (non-member, self-hosted or
// unresolved host).
func billingProxyOrganization(r *http.Request) (string, error) {
	ctx := r.Context()
	if contextkeys.GetAPIKeyHash(ctx) != "" {
		return "", connect.NewError(connect.CodePermissionDenied, errors.New("API keys cannot read billing for the console"))
	}
	if !contextkeys.IsTenantAuthenticated(ctx) || contextkeys.GetAuthenticatedUserID(ctx) == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	scope, ok := contextkeys.RequestInstanceScopeFromContext(ctx)
	if !ok || strings.TrimSpace(scope.OrganizationID) == "" {
		return "", connect.NewError(connect.CodePermissionDenied, errors.New("no verified instance scope for this request"))
	}
	return strings.TrimSpace(scope.OrganizationID), nil
}

// authorizeBillingProxyRequest authenticates the caller and replaces the
// request body with a message that names the request's verified organization
// and nothing else, in the wire format the client used. It returns a
// *connect.Error for the caller.
func authorizeBillingProxyRequest(r *http.Request) error {
	orgID, err := billingProxyOrganization(r)
	if err != nil {
		return err
	}
	if r.Method != http.MethodPost {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("POST required"))
	}

	codec, err := newLicenseProxyCodec(r)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Read (bounded) and discard: the caller's organization_id is never used.
	raw, err := io.ReadAll(io.LimitReader(r.Body, billingProxyMaxBody+1))
	_ = r.Body.Close()
	if err != nil || len(raw) > billingProxyMaxBody {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("request body unreadable or too large"))
	}

	encoded, err := codec.encode(&billingv1.GetModelTrialRequest{OrganizationId: orgID})
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("encode request"))
	}
	r.Body = io.NopCloser(bytes.NewReader(encoded))
	r.ContentLength = int64(len(encoded))
	r.Header.Set("Content-Length", strconv.Itoa(len(encoded)))
	return nil
}
