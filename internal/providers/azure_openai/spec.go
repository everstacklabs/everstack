package azure_openai

import "github.com/everstacklabs/everstack/internal/providers/catalog"

func DefaultSpec() catalog.Spec {
	return catalog.Spec{
		Name:       "azure-openai",
		Display:    "Azure OpenAI",
		BaseURL:    "https://{resource}.openai.azure.com",
		APIVersion: "2024-10-21",
	}
}
