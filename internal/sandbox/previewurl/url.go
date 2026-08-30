// Package previewurl centralizes sandbox preview URL construction.
//
// Direct URLs are the default app-preview links returned by port exposure. They
// use path routing for localhost-style domains and subdomain routing for real
// wildcard DNS domains. Signed URLs are shareable links carrying an HMAC token;
// those always use subdomain routing because token claims bind to subdomain,
// sandbox, tenant, and port. Private URLs use gateway path routing for internal
// tools that already carry sandbox scope and should not depend on wildcard DNS.
package previewurl

import (
	"fmt"
	"strings"
)

type Mode string

const (
	ModeDirect  Mode = "direct"
	ModeSigned  Mode = "signed"
	ModePrivate Mode = "private"
)

type Config struct {
	BaseDomain string
	TLSEnabled bool
	ListenPort string
}

// DirectURL returns the standard user-facing preview URL. Localhost-style
// domains use path routing because wildcard subdomains are not reliably
// available in local development; real domains use subdomain routing.
func DirectURL(config Config, subdomain, sandboxID string, port int) string {
	if IsLocalBaseDomain(config.BaseDomain) {
		return PathURL(config, sandboxID, port)
	}
	return SubdomainURL(config, subdomain)
}

// SignedURLBase returns the base URL for signed/shareable preview URLs. Signed
// tokens are bound to subdomain + sandbox + port, so this always uses the
// canonical subdomain form.
func SignedURLBase(config Config, subdomain string) string {
	return SubdomainURL(config, subdomain)
}

// PrivateURL returns the internal gateway path form for private/internal tools
// that already have sandbox scope and should not depend on wildcard DNS.
func PrivateURL(config Config, sandboxID string, port int) string {
	return PathURL(config, sandboxID, port)
}

func SubdomainURL(config Config, subdomain string) string {
	baseDomain := normalizedBaseDomain(config.BaseDomain)
	if config.ListenPort != "" && config.ListenPort != defaultPort(config.TLSEnabled) {
		return fmt.Sprintf("%s://%s.%s:%s", scheme(config.TLSEnabled), subdomain, baseDomain, config.ListenPort)
	}
	return fmt.Sprintf("%s://%s.%s", scheme(config.TLSEnabled), subdomain, baseDomain)
}

func PathURL(config Config, sandboxID string, port int) string {
	host := normalizedBaseDomain(config.BaseDomain)
	if config.ListenPort != "" && config.ListenPort != defaultPort(config.TLSEnabled) {
		host = fmt.Sprintf("%s:%s", host, config.ListenPort)
	}
	return fmt.Sprintf("%s://%s/_sandbox/%s/port/%d/", scheme(config.TLSEnabled), host, sandboxID, port)
}

func IsLocalBaseDomain(baseDomain string) bool {
	baseDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(baseDomain), "."))
	return baseDomain == "" || baseDomain == "localhost" || baseDomain == "127.0.0.1" || baseDomain == "0.0.0.0" || strings.HasSuffix(baseDomain, ".localhost")
}

func normalizedBaseDomain(baseDomain string) string {
	baseDomain = strings.TrimSpace(baseDomain)
	if baseDomain == "" {
		return "localhost"
	}
	return strings.TrimSuffix(baseDomain, ".")
}

func scheme(tlsEnabled bool) string {
	if tlsEnabled {
		return "https"
	}
	return "http"
}

func defaultPort(tlsEnabled bool) string {
	if tlsEnabled {
		return "443"
	}
	return "80"
}
