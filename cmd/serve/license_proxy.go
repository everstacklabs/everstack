package serve

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"net/http"
	nethttputil "net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
)

const (
	licenseProcGetPlans         = "/everstack.license.v1.LicenseService/GetPlans"
	licenseProcGetSpendLimits   = "/everstack.license.v1.LicenseService/GetSpendLimits"
	licenseProcSetSpendLimit    = "/everstack.license.v1.LicenseService/SetSpendLimit"
	licenseProcDeleteSpendLimit = "/everstack.license.v1.LicenseService/DeleteSpendLimit"
	licenseProcCheckSpendLimit  = "/everstack.license.v1.LicenseService/CheckSpendLimit"

	// licenseProxyMaxBody bounds the spend-limit request bodies the proxy
	// parses. They are a handful of scalar fields.
	licenseProxyMaxBody = 64 << 10
)

// licenseProxyAllowedProcedures are the LicenseService procedures the admin UI
// reaches through the gateway's license proxy (apps/admin/src/server/license.ts).
// The proxy attaches the portal SERVICE credentials to whatever it forwards,
// so it must be an allow-list: GenerateActivationToken and the license CRUD
// RPCs are never forwarded, or any caller that reaches the gateway would mint
// licenses as the portal client.
var licenseProxyAllowedProcedures = map[string]bool{
	licenseProcGetPlans:         true,
	licenseProcGetSpendLimits:   true,
	licenseProcSetSpendLimit:    true,
	licenseProcDeleteSpendLimit: true,
	licenseProcCheckSpendLimit:  true,
}

// licenseProxyAdminProcedures change spend limits: owner/admin only.
var licenseProxyAdminProcedures = map[string]bool{
	licenseProcSetSpendLimit:    true,
	licenseProcDeleteSpendLimit: true,
}

// licensedIdentityFunc returns the organization and instance this gateway is
// licensed to (enterprise.LicensedIdentity). Empty org = not activated.
type licensedIdentityFunc func() (orgID, instanceID string)

// newLicenseProxyHandler builds the reverse proxy for the license service
// using the given (credential-attaching) transport.
//
// Everything except GetPlans is authenticated here (EVS-324 item 13): the
// portal credentials would otherwise let any caller that reaches the gateway
// read, set or delete spend limits for any organization id it names. The
// caller must be a verified principal of this gateway (owner/admin for
// Set/Delete), and organization_id / instance_id must be the license this
// gateway holds. The admin UI already sends exactly those
// (billing-page.tsx: license.tenant_id / license.instance_id). The body is
// parsed, checked and re-encoded, so the license service sees only the
// message that was checked.
func newLicenseProxyHandler(licenseURLStr string, transport http.RoundTripper, identity licensedIdentityFunc) http.Handler {
	targetURL, _ := url.Parse(licenseURLStr)
	lookupClient := licenseconnect.NewLicenseServiceClient(&http.Client{Transport: transport, Timeout: 10 * time.Second}, licenseURLStr)
	errWriter := connect.NewErrorWriter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add CORS headers for browser requests
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Connect-Protocol-Version, Connect-Timeout-Ms, X-User-Agent")
		w.Header().Set("Access-Control-Expose-Headers", "Connect-Content-Encoding, Connect-Timeout-Ms")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		procedure := strings.TrimSuffix(r.URL.Path, "/")
		if !licenseProxyAllowedProcedures[procedure] {
			logger.WithFields("path", r.URL.Path).Warn("license_proxy: refusing to forward procedure with service credentials")
			http.Error(w, "procedure not available through the gateway", http.StatusForbidden)
			return
		}

		if procedure != licenseProcGetPlans {
			if err := authorizeLicenseProxyRequest(r, procedure, identity, lookupClient); err != nil {
				logger.WithFields("path", r.URL.Path, "reason", err.Error()).Warn("license_proxy: request refused")
				_ = errWriter.Write(w, r, err)
				return
			}
		}

		proxy := nethttputil.NewSingleHostReverseProxy(targetURL)
		proxy.Transport = transport

		// Fix the director to properly set the host
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = targetURL.Host
			req.Header.Del("Origin")
			req.Header.Del("Referer")
		}

		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
			logger.WithFields("path", r.URL.Path, "error", e.Error()).Error("license proxy error")
			http.Error(w, "License service unavailable", http.StatusBadGateway)
		}

		proxy.ServeHTTP(w, r)
	})
}

// authorizeLicenseProxyRequest authenticates the caller, decodes the body,
// pins it to this gateway's license and replaces r.Body with the re-encoded
// message. It returns a *connect.Error for the caller.
func authorizeLicenseProxyRequest(r *http.Request, procedure string, identity licensedIdentityFunc, lookup licenseconnect.LicenseServiceClient) error {
	ctx := r.Context()
	if !contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if licenseProxyAdminProcedures[procedure] && !contextkeys.IsVerifiedOrgAdmin(ctx) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("owner or admin role required"))
	}
	if r.Method != http.MethodPost {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("POST required"))
	}
	// A shared gateway serves many tenants, so one license can't speak for the
	// caller. Pinning to it would hand that org's limits to every tenant.
	if enterprise.ManagedGateway() {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("spend limits are not available on a shared gateway"))
	}
	orgID, instanceID := "", ""
	if identity != nil {
		orgID, instanceID = identity()
	}
	if orgID == "" {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("gateway is not activated"))
	}

	codec, err := newLicenseProxyCodec(r)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, licenseProxyMaxBody+1))
	_ = r.Body.Close()
	if err != nil || len(raw) > licenseProxyMaxBody {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("request body unreadable or too large"))
	}

	var msg proto.Message
	switch procedure {
	case licenseProcGetSpendLimits:
		msg = &licv1.GetSpendLimitsRequest{}
	case licenseProcSetSpendLimit:
		msg = &licv1.SetSpendLimitRequest{}
	case licenseProcDeleteSpendLimit:
		msg = &licv1.DeleteSpendLimitRequest{}
	case licenseProcCheckSpendLimit:
		msg = &licv1.CheckSpendLimitRequest{}
	default:
		return connect.NewError(connect.CodePermissionDenied, errors.New("procedure not available"))
	}
	if err := codec.decode(raw, msg); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	bound := func(org, inst string) error {
		if org != orgID {
			return connect.NewError(connect.CodePermissionDenied, errors.New("organization_id is not this gateway's organization"))
		}
		if inst != "" && inst != instanceID {
			return connect.NewError(connect.CodePermissionDenied, errors.New("instance_id is not this gateway's instance"))
		}
		return nil
	}
	switch m := msg.(type) {
	case *licv1.GetSpendLimitsRequest:
		err = bound(m.GetOrganizationId(), m.GetInstanceId())
	case *licv1.SetSpendLimitRequest:
		err = bound(m.GetOrganizationId(), m.GetInstanceId())
	case *licv1.CheckSpendLimitRequest:
		err = bound(m.GetOrganizationId(), m.GetInstanceId())
	case *licv1.DeleteSpendLimitRequest:
		err = spendLimitBelongsTo(ctx, lookup, m.GetId(), orgID)
	}
	if err != nil {
		return err
	}

	encoded, err := codec.encode(msg)
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("re-encode request"))
	}
	r.Body = io.NopCloser(bytes.NewReader(encoded))
	r.ContentLength = int64(len(encoded))
	r.Header.Set("Content-Length", strconv.Itoa(len(encoded)))
	return nil
}

// spendLimitBelongsTo checks a DeleteSpendLimit id against the limits of the
// gateway's own organization; the request carries no organization.
func spendLimitBelongsTo(ctx context.Context, lookup licenseconnect.LicenseServiceClient, id, orgID string) error {
	if strings.TrimSpace(id) == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := lookup.GetSpendLimits(lookupCtx, connect.NewRequest(&licv1.GetSpendLimitsRequest{OrganizationId: orgID}))
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("could not verify spend limit ownership"))
	}
	for _, limit := range resp.Msg.GetSpendLimits() {
		if limit.GetId() == id && limit.GetOrganizationId() == orgID {
			return nil
		}
	}
	return connect.NewError(connect.CodePermissionDenied, errors.New("spend limit is not this gateway's organization"))
}

// licenseProxyCodec decodes and re-encodes a unary request body in the wire
// format the client used: Connect unary (proto / JSON) or gRPC-web (the
// admin UI's transport, packages/client/src/web.ts). Streaming, compressed
// and base64 gRPC-web-text bodies are refused rather than forwarded unchecked.
type licenseProxyCodec struct {
	json    bool
	grpcWeb bool
}

func newLicenseProxyCodec(r *http.Request) (licenseProxyCodec, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return licenseProxyCodec{}, errors.New("unsupported content type")
	}
	var c licenseProxyCodec
	switch strings.ToLower(mediaType) {
	case "application/proto":
	case "application/json":
		c.json = true
	case "application/grpc-web", "application/grpc-web+proto":
		c.grpcWeb = true
	case "application/grpc-web+json":
		c.grpcWeb, c.json = true, true
	default:
		return licenseProxyCodec{}, errors.New("unsupported content type")
	}
	if enc := strings.TrimSpace(r.Header.Get("Content-Encoding")); enc != "" && !strings.EqualFold(enc, "identity") {
		return licenseProxyCodec{}, errors.New("compressed requests are not supported")
	}
	if c.grpcWeb {
		if enc := strings.TrimSpace(r.Header.Get("Grpc-Encoding")); enc != "" && !strings.EqualFold(enc, "identity") {
			return licenseProxyCodec{}, errors.New("compressed requests are not supported")
		}
	}
	return c, nil
}

func (c licenseProxyCodec) decode(raw []byte, msg proto.Message) error {
	payload := raw
	if c.grpcWeb {
		// Exactly one uncompressed data frame: flags(1) | length(4) | message.
		if len(raw) < 5 {
			return errors.New("malformed gRPC-web frame")
		}
		if raw[0] != 0 {
			return errors.New("compressed or trailer frames are not supported")
		}
		n := binary.BigEndian.Uint32(raw[1:5])
		if uint64(len(raw)-5) != uint64(n) {
			return errors.New("malformed gRPC-web frame")
		}
		payload = raw[5:]
	}
	if c.json {
		// protojson rejects unknown and duplicate fields.
		return protojson.Unmarshal(payload, msg)
	}
	return proto.Unmarshal(payload, msg)
}

func (c licenseProxyCodec) encode(msg proto.Message) ([]byte, error) {
	var payload []byte
	var err error
	if c.json {
		payload, err = protojson.Marshal(msg)
	} else {
		payload, err = proto.Marshal(msg)
	}
	if err != nil {
		return nil, err
	}
	if !c.grpcWeb {
		return payload, nil
	}
	framed := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(framed[1:5], uint32(len(payload)))
	copy(framed[5:], payload)
	return framed, nil
}
