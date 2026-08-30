package m2m

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/auth/session"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// claimsContextKey is the context key for M2M claims.
type claimsContextKey struct{}

// ClaimsFromContext extracts M2M claims from the context.
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsContextKey{}).(*Claims)
	return claims
}

// ContextWithClaims returns a new context with M2M claims.
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// InterceptorConfig configures the M2M interceptor.
type InterceptorConfig struct {
	// Validator is the token validator
	Validator TokenValidator

	// PublicEndpoints are procedures that bypass M2M authentication
	PublicEndpoints []string

	// RequiredAudience is the audience that must be present in the token (optional)
	RequiredAudience string

	// RequiredScopes are scopes that must be present for all requests (optional)
	// These apply to ALL protected endpoints
	RequiredScopes []string

	// EndpointScopes contains explicit scope overrides for specific endpoints
	// Only used for exceptions - most scopes are auto-derived
	EndpointScopes map[string][]string

	// ScopePolicy configures automatic scope derivation from endpoint names
	ScopePolicy *ScopePolicyConfig

	// AllowAllAuthenticated allows any authenticated client to access endpoints
	// that don't have explicit scope requirements in EndpointScopes
	AllowAllAuthenticated bool
}

// Interceptor is a Connect interceptor that validates M2M tokens.
type Interceptor struct {
	config *InterceptorConfig
}

// NewInterceptor creates a new M2M interceptor.
func NewInterceptor(config *InterceptorConfig) *Interceptor {
	return &Interceptor{config: config}
}

// WrapUnary implements connect.Interceptor for unary requests.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure

		// Check if this is a public endpoint
		if i.isPublicEndpoint(procedure) {
			return next(ctx, req)
		}

		// Check if session auth has already authenticated this request
		if session.IsSessionAuthenticated(ctx) {
			logger.Debugf("m2m: skipping M2M auth, already authenticated via session for %s", procedure)
			return next(ctx, req)
		}

		// Extract and validate token
		claims, err := i.validateRequest(ctx, req.Header())
		if err != nil {
			return nil, i.toConnectError(err)
		}

		// Check endpoint-specific scopes
		if err := i.checkEndpointScopes(procedure, claims); err != nil {
			return nil, i.toConnectError(err)
		}

		// Add claims to context
		ctx = ContextWithClaims(ctx, claims)

		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor (no-op for client-side).
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor for streaming requests.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		procedure := conn.Spec().Procedure

		// Check if this is a public endpoint
		if i.isPublicEndpoint(procedure) {
			return next(ctx, conn)
		}

		// Check if session auth has already authenticated this request
		if session.IsSessionAuthenticated(ctx) {
			return next(ctx, conn)
		}

		// Extract and validate token
		claims, err := i.validateRequest(ctx, conn.RequestHeader())
		if err != nil {
			return i.toConnectError(err)
		}

		// Check endpoint-specific scopes
		if err := i.checkEndpointScopes(procedure, claims); err != nil {
			return i.toConnectError(err)
		}

		// Add claims to context
		ctx = ContextWithClaims(ctx, claims)

		return next(ctx, conn)
	}
}

// validateRequest extracts and validates the token from headers.
func (i *Interceptor) validateRequest(ctx context.Context, headers interface{ Get(string) string }) (*Claims, error) {
	// Extract token from Authorization header
	authHeader := headers.Get("Authorization")
	if authHeader == "" {
		return nil, ErrMissingToken
	}

	// Parse Bearer token
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return nil, ErrInvalidToken
	}
	token := parts[1]

	// Validate token
	claims, err := i.config.Validator.ValidateToken(ctx, token)
	if err != nil {
		return nil, err
	}

	// Check required audience
	if i.config.RequiredAudience != "" && !claims.HasAudience(i.config.RequiredAudience) {
		return nil, ErrInvalidAudience
	}

	// Check required scopes
	for _, scope := range i.config.RequiredScopes {
		if !claims.HasScope(scope) {
			return nil, ErrInvalidToken
		}
	}

	return claims, nil
}

// isPublicEndpoint checks if a procedure is in the public endpoints list.
func (i *Interceptor) isPublicEndpoint(procedure string) bool {
	// Normalize procedure name (handle both /service/method and service/method)
	normalizedProc := normalizeProcedure(procedure)

	for _, ep := range i.config.PublicEndpoints {
		normalizedEp := normalizeProcedure(ep)
		if normalizedEp == normalizedProc {
			return true
		}
	}
	return false
}

// checkEndpointScopes verifies the client has required scopes for the endpoint.
func (i *Interceptor) checkEndpointScopes(procedure string, claims *Claims) error {
	// Normalize procedure name
	normalizedProc := normalizeProcedure(procedure)

	// Step 1: Check explicit overrides first
	requiredScopes := i.findExplicitScopes(normalizedProc)

	// Step 2: If no explicit scopes, try auto-derive
	if len(requiredScopes) == 0 && i.config.ScopePolicy != nil && i.config.ScopePolicy.AutoDerive {
		requiredScopes = i.deriveScopes(normalizedProc)
	}

	// Step 3: If still no scopes, check AllowAllAuthenticated
	if len(requiredScopes) == 0 {
		if i.config.AllowAllAuthenticated {
			return nil
		}
		return ErrInsufficientScope
	}

	// Check if the client has at least one of the required scopes
	for _, requiredScope := range requiredScopes {
		if claims.HasScope(requiredScope) {
			return nil
		}
	}

	// Log scope mismatch with explicit formatting for visibility
	logger.Errorf("m2m: scope check failed - procedure=%s required=%v client_scopes=%v client_id=%s",
		normalizedProc, requiredScopes, claims.Scopes, claims.ClientID)

	return ErrInsufficientScope
}

// findExplicitScopes looks for explicit scope overrides for this endpoint.
func (i *Interceptor) findExplicitScopes(procedure string) []string {
	for endpoint, scopes := range i.config.EndpointScopes {
		// Exact match
		if endpoint == procedure || endpoint == "/"+procedure {
			return scopes
		}
		// Suffix match
		if strings.HasSuffix(procedure, endpoint) {
			return scopes
		}
	}
	return nil
}

// deriveScopes automatically derives the required scope from the endpoint name.
// Format: {service}:{action}
// e.g., "everstack.license.v1.InstanceService/ReportUsage" -> "instance:write"
func (i *Interceptor) deriveScopes(procedure string) []string {
	// Extract service and method from procedure
	// Format: package.Service/Method or just Service/Method
	parts := strings.Split(procedure, "/")
	if len(parts) != 2 {
		return nil
	}

	servicePart := parts[0]
	methodName := parts[1]

	// Extract just the service name (e.g., "InstanceService" from "everstack.license.v1.InstanceService")
	serviceParts := strings.Split(servicePart, ".")
	serviceName := serviceParts[len(serviceParts)-1]

	// Convert service name to scope prefix
	// InstanceService -> instance, LicenseService -> license, BillingService -> billing
	scopePrefix := strings.ToLower(strings.TrimSuffix(serviceName, "Service"))

	// Find the action based on method name prefix
	action := i.findAction(methodName)
	if action == "" {
		// Default to "read" if no pattern matches
		action = "read"
	}

	return []string{scopePrefix + ":" + action}
}

// findAction finds the scope action for a method name based on configured patterns.
func (i *Interceptor) findAction(methodName string) string {
	if i.config.ScopePolicy == nil {
		return ""
	}

	for _, pattern := range i.config.ScopePolicy.ActionPatterns {
		if strings.HasPrefix(methodName, pattern.Prefix) {
			return pattern.Action
		}
	}
	return ""
}

// normalizeProcedure removes leading slash from procedure name for consistent matching.
func normalizeProcedure(procedure string) string {
	return strings.TrimPrefix(procedure, "/")
}

// toConnectError converts an M2M error to a Connect error.
func (i *Interceptor) toConnectError(err error) error {
	switch err {
	case ErrMissingToken, ErrInvalidToken, ErrTokenExpired, ErrTokenNotYetValid:
		return connect.NewError(connect.CodeUnauthenticated, err)
	case ErrInvalidAudience, ErrInvalidIssuer:
		return connect.NewError(connect.CodeUnauthenticated, err)
	case ErrInsufficientScope:
		return connect.NewError(connect.CodePermissionDenied, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
