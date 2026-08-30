package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// nextRoutesManifest mirrors only the fields we use from Next's routes-manifest.json.
type nextRoutesManifest struct {
	BasePath      string                `json:"basePath"`
	TrailingSlash bool                  `json:"trailingSlash"`
	Redirects     []nextRouteWithRegex  `json:"redirects"`
	Rewrites      nextRewritesContainer `json:"rewrites"`
	Headers       []nextHeaderRule      `json:"headers"`
}

type nextRouteWithRegex struct {
	Regex       string `json:"regex"`
	Destination string `json:"destination"`
	StatusCode  int    `json:"statusCode"`
}

type nextRewritesContainer struct {
	BeforeFiles []nextRewrite `json:"beforeFiles"`
	AfterFiles  []nextRewrite `json:"afterFiles"`
	Fallback    []nextRewrite `json:"fallback"`
}

type nextRewrite struct {
	Regex       string `json:"regex"`
	Destination string `json:"destination"`
}

type nextHeaderRule struct {
	Regex   string            `json:"regex"`
	Headers []nextHeaderEntry `json:"headers"`
}

type nextHeaderEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// compiledNext holds compiled regexes for fast matching.
type compiledNext struct {
	BasePath      string
	TrailingSlash bool
	Redirects     []compiledRedirect
	Rewrites      compiledRewrites
	Headers       []compiledHeader
}

type compiledRedirect struct {
	re   *regexp.Regexp
	dest string
	code int
}

type compiledHeader struct {
	re  *regexp.Regexp
	set [][2]string
}

type compiledRewrites struct {
	BeforeFiles []compiledRewrite
	AfterFiles  []compiledRewrite
	Fallback    []compiledRewrite
}

type compiledRewrite struct {
	re   *regexp.Regexp
	dest string
}

func loadNextManifest(nextDir string) *compiledNext {
	if nextDir == "" {
		return nil
	}
	path := filepath.Join(nextDir, "routes-manifest.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw nextRoutesManifest
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}
	cn := &compiledNext{BasePath: raw.BasePath, TrailingSlash: raw.TrailingSlash}
	// Redirects
	for _, r := range raw.Redirects {
		if r.Regex == "" || r.Destination == "" || r.StatusCode == 0 {
			continue
		}
		if re, err := regexp.Compile(r.Regex); err == nil {
			cn.Redirects = append(cn.Redirects, compiledRedirect{re: re, dest: r.Destination, code: r.StatusCode})
		}
	}
	// Rewrites
	compileRw := func(list []nextRewrite) []compiledRewrite {
		out := make([]compiledRewrite, 0, len(list))
		for _, rw := range list {
			if rw.Regex == "" || rw.Destination == "" {
				continue
			}
			if re, err := regexp.Compile(rw.Regex); err == nil {
				out = append(out, compiledRewrite{re: re, dest: rw.Destination})
			}
		}
		return out
	}
	cn.Rewrites = compiledRewrites{
		BeforeFiles: compileRw(raw.Rewrites.BeforeFiles),
		AfterFiles:  compileRw(raw.Rewrites.AfterFiles),
		Fallback:    compileRw(raw.Rewrites.Fallback),
	}
	// Headers
	for _, h := range raw.Headers {
		if h.Regex == "" || len(h.Headers) == 0 {
			continue
		}
		if re, err := regexp.Compile(h.Regex); err == nil {
			pairs := make([][2]string, 0, len(h.Headers))
			for _, kv := range h.Headers {
				if kv.Key == "" {
					continue
				}
				pairs = append(pairs, [2]string{kv.Key, kv.Value})
			}
			cn.Headers = append(cn.Headers, compiledHeader{re: re, set: pairs})
		}
	}
	return cn
}

// replaceWithGroups replaces $1, $2... in dest with regex capture groups.
func replaceWithGroups(dest string, groups []string) string {
	res := dest
	for i := 1; i < len(groups); i++ {
		ph := "$" + strconv.Itoa(i)
		res = strings.ReplaceAll(res, ph, groups[i])
	}
	return res
}
