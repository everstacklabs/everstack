//go:build ui_embed

package ui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed all:dist
var embeddedUI embed.FS

// NewSPAHandler serves the embedded single-page app with fallback to index.html for client-side routes.
func NewSPAHandler() http.Handler {
	// Strip the "dist" prefix from the embedded filesystem
	uiFS, err := fs.Sub(embeddedUI, "dist")
	if err != nil {
		panic("failed to create sub-filesystem from embedded UI: " + err.Error())
	}

	// Helper to check if a path exists in the FS and is a file
	exists := func(name string) bool {
		info, err := fs.Stat(uiFS, name)
		return err == nil && !info.IsDir()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// 1) Try to serve the exact file
		if exists(path) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")

			if strings.HasSuffix(path, ".html") {
				if err := writeRuntimeHTML(w, r, uiFS, path); err != nil {
					http.Error(w, "File not found", http.StatusNotFound)
				}
				return
			}

			file, err := uiFS.Open(path)
			if err != nil {
				http.Error(w, "File not found", http.StatusNotFound)
				return
			}
			defer file.Close()

			// Set content type
			if strings.HasSuffix(path, ".js") {
				w.Header().Set("Content-Type", "application/javascript")
			} else if strings.HasSuffix(path, ".css") {
				w.Header().Set("Content-Type", "text/css")
			} else if strings.HasSuffix(path, ".html") {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			}

			if stat, err := file.Stat(); err == nil {
				w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
			}

			if seeker, ok := file.(io.ReadSeeker); ok {
				http.ServeContent(w, r, path, time.Time{}, seeker)
			} else {
				io.Copy(w, file)
			}
			return
		}

		// API-shaped paths must not fall through to the SPA shell — see
		// the matching note in ui.go (the non-embed build).
		if isAPILikePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		// 2) SPA fallback: serve index.html for client-side routes with runtime env injection
		if exists("index.html") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")

			if err := writeRuntimeHTML(w, r, uiFS, "index.html"); err != nil {
				http.Error(w, "Index file not found", http.StatusNotFound)
			}
			return
		}

		// 3) Nothing found
		http.NotFound(w, r)
	})
}
