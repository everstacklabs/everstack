package correlation

import (
	"context"
	"net/http"
	"strings"

	typeid "github.com/sumup/typeid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Constants for HTTP headers
const (
	CorrelationIDHeader = "x-correlation-id"
)

// Endpoint types for correlation ID prefixes
const (
	EndpointChat       = "chat"
	EndpointEmbeddings = "emb"
	EndpointResponse   = "resp"
	EndpointGeneric    = "corr"
)

type correlationIDKey struct{}

// WithCorrelationID adds a correlation ID to the context
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, correlationIDKey{}, correlationID)
}

// GetCorrelationID retrieves the correlation ID from the context
// Returns an empty string if no correlation ID is found
func GetCorrelationID(ctx context.Context) string {
	correlationID, ok := ctx.Value(correlationIDKey{}).(string)
	if !ok {
		return ""
	}
	return correlationID
}

// Endpoint-specific prefixes for correlation IDs

// ChatPrefix is the prefix for chat completion correlation IDs
type ChatPrefix struct{}

func (ChatPrefix) Prefix() string {
	return "chat"
}

// EmbPrefix is the prefix for embeddings correlation IDs
type EmbPrefix struct{}

func (EmbPrefix) Prefix() string {
	return "emb"
}

// RespPrefix is the prefix for response API correlation IDs
type RespPrefix struct{}

func (RespPrefix) Prefix() string {
	return "resp"
}

// CorrelationPrefix is the generic prefix (legacy)
type CorrelationPrefix struct{}

func (CorrelationPrefix) Prefix() string {
	return "corr"
}

// Type aliases for different correlation ID types
type CorrelationID = typeid.Sortable[CorrelationPrefix]
type ChatCorrelationID = typeid.Sortable[ChatPrefix]
type EmbCorrelationID = typeid.Sortable[EmbPrefix]
type RespCorrelationID = typeid.Sortable[RespPrefix]

// GenerateCorrelationID generates a random correlation ID using nanoid (legacy)
func GenerateCorrelationID() CorrelationID {
	id, err := typeid.New[CorrelationID]()
	if err != nil {
		return CorrelationID{}
	}
	return id
}

// GenerateChatCorrelationID generates a correlation ID for chat completion requests
func GenerateChatCorrelationID() ChatCorrelationID {
	id, err := typeid.New[ChatCorrelationID]()
	if err != nil {
		return ChatCorrelationID{}
	}
	return id
}

// GenerateEmbCorrelationID generates a correlation ID for embeddings requests
func GenerateEmbCorrelationID() EmbCorrelationID {
	id, err := typeid.New[EmbCorrelationID]()
	if err != nil {
		return EmbCorrelationID{}
	}
	return id
}

// GenerateRespCorrelationID generates a correlation ID for response API requests
func GenerateRespCorrelationID() RespCorrelationID {
	id, err := typeid.New[RespCorrelationID]()
	if err != nil {
		return RespCorrelationID{}
	}
	return id
}

// GenerateEndpointCorrelationID generates a correlation ID with the appropriate prefix
// based on the endpoint type. Valid endpoints: "chat", "emb", "resp", or "" for generic.
func GenerateEndpointCorrelationID(endpoint string) string {
	switch endpoint {
	case EndpointChat:
		return GenerateChatCorrelationID().String()
	case EndpointEmbeddings:
		return GenerateEmbCorrelationID().String()
	case EndpointResponse:
		return GenerateRespCorrelationID().String()
	default:
		return GenerateCorrelationID().String()
	}
}

// DetectEndpointFromPath detects the endpoint type from an HTTP path
func DetectEndpointFromPath(path string) string {
	pathLower := strings.ToLower(path)
	switch {
	case strings.Contains(pathLower, "chat") || strings.Contains(pathLower, "completion"):
		return EndpointChat
	case strings.Contains(pathLower, "embed"):
		return EndpointEmbeddings
	case strings.Contains(pathLower, "response"):
		return EndpointResponse
	default:
		return EndpointGeneric
	}
}

// DetectEndpointFromProcedure detects the endpoint type from a gRPC procedure name
func DetectEndpointFromProcedure(procedure string) string {
	procLower := strings.ToLower(procedure)
	switch {
	case strings.Contains(procLower, "chat") || strings.Contains(procLower, "completion"):
		return EndpointChat
	case strings.Contains(procLower, "embed"):
		return EndpointEmbeddings
	case strings.Contains(procLower, "response"):
		return EndpointResponse
	default:
		return EndpointGeneric
	}
}

// WithCorrelationIDTransport is an http.RoundTripper that adds the correlation ID
// from the context to outgoing HTTP requests
type WithCorrelationIDTransport struct {
	Base http.RoundTripper
}

// RoundTrip implements the http.RoundTripper interface
func (t *WithCorrelationIDTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Get the correlation ID from the context
	correlationID := GetCorrelationID(req.Context())
	if correlationID != "" {
		// Add the correlation ID to the request headers.
		// It's important NOT to clone the request here, as `req.Clone` does not copy
		// the request body, which is necessary for the OIDC token exchange.
		// Modifying the request in place is safe because the OIDC library does not reuse it.
		req.Header.Set(CorrelationIDHeader, correlationID)

		// Add correlation ID as a span attribute if we have an active span
		span := trace.SpanFromContext(req.Context())
		if span.IsRecording() {
			span.SetAttributes(attribute.String("correlation_id", correlationID))
		}
	}

	// Use the base transport to perform the request
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	return base.RoundTrip(req)
}

// NewHTTPClientWithCorrelationID creates a new HTTP client that propagates correlation IDs
func NewHTTPClientWithCorrelationID() *http.Client {
	return &http.Client{
		Transport: &WithCorrelationIDTransport{
			Base: http.DefaultTransport,
		},
	}
}

// EnsureCorrelationID ensures that the context has a correlation ID
// If one doesn't exist, it generates a new one and adds it to the context
// This is the legacy function that uses the generic "corr" prefix
func EnsureCorrelationID(ctx context.Context) (context.Context, string) {
	correlationID := GetCorrelationID(ctx)
	if correlationID == "" {
		correlationID = GenerateCorrelationID().String()
		ctx = WithCorrelationID(ctx, correlationID)
		// Add correlation ID to the current span
		span := trace.SpanFromContext(ctx)
		if span.IsRecording() {
			span.SetAttributes(attribute.String("correlation_id", correlationID))
			span.AddEvent("Generated new correlation ID")
		}
	}
	return ctx, correlationID
}

// EnsureEndpointCorrelationID ensures that the context has a correlation ID
// with the appropriate endpoint-specific prefix.
// If one doesn't exist, it generates a new one and adds it to the context.
func EnsureEndpointCorrelationID(ctx context.Context, endpoint string) (context.Context, string) {
	correlationID := GetCorrelationID(ctx)
	if correlationID == "" {
		correlationID = GenerateEndpointCorrelationID(endpoint)
		ctx = WithCorrelationID(ctx, correlationID)
		// Add correlation ID to the current span
		span := trace.SpanFromContext(ctx)
		if span.IsRecording() {
			span.SetAttributes(
				attribute.String("correlation_id", correlationID),
				attribute.String("correlation_endpoint", endpoint),
			)
			span.AddEvent("Generated new correlation ID")
		}
	}
	return ctx, correlationID
}

// AddCorrelationIDToSpan adds the correlation ID as an attribute to the current span
func AddCorrelationIDToSpan(ctx context.Context, correlationID string) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attribute.String("correlation_id", correlationID))
	}
}
