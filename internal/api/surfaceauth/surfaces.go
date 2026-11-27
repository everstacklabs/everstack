// Package surfaceauth defines the exact inference entry points for bot keys.
package surfaceauth

import (
	"net/http"

	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
)

func Procedure(surfaces []string, procedure string) bool {
	if surfaces == nil {
		return true
	}
	if !apikey.Allowed(surfaces, apikey.SurfaceInference) {
		return false
	}
	switch procedure {
	case gatewayconnect.GatewayServiceChatCompletionProcedure,
		gatewayconnect.GatewayServiceCreateResponseProcedure,
		gatewayconnect.GatewayServiceEmbeddingsProcedure,
		gatewayconnect.GatewayServiceListModelsProcedure:
		return true
	}
	return false
}

func HTTP(surfaces []string, method, path string) bool {
	if surfaces == nil {
		return true
	}
	if !apikey.Allowed(surfaces, apikey.SurfaceInference) {
		return false
	}
	switch method + " " + path {
	case "POST /v1/chat/completions", "POST /v1/responses", "POST /v1/embeddings", "POST /v1/messages",
		"GET /v1/models", "GET /v1/gateway/models",
		"POST /openai/v1/chat/completions", "POST /openai/v1/responses", "POST /openai/v1/embeddings", "POST /openai/v1/messages",
		"GET /openai/v1/models", "GET /openai/v1/gateway/models":
		return true
	}
	return false
}

func Request(surfaces []string, r *http.Request) bool { return HTTP(surfaces, r.Method, r.URL.Path) }
