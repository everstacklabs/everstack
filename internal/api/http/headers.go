package http

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/gorilla/mux"
)

// Using common package for header constants
const (
	Authorization = "authorization"

	// Other common headers can be moved here as needed
	Accept             = "accept"
	AcceptLanguage     = "accept-language"
	AcceptEncoding     = "accept-encoding"
	AcceptCharset      = "accept-charset"
	AcceptDatetime     = "accept-datetime"
	CacheControl       = "cache-control"
	ContentType        = "content-type"
	ContentLength      = "content-length"
	ContentLocation    = "content-location"
	Expires            = "expires"
	Forwarded          = "forwarded"
	ForwardedFor       = "x-forwarded-for"
	ForwardedProto     = "x-forwarded-proto"
	ForwardedHost      = "x-forwarded-host"
	ForwardedServer    = "x-forwarded-server"
	EverstackForwarded = "x-everstack-forwarded"
	ForwardedVia       = "x-forwarded-via"
	ForwardedBy        = "x-forwarded-by"
	ForwardedByReason  = "x-forwarded-by-reason"
	GrpcMetadataPrefix = "Grpc-Metadata-"

	// Additional headers used by CORS and other components
	Origin         = "origin"
	Location       = "location"
	Referer        = "referer"
	UserAgent      = "user-agent"
	XUserAgent     = "x-user-agent"
	XGrpcWeb       = "x-grpc-web"
	XRequestedWith = "x-requested-with"
	XRequestID     = "x-request-id"
	XCorrelationID = "x-correlation-id"
	XSessionID     = "x-session-id"
	XOrgID         = "x-org-id"
	XUserID        = "x-user-id"
	XRealIP        = "x-real-ip"

	ForwardedByReasonCode = "x-forwarded-by-reason-code"
	Pragma                = "pragma"
	XRobotsTag            = "x-robots-tag"
	IfNoneMatch           = "If-None-Match"
	LastModified          = "Last-Modified"
	Etag                  = "Etag"
	CallDuration          = "x-request-duration-ms"

	ContentSecurityPolicy   = "content-security-policy"
	XXSSProtection          = "x-xss-protection"
	StrictTransportSecurity = "strict-transport-security"
	XFrameOptions           = "x-frame-options"
	XContentTypeOptions     = "x-content-type-options"
	ReferrerPolicy          = "referrer-policy"
	FeaturePolicy           = "feature-policy"
	PermissionsPolicy       = "permissions-policy"

	// Everstack-specific headers. Aliased to the canonical x-evs-* values in the
	// common package (single source of truth); legacy names are accepted at
	// read-sites via common.GetHeader.
	EverstackApiKey     = common.EverstackApiKey
	EverstackLicenseKey = common.EverstackLicenseKey
	EverstackOrgId      = common.EverstackOrgId

	OrgIdInPathVariableName = "orgId"
	OrgIdInPathVariable     = "{" + OrgIdInPathVariableName + "}"
)

type key int

const (
	httpHeaders key = iota
	remoteAddr
	domainCtx
)

func CopyHeadersToContext(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, httpHeaders, r.Header)
		r = r.WithContext(ctx)
		h.ServeHTTP(w, r)
	})
}

func HeadersFromCtx(ctx context.Context) (http.Header, bool) {
	headers, ok := ctx.Value(httpHeaders).(http.Header)
	return headers, ok
}

func OriginHeader(ctx context.Context) string {
	headers, ok := ctx.Value(httpHeaders).(http.Header)
	if !ok {
		return ""
	}
	return headers.Get(Origin)
}

func RemoteIPFromCtx(ctx context.Context) string {
	ctxHeaders, ok := HeadersFromCtx(ctx)
	if !ok {
		return RemoteAddrFromCtx(ctx)
	}
	forwarded, ok := GetForwardedFor(ctxHeaders)
	if ok {
		return forwarded
	}
	return RemoteAddrFromCtx(ctx)
}

func RemoteIPFromRequest(r *http.Request) net.IP {
	return net.ParseIP(RemoteIPStringFromRequest(r))
}

func RemoteIPStringFromRequest(r *http.Request) string {
	ip, ok := GetForwardedFor(r.Header)
	if ok {
		return ip
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func GetAuthorization(r *http.Request) string {
	return r.Header.Get(common.Authorization)
}

func GetOrgID(r *http.Request) string {
	// path variable takes precedence over header
	orgID, ok := mux.Vars(r)[OrgIdInPathVariableName]
	if ok {
		return orgID
	}

	return common.GetHTTPHeader(r.Header, common.EverstackOrgID, common.LegacyMFOrgID)
}

func GetForwardedFor(headers http.Header) (string, bool) {
	forwarded := strings.Split(headers.Get(common.ForwardedFor), ",")[0]
	return forwarded, forwarded != ""
}

func RemoteAddrFromCtx(ctx context.Context) string {
	ctxRemoteAddr, _ := ctx.Value(remoteAddr).(string)
	return ctxRemoteAddr
}
