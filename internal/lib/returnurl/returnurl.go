// Package returnurl decides where a service may redirect a browser when the
// destination comes from request input (returnUrl, next, OAuth state). It
// mirrors the console's TypeScript helper (@everstack/utils return-url).
package returnurl

import (
	"net"
	"net/url"
	"strings"
)

// Resolve returns a redirect target for raw, or fallback when raw is not
// safe. Accepted targets are rebuilt from the parsed URL with dot segments
// (literal or percent-encoded) removed, never echoed:
//
//   - a same-origin relative path: one leading "/", and no backslash,
//     control character, or leading "//" or "/\" either as given, once
//     percent-decoded, or after dot-segment normalization;
//   - an http(s) URL without userinfo whose origin (scheme, host, port)
//     exactly equals one of allowedOrigins, with the same path rules.
//
// Each allowedOrigins entry contributes only its normalized origin.
func Resolve(raw string, allowedOrigins []string, fallback string) string {
	if raw == "" || raw != strings.TrimSpace(raw) || hasUnsafeByte(raw) {
		return fallback
	}

	if strings.HasPrefix(raw, "/") {
		if strings.HasPrefix(raw, "//") {
			return fallback
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" {
			return fallback
		}
		path, ok := safePath(parsed.EscapedPath())
		if !ok {
			return fallback
		}
		return path + suffixOf(parsed)
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return fallback
	}
	origin, ok := originOf(parsed)
	if !ok || !containsOrigin(allowedOrigins, origin) {
		return fallback
	}
	path := ""
	if escaped := parsed.EscapedPath(); escaped != "" {
		if path, ok = safePath(escaped); !ok {
			return fallback
		}
	}
	return origin + path + suffixOf(parsed)
}

// Path returns raw rebuilt as a same-origin relative path, or fallback. Use it
// for post-login "next" parameters that must never leave the current host.
func Path(raw, fallback string) string {
	return Resolve(raw, nil, fallback)
}

// SameRegistrableDomain reports whether a and b share their last two DNS
// labels (e.g. "tenant.eu-gra-1.everstack.ai" and "app.everstack.ai" both
// map to "everstack.ai"). Labels are compared whole, so "evil-everstack.ai"
// does not match. It does not handle multi-label public suffixes such as
// "co.uk"; the platform uses single-label suffixes only. It is not a
// redirect policy: Resolve uses exact origins.
func SameRegistrableDomain(a, b string) bool {
	return registrableSuffix(a) == registrableSuffix(b)
}

func registrableSuffix(host string) string {
	host = strings.ToLower(host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// safePath normalizes an escaped absolute path and reports whether it is
// still a same-origin path: it must not start with "//" or "/\", literally or
// once percent-decoded, before or after dot segments are removed.
func safePath(escaped string) (string, bool) {
	if !strings.HasPrefix(escaped, "/") || spellsAuthority(escaped) {
		return "", false
	}
	normalized, ok := removeDotSegments(escaped)
	if !ok || spellsAuthority(normalized) {
		return "", false
	}
	return normalized, true
}

func spellsAuthority(escaped string) bool {
	if strings.HasPrefix(escaped, "//") || strings.HasPrefix(escaped, "/\\") {
		return true
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return true
	}
	return strings.HasPrefix(decoded, "//") || strings.HasPrefix(decoded, "/\\")
}

// removeDotSegments applies RFC 3986 section 5.2.4 to an escaped path,
// treating percent-encoded "." and ".." segments as browsers do.
func removeDotSegments(escaped string) (string, bool) {
	segments := strings.Split(escaped, "/")[1:]
	out := make([]string, 0, len(segments))
	for i, segment := range segments {
		last := i == len(segments)-1
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return "", false
		}
		switch decoded {
		case ".":
			if last {
				out = append(out, "")
			}
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, segment)
		}
	}
	return "/" + strings.Join(out, "/"), true
}

func suffixOf(parsed *url.URL) string {
	suffix := ""
	if parsed.RawQuery != "" || parsed.ForceQuery {
		suffix += "?" + parsed.RawQuery
	}
	if parsed.Fragment != "" {
		suffix += "#" + parsed.EscapedFragment()
	}
	return suffix
}

// originOf returns scheme://host[:port] for an http(s) URL, lowercased, with
// the scheme's default port dropped.
func originOf(parsed *url.URL) (string, bool) {
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", false
	}
	port := parsed.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		return scheme + "://" + net.JoinHostPort(host, port), true
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host, true
}

func containsOrigin(allowed []string, origin string) bool {
	for _, entry := range allowed {
		parsed, err := url.Parse(strings.TrimSpace(entry))
		if err != nil || parsed.User != nil {
			continue
		}
		if candidate, ok := originOf(parsed); ok && candidate == origin {
			return true
		}
	}
	return false
}

func hasUnsafeByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return true
		}
	}
	return false
}
