package branding

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The gateway exposes two prefixes and they are not interchangeable. /v1 is the
// native REST surface and it explicitly REJECTS Authorization: Bearer, telling
// the caller to use x-evs-api-key. /openai/v1 is the OpenAI-wire-compatible
// surface: withOpenAIRoundTrip maps Bearer onto the api-key header, reshapes
// the model list, and converts errors to OpenAI's shape.
//
// Every published quick-start had this wrong. The flagship docs quick-start
// told customers to send `Authorization: Bearer YOUR_EVERSTACK_API_KEY` at
// .../v1/chat/completions, which can never authenticate, and eight comparison
// pages shipped a base_url on a host that does not resolve at all. Verified
// against the live endpoint on 2026-09-08:
//
//	/v1/chat/completions        -> 401 "Invalid authorization token; use x-evs-api-key"
//	/openai/v1/chat/completions -> 401 "Invalid API key"   (key checked, shape correct)
//
// The mistake is invisible in review because both URLs look plausible, so it is
// guarded here rather than left to the next person copying an existing snippet.
var (
	// Shape 1: a base URL assigned to an OpenAI client, ending at the native
	// prefix. Trailing slash and query string are allowed because a snippet
	// carrying either is just as broken.
	openAIBaseURLOnNativePrefix = regexp.MustCompile(
		`(?i)base_?url\s*[:=]\s*[("'` + "`" + `]+[^"'` + "`" + `\n]*(?:everstack\.ai|EVERSTACK_GATEWAY_URL|localhost:8089)[^"'` + "`" + `\n]*/v1/?(?:[?#][^"'` + "`" + `\n]*)?(?:["'` + "`" + `]|/chat|/embeddings|/models|/responses)`)

	// NOTE ON SCOPE. The native prefix IS a working surface when paired with
	// x-evs-api-key, confirmed live:
	//
	//	/v1/chat/completions + x-evs-api-key -> 401 "Invalid API key" (reaches validation)
	//	/v1/chat/completions + Bearer        -> 401 "use x-evs-api-key..."
	//
	// An earlier version of this note claimed no snippet documented the native
	// surface with the native header. That was already false: authentication.mdx
	// and vault/api-keys.mdx carry three, and they are correct. They escaped only
	// because their host, your-instance.example.com, was missing from the host
	// list -- so the guard was right by accident, for the wrong reason.
	//
	// So shape 2 no longer judges by URL alone. It looks at the auth header in
	// the surrounding lines and fires only on /v1 paired with Bearer, which is
	// the combination that cannot authenticate. A native-header example on /v1 is
	// legitimate and is left alone.

	// Shape 2: the URL appearing bare, with no base_url token anywhere -- a raw
	// curl with the URL as a positional argument, an unquoted env var, a CLI
	// flag, a YAML scalar. The first version of this guard only knew shape 1,
	// because shape 1 was the bug I had just finished fixing. The shape that
	// survived it was a curl on the API Reference landing page, published for
	// as long as the rest, so this matches an OpenAI-compat verb on the native
	// prefix regardless of the syntax around it.
	openAICompatVerbOnNativePrefix = regexp.MustCompile(
		`(?i)(?:everstack\.ai|EVERSTACK_GATEWAY_URL\}?|localhost:8089|your-instance\.example\.com)/v1/(?:chat/completions|embeddings|responses|models\b)`)

	bearerAuth = regexp.MustCompile(`(?i)authorization"?\s*[:=]?\s*"?\s*bearer`)
	nativeAuth = regexp.MustCompile(`(?i)x-evs-api-key|x-mf-api-key|x-everstack-api-key`)

	// A host we have never owned; it was published on eight comparison pages.
	unresolvableExampleHost = regexp.MustCompile(`everstack\.internal(?:[/"'` + "`" + `)\s]|$)`)
)

func TestPublishedOpenAISnippetsUseTheCompatiblePrefix(t *testing.T) {
	root := repoRoot(t)

	// Only customer-facing surfaces. Server code legitimately routes /v1.
	published := []string{
		filepath.Join("apps", "docs", "content"),
		filepath.Join("apps", "docs", "src"),
		filepath.Join("apps", "landing", "src"),
		filepath.Join("apps", "landing", "content"),
	}

	// The repo README is published twice over -- on the GitHub landing page and
	// again through the CE projection allowlist (sync-public-repo.sh:111) -- so
	// it is the first snippet an OSS reader copies. It sat outside the four
	// directories above and carried a /v1 quick-start using the retired
	// x-mf-api-key header, which is exactly the failure this guard exists to
	// prevent: the guard did not cover the shape, or the location, the bug took.
	rootFiles := []string{"README.md"}

	var offenders []string

	for _, name := range rootFiles {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")
		for i := range lines {
			if why := classify(lines, i); why != "" {
				offenders = append(offenders, name+":"+itoa(i+1)+"  ("+why+")")
			}
		}
	}

	for _, dir := range published {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue // app not present in this checkout
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				switch info.Name() {
				case "node_modules", "dist", ".next", "build":
					return filepath.SkipDir
				}
				return nil
			}
			switch filepath.Ext(path) {
			case ".ts", ".tsx", ".mdx", ".md":
			default:
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			lines := strings.Split(string(content), "\n")
			for i := range lines {
				if why := classify(lines, i); why != "" {
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders, rel+":"+itoa(i+1)+"  ("+why+")")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("published OpenAI snippets that cannot authenticate (%d):\n  %s\n\n"+
			"Use https://{instance}.{region}.everstack.ai/openai/v1 with Authorization: Bearer.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// classify reports why line i offends, or "" if it does not.
//
// A curl spans several lines, so the auth header is rarely on the URL's own
// line. The window is what lets the guard tell "/v1 with Bearer" (cannot
// authenticate) from "/v1 with x-evs-api-key" (correct, and what
// authentication.mdx and vault/api-keys.mdx deliberately document).
func classify(lines []string, i int) string {
	line := lines[i]

	// .example.com is the reserved documentation domain, used deliberately for
	// the self-hosted MCP example.
	if strings.Contains(line, "everstack.internal.example.com") {
		return ""
	}

	// RE2 has no lookbehind and /openai/v1 also ends in /v1, so neutralise the
	// correct form before testing for the wrong one.
	probe := strings.ReplaceAll(line, "/openai/v1", "/openai/[ok]")

	if unresolvableExampleHost.MatchString(probe) {
		return "everstack.internal does not resolve"
	}

	// Shape 1 is unconditional. base_url= on an OpenAI client IS an OpenAI-SDK
	// usage: the SDK sends Bearer itself, so the snippet never contains the
	// literal "Authorization: Bearer" and the window can only ever observe its
	// ABSENCE. There is no such thing as a correct base_url=".../v1" on an
	// OpenAI client, so nothing nearby should be able to excuse one.
	//
	// An earlier version of this function ran the window test here too. Because
	// classify defaults to firing and goes silent on nativeAuth && !bearerAuth,
	// that let any x-evs-api-key within six lines silence a genuinely broken SDK
	// snippet -- and the pages carrying legitimate native-header examples,
	// authentication.mdx and vault/api-keys.mdx, are exactly where someone would
	// add an SDK language tab. The silencing zone was where the bug is most
	// likely to appear.
	if openAIBaseURLOnNativePrefix.MatchString(probe) {
		return "/v1 rejects Bearer; use /openai/v1"
	}

	if !openAICompatVerbOnNativePrefix.MatchString(probe) {
		return ""
	}

	// Shape 2 is a raw curl or bare URL, where an auth header is genuinely
	// expected IN the snippet, so its presence is real evidence rather than an
	// absence that proves nothing.
	lo, hi := i-6, i+6
	if lo < 0 {
		lo = 0
	}
	if hi >= len(lines) {
		hi = len(lines) - 1
	}
	window := strings.Join(lines[lo:hi+1], "\n")

	// A native-header example on the native prefix authenticates. Leave it.
	if nativeAuth.MatchString(window) && !bearerAuth.MatchString(window) {
		return ""
	}
	return "/v1 rejects Bearer; use /openai/v1"
}
