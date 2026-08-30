package cohere

import "github.com/everstacklabs/everstack/internal/providers/catalog"

// DefaultSpec returns the default Cohere provider specification
func DefaultSpec() catalog.Spec {
	return catalog.Spec{
		Name:       "cohere",
		Display:    "Cohere",
		BaseURL:    "https://api.cohere.com/v1",
		APIVersion: "2024-01-01",
	}
}
