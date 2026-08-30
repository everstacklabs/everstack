//go:build !ui_embed

package ui

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var (
	uiDist = "ui/dist" // In Docker: /app/ui/dist; Local dev: apps/admin/dist (set via EVS_UI_DIST)
	uiURL  = ""        // Empty = serve from disk (production); set EVS_UI_PROXY_URL="http://localhost:3000" for dev hot reload
	// Static configuration (prefer editing here over envs). Only EVS_UI_PROXY_URL
	// is read from the environment for dev proxying.
	UIRootOverride         = ""                             // absolute/relative path; if empty, use uiDist
	SSRURL                 = ""                             // optional SSR server (TanStack Start); if empty, SSR disabled
	SSRPrefixes            = []string{"/api", "/serverFn/"} // request path prefixes to proxy to SSR when SSRURL is set
	TrailingSlash          = ""                             // "add" | "remove" | "" for no redirect
	NoCache                = false                          // when true, disable HTTP caching for files
	NextRoutesManifestPath = ""                             // optional Next.js routes-manifest path or dir
	TanstackManifestPath   = ""                             // optional TanStack routes manifest path or dir
	ViteManifestPath       = ""                             // optional Vite client manifest path or dir
)

// NewSPAHandler serves a built single-page app (SPA) from disk with a
// fallback to index.html for client-side routes.
//
// It serves from EVS_UI_DIST if set; otherwise tries apps/web/out then apps/web/dist.
func NewSPAHandler() http.Handler {
	// Prefer dev proxy for hot-reload if configured
	proxy := strings.TrimSpace(os.Getenv("EVS_UI_PROXY_URL"))
	if proxy == "" {
		proxy = strings.TrimSpace(uiURL)
	}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			rp := httputil.NewSingleHostReverseProxy(u)
			// Customize the director to make requests appear as if they're coming from Vite's origin
			originalDirector := rp.Director
			rp.Director = func(req *http.Request) {
				originalDirector(req)
				// Set Origin to match Vite's dev server so it accepts the requests
				req.Header.Set("Origin", u.String())
				// Set Referer to Vite's origin as well
				req.Header.Set("Referer", u.String()+"/")
				// Preserve original host in X-Forwarded-Host for debugging
				req.Header.Set("X-Forwarded-Host", req.Host)
				// Allow Vite HMR WebSocket upgrade
				if req.Header.Get("Upgrade") == "websocket" {
					req.Header.Set("Connection", "Upgrade")
				}
			}
			// Modify response headers to allow CORS from the Go server's origin
			rp.ModifyResponse = func(resp *http.Response) error {
				// Allow the browser to accept responses from the proxied Vite
				// server. This is the dev hot-reload path only: the file is
				// behind `//go:build !ui_embed` and production binaries are
				// built with -tags ui_embed, so it is not compiled in.
				//
				// No Access-Control-Allow-Credentials here. Browsers reject
				// credentials alongside a "*" origin, so the pair never worked;
				// it only looked like it granted something.
				resp.Header.Set("Access-Control-Allow-Origin", "*")
				resp.Header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				resp.Header.Set("Access-Control-Allow-Headers", "*")
				return nil
			}
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rp.ServeHTTP(w, r)
			})
		}
	}

	// Optional TanStack Start SSR proxy (production SSR or server functions)
	var ssrProxy *httputil.ReverseProxy
	var ssrPrefixes []string
	if ssr := strings.TrimSpace(SSRURL); ssr != "" {
		if u, err := url.Parse(ssr); err == nil {
			ssrProxy = httputil.NewSingleHostReverseProxy(u)
		}
	}
	ssrPrefixes = append(ssrPrefixes, SSRPrefixes...)

	// Select root directory
	var root string
	{
		// Check EVS_UI_DIST env var for runtime override (e.g., local dev)
		envDist := strings.TrimSpace(os.Getenv("EVS_UI_DIST"))

		candidates := []string{
			strings.TrimSpace(UIRootOverride),
			envDist,
			uiDist,
		}
		for _, c := range candidates {
			if c == "" {
				continue
			}
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				root = filepath.Clean(c)
				break
			}
		}
		if root == "" {
			root = filepath.Clean(uiDist)
		}
	}

	// Optionally load framework-specific route manifests
	// Next.js: NextRoutesManifestPath can override the lookup directory (defaults to root)
	var nextRules *compiledNext
	if nextDir := strings.TrimSpace(NextRoutesManifestPath); nextDir != "" {
		nextRules = loadNextManifest(nextDir)
	} else {
		nextRules = loadNextManifest(root)
	}
	// TanStack Router (Vite): TanstackManifestPath can point to file/dir; defaults to root
	var tanRules *TanstackRules
	if tanPath := strings.TrimSpace(TanstackManifestPath); tanPath != "" {
		tanRules = LoadTanstackManifest(tanPath)
	} else {
		tanRules = LoadTanstackManifest(root)
	}

	// Use a disk-backed file server
	fsys := os.DirFS(root)
	fileServer := http.FileServer(http.Dir(root))

	// Optionally load Vite client manifest (TanStack Start/Router builds)
	// ViteManifestPath can point to file or directory; defaults to common locations under root
	var vite *compiledVite
	if mp := strings.TrimSpace(ViteManifestPath); mp != "" {
		vite = loadViteManifest(root, mp)
	} else {
		vite = loadViteManifest(root, "")
	}

	// Helper to check if a path exists in the FS and is a file
	exists := func(name string) bool {
		info, err := fs.Stat(fsys, name)
		return err == nil && !info.IsDir()
	}

	// Trailing slash behavior: "add" | "remove" | other = no redirect
	trailing := strings.ToLower(strings.TrimSpace(TrailingSlash))

	// Optionally disable HTTP caching for rapid local iteration
	noCache := NoCache

	setNoCache := func(h http.Header) {
		if noCache {
			h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
			h.Set("Pragma", "no-cache")
			h.Set("Expires", "0")
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If TanStack Start SSR is configured, route configured prefixes to SSR (server functions)
		if ssrProxy != nil {
			for _, p := range ssrPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					ssrProxy.ServeHTTP(w, r)
					return
				}
			}
		}
		// Next.js redirects/headers/rewrites via routes-manifest.json (best-effort)
		if nextRules != nil {
			// Redirects
			for _, rd := range nextRules.Redirects {
				if m := rd.re.FindStringSubmatch(r.URL.Path); m != nil {
					dest := replaceWithGroups(rd.dest, m)
					if strings.HasPrefix(dest, "/") {
						http.Redirect(w, r, dest, rd.code)
						return
					}
				}
			}
			// Headers
			for _, hh := range nextRules.Headers {
				if hh.re.MatchString(r.URL.Path) {
					for _, kv := range hh.set {
						w.Header().Set(kv[0], kv[1])
					}
					break
				}
			}
			// Rewrites (only change URL.Path if matches)
			rewriteOnce := func(list []compiledRewrite) bool {
				for _, rw := range list {
					if m := rw.re.FindStringSubmatch(r.URL.Path); m != nil {
						dest := replaceWithGroups(rw.dest, m)
						if strings.HasPrefix(dest, "/") {
							r.URL.Path = dest
							return true
						}
					}
				}
				return false
			}
			_ = rewriteOnce(nextRules.Rewrites.BeforeFiles)
		}

		// TanStack (Vite) redirects/headers/rewrites via tanstack.routes.json (best-effort)
		if tanRules != nil {
			// Redirects
			for _, rd := range tanRules.Redirects {
				if m := rd.re.FindStringSubmatch(r.URL.Path); m != nil {
					dest := replaceWithGroups(rd.dest, m)
					if strings.HasPrefix(dest, "/") {
						http.Redirect(w, r, dest, rd.code)
						return
					}
				}
			}
			// Headers
			for _, hh := range tanRules.Headers {
				if hh.re.MatchString(r.URL.Path) {
					for _, kv := range hh.set {
						w.Header().Set(kv[0], kv[1])
					}
					break
				}
			}
			// Rewrites
			rewriteOnce := func(list []tanRewrite) bool {
				for _, rw := range list {
					if m := rw.re.FindStringSubmatch(r.URL.Path); m != nil {
						dest := replaceWithGroups(rw.dest, m)
						if strings.HasPrefix(dest, "/") {
							r.URL.Path = dest
							return true
						}
					}
				}
				return false
			}
			_ = rewriteOnce(tanRules.Rewrites)
		}

		// Attempt to serve the exact file first
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}

		// Trailing slash redirects to match Next.js expectations when exporting
		switch trailing {
		case "add":
			// If requesting "/path" and there is a directory page at /path/index.html, redirect to "/path/"
			if !strings.HasSuffix(r.URL.Path, "/") && exists(filepath.Join(p, "index.html")) {
				http.Redirect(w, r, r.URL.Path+"/", http.StatusPermanentRedirect)
				return
			}
		case "remove":
			// If requesting "/path/" and there is a standalone file /path.html, redirect to "/path"
			if strings.HasSuffix(r.URL.Path, "/") {
				pp := strings.TrimSuffix(p, "/")
				if pp != "" && exists(pp+".html") {
					http.Redirect(w, r, "/"+pp, http.StatusPermanentRedirect)
					return
				}
			}
		}

		// 1) Exact file (e.g., app.js, styles.css, index.html)
		if exists(p) {
			setNoCache(w.Header())
			if strings.HasSuffix(p, ".html") {
				if err := writeRuntimeHTML(w, r, fsys, p); err != nil {
					http.Error(w, "File not found", http.StatusNotFound)
				}
				return
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		// 2) Nested route directories from static exports (e.g., keys/index.html)
		if exists(filepath.Join(p, "index.html")) {
			setNoCache(w.Header())
			if err := writeRuntimeHTML(w, r, fsys, filepath.Join(p, "index.html")); err != nil {
				http.Error(w, "File not found", http.StatusNotFound)
			}
			return
		}

		// 3) Extensionless html files (e.g., about.html)
		if !strings.Contains(p, ".") && exists(p+".html") {
			setNoCache(w.Header())
			if err := writeRuntimeHTML(w, r, fsys, p+".html"); err != nil {
				http.Error(w, "File not found", http.StatusNotFound)
			}
			return
		}

		// API-shaped paths must not fall through to the SPA shell. Returning
		// the HTML for an unknown /everstack.* or /auth/* hides routing
		// bugs (200 OK with HTML when the caller expected JSON) and was
		// previously the path through which the runtime env block —
		// including the PostHog key — leaked to arbitrary visitors.
		if isAPILikePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		// 4) SPA fallback: prefer index.html if present; else SSR (if configured); else synthesize from Vite manifest (TanStack)
		if exists("index.html") {
			setNoCache(w.Header())
			// Serve index.html directly for SPA routes and inject runtime env
			if err := writeRuntimeHTML(w, r, fsys, "index.html"); err == nil {
				return
			}
		}
		if ssrProxy != nil {
			ssrProxy.ServeHTTP(w, r)
			return
		}
		if vite != nil && vite.entryPath != "" {
			setNoCache(w.Header())
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			writeViteShell(w, vite)
			return
		}

		// 5) Serve exported 404 page if present
		if exists("404.html") {
			setNoCache(w.Header())
			w.WriteHeader(http.StatusNotFound)
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/404.html"
			fileServer.ServeHTTP(w, r2)
			return
		}

		// Nothing to serve
		http.NotFound(w, r)
	})
}
