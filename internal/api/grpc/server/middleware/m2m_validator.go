package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// M2MValidatorConfig configures the M2M validator interceptor
type M2MValidatorConfig struct {
	// Services contains credentials for pre-registered services (portal, internal)
	Services map[string]ServiceCredential

	// InstanceLookup retrieves the signing key for a gateway instance
	InstanceLookup func(ctx context.Context, instanceID string) (signingKey []byte, err error)

	// NonceStore for replay attack prevention
	NonceStore NonceStore

	// TimestampWindow is the maximum age of a request (default: 5 minutes)
	TimestampWindow time.Duration

	// NonceTTL is how long nonces are stored (default: 10 minutes)
	NonceTTL time.Duration

	// PublicEndpoints are procedures that bypass M2M authentication entirely
	// These are typically read-only endpoints needed before activation
	PublicEndpoints []string

	// AllowAllAuthenticated: if true, any authenticated client can access any endpoint
	// This is the simple/recommended approach - M2M handles authentication only.
	AllowAllAuthenticated bool

	// AllowedClients maps procedure names to allowed client types (optional)
	// Only used if AllowAllAuthenticated is false
	AllowedClients map[string][]string

	// Enabled controls whether M2M validation is active (default: true)
	Enabled bool
}

// M2MValidatorInterceptor validates M2M authentication for incoming requests
type M2MValidatorInterceptor struct {
	config *M2MValidatorConfig
}

// NewM2MValidatorInterceptor creates a new M2M validator interceptor
func NewM2MValidatorInterceptor(cfg *M2MValidatorConfig) *M2MValidatorInterceptor {
	if cfg == nil {
		cfg = &M2MValidatorConfig{Enabled: false}
	}

	// Set defaults
	if cfg.TimestampWindow <= 0 {
		cfg.TimestampWindow = 5 * time.Minute
	}
	if cfg.NonceTTL <= 0 {
		cfg.NonceTTL = 10 * time.Minute
	}
	if cfg.NonceStore == nil {
		cfg.NonceStore = NewNoopNonceStore()
	}

	return &M2MValidatorInterceptor{config: cfg}
}

// WrapUnary implements connect.Interceptor for unary requests
func (i *M2MValidatorInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if !i.config.Enabled {
			return next(ctx, req)
		}

		procedure := req.Spec().Procedure

		// Check if this is a public endpoint (bypass M2M auth)
		if i.isPublicEndpoint(procedure) {
			return next(ctx, req)
		}

		headers := req.Header()

		// Validate the request
		clientType, clientID, err := i.validateRequest(ctx, procedure, headers, nil)
		if err != nil {
			logger.WithFields(
				"procedure", procedure,
				"error", err.Error(),
			).Warn("m2m_validator: authentication failed")
			return nil, i.toConnectError(err)
		}

		logger.WithFields(
			"procedure", procedure,
			"client_type", clientType,
			"client_id", truncateID(clientID),
		).Debug("m2m_validator: request authenticated")

		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor (no-op for client-side)
func (i *M2MValidatorInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor for streaming requests
func (i *M2MValidatorInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if !i.config.Enabled {
			return next(ctx, conn)
		}

		procedure := conn.Spec().Procedure

		// Check if this is a public endpoint (bypass M2M auth)
		if i.isPublicEndpoint(procedure) {
			return next(ctx, conn)
		}

		headers := conn.RequestHeader()

		// Validate the request
		clientType, clientID, err := i.validateRequest(ctx, procedure, headers, nil)
		if err != nil {
			logger.WithFields(
				"procedure", procedure,
				"error", err.Error(),
			).Warn("m2m_validator: streaming authentication failed")
			return i.toConnectError(err)
		}

		logger.WithFields(
			"procedure", procedure,
			"client_type", clientType,
			"client_id", truncateID(clientID),
		).Debug("m2m_validator: streaming request authenticated")

		return next(ctx, conn)
	}
}

// validateRequest performs the full M2M authentication chain
func (i *M2MValidatorInterceptor) validateRequest(ctx context.Context, procedure string, headers http.Header, body []byte) (clientType, clientID string, err error) {
	// Step 1: Extract client identity
	identityType, identityValue, err := ExtractClientIdentity(headers)
	if err != nil {
		return "", "", err
	}

	// Step 2: Look up signing key based on client type
	var signingKey []byte
	var resolvedClientType string

	switch identityType {
	case "gateway":
		// Gateway instance - look up signing key from database
		if i.config.InstanceLookup == nil {
			return "", "", ErrUnknownClient
		}
		signingKey, err = i.config.InstanceLookup(ctx, identityValue)
		if err != nil {
			logger.WithFields(
				"instance_id", truncateID(identityValue),
				"error", err.Error(),
			).Warn("m2m_validator: instance lookup failed")
			return "", "", ErrUnknownClient
		}
		resolvedClientType = "gateway"
		clientID = identityValue

	case "service":
		// Service token - validate against registered services
		serviceCred, serviceName := i.lookupServiceByToken(identityValue)
		if serviceCred == nil {
			return "", "", ErrUnknownClient
		}
		signingKey = serviceCred.SigningKey
		resolvedClientType = serviceName
		clientID = serviceName
	}

	// Step 3: Verify timestamp is within window
	timestampStr := GetTimestamp(headers)
	if timestampStr == "" {
		return "", "", ErrMissingTimestamp
	}

	// Step 4: Verify HMAC signature
	// Note: For Connect/gRPC requests, body may be nil at interceptor level
	// The signature verification will use the method and path
	method := "POST" // Connect always uses POST
	path := procedure

	err = VerifySignature(method, path, body, headers, signingKey, i.config.TimestampWindow)
	if err != nil {
		return "", "", err
	}

	// Step 5: Check and store nonce (replay prevention)
	nonce := GetNonce(headers)
	if nonce == "" {
		return "", "", ErrMissingNonce
	}

	err = i.config.NonceStore.CheckAndStore(ctx, nonce, clientID, i.config.NonceTTL)
	if err != nil {
		return "", "", err
	}

	// Step 6: Check if client type is allowed for this procedure
	if !i.isClientAllowed(procedure, resolvedClientType) {
		return "", "", ErrClientNotAllowed
	}

	return resolvedClientType, clientID, nil
}

// lookupServiceByToken finds a service credential by its token
func (i *M2MValidatorInterceptor) lookupServiceByToken(token string) (*ServiceCredential, string) {
	if i.config.Services == nil {
		return nil, ""
	}

	tokenHash := HashToken(token)

	for name, cred := range i.config.Services {
		if cred.TokenHash == tokenHash {
			return &cred, name
		}
	}

	return nil, ""
}

// isClientAllowed checks if a client type is allowed to access a procedure
func (i *M2MValidatorInterceptor) isClientAllowed(procedure, clientType string) bool {
	// Simple mode: any authenticated client can access any endpoint
	// This is the recommended approach - M2M handles authentication,
	// authorization should be handled at the application layer.
	if i.config.AllowAllAuthenticated {
		return true
	}

	// If no policy defined, allow all authenticated clients (backward compat)
	if len(i.config.AllowedClients) == 0 {
		return true
	}

	// Check for exact procedure match
	if allowed, ok := i.config.AllowedClients[procedure]; ok {
		return containsClientType(allowed, clientType)
	}

	// Check for service-level match (e.g., "LicenseService" matches any method)
	serviceName := extractServiceName(procedure)
	if allowed, ok := i.config.AllowedClients[serviceName]; ok {
		return containsClientType(allowed, clientType)
	}

	// No matching policy - deny by default (zero trust)
	return false
}

// isPublicEndpoint checks if a procedure is in the public endpoints list
func (i *M2MValidatorInterceptor) isPublicEndpoint(procedure string) bool {
	for _, ep := range i.config.PublicEndpoints {
		if ep == procedure {
			return true
		}
	}
	return false
}

// extractServiceName extracts the service name from a procedure path
// Input:  "/everstack.license.v1.LicenseService/GetPlans"
// Output: "LicenseService"
func extractServiceName(procedure string) string {
	// Remove leading slash
	p := strings.TrimPrefix(procedure, "/")

	// Find the method separator
	slashIdx := strings.LastIndex(p, "/")
	if slashIdx == -1 {
		return ""
	}

	// Get the service part (before the method)
	servicePath := p[:slashIdx]

	// Extract just the service name (after the last dot)
	dotIdx := strings.LastIndex(servicePath, ".")
	if dotIdx == -1 {
		return servicePath
	}

	return servicePath[dotIdx+1:]
}

// toConnectError converts an M2M error to a Connect error
func (i *M2MValidatorInterceptor) toConnectError(err error) error {
	switch err {
	case ErrMissingIdentity, ErrMissingSignature, ErrMissingTimestamp, ErrMissingNonce:
		return connect.NewError(connect.CodeUnauthenticated, err)
	case ErrInvalidSignature, ErrInvalidTimestamp:
		return connect.NewError(connect.CodeUnauthenticated, err)
	case ErrTimestampExpired, ErrTimestampFuture:
		return connect.NewError(connect.CodeUnauthenticated, err)
	case ErrNonceReused:
		return connect.NewError(connect.CodeUnauthenticated, err)
	case ErrUnknownClient:
		return connect.NewError(connect.CodeUnauthenticated, err)
	case ErrClientNotAllowed:
		return connect.NewError(connect.CodePermissionDenied, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// containsClientType checks if a slice contains a client type
func containsClientType(allowed []string, clientType string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, clientType) {
			return true
		}
	}
	return false
}

// truncateID truncates an ID for safe logging
func truncateID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "..."
}

// M2MValidatorHTTPMiddleware wraps an HTTP handler with M2M validation
func M2MValidatorHTTPMiddleware(cfg *M2MValidatorConfig) func(http.Handler) http.Handler {
	interceptor := NewM2MValidatorInterceptor(cfg)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Read body for signature verification
			var body []byte
			if r.Body != nil {
				var err error
				body, err = io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "Failed to read request body", http.StatusBadRequest)
					return
				}
				// Restore body for downstream handlers
				r.Body = io.NopCloser(bytes.NewReader(body))
			}

			// Validate request
			_, _, err := interceptor.validateRequest(r.Context(), r.URL.Path, r.Header, body)
			if err != nil {
				logger.WithFields(
					"path", r.URL.Path,
					"error", err.Error(),
				).Warn("m2m_validator: HTTP authentication failed")

				code := http.StatusUnauthorized
				if err == ErrClientNotAllowed {
					code = http.StatusForbidden
				}
				http.Error(w, err.Error(), code)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
