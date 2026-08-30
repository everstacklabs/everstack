package serve

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	stdnet "net"

	"github.com/gorilla/mux"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v3"

	"github.com/everstacklabs/everstack/internal/api"
	"github.com/everstacklabs/everstack/internal/api/http/middleware"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"golang.org/x/net/http2"
)

// proxySpec is a minimal shape for a reverse proxy wiring.
type proxySpec struct {
	name     string
	prefix   string
	upstream string
	apiKey   string // Optional: API key for authenticating with upstream
}

// wireReverseProxies reads defaults.server proxy config from viper and registers
// reverse proxies on the provided API/router. It attaches startup context, API key
// validation, and license enforcement to the proxied handlers.
func wireReverseProxies(ctx context.Context, apis *api.API, router *mux.Router) bool {
	_ = router // currently unused; kept for signature compatibility

	specs := loadProxySpecs()
	if len(specs) == 0 {
		return false
	}

	for _, s := range specs {
		rp := newH2CReverseProxy(s.name, s.upstream, s.apiKey)
		h := protectedProxyHandler(ctx, s.name, s.upstream, rp, apilic.FromGlobal())
		registerProxy(apis, s.prefix, h)
	}

	apis.EnableServerReflection()
	return true
}

// expandEnvVar expands environment variables in the format ${VAR_NAME} or $VAR_NAME
// If the variable is not set, it returns the original string or a fallback if provided
func expandEnvVar(value string) string {
	// Support ${VAR_NAME:default} syntax for fallback values
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		inner := value[2 : len(value)-1]
		parts := strings.SplitN(inner, ":", 2)
		envKey := strings.TrimSpace(parts[0])
		fallback := ""
		if len(parts) > 1 {
			fallback = parts[1]
		}
		if envVal := os.Getenv(envKey); envVal != "" {
			return envVal
		}
		return fallback
	}
	// Support $VAR_NAME syntax
	if strings.HasPrefix(value, "$") {
		envKey := strings.TrimPrefix(value, "$")
		if envVal := os.Getenv(envKey); envVal != "" {
			return envVal
		}
	}
	return value
}

// loadProxySpecs parses defaults.server YAML from viper and extracts enabled proxy specs.
func loadProxySpecs() []proxySpec {
	data := strings.TrimSpace(viper.GetString("defaults.server"))
	if data == "" {
		return nil
	}
	var defaults map[string]any
	if err := yaml.Unmarshal([]byte(data), &defaults); err != nil {
		return nil
	}
	gw, ok := defaults["gateway"].(map[string]any)
	if !ok {
		return nil
	}
	proxyMap, ok := gw["proxy"].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]proxySpec, 0, len(proxyMap))
	for name, raw := range proxyMap {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		enabled, _ := p["enabled"].(bool)
		prefix, _ := p["mount_prefix"].(string)
		upstream, _ := p["upstream"].(string)
		apiKey, _ := p["api_key"].(string)

		// Expand environment variables in upstream URL and API key
		upstream = expandEnvVar(upstream)
		apiKey = expandEnvVar(apiKey)

		if !enabled || strings.TrimSpace(prefix) == "" || strings.TrimSpace(upstream) == "" {
			continue
		}
		out = append(out, proxySpec{name: name, prefix: prefix, upstream: upstream, apiKey: apiKey})
	}
	return out
}

// newH2CReverseProxy constructs a reverse proxy with appropriate transport based on upstream URL.
// For http:// URLs, uses h2c (HTTP/2 cleartext) for internal gRPC services.
// For https:// URLs, uses standard HTTPS transport for external services.
// If apiKey is provided, it will be added as Bearer token in Authorization header.
func newH2CReverseProxy(name, upstream, apiKey string) *httputil.ReverseProxy {
	u, err := url.Parse(upstream)
	if err != nil {
		return nil
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.ErrorLog = stdLogger()

	// Always customize the Director to set proper headers for upstream (Cloudflare compatibility)
	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		origDirector(req)

		// CRITICAL: Set Host header to upstream host (Cloudflare requires this)
		req.Host = u.Host

		// Set User-Agent if not present (Cloudflare may block requests without it)
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "Everstack-Gateway/1.0")
		}

		// Remove headers that might trigger Cloudflare bot protection
		req.Header.Del("Origin")
		req.Header.Del("Referer")

		// Add API key if configured
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	// Choose transport based on URL scheme
	if u.Scheme == "https" {
		// For HTTPS upstreams, use standard HTTP transport with HTTP/2 support
		rp.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			ForceAttemptHTTP2: true, // Enable HTTP/2 over TLS
		}
	} else {
		// For HTTP upstreams, use h2c (HTTP/2 cleartext) for internal gRPC services
		rp.Transport = &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, cfg *tls.Config) (stdnet.Conn, error) {
				return stdnet.Dial(network, addr)
			},
		}
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		logger.WithFields("service", name, "path", r.URL.Path, "upstream", upstream, "error", e.Error()).Error("proxy error")
		w.WriteHeader(http.StatusBadGateway)
	}
	return rp
}

// protectedProxyHandler wraps reverse proxy with startup context, API key validation, and license enforcement.
func protectedProxyHandler(ctx context.Context, name, upstream string, rp *httputil.ReverseProxy, policy *apilic.Policy) http.Handler {
	// Use shared LicenseEnforcer from context (no-op in CE builds).
	lic := enterprise.LicenseEnforcerFromContext(ctx)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// attach startup ctx so middleware can access CQRS
		r = r.WithContext(ctx)
		validated := middleware.WithAPIKeyValidation(lic.WithLicenseEnforcement(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			start := time.Now()
			rp.ServeHTTP(rec, r)
			logger.WithFields(
				"service", name,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"upstream", upstream,
				"elapsed_ms", time.Since(start).Milliseconds(),
			).Info("proxy")
		})), false)
		validated.ServeHTTP(w, r)
	})
}

// registerProxy mounts the handler and adds to reflection if the prefix looks like a Connect service base path.
func registerProxy(apis *api.API, prefix string, handler http.Handler) {
	apis.RegisterHandlerPrefixes(handler, prefix)
	if strings.HasPrefix(prefix, "/") && strings.Count(prefix, "/") >= 2 && strings.Contains(prefix, ".") {
		apis.AddExternalServicePrefixes(prefix)
	}
}
