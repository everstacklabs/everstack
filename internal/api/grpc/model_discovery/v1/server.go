package model_discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/domain/custom_models"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/services/model_discovery"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/model_discovery/v1"
	modeldiscoveryconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/model_discovery/v1/model_discoveryconnect"
	"github.com/jmoiron/sqlx"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func tenantIDFromContext(ctx context.Context) (string, error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	return tenantID, nil
}

// Server implements the ModelDiscoveryService gRPC server
type Server struct {
	providerspb.UnimplementedModelDiscoveryServiceServer
	discoveryService    *model_discovery.Service
	customModelsRepo    *custom_models.Repository
	providerRepo        *provider_config.Repository
	apiKeyRepo          provider_api_keys.Repository
	serviceInterceptors []connect.Interceptor
}

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *Server) WithInterceptors(interceptors ...connect.Interceptor) *Server {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

// NewServer creates a new model discovery gRPC server
func NewServer(
	discoveryService *model_discovery.Service,
	customModelsRepo *custom_models.Repository,
	providerRepo *provider_config.Repository,
	apiKeyRepo provider_api_keys.Repository,
) *Server {
	return &Server{
		discoveryService: discoveryService,
		customModelsRepo: customModelsRepo,
		providerRepo:     providerRepo,
		apiKeyRepo:       apiKeyRepo,
	}
}

// SearchModels searches for available models from a meta-provider
func (s *Server) SearchModels(ctx context.Context, req *connect.Request[providerspb.SearchModelsRequest]) (*connect.Response[providerspb.SearchModelsResponse], error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.ProviderName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider_name is required"))
	}

	// Get API key if requested
	var apiKey string
	if req.Msg.UseProviderApiKey {
		// First try to get provider configuration
		providerConfig, err := s.providerRepo.GetForOrg(ctx, tenantID, req.Msg.ProviderName)
		if err == nil {
			// Provider config exists, use its API key
			apiKey = providerConfig.APIKeyEncrypted // TODO: Decrypt this

			// If no API key in config, try to get from API keys table
			if apiKey == "" && s.apiKeyRepo != nil {
				apiKeys, keyErr := s.apiKeyRepo.ListByProviderConfig(ctx, providerConfig.ID)
				if keyErr == nil && len(apiKeys) > 0 {
					// Use the first active API key
					for _, key := range apiKeys {
						if key.IsActive {
							apiKey = key.KeyEncrypted
							break
						}
					}
				}
			}
		}
		// If provider config doesn't exist, apiKey remains empty
		// Some providers (like HuggingFace) will return an error if API key is required
	}

	var discoveredModels []model_discovery.DiscoveredModel
	var warning string
	var searchErr error

	switch req.Msg.ProviderName {
	case "openrouter":
		discoveredModels, searchErr = s.discoveryService.SearchOpenRouterModels(ctx, apiKey, req.Msg.Query, int(req.Msg.Limit))
	case "huggingface":
		discoveredModels, searchErr = s.discoveryService.SearchHuggingFaceModels(ctx, apiKey, req.Msg.Query, int(req.Msg.Limit))
	case "ollama":
		// For Ollama, determine the base URL from multiple sources (priority order):
		// 1. Custom base URL from request (highest priority - from UI form)
		// 2. Custom base URL from saved provider config
		// 3. Default localhost
		baseURL := "http://localhost:11434"

		// Check if custom base URL was provided in the request
		if req.Msg.CustomBaseUrl != "" {
			baseURL = req.Msg.CustomBaseUrl
		} else {
			// Fall back to saved provider configuration
			providerConfig, configErr := s.providerRepo.GetForOrg(ctx, tenantID, req.Msg.ProviderName)
			if configErr == nil && providerConfig != nil && providerConfig.CustomBaseURL != nil {
				baseURL = *providerConfig.CustomBaseURL
			}
		}

		// For Ollama Cloud (https://ollama.com), API key is required
		// For local Ollama, API key is optional and not used
		result, ollamaErr := s.discoveryService.ListOllamaModels(ctx, baseURL, apiKey, req.Msg.Query)
		if ollamaErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to search models: %w", ollamaErr))
		}
		discoveredModels = result.Models
		warning = result.Warning
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider %s does not support model discovery", req.Msg.ProviderName))
	}

	if searchErr != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to search models: %w", searchErr))
	}

	// Convert to protobuf models
	pbModels := make([]*providerspb.DiscoveredModel, len(discoveredModels))
	for i, model := range discoveredModels {
		pbModels[i] = &providerspb.DiscoveredModel{
			Id:            model.ID,
			Name:          model.Name,
			DisplayName:   model.DisplayName,
			Provider:      model.Provider,
			Metadata:      model.Metadata,
			Description:   model.Description,
			Capabilities:  model.Capabilities,
			ContextLength: model.ContextLength,
			PricingInfo:   model.PricingInfo,
		}
	}

	return connect.NewResponse(&providerspb.SearchModelsResponse{
		Models:       pbModels,
		TotalCount:   int32(len(pbModels)),
		ProviderName: req.Msg.ProviderName,
		Warning:      warning,
	}), nil
}

// AddCustomModel adds a custom model to the database
func (s *Server) AddCustomModel(ctx context.Context, req *connect.Request[providerspb.AddCustomModelRequest]) (*connect.Response[providerspb.AddCustomModelResponse], error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.ProviderName == "" || req.Msg.ModelName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider_name and model_name are required"))
	}

	// Check if model already exists
	exists, err := s.customModelsRepo.Exists(ctx, tenantID, req.Msg.ProviderName, req.Msg.ModelName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to check if model exists: %w", err))
	}
	if exists {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("model %s already exists for provider %s", req.Msg.ModelName, req.Msg.ProviderName))
	}

	// Create custom model
	model := &custom_models.CustomModel{
		ProviderName:  req.Msg.ProviderName,
		ModelName:     req.Msg.ModelName,
		DisplayName:   req.Msg.DisplayName,
		ModelMetadata: convertMetadataToInterface(req.Msg.ModelMetadata),
		Source:        custom_models.ModelSource(req.Msg.Source),
		IsActive:      true,
	}

	if err := s.customModelsRepo.Create(ctx, tenantID, model); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create custom model: %w", err))
	}
	model_discovery.InvalidateCustomModels(tenantID)

	return connect.NewResponse(&providerspb.AddCustomModelResponse{
		Model: convertToProtoModel(model),
	}), nil
}

// ListCustomModels lists all custom models, optionally filtered by provider
func (s *Server) ListCustomModels(ctx context.Context, req *connect.Request[providerspb.ListCustomModelsRequest]) (*connect.Response[providerspb.ListCustomModelsResponse], error) {
	tenantID, tenantErr := tenantIDFromContext(ctx)
	if tenantErr != nil {
		return nil, tenantErr
	}
	var models []*custom_models.CustomModel
	var err error

	if req.Msg.ProviderName != "" {
		if req.Msg.ActiveOnly {
			models, err = s.customModelsRepo.ListActiveByProvider(ctx, tenantID, req.Msg.ProviderName)
		} else {
			models, err = s.customModelsRepo.ListByProvider(ctx, tenantID, req.Msg.ProviderName)
		}
	} else {
		models, err = s.customModelsRepo.List(ctx, tenantID)
		// Filter by active if requested
		if req.Msg.ActiveOnly && err == nil {
			activeModels := make([]*custom_models.CustomModel, 0, len(models))
			for _, m := range models {
				if m.IsActive {
					activeModels = append(activeModels, m)
				}
			}
			models = activeModels
		}
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list custom models: %w", err))
	}

	// Convert to protobuf models
	pbModels := make([]*providerspb.CustomModel, len(models))
	for i, model := range models {
		pbModels[i] = convertToProtoModel(model)
	}

	return connect.NewResponse(&providerspb.ListCustomModelsResponse{
		Models:     pbModels,
		TotalCount: int32(len(pbModels)),
	}), nil
}

// DeleteCustomModel removes a custom model
func (s *Server) DeleteCustomModel(ctx context.Context, req *connect.Request[providerspb.DeleteCustomModelRequest]) (*connect.Response[providerspb.DeleteCustomModelResponse], error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id is required"))
	}

	if err := s.customModelsRepo.Delete(ctx, tenantID, req.Msg.Id); err != nil {
		return nil, customModelError("delete", err)
	}
	model_discovery.InvalidateCustomModels(tenantID)

	return connect.NewResponse(&providerspb.DeleteCustomModelResponse{
		Success: true,
		Message: "Custom model deleted successfully",
	}), nil
}

// UpdateCustomModel updates a custom model's metadata
func (s *Server) UpdateCustomModel(ctx context.Context, req *connect.Request[providerspb.UpdateCustomModelRequest]) (*connect.Response[providerspb.UpdateCustomModelResponse], error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id is required"))
	}

	// Get existing model
	model, err := s.customModelsRepo.GetByID(ctx, tenantID, req.Msg.Id)
	if err != nil {
		return nil, customModelError("get", err)
	}

	// Update fields
	if req.Msg.DisplayName != "" {
		model.DisplayName = req.Msg.DisplayName
	}
	if req.Msg.ModelMetadata != nil {
		model.ModelMetadata = convertMetadataToInterface(req.Msg.ModelMetadata)
	}
	model.IsActive = req.Msg.IsActive

	if err := s.customModelsRepo.Update(ctx, tenantID, model); err != nil {
		return nil, customModelError("update", err)
	}
	model_discovery.InvalidateCustomModels(tenantID)

	return connect.NewResponse(&providerspb.UpdateCustomModelResponse{
		Model: convertToProtoModel(model),
	}), nil
}

// GetCustomModel retrieves a specific custom model
func (s *Server) GetCustomModel(ctx context.Context, req *connect.Request[providerspb.GetCustomModelRequest]) (*connect.Response[providerspb.GetCustomModelResponse], error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id is required"))
	}

	model, err := s.customModelsRepo.GetByID(ctx, tenantID, req.Msg.Id)
	if err != nil {
		return nil, customModelError("get", err)
	}

	return connect.NewResponse(&providerspb.GetCustomModelResponse{
		Model: convertToProtoModel(model),
	}), nil
}

// Helper functions

func convertToProtoModel(model *custom_models.CustomModel) *providerspb.CustomModel {
	return &providerspb.CustomModel{
		Id:            model.ID,
		ProviderName:  model.ProviderName,
		ModelName:     model.ModelName,
		DisplayName:   model.DisplayName,
		ModelMetadata: convertMetadataToString(model.ModelMetadata),
		Source:        string(model.Source),
		IsActive:      model.IsActive,
		CreatedAt:     model.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:     model.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func convertMetadataToString(metadata map[string]interface{}) map[string]string {
	result := make(map[string]string, len(metadata))
	for k, v := range metadata {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

func convertMetadataToInterface(metadata map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(metadata))
	for k, v := range metadata {
		result[k] = v
	}
	return result
}

// CreateServerWithContext initializes the ModelDiscoveryService with dependencies from context
func CreateServerWithContext(ctx context.Context) (*Server, error) {
	// Get database from context
	dbAny := ctx.Value(contextkeys.PrimaryDB)
	if dbAny == nil {
		return nil, fmt.Errorf("primary database not found in context")
	}
	db, ok := dbAny.(*sqlx.DB)
	if !ok {
		return nil, fmt.Errorf("invalid database type in context")
	}

	// Initialize repositories
	customModelsRepo := custom_models.NewRepository(db, enterprise.ManagedGateway())
	providerRepo := provider_config.NewRepository(db)
	apiKeyRepo := provider_api_keys.NewPostgresRepository(db)

	// Initialize model discovery service
	discoveryService := model_discovery.NewService()

	return NewServer(discoveryService, customModelsRepo, providerRepo, apiKeyRepo), nil
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(opts ...connect.HandlerOption) (string, http.Handler) {
	// The service\'s own interceptors run first, then whatever the caller
	// supplied. connect accumulates interceptors across options, so this
	// preserves the previous ordering while letting the caller pass other
	// handler options through untouched.
	all := append([]connect.HandlerOption{connect.WithInterceptors(s.serviceInterceptors...)}, opts...)
	return modeldiscoveryconnect.NewModelDiscoveryServiceHandler(s, all...)
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return providerspb.File_everstack_providers_model_discovery_proto
}

func (s *Server) AppName() string {
	return modeldiscoveryconnect.ModelDiscoveryServiceName
}

func (s *Server) MethodPrefix() string {
	return "/" + modeldiscoveryconnect.ModelDiscoveryServiceName + "/"
}

// customModelError maps a repository error to a Connect error. A model that
// is missing, or that belongs to another tenant, is NotFound with a fixed
// message; anything else is Internal.
func customModelError(action string, err error) error {
	if errors.Is(err, custom_models.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, errors.New("custom model not found"))
	}
	return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to %s custom model: %w", action, err))
}
