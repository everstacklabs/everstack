package gateway

import (
	"net"
	"net/http"
	"strings"
)

// RouteType indicates which handler should serve the request.
type RouteType int

const (
	RouteWebhook   RouteType = iota // /wh/* paths
	RouteSubdomain                  // subdomain.baseDomain
	RouteSession                    // {prefix}.session.baseDomain
	RouteHeader                     // X-Sandbox-ID + X-Sandbox-Port headers
	RoutePath                       // /_sandbox/{id}/port/{port}/{path...}
	RouteUnknown                    // no match
)

// RouteResult contains the resolved routing information.
type RouteResult struct {
	Type RouteType

	// Subdomain is the extracted subdomain for subdomain-based routing.
	Subdomain string

	// SandboxID and Port are used for header and path-based routing.
	SandboxID string
	Port      string

	// Path is the remaining path after stripping routing prefix.
	Path string

	// SessionID is the session prefix for session-based routing.
	SessionID string

	// WebhookPath is the path segment after /wh/ for webhook routing.
	WebhookPath string
}

// RouteResolver resolves incoming requests to backend targets.
type RouteResolver struct {
	baseDomain           string
	enableSessionRouting bool
}

// NewRouteResolver creates a new route resolver.
func NewRouteResolver(baseDomain string, enableSessionRouting bool) *RouteResolver {
	return &RouteResolver{
		baseDomain:           baseDomain,
		enableSessionRouting: enableSessionRouting,
	}
}

// Resolve determines the route for an incoming request.
// Resolution order: webhook path > session subdomain > regular subdomain > header > path
func (rr *RouteResolver) Resolve(r *http.Request) RouteResult {
	// 1. Webhook: path starts with /wh/
	if strings.HasPrefix(r.URL.Path, "/wh/") {
		webhookPath := strings.TrimPrefix(r.URL.Path, "/wh/")
		if webhookPath != "" {
			return RouteResult{
				Type:        RouteWebhook,
				WebhookPath: webhookPath,
			}
		}
	}

	// Extract host without port
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// 2. Session subdomain: {session_id_prefix}.session.{baseDomain}
	if rr.enableSessionRouting {
		sessionSuffix := ".session." + rr.baseDomain
		if strings.HasSuffix(host, sessionSuffix) {
			sessionID := strings.TrimSuffix(host, sessionSuffix)
			if sessionID != "" {
				return RouteResult{
					Type:      RouteSession,
					SessionID: sessionID,
				}
			}
		}
	}

	// 3. Subdomain: {subdomain}.{baseDomain}
	subdomain := extractSubdomain(host, rr.baseDomain)
	if subdomain != "" {
		return RouteResult{
			Type:      RouteSubdomain,
			Subdomain: subdomain,
		}
	}

	// 4. Header: X-Sandbox-ID + X-Sandbox-Port
	sandboxID := r.Header.Get("X-Sandbox-ID")
	sandboxPort := r.Header.Get("X-Sandbox-Port")
	if sandboxID != "" && sandboxPort != "" {
		return RouteResult{
			Type:      RouteHeader,
			SandboxID: sandboxID,
			Port:      sandboxPort,
			Path:      r.URL.Path,
		}
	}

	// 5. Path: /_sandbox/{sandbox_id}/port/{port}/{path...}
	if strings.HasPrefix(r.URL.Path, "/_sandbox/") {
		result, ok := parsePathRoute(r.URL.Path)
		if ok {
			return result
		}
	}

	return RouteResult{Type: RouteUnknown}
}

// extractSubdomain strips the base domain suffix and returns the subdomain.
func extractSubdomain(host, baseDomain string) string {
	if strings.HasSuffix(host, "."+baseDomain) {
		return strings.TrimSuffix(host, "."+baseDomain)
	}
	// Handle trailing dot in DNS
	if strings.HasSuffix(host, "."+baseDomain+".") {
		return strings.TrimSuffix(host, "."+baseDomain+".")
	}
	return ""
}

// parsePathRoute parses /_sandbox/{sandbox_id}/port/{port}/{path...}
func parsePathRoute(urlPath string) (RouteResult, bool) {
	// Strip /_sandbox/ prefix
	rest := strings.TrimPrefix(urlPath, "/_sandbox/")

	// Extract sandbox_id
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return RouteResult{}, false
	}
	sandboxID := rest[:slashIdx]
	rest = rest[slashIdx+1:]

	// Expect "port/"
	if !strings.HasPrefix(rest, "port/") {
		return RouteResult{}, false
	}
	rest = strings.TrimPrefix(rest, "port/")

	// Extract port
	slashIdx = strings.Index(rest, "/")
	var port, remainingPath string
	if slashIdx < 0 {
		port = rest
		remainingPath = "/"
	} else {
		port = rest[:slashIdx]
		remainingPath = rest[slashIdx:]
	}

	if sandboxID == "" || port == "" {
		return RouteResult{}, false
	}

	return RouteResult{
		Type:      RoutePath,
		SandboxID: sandboxID,
		Port:      port,
		Path:      remainingPath,
	}, true
}
