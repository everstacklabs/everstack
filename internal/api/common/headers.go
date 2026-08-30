package common

// HTTP header constants shared across the application
const (
	// Authorization is the HTTP Authorization header
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
	ForwardedFor       = "x-forwarded-for"
	ForwardedProto     = "x-forwarded-proto"
	ForwardedHost      = "x-forwarded-host"
	ForwardedServer    = "x-forwarded-server"
	EverstackForwarded = "x-everstack-forwarded"
	ForwardedVia       = "x-forwarded-via"
	ForwardedBy        = "x-forwarded-by"
	ForwardedByReason  = "x-forwarded-by-reason"
	SetCookie          = "set-cookie"
	SecFetchSite       = "sec-fetch-site"

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
	XThreadID      = "x-thread-id"
	XOrgID         = "x-org-id"
	XUserID        = "x-user-id"
	XRealIP        = "x-real-ip"
	SameOrigin     = "same-origin"

	// Everstack-specific headers. Canonical form is x-evs-*. These constants hold
	// the CANONICAL name — emit these. Read-sites must also accept the legacy
	// x-mf-* and x-everstack-* names via the Legacy* aliases below and
	// common.GetHeader (which records legacy usage for eventual retirement).
	EverstackApiKey          = "x-evs-api-key"
	EverstackLicenseKey      = "x-evs-license-key"
	EverstackOrgId           = "x-evs-org-id"
	EverstackFallbackUsed    = "x-evs-fallback-used"
	EverstackRequestedModel  = "x-evs-requested-model"
	EverstackActualModel     = "x-evs-actual-model"
	EverstackFallbackReason  = "x-evs-fallback-reason"
	EverstackFallbackAttempt = "x-evs-fallback-attempt"
	EverstackUserID          = "x-evs-user-id"
	EverstackSessionID       = "x-evs-session-id"
	EverstackThreadID        = "x-evs-thread-id"
	EverstackTenantID        = "x-evs-tenant-id"
	EverstackAPIKeyHash      = "x-evs-api-key-hash"
	EverstackAPIKey          = "x-evs-api-key"
	EverstackOrgID           = "x-evs-org-id"
	EverstackMode            = "x-evs-mode"

	// Legacy x-mf-* names, kept from before the rename. Accepted at read-sites
	// for backward compatibility with deployed CLIs, published SDKs, and user
	// integrations.
	// Do NOT emit these except where a comment explicitly documents emit-both.
	LegacyMFApiKey          = "x-mf-api-key"
	LegacyMFLicenseKey      = "x-mf-license-key"
	LegacyMFOrgID           = "x-mf-org-id"
	LegacyMFFallbackUsed    = "x-mf-fallback-used"
	LegacyMFRequestedModel  = "x-mf-requested-model"
	LegacyMFActualModel     = "x-mf-actual-model"
	LegacyMFFallbackReason  = "x-mf-fallback-reason"
	LegacyMFFallbackAttempt = "x-mf-fallback-attempt"
	LegacyMFUserID          = "x-mf-user-id"
	LegacyMFSessionID       = "x-mf-session-id"
	LegacyMFThreadID        = "x-mf-thread-id"
	LegacyMFTenantID        = "x-mf-tenant-id"
	LegacyMFAPIKeyHash      = "x-mf-api-key-hash"
	LegacyMFMode            = "x-mf-mode"

	// Legacy x-everstack-* names (partial rebrand). Accepted at read-sites; still
	// emitted alongside the canonical name on external sender sites (OTLP ingest,
	// sandbox webhook signature, license response headers) so shipped consumers
	// keep working.
	LegacyEverstackApiKey = "x-everstack-api-key"
	LegacyEverstackOrgID  = "x-everstack-org-id"
	LegacyEverstackUserID = "x-everstack-user-id"
	LegacyEverstackMode   = "x-everstack-mode"
)
