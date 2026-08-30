package interceptors

import (
	"net/http"

	"github.com/rs/cors"

	"github.com/everstacklabs/everstack/internal/api/common"
)

var (
	DefaultCORSOptions = cors.Options{
		// Use specific origins instead of wildcard when credentials are enabled
		// Browser security requires exact origin matching when credentials are enabled
		AllowedOrigins: []string{
			"http://localhost:3000",
			"http://localhost:4321",
			"http://localhost:5173",
			"http://localhost:8089",
			"http://main.everstack.local",
			"http://main.everstack.local:8080",
			"http://main-gateway.everstack.local",
			"http://main-gateway.everstack.local:8080",
			"https://portal.everstack.com",
		},
		AllowCredentials: true,
		AllowedHeaders: []string{
			common.Origin,
			common.ContentType,
			common.Accept,

			common.EverstackLicenseKey,
			common.EverstackOrgId,
			common.EverstackApiKey, // API Key header (canonical x-evs-api-key)
			// Legacy header names must stay in the CORS allowlist (add-both, never
			// replace) so browsers/integrations still sending x-mf-*/x-everstack-*
			// pass preflight until those clients upgrade.
			common.LegacyMFApiKey,
			common.LegacyEverstackApiKey,
			common.LegacyMFOrgID,
			common.LegacyMFLicenseKey,
			common.Authorization, // Bearer token header
			common.XUserAgent,
			common.XGrpcWeb,
			common.XRequestedWith,
			common.Authorization,
			common.ContentType,
			"Cookie", // Allow Cookie header for auth
		},
		AllowedMethods: []string{
			http.MethodOptions,
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodHead,
		},
		ExposedHeaders: []string{
			common.ContentLength,
			common.Location,
			common.XCorrelationID, // Correlation ID header
			common.SetCookie,      // Expose Set-Cookie header to client
		},
	}
)

func CORSInterceptorOpts(opts cors.Options, h http.Handler) http.Handler {
	return cors.New(opts).Handler(h)
}

func CORSInterceptor(h http.Handler) http.Handler {
	return CORSInterceptorOpts(DefaultCORSOptions, h)
}
