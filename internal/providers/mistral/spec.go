package mistral

import "github.com/everstacklabs/everstack/internal/providers/catalog"

// DefaultSpec returns the default Mistral AI provider specification
func DefaultSpec() catalog.Spec {
	return catalog.Spec{
		Name:       "mistral",
		Display:    "Mistral AI",
		BaseURL:    "https://api.mistral.ai/v1",
		APIVersion: "2024-01-01",
	}
}
