// Package cache provides caching implementations for the gateway.
package cache

import (
	"context"
	"fmt"
	"reflect"
)

// RouterAdapter adapts a gateway router to the cache Router interface
// It uses reflection to avoid circular dependencies with the gateway package
type RouterAdapter struct {
	gatewayRouter interface{}
}

// NewRouterAdapter creates a new router adapter that bridges gateway.Router to cache.Router
// The gatewayRouter parameter should be a *gateway.Router but we use interface{} to avoid import cycles
func NewRouterAdapter(gatewayRouter interface{}) Router {
	return &RouterAdapter{
		gatewayRouter: gatewayRouter,
	}
}

// Resolve implements cache.Router by delegating to the gateway router using reflection
func (a *RouterAdapter) Resolve(model string) (Provider, Route, error) {
	if a.gatewayRouter == nil {
		return nil, Route{}, fmt.Errorf("gateway router is nil")
	}

	// Use reflection to call the Resolve method
	routerVal := reflect.ValueOf(a.gatewayRouter)

	// Check if the value is nil (for pointer types)
	if routerVal.Kind() == reflect.Ptr && routerVal.IsNil() {
		return nil, Route{}, fmt.Errorf("gateway router pointer is nil")
	}

	resolveMethod := routerVal.MethodByName("Resolve")
	if !resolveMethod.IsValid() {
		return nil, Route{}, fmt.Errorf("gateway router does not have a Resolve method")
	}

	// Call Resolve(model string) (Provider, ModelRoute, error)
	args := []reflect.Value{reflect.ValueOf(model)}
	results := resolveMethod.Call(args)

	if len(results) != 3 {
		return nil, Route{}, fmt.Errorf("Resolve method returned unexpected number of values: %d", len(results))
	}

	// Check for error (third return value)
	if !results[2].IsNil() {
		err, ok := results[2].Interface().(error)
		if !ok {
			return nil, Route{}, fmt.Errorf("Resolve method third return value is not an error")
		}
		return nil, Route{}, err
	}

	// Get the provider (first return value)
	providerIface := results[0].Interface()
	if providerIface == nil {
		return nil, Route{}, fmt.Errorf("provider is nil")
	}

	// Get the route (second return value)
	routeIface := results[1].Interface()

	// Extract ModelName from the route using reflection
	cacheRoute := Route{}
	if routeIface != nil {
		routeVal := reflect.ValueOf(routeIface)
		if routeVal.Kind() == reflect.Struct {
			modelNameField := routeVal.FieldByName("ModelName")
			if modelNameField.IsValid() && modelNameField.Kind() == reflect.String {
				cacheRoute.ModelName = modelNameField.String()
			}
		}
	}

	// Wrap the provider
	wrappedProvider := &ProviderAdapter{
		gatewayProvider: providerIface,
	}

	return wrappedProvider, cacheRoute, nil
}

// ProviderAdapter adapts a gateway provider to the cache Provider interface
type ProviderAdapter struct {
	gatewayProvider interface{}
}

// Embed implements cache.Provider by delegating to the gateway provider using reflection
func (p *ProviderAdapter) Embed(ctx context.Context, request EmbeddingsRequest) (EmbeddingsResponse, error) {
	if p.gatewayProvider == nil {
		return EmbeddingsResponse{}, fmt.Errorf("gateway provider is nil")
	}

	// Use reflection to call the Embed method
	providerVal := reflect.ValueOf(p.gatewayProvider)
	embedMethod := providerVal.MethodByName("Embed")
	if !embedMethod.IsValid() {
		return EmbeddingsResponse{}, fmt.Errorf("gateway provider does not have an Embed method")
	}

	// Create a request struct that matches gateway.EmbeddingsRequest
	// We know from the gateway package that it has Model and Input fields
	requestType := reflect.StructOf([]reflect.StructField{
		{Name: "Model", Type: reflect.TypeOf(""), Tag: `json:"model"`},
		{Name: "Input", Type: reflect.TypeOf(""), Tag: `json:"input"`},
		{Name: "Metadata", Type: reflect.TypeOf(map[string]interface{}(nil)), Tag: `json:"metadata,omitempty"`},
	})

	requestVal := reflect.New(requestType).Elem()
	requestVal.FieldByName("Model").SetString(request.Model)
	requestVal.FieldByName("Input").SetString(request.Input)

	// Call Embed(ctx, request) (response, error)
	args := []reflect.Value{reflect.ValueOf(ctx), requestVal}
	results := embedMethod.Call(args)

	if len(results) != 2 {
		return EmbeddingsResponse{}, fmt.Errorf("Embed method returned unexpected number of values: %d", len(results))
	}

	// Check for error (second return value)
	if !results[1].IsNil() {
		err, ok := results[1].Interface().(error)
		if !ok {
			return EmbeddingsResponse{}, fmt.Errorf("Embed method second return value is not an error")
		}
		return EmbeddingsResponse{}, err
	}

	// Get the response (first return value)
	respIface := results[0].Interface()
	if respIface == nil {
		return EmbeddingsResponse{}, fmt.Errorf("embedding response is nil")
	}

	// Extract Embedding field using reflection
	respVal := reflect.ValueOf(respIface)
	if respVal.Kind() == reflect.Struct {
		embeddingField := respVal.FieldByName("Embedding")
		if embeddingField.IsValid() && embeddingField.Kind() == reflect.Slice {
			// Convert to []float64
			embedding := make([]float64, embeddingField.Len())
			for i := 0; i < embeddingField.Len(); i++ {
				elem := embeddingField.Index(i)
				if elem.Kind() == reflect.Float64 {
					embedding[i] = elem.Float()
				}
			}
			return EmbeddingsResponse{
				Embedding: embedding,
			}, nil
		}
	}

	return EmbeddingsResponse{}, fmt.Errorf("failed to extract embedding from response")
}
