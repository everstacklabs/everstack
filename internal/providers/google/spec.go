package google

import "github.com/everstacklabs/everstack/internal/providers/catalog"

// DefaultSpec returns the default Google (Gemini) provider specification
func DefaultSpec() catalog.Spec {
	return catalog.Spec{
		Name:       "google",
		Display:    "Google",
		BaseURL:    "https://generativelanguage.googleapis.com/v1",
		APIVersion: "2024-01-01",
	}
}
