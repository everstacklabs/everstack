package deviceauth

import (
	"net/http"
	"net/url"
	"strings"
)

func VerificationURI(configuredURL string, header http.Header) string {
	configuredExternalURL := validExternalURL(configuredURL)
	var forwardedExternalURL string
	forwardedHost := firstForwardedValue(header.Get("X-Forwarded-Host"))
	if forwardedHost != "" {
		proto := strings.ToLower(firstForwardedValue(header.Get("X-Forwarded-Proto")))
		if proto != "http" && proto != "https" {
			proto = "https"
		}
		forwardedExternalURL = validExternalURL(proto + "://" + forwardedHost)
	}

	// A tenant gateway cannot have one static external hostname. Preserve an
	// explicitly configured public URL, but let ingress replace loopback-only
	// development values such as the dev chart's localhost setting.
	if configuredExternalURL != "" && !isLoopbackURL(configuredExternalURL) {
		return configuredExternalURL + "/device"
	}
	if forwardedExternalURL != "" {
		return forwardedExternalURL + "/device"
	}
	if configuredExternalURL != "" {
		return configuredExternalURL + "/device"
	}
	if origin := validExternalURL(header.Get("Origin")); origin != "" {
		return origin + "/device"
	}
	return "/device"
}

func validExternalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/")
}

func isLoopbackURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func firstForwardedValue(value string) string {
	if before, _, ok := strings.Cut(value, ","); ok {
		return strings.TrimSpace(before)
	}
	return strings.TrimSpace(value)
}
