package utils

import "strings"

func MaskApiKey(apiKey string) string {
	if apiKey == "" {
		return ""
	}

	// Find the prefix (e.g., "sk-mf-svcacct-api01-")
	parts := strings.Split(apiKey, "-")
	if len(parts) < 3 {
		return strings.Repeat("*", len(apiKey))
	}

	// Keep the first 3 parts (e.g., "sk-mf-svcacct")
	prefix := strings.Join(parts[:3], "-") + "-"

	// Get the last 4 characters of the key
	suffix := ""
	if len(apiKey) > 4 {
		suffix = apiKey[len(apiKey)-4:]
	}

	// Calculate how many asterisks we need
	// Total length should be similar to original, but we'll use a fixed pattern
	asteriskCount := 100 // Fixed number of asterisks for consistent display

	return prefix + strings.Repeat("*", asteriskCount) + suffix
}
