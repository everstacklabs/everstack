package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	rtconfig "github.com/everstacklabs/everstack/internal/domain/runtime_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// RuntimeCORS layers per-tenant CORS on top of an existing static CORS
// middleware. It runs after auth, so the request context carries a
// tenant id; if the tenant has a runtime CORS override, this writes
// the tenant's headers, otherwise it leaves the static middleware's
// output untouched. Preflight (OPTIONS) requests don't carry auth and
// therefore don't get tenant-specific treatment — the static CORS
// middleware handles them with the gateway-wide policy.
//
// This deliberately doesn't use rs/cors per-request because that lib
// is built around a single config bound at handler construction. We
// don't need its full feature set here; we just need to overwrite the
// standard Access-Control-Allow-* headers.
type RuntimeCORS struct {
	svc *rtconfig.Service
}

func NewRuntimeCORS(svc *rtconfig.Service) *RuntimeCORS {
	return &RuntimeCORS{svc: svc}
}

func (r *RuntimeCORS) Wrap(next http.Handler) http.Handler {
	if r == nil || r.svc == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Preflight requests: hand off to the static middleware that
		// already ran outside of us. Don't touch the headers.
		if req.Method == http.MethodOptions {
			next.ServeHTTP(w, req)
			return
		}

		tenantID := contextkeys.ExtractTenantID(req.Context())
		cfg := r.svc.GetCORS(tenantID)

		// If the tenant hasn't enabled runtime CORS or hasn't configured
		// origins, leave the static middleware's headers in place.
		if !cfg.Enabled || len(cfg.AllowedOrigins) == 0 {
			next.ServeHTTP(w, req)
			return
		}

		// Overwrite headers based on tenant config. We use a wrapper
		// writer so the values land on the response even if the inner
		// handler tries to set them too — last writer wins for these
		// headers, and we want tenant policy to win.
		ww := &corsHeaderWriter{
			ResponseWriter: w,
			origin:         resolveAllowedOrigin(req.Header.Get("Origin"), cfg.AllowedOrigins),
			cfg:            cfg,
		}
		next.ServeHTTP(ww, req)
	})
}

// corsHeaderWriter overwrites Access-Control-Allow-* headers on the
// way out. It keeps a reference to the chosen origin (already matched
// against the tenant's allow-list) so we don't echo arbitrary origins.
//
// Implements http.Flusher and http.Hijacker so SSE / Connect-stream /
// websocket handlers can still type-assert their way to the underlying
// writer's streaming primitives. Without these, wrapping the writer
// strips Flusher/Hijacker from the interface set and breaks anything
// that needs to push bytes mid-response (observability traces is the
// concrete case that surfaced this).
type corsHeaderWriter struct {
	http.ResponseWriter
	origin    string
	cfg       rtconfig.CORSConfig
	committed bool
}

func (w *corsHeaderWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *corsHeaderWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not support hijacking")
}

func (w *corsHeaderWriter) WriteHeader(code int) {
	w.applyHeaders()
	w.ResponseWriter.WriteHeader(code)
}

func (w *corsHeaderWriter) Write(b []byte) (int, error) {
	w.applyHeaders()
	return w.ResponseWriter.Write(b)
}

func (w *corsHeaderWriter) applyHeaders() {
	if w.committed {
		return
	}
	w.committed = true
	h := w.Header()
	if w.origin != "" {
		h.Set("Access-Control-Allow-Origin", w.origin)
		// Vary on Origin so caches don't serve a wrong origin to a
		// different requester.
		h.Add("Vary", "Origin")
	}
	if len(w.cfg.AllowedMethods) > 0 {
		h.Set("Access-Control-Allow-Methods", strings.Join(w.cfg.AllowedMethods, ", "))
	}
	if len(w.cfg.AllowedHeaders) > 0 {
		h.Set("Access-Control-Allow-Headers", strings.Join(w.cfg.AllowedHeaders, ", "))
	}
	if len(w.cfg.ExposedHeaders) > 0 {
		h.Set("Access-Control-Expose-Headers", strings.Join(w.cfg.ExposedHeaders, ", "))
	}
	if w.cfg.AllowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	} else {
		h.Del("Access-Control-Allow-Credentials")
	}
	if w.cfg.MaxAge != "" {
		// Accept either "3600" or a duration-ish string; clamp to a sane
		// integer for the header. Best-effort: bad input falls through
		// silently rather than 500ing the response.
		if n, err := strconv.Atoi(strings.TrimSpace(w.cfg.MaxAge)); err == nil && n >= 0 {
			h.Set("Access-Control-Max-Age", strconv.Itoa(n))
		}
	}
}

// resolveAllowedOrigin returns the request's Origin header if it
// matches the allow-list (with "*" matching anything), else empty.
// Returning "*" on a credentialed request is a browser-side error,
// so when AllowCredentials is true and the list is "*", we echo the
// origin instead of the wildcard.
func resolveAllowedOrigin(reqOrigin string, allowed []string) string {
	if reqOrigin == "" {
		return ""
	}
	for _, o := range allowed {
		if o == "*" {
			return reqOrigin
		}
		if strings.EqualFold(o, reqOrigin) {
			return o
		}
	}
	return ""
}
