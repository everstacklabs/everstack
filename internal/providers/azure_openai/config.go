package azure_openai

import "net/http"

type Config struct {
	APIKey          string
	BaseURL         string
	APIVersion      string
	SupportedModels []string
	HTTPClient      *http.Client
}
