package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TanstackRules represents optional SPA rules for Vite + TanStack Router builds.
// This format is intentionally simple and mirrors the parts we support from Next.js
// routes-manifest: redirects, rewrites, and headers.
//
// Example JSON (place it at "tanstack.routes.json" next to your built index.html):
//
//	{
//	  "redirects": [
//	    {"source": "/old/(.*)", "destination": "/new/$1", "statusCode": 308}
//	  ],
//	  "rewrites": [
//	    {"source": "/docs/(.*)", "destination": "/docs/index.html"}
//	  ],
//	  "headers": [
//	    {"source": "/assets/(.*)", "headers": [["Cache-Control", "public, max-age=31536000"]]}
//	  ]
//	}
type TanstackRules struct {
	Redirects []tanRedirect
	Rewrites  []tanRewrite
	Headers   []tanHeader
}

type tanRedirect struct {
	re   *regexp.Regexp
	dest string
	code int
}

type tanRewrite struct {
	re   *regexp.Regexp
	dest string
}

type tanHeader struct {
	re  *regexp.Regexp
	set [][2]string
}

// compiledVite holds entry CSS/JS from Vite client manifest to synthesize an HTML shell.
type compiledVite struct {
	entryPath string
	cssPaths  []string
}

// loadViteManifest attempts to load a Vite client manifest to resolve the SPA entry and CSS.
// If manifestPath is empty, searches common locations under root.
func loadViteManifest(root string, manifestPath string) *compiledVite {
	// Resolve manifest path
	if manifestPath == "" {
		candidates := []string{
			filepath.Join(root, ".vite", "manifest.json"),
			filepath.Join(root, "client", ".vite", "manifest.json"),
			filepath.Join(root, "manifest.json"),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				manifestPath = c
				break
			}
		}
	} else {
		// Allow providing a directory
		if fi, err := os.Stat(manifestPath); err == nil && fi.IsDir() {
			// Look for manifest.json inside provided directory
			p := filepath.Join(manifestPath, "manifest.json")
			if fj, e := os.Stat(p); e == nil && !fj.IsDir() {
				manifestPath = p
			}
		}
	}
	if manifestPath == "" {
		return nil
	}

	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return tryScanTanstackClient(root)
	}

	// Vite manifest structure: map of input -> { file, isEntry?, css?: [] }
	var raw map[string]struct {
		File    string   `json:"file"`
		IsEntry bool     `json:"isEntry"`
		Css     []string `json:"css"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		// Not a Vite manifest; try scanning TanStack Start client assets
		return tryScanTanstackClient(root)
	}
	// Find the first isEntry=true item
	out := &compiledVite{}
	for _, v := range raw {
		if v.IsEntry && v.File != "" {
			out.entryPath = "/" + filepath.ToSlash(v.File)
			for _, c := range v.Css {
				if c == "" {
					continue
				}
				out.cssPaths = append(out.cssPaths, "/"+filepath.ToSlash(c))
			}
			break
		}
	}
	if out.entryPath == "" {
		// No Vite entry detected; try scanning TanStack Start client assets
		return tryScanTanstackClient(root)
	}
	return out
}

// tryScanTanstackClient attempts to infer entry and css from TanStack Start client assets
// located at <root>/client/assets. It picks index-*.js or main-*.js and styles-*.css if present.
func tryScanTanstackClient(root string) *compiledVite {
	clientAssets := filepath.Join(root, "client", "assets")
	entries, err := os.ReadDir(clientAssets)
	if err != nil {
		return nil
	}
	var indexJS, mainJS string
	var cssList []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".js") {
			if strings.HasPrefix(lower, "index-") {
				indexJS = name
			} else if strings.HasPrefix(lower, "main-") {
				mainJS = name
			}
		} else if strings.HasSuffix(lower, ".css") {
			if strings.HasPrefix(lower, "styles-") {
				cssList = append(cssList, name)
			}
		}
	}
	entry := indexJS
	if entry == "" {
		entry = mainJS
	}
	if entry == "" {
		return nil
	}
	out := &compiledVite{entryPath: "/client/assets/" + filepath.ToSlash(entry)}
	for _, c := range cssList {
		out.cssPaths = append(out.cssPaths, "/client/assets/"+filepath.ToSlash(c))
	}
	return out
}

// writeViteShell writes a minimal HTML shell that loads Vite-built assets.
func writeViteShell(w interface{ Write([]byte) (int, error) }, vite *compiledVite) {
	if vite == nil || vite.entryPath == "" {
		return
	}
	// Build link tags for CSS
	var css string
	for _, c := range vite.cssPaths {
		css += fmt.Sprintf("<link rel=\"stylesheet\" href=\"%s\"/>\n", c)
	}
	// Basic HTML skeleton; defer to router to take over client-side
	html := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  %s
  <title>Everstack</title>
  <script type="module" src="%s"></script>
</head>
<body>
  <div id="root"></div>
</body>
</html>`, css, vite.entryPath)
	_, _ = w.Write([]byte(html))
}

// LoadTanstackManifest loads a TanStack SPA routing manifest from either a file path
// or a directory (searching common file names). Returns nil if no manifest is found
// or if the file is invalid.
func LoadTanstackManifest(pathOrDir string) *TanstackRules {
	pathOrDir = strings.TrimSpace(pathOrDir)
	if pathOrDir == "" {
		return nil
	}

	// Resolve file path
	var filePath string
	if info, err := os.Stat(pathOrDir); err == nil {
		if info.IsDir() {
			candidates := []string{
				filepath.Join(pathOrDir, "tanstack.routes.json"),
				filepath.Join(pathOrDir, "tanstack-manifest.json"),
				filepath.Join(pathOrDir, "routes.manifest.json"),
			}
			for _, c := range candidates {
				if fi, e := os.Stat(c); e == nil && !fi.IsDir() {
					filePath = c
					break
				}
			}
		} else {
			filePath = pathOrDir
		}
	}
	if filePath == "" {
		return nil
	}

	b, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	// Parse a minimal, framework-agnostic manifest structure
	var raw struct {
		Redirects []struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
			StatusCode  int    `json:"statusCode"`
		} `json:"redirects"`
		Rewrites []struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		} `json:"rewrites"`
		Headers []struct {
			Source  string      `json:"source"`
			Headers [][2]string `json:"headers"`
		} `json:"headers"`
	}

	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}

	rules := &TanstackRules{}

	// Compile redirects
	for _, r := range raw.Redirects {
		src := strings.TrimSpace(r.Source)
		dst := strings.TrimSpace(r.Destination)
		if src == "" || dst == "" {
			continue
		}
		code := r.StatusCode
		if code == 0 {
			code = 308
		}
		if re := compilePathPattern(src); re != nil {
			rules.Redirects = append(rules.Redirects, tanRedirect{re: re, dest: dst, code: code})
		}
	}

	// Compile rewrites
	for _, rw := range raw.Rewrites {
		src := strings.TrimSpace(rw.Source)
		dst := strings.TrimSpace(rw.Destination)
		if src == "" || dst == "" {
			continue
		}
		if re := compilePathPattern(src); re != nil {
			rules.Rewrites = append(rules.Rewrites, tanRewrite{re: re, dest: dst})
		}
	}

	// Compile headers
	for _, hh := range raw.Headers {
		src := strings.TrimSpace(hh.Source)
		if src == "" || len(hh.Headers) == 0 {
			continue
		}
		if re := compilePathPattern(src); re != nil {
			// Normalize header keys/values
			set := make([][2]string, 0, len(hh.Headers))
			for _, kv := range hh.Headers {
				if len(kv) != 2 {
					continue
				}
				k := strings.TrimSpace(kv[0])
				v := strings.TrimSpace(kv[1])
				if k == "" {
					continue
				}
				set = append(set, [2]string{k, v})
			}
			if len(set) > 0 {
				rules.Headers = append(rules.Headers, tanHeader{re: re, set: set})
			}
		}
	}

	if len(rules.Redirects) == 0 && len(rules.Rewrites) == 0 && len(rules.Headers) == 0 {
		return nil
	}
	return rules
}

// compilePathPattern converts a path-like pattern to a RE2 regexp.
// If input looks like a raw regexp (starts with ^ or contains (?)) we use it as-is.
// Otherwise we anchor it and keep capture groups/wildcards like (.*) intact.
func compilePathPattern(p string) *regexp.Regexp {
	if p == "" {
		return nil
	}
	if strings.HasPrefix(p, "^") || strings.Contains(p, "(?") {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil
		}
		return re
	}
	// Ensure it is anchored and uses RE2 compatible groups/wildcards
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	pattern := "^" + p + "$"
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}
