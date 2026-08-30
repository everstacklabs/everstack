package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/api/grpc/mferrors"
	providerHandler "github.com/everstacklabs/everstack/internal/commands/handlers/provider"
	providerCmd "github.com/everstacklabs/everstack/internal/commands/provider"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/events"
	providerEvents "github.com/everstacklabs/everstack/internal/events/provider"
	providerSubscriber "github.com/everstacklabs/everstack/internal/events/subscribers/provider"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/config_sync"
	"github.com/everstacklabs/everstack/internal/services/provider_catalog"
	provider_config_svc "github.com/everstacklabs/everstack/internal/services/provider_config"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1/providersconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Ensure Server implements ConnectServer contract
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

// Server implements the ProvidersService gRPC server
type Server struct {
	catalog         *provider_catalog.Service
	configService   *provider_config_svc.Service
	syncWorker      *config_sync.Worker
	db              *sqlx.DB // Added for API key repository access
	providerHandler *providerHandler.Handler
	configPath      string     // Path to YAML config file
	reloadMu        sync.Mutex // Prevents concurrent reload operations
	eventBus        events.Bus // Shared CQRS-backed bus; the gateway's refresh subscriber lives on the other end
}

// NewServerWithDB creates a new ProvidersService server with database access
func NewServerWithDB(
	catalog *provider_catalog.Service,
	configService *provider_config_svc.Service,
	syncWorker *config_sync.Worker,
	db *sqlx.DB,
	providerHandler *providerHandler.Handler,
	configPath string,
	eventBus events.Bus,
) *Server {
	return &Server{
		catalog:         catalog,
		configService:   configService,
		syncWorker:      syncWorker,
		db:              db,
		providerHandler: providerHandler,
		configPath:      configPath,
		eventBus:        eventBus,
	}
}

// publishBus returns the bus reload/save operations must publish on. The
// gateway's provider-refresh handler is subscribed to the shared CQRS bus,
// so publishing anywhere else means the router never picks up the change.
func (s *Server) publishBus() events.Bus {
	if s.eventBus != nil {
		return s.eventBus
	}
	return events.NewInMemoryBus()
}

// GetCatalog returns the provider catalog service
func (s *Server) GetCatalog() *provider_catalog.Service {
	return s.catalog
}

// CreateServerWithContext initializes the ProvidersService with dependencies from context
func CreateServerWithContext(ctx context.Context, configPath string) (*Server, error) {
	// Get database from context
	dbAny := ctx.Value(contextkeys.PrimaryDB)
	if dbAny == nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("primary database not found in context"))
	}
	db, ok := dbAny.(*sqlx.DB)
	if !ok {
		return nil, mferrors.MFToConnectError(fmt.Errorf("invalid database type in context"))
	}

	// Initialize provider catalog service (loads defaults)
	catalog, err := provider_catalog.New()
	if err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to initialize provider catalog: %w", err))
	}

	// Connect to catalog sync service if available for dynamic provider updates
	if catalogSyncAny := ctx.Value(contextkeys.CatalogSync); catalogSyncAny != nil {
		if catalogSync, ok := catalogSyncAny.(provider_catalog.CatalogSyncService); ok {
			catalog.SetCatalogSync(catalogSync)
		}
	}

	// Initialize repository
	repo := provider_config.NewRepository(db)

	// Initialize config service
	configService := provider_config_svc.New(catalog, repo)

	// Get CQRS event bus and writer from context (shared with gateway)
	var eventBus events.Bus
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err == nil && sys != nil && sys.EventBus != nil {
		// Wrap database.InMemoryEventBus to match events.Bus interface
		// Include Writer so events are persisted to the database
		eventBus = &eventBusAdapter{bus: sys.EventBus, writer: sys.Writer}
		logger.Debug("providers: using CQRS event bus and writer for provider events")
	} else {
		// Fallback to local event bus (for testing/development)
		eventBus = events.NewInMemoryBus()
		logger.Warn("providers: CQRS event bus not available, using local event bus")
	}

	// Initialize reconciler and run startup reconciliation
	reconciler := config_sync.NewReconciler(repo, configPath, eventBus)
	if err := reconciler.ReconcileOnStartup(ctx); err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to reconcile YAML and database: %w", err))
	}

	// Initialize and start sync worker
	syncWorker := config_sync.NewWorker(repo, configPath)
	syncWorker.Start()

	// Initialize provider command handler
	apiKeyRepo := provider_api_keys.NewPostgresRepository(db)
	handler := providerHandler.NewHandler(repo, apiKeyRepo, eventBus)

	// Subscribe to events for YAML sync
	subscriber := providerSubscriber.NewSyncSubscriber(syncWorker)
	subscriber.Subscribe(eventBus)

	return NewServerWithDB(catalog, configService, syncWorker, db, handler, configPath, eventBus), nil
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return providersconnect.NewProvidersServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return providerspb.File_everstack_providers_providers_service_proto
}

func (s *Server) AppName() string {
	return providersconnect.ProvidersServiceName
}

func (s *Server) MethodPrefix() string {
	return providersconnect.ProvidersServiceName
}

// RegisterGateway wires REST endpoints under /v1 via grpc-gateway
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return providerspb.RegisterProvidersServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// API Key Management Methods

// AddProviderAPIKey adds a new API key to a provider configuration
func (s *Server) AddProviderAPIKey(ctx context.Context, req *connect.Request[providerspb.AddProviderAPIKeyRequest]) (*connect.Response[providerspb.AddProviderAPIKeyResponse], error) {
	msg := req.Msg

	// Create command
	cmd := providerCmd.AddProviderAPIKeyCommand{
		ProviderConfigID: msg.ProviderConfigId,
		KeyName:          msg.KeyName,
		APIKey:           msg.ApiKey,
		Weight:           int(msg.Weight),
		UserID:           "", // TODO: Extract from context
		TraceID:          "", // TODO: Extract from context
		Timestamp:        time.Now(),
	}

	// Execute command via handler
	newKey, err := s.providerHandler.HandleAddProviderAPIKey(ctx, cmd)
	if err != nil {
		logger.Errorf("providers_api: failed to add API key: %v", err)
		if strings.Contains(err.Error(), "already exists") {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logger.Infof("providers_api: successfully added API key %s for provider %s", newKey.ID, msg.ProviderConfigId)

	// Return created key
	return connect.NewResponse(&providerspb.AddProviderAPIKeyResponse{
		Key: &providerspb.ProviderAPIKey{
			Id:               newKey.ID,
			ProviderConfigId: newKey.ProviderConfigID,
			KeyName:          newKey.KeyName,
			KeyMasked:        maskAPIKey(newKey.KeyEncrypted),
			Weight:           int32(newKey.Weight),
			IsActive:         newKey.IsActive,
			CreatedAt:        newKey.CreatedAt.Format(time.RFC3339),
			UpdatedAt:        newKey.UpdatedAt.Format(time.RFC3339),
		},
	}), nil
}

// UpdateAPIKeyWeight updates the weight of an API key for load balancing
func (s *Server) UpdateAPIKeyWeight(ctx context.Context, req *connect.Request[providerspb.UpdateAPIKeyWeightRequest]) (*connect.Response[providerspb.UpdateAPIKeyWeightResponse], error) {
	msg := req.Msg

	// Create command
	cmd := providerCmd.UpdateAPIKeyWeightCommand{
		KeyID:     msg.KeyId,
		Weight:    int(msg.Weight),
		UserID:    "", // TODO: Extract from context
		TraceID:   "", // TODO: Extract from context
		Timestamp: time.Now(),
	}

	// Execute command via handler
	updatedKey, err := s.providerHandler.HandleUpdateAPIKeyWeight(ctx, cmd)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Return updated key
	return connect.NewResponse(&providerspb.UpdateAPIKeyWeightResponse{
		Key: &providerspb.ProviderAPIKey{
			Id:               updatedKey.ID,
			ProviderConfigId: updatedKey.ProviderConfigID,
			KeyName:          updatedKey.KeyName,
			KeyMasked:        maskAPIKey(updatedKey.KeyEncrypted),
			Weight:           int32(updatedKey.Weight),
			IsActive:         updatedKey.IsActive,
			CreatedAt:        updatedKey.CreatedAt.Format(time.RFC3339),
			UpdatedAt:        updatedKey.UpdatedAt.Format(time.RFC3339),
		},
	}), nil
}

// ToggleAPIKey activates or deactivates an API key
func (s *Server) ToggleAPIKey(ctx context.Context, req *connect.Request[providerspb.ToggleAPIKeyRequest]) (*connect.Response[providerspb.ToggleAPIKeyResponse], error) {
	msg := req.Msg

	// Create command
	cmd := providerCmd.ToggleAPIKeyCommand{
		KeyID:     msg.KeyId,
		IsActive:  msg.IsActive,
		UserID:    "", // TODO: Extract from context
		TraceID:   "", // TODO: Extract from context
		Timestamp: time.Now(),
	}

	// Execute command via handler
	toggledKey, err := s.providerHandler.HandleToggleAPIKey(ctx, cmd)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Return updated key
	return connect.NewResponse(&providerspb.ToggleAPIKeyResponse{
		Key: &providerspb.ProviderAPIKey{
			Id:               toggledKey.ID,
			ProviderConfigId: toggledKey.ProviderConfigID,
			KeyName:          toggledKey.KeyName,
			KeyMasked:        maskAPIKey(toggledKey.KeyEncrypted),
			Weight:           int32(toggledKey.Weight),
			IsActive:         toggledKey.IsActive,
			CreatedAt:        toggledKey.CreatedAt.Format(time.RFC3339),
			UpdatedAt:        toggledKey.UpdatedAt.Format(time.RFC3339),
		},
	}), nil
}

// DeleteProviderAPIKey deletes an API key
func (s *Server) DeleteProviderAPIKey(ctx context.Context, req *connect.Request[providerspb.DeleteProviderAPIKeyRequest]) (*connect.Response[providerspb.DeleteProviderAPIKeyResponse], error) {
	msg := req.Msg

	// Create command
	cmd := providerCmd.DeleteProviderAPIKeyCommand{
		KeyID:     msg.KeyId,
		UserID:    "", // TODO: Extract from context
		TraceID:   "", // TODO: Extract from context
		Timestamp: time.Now(),
	}

	// Execute command via handler
	err := s.providerHandler.HandleDeleteProviderAPIKey(ctx, cmd)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&providerspb.DeleteProviderAPIKeyResponse{
		Success: true,
	}), nil
}

// ListProviderAPIKeys lists all API keys for a provider configuration
func (s *Server) ListProviderAPIKeys(ctx context.Context, req *connect.Request[providerspb.ListProviderAPIKeysRequest]) (*connect.Response[providerspb.ListProviderAPIKeysResponse], error) {
	msg := req.Msg

	// Validate request
	if msg.ProviderConfigId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider_config_id is required"))
	}

	// Create repository
	repo := provider_api_keys.NewPostgresRepository(s.db)

	// List keys
	keys, err := repo.ListByProviderConfig(ctx, msg.ProviderConfigId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list API keys: %w", err))
	}

	// Convert to protobuf
	pbKeys := make([]*providerspb.ProviderAPIKey, len(keys))
	for i, key := range keys {
		pbKeys[i] = &providerspb.ProviderAPIKey{
			Id:               key.ID,
			ProviderConfigId: key.ProviderConfigID,
			KeyName:          key.KeyName,
			KeyMasked:        maskAPIKey(key.KeyEncrypted),
			Weight:           int32(key.Weight),
			IsActive:         key.IsActive,
			CreatedAt:        key.CreatedAt.Format(time.RFC3339),
			UpdatedAt:        key.UpdatedAt.Format(time.RFC3339),
			Source:           key.Source,
		}
	}

	return connect.NewResponse(&providerspb.ListProviderAPIKeysResponse{
		Keys: pbKeys,
	}), nil
}

// GetSyncStatus checks if YAML and DB are in sync
func (s *Server) GetSyncStatus(ctx context.Context, req *connect.Request[providerspb.GetSyncStatusRequest]) (*connect.Response[providerspb.GetSyncStatusResponse], error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	yamlStat, err := os.Stat(s.configPath)
	if err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to stat YAML file: %w", err))
	}

	configs, err := s.configService.ListConfigurationsForOrg(ctx, tenantID)
	if err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to list configurations: %w", err))
	}

	// Find newest DB sync time
	var newestSync time.Time
	for _, cfg := range configs {
		if cfg.SyncedToYAMLAt != nil && cfg.SyncedToYAMLAt.After(newestSync) {
			newestSync = *cfg.SyncedToYAMLAt
		}
	}

	// Determine if in sync (YAML is not newer than newest DB sync)
	inSync := !yamlStat.ModTime().After(newestSync)

	return connect.NewResponse(&providerspb.GetSyncStatusResponse{
		InSync:       inSync,
		YamlModTime:  yamlStat.ModTime().Format(time.RFC3339),
		LastSyncTime: newestSync.Format(time.RFC3339),
	}), nil
}

// GetConfigYAML gets the current YAML config content
func (s *Server) GetConfigYAML(ctx context.Context, req *connect.Request[providerspb.GetConfigYAMLRequest]) (*connect.Response[providerspb.GetConfigYAMLResponse], error) {
	// Read YAML file content
	yamlContent, err := os.ReadFile(s.configPath)
	if err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to read YAML file: %w", err))
	}

	return connect.NewResponse(&providerspb.GetConfigYAMLResponse{
		YamlContent: string(yamlContent),
	}), nil
}

// SaveConfigYAML saves YAML config and syncs to database
func (s *Server) SaveConfigYAML(ctx context.Context, req *connect.Request[providerspb.SaveConfigYAMLRequest]) (*connect.Response[providerspb.SaveConfigYAMLResponse], error) {
	// Write YAML content to file
	if err := os.WriteFile(s.configPath, []byte(req.Msg.YamlContent), 0644); err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to write YAML file: %w", err))
	}

	// Create reconciler to sync from YAML to DB. Publish on the shared bus:
	// a locally constructed bus has no subscribers, so the gateway would keep
	// serving its stale provider bundle after the save.
	repo := provider_config.NewRepository(s.db)
	eventBus := s.publishBus()

	reconciler := config_sync.NewReconciler(repo, s.configPath, eventBus)

	// Force sync from YAML to database
	if err := reconciler.ForceSyncFromYAML(ctx); err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to sync YAML to database: %w", err))
	}

	// Tell the gateway to rebuild its provider bundle from the freshly synced DB.
	userID := contextkeys.ExtractUserID(ctx, "")
	if userID == "anonymous" {
		userID = "system"
	}
	if err := eventBus.Publish(ctx, providerEvents.ConfigReloadedEvent{
		UserID:    userID,
		Timestamp: time.Now(),
	}); err != nil {
		logger.Warnf("failed to publish ConfigReloadedEvent after SaveConfigYAML: %v", err)
	}

	return connect.NewResponse(&providerspb.SaveConfigYAMLResponse{
		Success: true,
		Message: "Config saved and synced to database successfully",
	}), nil
}

// ReloadConfig syncs YAML to DB and triggers gateway reload
func (s *Server) ReloadConfig(ctx context.Context, req *connect.Request[providerspb.ReloadConfigRequest]) (*connect.Response[providerspb.ReloadConfigResponse], error) {
	// Lock mutex to prevent concurrent reloads
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	// Read YAML file content to ensure it exists and is accessible
	_, err := os.ReadFile(s.configPath)
	if err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to read YAML file: %w", err))
	}

	// Create reconciler to sync from YAML to DB. Publish on the shared bus:
	// the gateway's refresh handler subscribes to provider.config.reloaded on
	// the CQRS bus, so a locally constructed bus makes this RPC a silent no-op
	// for routing — the "reload" would never reach the router.
	repo := provider_config.NewRepository(s.db)
	eventBus := s.publishBus()

	reconciler := config_sync.NewReconciler(repo, s.configPath, eventBus)

	// Force sync from YAML to database
	if err := reconciler.ForceSyncFromYAML(ctx); err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to sync YAML to database: %w", err))
	}

	// Count synced providers for response
	configs, err := repo.List(ctx)
	if err != nil {
		// Don't fail the request if we can't count providers
		configs = []*provider_config.Configuration{}
	}

	// Publish config reloaded event to trigger gateway refresh
	// Extract user ID from context (falls back to "system" for system operations)
	userID := contextkeys.ExtractUserID(ctx, "")
	if userID == "anonymous" {
		userID = "system" // Use "system" for anonymous system operations
	}
	event := providerEvents.ConfigReloadedEvent{
		ProvidersSynced: len(configs),
		UserID:          userID,
		TraceID:         "", // TODO: Get from context when tracing is available
		Timestamp:       time.Now(),
	}

	// Publish event using the existing event bus
	if err := eventBus.Publish(ctx, event); err != nil {
		// Log error but don't fail the request - gateway will eventually sync
		logger.Warnf("failed to publish ConfigReloadedEvent: %v", err)
	}

	return connect.NewResponse(&providerspb.ReloadConfigResponse{
		Success:         true,
		Message:         "Config reloaded and gateway refreshed successfully",
		ProvidersSynced: int32(len(configs)),
	}), nil
}

// maskAPIKey masks an API key for display (shows only first 4 and last 4 characters)
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

// eventBusAdapter adapts database.InMemoryEventBus to events.Bus interface
// and persists events to the database via the Writer
type eventBusAdapter struct {
	bus    *database.InMemoryEventBus
	writer database.Writer
}

func (a *eventBusAdapter) Publish(ctx context.Context, event interface{}) error {
	// Convert to database.Event with payload
	var dbEvent database.Event
	now := time.Now().Unix()

	// Helper to serialize event to JSON payload
	toPayload := func(e interface{}) []byte {
		payload, err := json.Marshal(e)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to serialize event payload")
			return []byte("{}")
		}
		return payload
	}

	switch e := event.(type) {
	case providerEvents.ProviderConfiguredEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	case providerEvents.ProviderAPIKeyAddedEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	case providerEvents.ProviderAPIKeyToggledEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	case providerEvents.ProviderAPIKeyWeightUpdatedEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	case providerEvents.ProviderAPIKeyDeletedEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	case providerEvents.ProviderToggledEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	case providerEvents.ProviderDeletedEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	case providerEvents.ConfigReloadedEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	case providerEvents.ProviderConfigAPIKeySyncedEvent:
		dbEvent = database.Event{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Type:      e.Event(),
			Stream:    "providers",
			Payload:   toPayload(e),
			CreatedAt: now,
		}
	default:
		return fmt.Errorf("unknown event type: %T", event)
	}

	// Step 1: Persist to database (if writer is available)
	if a.writer != nil {
		if err := a.writer.Append(ctx, dbEvent); err != nil {
			logger.WithFields("error", err.Error(), "event_type", dbEvent.Type).Error("failed to persist event to database")
			return fmt.Errorf("failed to persist event: %w", err)
		}
	}

	// Step 2: Publish to in-memory event bus (for subscribers)
	return a.bus.Publish(ctx, dbEvent)
}

func (a *eventBusAdapter) Subscribe(eventType string, handler func(ctx context.Context, event interface{}) error) {
	// This method is not used by the command handler (only Publish is used)
	// but required by events.Bus interface
	logger.Debugf("eventBusAdapter.Subscribe called for eventType: %s (not implemented)", eventType)
}
