package deepseek

import "github.com/everstacklabs/everstack/internal/providers/catalog"

func DefaultSpec() catalog.Spec {
	return catalog.Spec{
		Name:       "deepseek",
		Display:    "DeepSeek",
		BaseURL:    "https://api.deepseek.com",
		APIVersion: "2024-01-01",
	}
}
