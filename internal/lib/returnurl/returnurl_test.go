package returnurl

import "testing"

func TestResolve(t *testing.T) {
	allowed := []string{"https://app.everstack.ai"}
	const fallback = "/login"

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", fallback},
		{"root", "/", "/"},
		{"relative path", "/acme/settings?tab=1#x", "/acme/settings?tab=1#x"},
		{"relative path with encoded query", "/acme?q=100%25", "/acme?q=100%25"},
		{"relative path with trailing slash", "/acme/", "/acme/"},
		{"dot segments are normalized", "/acme/./settings/../billing", "/acme/billing"},
		{"encoded dot segments are normalized", "/acme/%2e/billing", "/acme/billing"},
		{"dot-dot above root stays at root", "/../../acme", "/acme"},
		{"allowed origin", "https://app.everstack.ai/acme", "https://app.everstack.ai/acme"},
		{"allowed origin, bare", "https://app.everstack.ai", "https://app.everstack.ai"},
		{"allowed origin, explicit default port", "https://app.everstack.ai:443/acme", "https://app.everstack.ai/acme"},
		{"allowed origin, uppercase host", "https://APP.EVERSTACK.AI/acme", "https://app.everstack.ai/acme"},
		{"allowed origin, uppercase scheme", "HTTPS://app.everstack.ai/acme", "https://app.everstack.ai/acme"},
		{"allowed origin, dot segments normalized", "https://app.everstack.ai/a/../acme", "https://app.everstack.ai/acme"},
		{"sibling host on the same registrable domain", "https://evil.everstack.ai/acme", fallback},
		{"tenant host is not an allowed origin", "https://acme-a1b2c3.eu-gra-1.everstack.ai/", fallback},
		{"allowed host on another port", "https://app.everstack.ai:8443/acme", fallback},
		{"prefix lookalike", "https://app.everstack.ai.evil.com", fallback},
		{"registrable lookalike", "https://evil-everstack.ai", fallback},
		{"protocol-relative", "//evil.com", fallback},
		{"slash-backslash", "/\\evil.com", fallback},
		{"backslash-backslash", "\\\\evil.com", fallback},
		{"backslash in path", "/acme\\..\\evil", fallback},
		{"scheme without slashes", "https:evil.com", fallback},
		{"javascript", "javascript:alert(1)", fallback},
		{"encoded protocol-relative", "%2F%2Fevil.com", fallback},
		{"encoded slash after slash", "/%2F/evil.com", fallback},
		{"encoded slash after slash, lowercase", "/%2f/evil.com", fallback},
		{"encoded backslash after slash", "/%5Cevil.com", fallback},
		{"dot segment before protocol-relative", "/.//evil.com", fallback},
		{"dot-dot before protocol-relative", "/a/..//evil.com", fallback},
		{"encoded dot before protocol-relative", "/%2e//evil.com", fallback},
		{"encoded dot-dot before protocol-relative", "/a/%2E%2E//evil.com", fallback},
		{"dot segment before backslash", "/./%5Cevil.com", fallback},
		{"dot segment on allowed origin", "https://app.everstack.ai/.//evil.com", fallback},
		{"encoded slash on allowed origin", "https://app.everstack.ai/%2F/evil.com", fallback},
		{"userinfo naming the allowed host", "https://app.everstack.ai@evil.com", fallback},
		{"userinfo on the allowed host", "https://u:p@app.everstack.ai/acme", fallback},
		{"http on the allowed host", "http://app.everstack.ai/acme", fallback},
		{"foreign host", "https://evil.com", fallback},
		{"leading space", " //evil.com", fallback},
		{"trailing space", "/acme ", fallback},
		{"tab", "/\t/evil.com", fallback},
		{"newline", "/acme\n//evil.com", fallback},
		{"null byte", "/acme\x00", fallback},
		{"relative without slash", "evil.com", fallback},
		{"malformed escape", "/acme%zz", fallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.raw, allowed, fallback); got != tc.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestResolveAllowedOriginsAreExact(t *testing.T) {
	// Configured origins are normalized, and a non-https or path-bearing
	// entry contributes only its origin.
	allowed := []string{"HTTPS://App.Everstack.AI:443/ignored/path", "http://localhost:3001"}
	if got := Resolve("https://app.everstack.ai/acme", allowed, "/"); got != "https://app.everstack.ai/acme" {
		t.Fatalf("normalized allowed origin: got %q", got)
	}
	if got := Resolve("http://localhost:3001/acme", allowed, "/"); got != "http://localhost:3001/acme" {
		t.Fatalf("exact http dev origin: got %q", got)
	}
	if got := Resolve("http://localhost:3002/acme", allowed, "/"); got != "/" {
		t.Fatalf("other dev port: got %q", got)
	}
	if got := Resolve("https://app.everstack.ai/acme", nil, "/"); got != "/" {
		t.Fatalf("no allowed origins accepts only paths: got %q", got)
	}
	if got := Resolve("https://app.everstack.ai/acme", []string{"", "not a url"}, "/"); got != "/" {
		t.Fatalf("unusable allowed origins: got %q", got)
	}
}

func TestPath(t *testing.T) {
	cases := map[string]string{
		"":                           "/",
		"/":                          "/",
		"/traces?id=1#span":          "/traces?id=1#span",
		"/traces/./spans":            "/traces/spans",
		"//evil.com":                 "/",
		"/\\evil.com":                "/",
		"/\tevil.com":                "/",
		"/%2F/evil.com":              "/",
		"/%5Cevil.com":               "/",
		"/.//evil.com":               "/",
		"/a/..//evil.com":            "/",
		"/%2e//evil.com":             "/",
		"https://evil.com":           "/",
		"https://app.everstack.ai/x": "/",
		"javascript:alert(1)":        "/",
		"traces":                     "/",
	}
	for raw, want := range cases {
		if got := Path(raw, "/"); got != want {
			t.Fatalf("Path(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSameRegistrableDomain(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"acme.eu-gra-1.everstack.ai", "app.everstack.ai", true},
		{"APP.everstack.ai:443", "app.everstack.ai", true},
		{"evil-everstack.ai", "app.everstack.ai", false},
		{"app.everstack.ai.evil.com", "app.everstack.ai", false},
	}
	for _, tc := range cases {
		if got := SameRegistrableDomain(tc.a, tc.b); got != tc.want {
			t.Fatalf("SameRegistrableDomain(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
