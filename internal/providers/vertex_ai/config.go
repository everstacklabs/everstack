package vertex_ai

import (
	"net/http"

	"golang.org/x/oauth2"
)

type Config struct {
	Credentials     string
	BaseURL         string
	SupportedModels []string
	HTTPClient      *http.Client
	TokenSource     oauth2.TokenSource
}
