package tls

import (
	gotls "crypto/tls"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	common "github.com/everstacklabs/everstack/internal/api/common"
	httpheaders "github.com/everstacklabs/everstack/internal/api/http"
	"github.com/everstacklabs/everstack/internal/api/http/middleware"
	http_mw "github.com/everstacklabs/everstack/internal/api/http/middleware/interceptors"
)

// ConfigureFromServerConfig applies origin, CORS, and HSTS middleware to the router
// and returns a TLS config if enabled in the provided server config.
func ConfigureFromServerConfig(router *mux.Router, server *validator.ServerConfig) (*gotls.Config, error) {
	// Apply origin context middleware
	allowed := server.CORS.AllowedHeaders
	if allowed == nil {
		allowed = []string{"*"}
	}
	router.Use(middleware.WithOrigin(
		server.Config.ExternalSecure,
		allowed,
		server.Config.InstanceHostHeaders,
		server.Config.PublicHostHeaders,
	))

	// Build TLS config, if enabled
	var tlsConfig *gotls.Config
	if server.TLS.Enabled {
		c, err := server.TLS.Config()
		if err != nil {
			return nil, err
		}
		tlsConfig = c
	}

	// Apply CORS if enabled
	if server.CORS.Enabled {
		c := http_mw.DefaultCORSOptions
		if len(server.CORS.AllowedOrigins) > 0 {
			c.AllowedOrigins = append([]string{}, server.CORS.AllowedOrigins...)
		}
		if len(server.CORS.AllowedMethods) > 0 {
			c.AllowedMethods = append([]string{}, server.CORS.AllowedMethods...)
		}
		if len(server.CORS.AllowedHeaders) > 0 {
			c.AllowedHeaders = append([]string{}, server.CORS.AllowedHeaders...)
		}
		if len(server.CORS.ExposedHeaders) > 0 {
			c.ExposedHeaders = append([]string{}, server.CORS.ExposedHeaders...)
		}
		c.AllowCredentials = server.CORS.AllowCredentials
		if d, err := time.ParseDuration(server.CORS.MaxAge); err == nil && d > 0 {
			c.MaxAge = int(d / time.Second)
		}

		// Install CORS middleware
		router.Use(func(next http.Handler) http.Handler { return http_mw.CORSInterceptorOpts(c, next) })

		// Enforce Origin allowlist beyond CORS layer
		if len(c.AllowedOrigins) > 0 && !(len(c.AllowedOrigins) == 1 && c.AllowedOrigins[0] == "*") {
			router.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if o := r.Header.Get(common.Origin); o != "" && !common.IsOriginAllowed(c.AllowedOrigins, o) {
						http.Error(w, "origin not allowed", http.StatusForbidden)
						return
					}
					next.ServeHTTP(w, r)
				})
			})
		}
	}

	// Add HSTS when TLS is enabled
	if tlsConfig != nil {
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add(httpheaders.StrictTransportSecurity, "max-age=63072000; includeSubDomains; preload")
				next.ServeHTTP(w, r)
			})
		})
	}

	return tlsConfig, nil
}
