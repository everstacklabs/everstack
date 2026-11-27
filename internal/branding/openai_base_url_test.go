package branding

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The base URL we hand OpenAI-compatible clients is https://<instance>/v1.
// The gateway accepts an issued API key there as Authorization: Bearer (what
// every OpenAI SDK sends) and answers the OpenAI verbs in OpenAI's shape; see
// withV1OpenAICompat in internal/api. /openai/v1 is the older alias of the same
// surface and is still served, so clients configured against it keep working.
//
// History, because both URLs look plausible in review: until the /v1 Bearer
// fix, /v1 refused any Bearer and only /openai/v1 worked with an SDK, so the
// published snippets were moved to /openai/v1 and this guard enforced that.
// The docs now publish /v1 everywhere, and this guard keeps them there.
var (
	// A host-qualified /openai/v1 URL. Prose that names the alias without a
	// host ("`/openai/v1` is still served") is fine; handing it out as the
	// URL to configure is not.
	legacyAliasURL = regexp.MustCompile(
		`(?i)(?:everstack\.ai|EVERSTACK_GATEWAY_URL\}?|localhost:8089|your-instance\.example\.com|\{instance\}[^\s"'` + "`" + `]*)/openai/v1`)

	// A base URL that already includes the endpoint path. OpenAI SDKs append
	// /chat/completions (and the rest) themselves, so this doubles the path.
	baseURLWithEndpointPath = regexp.MustCompile(
		`(?i)base_?url\s*[:=]\s*[("'` + "`" + `]+[^"'` + "`" + `\n]*/v1/(?:chat/completions|embeddings|models|responses)`)

	// A host we have never owned; it was published on eight comparison pages.
	unresolvableExampleHost = regexp.MustCompile(`everstack\.internal(?:[/"'` + "`" + `)\s]|$)`)
)

func TestPublishedOpenAISnippetsUseTheV1BaseURL(t *testing.T) {
	root := repoRoot(t)

	type surface struct {
		path string
		// legacyAliasAllowed is true where /openai/v1 URLs may still appear.
		// The landing site deploys separately and still carries them; the
		// alias keeps those working, and moving landing to /v1 is its own
		// change. Everything else must hand out /v1.
		legacyAliasAllowed bool
	}
	published := []surface{
		{filepath.Join("apps", "docs", "content"), false},
		{filepath.Join("apps", "docs", "src"), false},
		{filepath.Join("apps", "landing", "src"), true},
		{filepath.Join("apps", "landing", "content"), true},
	}

	// The repo README is published twice over: on the GitHub landing page and
	// again through the CE projection allowlist, so it is the first snippet an
	// OSS reader copies.
	rootFiles := []string{"README.md"}

	var offenders []string
	check := func(rel string, content []byte, legacyAliasAllowed bool) {
		for i, line := range strings.Split(string(content), "\n") {
			if why := classify(line, legacyAliasAllowed); why != "" {
				offenders = append(offenders, rel+":"+itoa(i+1)+"  ("+why+")")
			}
		}
	}

	for _, name := range rootFiles {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		check(name, content, false)
	}

	for _, s := range published {
		base := filepath.Join(root, s.path)
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
			rel, _ := filepath.Rel(root, path)
			check(rel, content, s.legacyAliasAllowed)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", s.path, err)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("published OpenAI snippets off the documented base URL (%d):\n  %s\n\n"+
			"Use https://{instance}.{region}.everstack.ai/v1 as the base URL; the SDK appends /chat/completions.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// classify reports why a line offends, or "" if it does not.
func classify(line string, legacyAliasAllowed bool) string {
	// .example.com is the reserved documentation domain, used deliberately for
	// the self-hosted MCP example.
	if strings.Contains(line, "everstack.internal.example.com") {
		return ""
	}
	if unresolvableExampleHost.MatchString(line) {
		return "everstack.internal does not resolve"
	}
	if baseURLWithEndpointPath.MatchString(line) {
		return "base URL must end at /v1; the SDK appends the endpoint path"
	}
	if !legacyAliasAllowed && legacyAliasURL.MatchString(line) {
		return "/openai/v1 is a legacy alias; publish /v1"
	}
	return ""
}

func TestClassify(t *testing.T) {
	for line, want := range map[string]string{
		`base_url="https://{instance}.{region}.everstack.ai/v1",`:                  "",
		`curl https://{instance}.{region}.everstack.ai/v1/chat/completions \`:      "",
		`curl -X POST "http://localhost:8089/v1/embeddings" \`:                     "",
		"Clients configured with the older `/openai/v1` base URL keep working.":    "",
		`base_url="https://{instance}.{region}.everstack.ai/openai/v1",`:           "/openai/v1 is a legacy alias; publish /v1",
		`curl http://localhost:8089/openai/v1/chat/completions \`:                  "/openai/v1 is a legacy alias; publish /v1",
		"baseURL: `${process.env.EVERSTACK_GATEWAY_URL}/openai/v1`,":               "/openai/v1 is a legacy alias; publish /v1",
		`baseURL: "https://{instance}.{region}.everstack.ai/v1/chat/completions",`: "base URL must end at /v1; the SDK appends the endpoint path",
		`base_url="https://gateway.everstack.internal/v1"`:                         "everstack.internal does not resolve",
		`url: "https://everstack.internal.example.com/mcp"`:                        "",
	} {
		if got := classify(line, false); got != want {
			t.Errorf("classify(%q) = %q, want %q", line, got, want)
		}
	}
	if got := classify(`base_url="https://{instance}.{region}.everstack.ai/openai/v1",`, true); got != "" {
		t.Errorf("legacy alias flagged where it is allowed: %q", got)
	}
}
