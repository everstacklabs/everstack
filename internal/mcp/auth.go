package mcp

import "fmt"

// ResolveAuthHeaders converts an AuthConfig into HTTP headers suitable for
// authenticating requests to an MCP server.
func ResolveAuthHeaders(auth *AuthConfig) map[string]string {
	if auth == nil {
		return nil
	}

	switch auth.Type {
	case AuthTypeBearer:
		if auth.Token != "" {
			return map[string]string{
				"Authorization": fmt.Sprintf("Bearer %s", auth.Token),
			}
		}

	case AuthTypeAPIKey:
		if auth.Token != "" {
			header := auth.APIKeyHeader
			if header == "" {
				header = "Authorization"
			}
			return map[string]string{
				header: auth.Token,
			}
		}

	case AuthTypeOAuth2:
		if auth.AccessToken != "" {
			return map[string]string{
				"Authorization": fmt.Sprintf("Bearer %s", auth.AccessToken),
			}
		}
	}

	return nil
}

// mergeHeaders merges auth headers into an existing headers map. Auth headers
// take precedence over explicit headers when keys conflict.
func mergeHeaders(base map[string]string, auth map[string]string) map[string]string {
	if len(auth) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(auth))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range auth {
		merged[k] = v
	}
	return merged
}
