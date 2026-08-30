package http

import (
	"github.com/everstacklabs/everstack/internal/api/common"
)

// All functions in this file are used to determine the origin of a request.

// GetOriginFromUrlString delegates to common implementation
func GetOriginFromUrlString(s string) (string, error) {
	return common.GetOriginFromUrlString(s)
}

// IsOriginAllowed delegates to common implementation
func IsOriginAllowed(allowList []string, origin string) bool {
	return common.IsOriginAllowed(allowList, origin)
}

// IsOrigin delegates to common implementation
func IsOrigin(origin string) bool {
	return common.IsOrigin(origin)
}

// BuildHTTP delegates to common implementation
func BuildHTTP(hostname string, externalPort uint16, secure bool) string {
	return common.BuildHTTP(hostname, externalPort, secure)
}

// BuildOrigin delegates to common implementation
func BuildOrigin(host string, secure bool) string {
	return common.BuildOrigin(host, secure)
}
