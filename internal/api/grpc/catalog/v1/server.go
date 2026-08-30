package v1

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/api/grpc/mferrors"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/catalog_sync"
	catalogv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/catalog/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/catalog/v1/catalogconnect"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Ensure Server implements ConnectServer contract
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

// Server implements the catalog gRPC service
type Server struct {
	catalogv1.UnimplementedCatalogServiceServer
	syncService *catalog_sync.Service
}

// NewServer creates a new catalog server
func NewServer(syncService *catalog_sync.Service) *Server {
	return &Server{
		syncService: syncService,
	}
}

// GetSyncService returns the catalog sync service
func (s *Server) GetSyncService() *catalog_sync.Service {
	return s.syncService
}

// CreateServerWithContext initializes the CatalogService with dependencies from context
func CreateServerWithContext(ctx context.Context) (*Server, error) {
	// Get server config from context
	serverCfgAny := ctx.Value(contextkeys.ServerConfig)
	if serverCfgAny == nil {
		return nil, fmt.Errorf("server config not found in context")
	}
	serverCfg, ok := serverCfgAny.(*validator.ServerConfig)
	if !ok {
		return nil, fmt.Errorf("invalid server config type in context")
	}

	// Load embedded models and providers defaults directly
	modelsData, providersData, err := validator.LoadModelsAndProvidersDefaults()
	if err != nil {
		return nil, fmt.Errorf("failed to load defaults: %w", err)
	}

	// Parse models defaults
	embeddedModels, err := validator.ParseModelsDefaults(modelsData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse models defaults: %w", err)
	}

	// Parse providers defaults
	var embeddedProviders map[string]interface{}
	if len(providersData) > 0 {
		if err := validator.LoadYAMLIntoStruct(providersData, &embeddedProviders); err != nil {
			return nil, fmt.Errorf("failed to parse providers defaults: %w", err)
		}
	}

	// Create catalog sync config from server config
	syncConfig := catalog_sync.DefaultConfig()

	// Override with server catalog config
	catalogCfg := serverCfg.Catalog
	syncConfig.Source = catalogCfg.Source
	if catalogCfg.Source == "remote" && catalogCfg.RemoteURL != "" {
		syncConfig.RemoteURL = catalogCfg.RemoteURL
	}
	if catalogCfg.Source == "local" && catalogCfg.LocalPath != "" {
		syncConfig.LocalPath = catalogCfg.LocalPath
	}
	syncConfig.SyncInterval = configuredSyncInterval(catalogCfg.SyncInterval, syncConfig.SyncInterval)
	syncConfig.EnableAutoSync = catalogCfg.EnableAutoSync
	syncConfig.Channel = catalogCfg.Channel
	syncConfig.PublicKey = catalogCfg.PublicKey
	syncConfig.PublicKeys = catalogCfg.PublicKeys
	syncConfig.RequireSignature = catalogCfg.RequireSignature

	// Initialize catalog sync service
	syncService := catalog_sync.NewService(syncConfig, embeddedModels, embeddedProviders)

	// Start the sync service
	if err = syncService.Start(ctx); err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to start catalog sync service: %w", err))
	}

	return NewServer(syncService), nil
}

func configuredSyncInterval(value string, fallback time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval <= 0 {
		logger.Warnf("catalog_sync: invalid sync interval %q, using %s", value, fallback)
		return fallback
	}
	return interval
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return catalogconnect.NewCatalogServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return catalogv1.File_everstack_catalog_v1_catalog_service_proto
}

func (s *Server) AppName() string {
	return catalogconnect.CatalogServiceName
}

func (s *Server) MethodPrefix() string {
	return catalogconnect.CatalogServiceName
}

// GetCatalogStatus returns current catalog status
func (s *Server) GetCatalogStatus(ctx context.Context, req *connect.Request[catalogv1.GetCatalogStatusRequest]) (*connect.Response[catalogv1.CatalogStatus], error) {
	currentVersion, remoteVersion, hasUpdates, newModelsCount, newProvidersCount, lastSync := s.syncService.GetStatus()

	// Get sync URL/path from service config
	syncSource := s.syncService.GetSyncSource()

	// Format timestamps, handle zero time
	lastSyncStr := ""
	lastCheckStr := ""
	if !lastSync.IsZero() {
		lastSyncStr = lastSync.Format(time.RFC3339)
		lastCheckStr = lastSync.Format(time.RFC3339)
	}

	status := &catalogv1.CatalogStatus{
		CurrentVersion:    currentVersion,
		RemoteVersion:     remoteVersion,
		LastCheck:         lastCheckStr,
		SyncEnabled:       s.syncService.IsAutoSyncEnabled(),
		SyncUrl:           syncSource,
		NewModelsCount:    int32(newModelsCount),
		NewProvidersCount: int32(newProvidersCount),
		LastSync:          lastSyncStr,
		HasUpdates:        hasUpdates,
	}

	return connect.NewResponse(status), nil
}

// GetChangelog returns changelog for catalog updates
func (s *Server) GetChangelog(ctx context.Context, req *connect.Request[catalogv1.GetChangelogRequest]) (*connect.Response[catalogv1.Changelog], error) {
	changelog, err := s.syncService.GetChangelog()
	if err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to load catalog changelog: %w", err))
	}

	return connect.NewResponse(buildChangelogResponse(changelog, req.Msg.FromVersion)), nil
}

// GetNewModels returns list of new models and providers
func (s *Server) GetNewModels(ctx context.Context, req *connect.Request[catalogv1.GetNewModelsRequest]) (*connect.Response[catalogv1.NewModels], error) {
	changelog, err := s.syncService.GetChangelog()
	if err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to load catalog changelog: %w", err))
	}

	return connect.NewResponse(buildNewModelsResponse(changelog, req.Msg.Provider)), nil
}

func buildChangelogResponse(changelog *catalog_sync.Changelog, fromVersion string) *catalogv1.Changelog {
	response := &catalogv1.Changelog{Entries: []*catalogv1.ChangelogEntry{}}
	if changelog == nil {
		return response
	}

	for _, version := range changelog.Versions {
		if fromVersion != "" && version.Version == fromVersion {
			break
		}

		entry := &catalogv1.ChangelogEntry{
			Version:        version.Version,
			Date:           version.Date,
			Description:    version.Description,
			PricingChanges: append([]string(nil), version.Changes.PricingChanges...),
		}
		for _, change := range version.Changes.NewModels {
			entry.NewModels = append(entry.NewModels, formatModelChange(change, false))
		}
		for _, change := range version.Changes.NewProviders {
			entry.NewProviders = append(entry.NewProviders, displayProviderName(change.Name))
		}
		for _, change := range version.Changes.UpdatedModels {
			entry.UpdatedModels = append(entry.UpdatedModels, formatModelChange(change, true))
		}
		for _, change := range version.Changes.DeprecatedModels {
			entry.DeprecatedModels = append(entry.DeprecatedModels, formatModelChange(change, true))
		}
		response.Entries = append(response.Entries, entry)
	}

	return response
}

func buildNewModelsResponse(changelog *catalog_sync.Changelog, providerFilter string) *catalogv1.NewModels {
	response := &catalogv1.NewModels{Models: []*catalogv1.NewModel{}}
	if changelog == nil || len(changelog.Versions) == 0 {
		return response
	}

	for _, change := range changelog.Versions[0].Changes.NewModels {
		if providerFilter != "" && !strings.EqualFold(change.Provider, providerFilter) {
			continue
		}

		displayName := change.DisplayName
		if displayName == "" {
			displayName = change.Model
		}
		response.Models = append(response.Models, &catalogv1.NewModel{
			Provider:    change.Provider,
			Name:        change.Model,
			DisplayName: displayName,
		})
	}
	response.TotalCount = int32(len(response.Models))
	return response
}

func formatModelChange(change catalog_sync.ChangelogModelChange, includeDescription bool) string {
	modelName := change.DisplayName
	if modelName == "" {
		modelName = change.Model
	}

	label := modelName
	if change.Provider != "" {
		label = displayProviderName(change.Provider) + " · " + modelName
	}
	if includeDescription && change.Description != "" {
		label += " — " + change.Description
	}
	return label
}

func displayProviderName(provider string) string {
	switch strings.ToLower(provider) {
	case "aws-bedrock":
		return "AWS Bedrock"
	case "azure-openai":
		return "Azure OpenAI"
	case "nvidia-nim":
		return "NVIDIA NIM"
	case "xai":
		return "xAI"
	}

	words := strings.FieldsFunc(provider, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for index, word := range words {
		if word == "" {
			continue
		}
		words[index] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

// TriggerSync triggers manual catalog sync
func (s *Server) TriggerSync(ctx context.Context, req *connect.Request[catalogv1.TriggerSyncRequest]) (*connect.Response[catalogv1.SyncStatus], error) {
	logger.Debug("catalog_api: received sync trigger request")

	err := s.syncService.TriggerSync(ctx)
	if err != nil {
		logger.Errorf("catalog_api: sync failed: %v", err)
		return connect.NewResponse(&catalogv1.SyncStatus{
			Success: false,
			Message: mferrors.MFToConnectError(err).Error(),
		}), nil
	}

	_, remoteVersion, _, newModelsCount, newProvidersCount, _ := s.syncService.GetStatus()

	logger.Debugf("catalog_api: sync completed - version: %s, new models: %d, new providers: %d",
		remoteVersion, newModelsCount, newProvidersCount)

	return connect.NewResponse(&catalogv1.SyncStatus{
		Success:           true,
		Message:           "Sync completed successfully",
		Version:           remoteVersion,
		NewModelsCount:    int32(newModelsCount),
		NewProvidersCount: int32(newProvidersCount),
	}), nil
}
