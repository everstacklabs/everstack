package common

import (
	"context"
	"fmt"
	"net/url"
)

// IsOriginAllowed checks if the given origin is in the allowList
func IsOriginAllowed(allowList []string, origin string) bool {
	for _, allowedOrigin := range allowList {
		if allowedOrigin == origin {
			return true
		}
	}
	return false
}

// IsOrigin checks if the given string is a valid origin
func IsOrigin(origin string) bool {
	parsedUrl, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsedUrl.Scheme != "" && parsedUrl.Host != "" && parsedUrl.Path == "" && len(parsedUrl.Query()) == 0 && parsedUrl.Fragment == ""
}

// GetOriginFromUrlString extracts the origin from a URL string
func GetOriginFromUrlString(s string) (string, error) {
	parsed, err := url.Parse(s)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host), nil
}

// BuildHTTP builds an HTTP origin with the given hostname and port
func BuildHTTP(hostname string, externalPort uint16, secure bool) string {
	if externalPort == 0 || (externalPort == 443 && secure) {
		return BuildOrigin(hostname, secure)
	}
	return BuildOrigin(fmt.Sprintf("%s:%d", hostname, externalPort), secure)
}

// BuildOrigin builds an origin with the given host and security setting
func BuildOrigin(host string, secure bool) string {
	schema := "https"
	if !secure {
		schema = "http"
	}
	return fmt.Sprintf("%s://%s", schema, host)
}

// OriginFromContext extracts the origin from the context
// This function signature may need to be adjusted based on how you store headers in context
func OriginFromContext(ctx context.Context, headerKey interface{}, originHeader string) string {
	// This is a placeholder implementation - you'll need to adapt it to your actual context structure
	if headers, ok := ctx.Value(headerKey).(map[string][]string); ok {
		if values, exists := headers[originHeader]; exists && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}
