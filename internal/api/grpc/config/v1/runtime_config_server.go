package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	rtconfig "github.com/everstacklabs/everstack/internal/domain/runtime_config"
	"github.com/everstacklabs/everstack/internal/events"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	configpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/config/v1"
)

// readonlySections returns sections that the API will not write to.
// Only rate_limit, features, and cache are wired to live behaviour;
// the rest are deferred to post-release work (see
// docs/audits/runtime-config.md). Rejecting writes here keeps the DB
// from filling up with unread overrides via the YAML editor or
// direct gRPC clients.
func readonlySections() map[string]bool {
	return map[string]bool{
		string(rtconfig.SectionLoadBalancer): true,
		string(rtconfig.SectionTelemetry):    true,
		string(rtconfig.SectionCORS):         true,
	}
}

// RuntimeConfigServer handles runtime configuration operations
type RuntimeConfigServer struct {
	repo     *rtconfig.Repository
	eventBus events.Bus
}

// NewRuntimeConfigServer creates a new runtime config server
func NewRuntimeConfigServer(db *sqlx.DB) *RuntimeConfigServer {
	return &RuntimeConfigServer{
		repo:     rtconfig.NewRepository(db),
		eventBus: events.NewInMemoryBus(),
	}
}

// NewRuntimeConfigServerWithEventBus creates a new runtime config server with a custom event bus
func NewRuntimeConfigServerWithEventBus(db *sqlx.DB, eventBus events.Bus) *RuntimeConfigServer {
	return &RuntimeConfigServer{
		repo:     rtconfig.NewRepository(db),
		eventBus: eventBus,
	}
}

// SetEventBus sets the event bus for the server
func (s *RuntimeConfigServer) SetEventBus(bus events.Bus) {
	s.eventBus = bus
}

// publishConfigUpdated publishes a config updated event
func (s *RuntimeConfigServer) publishConfigUpdated(ctx context.Context, tenantID, section string, config json.RawMessage, version int, updatedBy string) {
	if s.eventBus == nil {
		return
	}

	event := rtconfig.RuntimeConfigUpdatedEvent{
		TenantID:  tenantID,
		Section:   section,
		Config:    config,
		Version:   version,
		UpdatedBy: updatedBy,
		Timestamp: time.Now(),
	}

	if err := s.eventBus.Publish(ctx, event); err != nil {
		logger.Warnf("Failed to publish runtime config updated event: %v", err)
	}
}

// publishConfigReset publishes a config reset event
func (s *RuntimeConfigServer) publishConfigReset(ctx context.Context, tenantID, section string, version int, resetBy string) {
	if s.eventBus == nil {
		return
	}

	event := rtconfig.RuntimeConfigResetEvent{
		TenantID:  tenantID,
		Section:   section,
		Version:   version,
		ResetBy:   resetBy,
		Timestamp: time.Now(),
	}

	if err := s.eventBus.Publish(ctx, event); err != nil {
		logger.Warnf("Failed to publish runtime config reset event: %v", err)
	}
}

// requireTenant returns the verified tenant id from context, failing closed with
// Unauthenticated when none is present. Per audit finding H6 the tenant boundary
// is never derived from a client header, so a missing tenant here means the
// request was not tenant-authenticated and must be rejected rather than silently
// reading or writing the empty-tenant ("") runtime-config row.
func requireTenant(ctx context.Context) (string, error) {
	tenantID := contextkeys.ExtractTenantID(ctx)
	if tenantID == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("no tenant in context"))
	}
	return tenantID, nil
}

// GetRuntimeConfig returns the full runtime configuration
func (s *RuntimeConfigServer) GetRuntimeConfig(
	ctx context.Context,
	req *connect.Request[configpb.GetRuntimeConfigRequest],
) (*connect.Response[configpb.RuntimeConfig], error) {
	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	fullConfig, err := s.repo.GetFullConfig(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pbConfig := &configpb.RuntimeConfig{
		RateLimit: &configpb.RateLimitConfig{
			Enabled:           fullConfig.RateLimit.Enabled,
			RequestsPerMinute: int32(fullConfig.RateLimit.RequestsPerMinute),
			Burst:             int32(fullConfig.RateLimit.Burst),
			KeySource:         fullConfig.RateLimit.KeySource,
		},
		LoadBalancer: &configpb.LoadBalancerConfig{
			Enabled:   fullConfig.LoadBalancer.Enabled,
			Strategy:  fullConfig.LoadBalancer.Strategy,
			KeySource: fullConfig.LoadBalancer.KeySource,
		},
		Features: &configpb.FeaturesConfig{
			EnableStreaming:       fullConfig.Features.EnableStreaming,
			EnableEmbeddings:      fullConfig.Features.EnableEmbeddings,
			EnableFunctionCalling: fullConfig.Features.EnableFunctionCalling,
			EnableResponseCaching: fullConfig.Features.EnableResponseCaching,
			EnableSse:             fullConfig.Features.EnableSSE,
			EnableRequestLogging:  fullConfig.Features.EnableRequestLogging,
			EnableHealthChecks:    fullConfig.Features.EnableHealthChecks,
			EnableAgents:          fullConfig.Features.EnableAgents,
		},
		Cache: &configpb.CacheConfig{
			Enabled:       fullConfig.Cache.Enabled,
			Type:          fullConfig.Cache.Type,
			Ttl:           fullConfig.Cache.TTL,
			MemoryMaxSize: int32(fullConfig.Cache.MemoryMaxSize),
			RedisAddress:  fullConfig.Cache.RedisAddress,
			RedisDb:       int32(fullConfig.Cache.RedisDB),
			RedisPoolSize: int32(fullConfig.Cache.RedisPoolSize),
		},
		Telemetry: &configpb.TelemetryConfig{
			Enabled:            fullConfig.Telemetry.Enabled,
			SamplingRate:       fullConfig.Telemetry.SamplingRate,
			Granularity:        fullConfig.Telemetry.Granularity,
			TraceProviderCalls: fullConfig.Telemetry.TraceProviderCalls,
			TraceStreamChunks:  fullConfig.Telemetry.TraceStreamChunks,
			TraceFallbacks:     fullConfig.Telemetry.TraceFallbacks,
			CollectorUrl:       fullConfig.Telemetry.CollectorURL,
			ServiceName:        fullConfig.Telemetry.ServiceName,
		},
		Cors: &configpb.CORSConfig{
			Enabled:          fullConfig.CORS.Enabled,
			AllowedOrigins:   fullConfig.CORS.AllowedOrigins,
			AllowedMethods:   fullConfig.CORS.AllowedMethods,
			AllowedHeaders:   fullConfig.CORS.AllowedHeaders,
			ExposedHeaders:   fullConfig.CORS.ExposedHeaders,
			AllowCredentials: fullConfig.CORS.AllowCredentials,
			MaxAge:           fullConfig.CORS.MaxAge,
		},
		UpdatedAt: timestamppb.New(fullConfig.UpdatedAt),
		Version:   int32(fullConfig.Version),
	}

	return connect.NewResponse(pbConfig), nil
}

// UpdateRuntimeConfig updates the full runtime configuration
func (s *RuntimeConfigServer) UpdateRuntimeConfig(
	ctx context.Context,
	req *connect.Request[configpb.UpdateRuntimeConfigRequest],
) (*connect.Response[configpb.RuntimeConfig], error) {
	cfg := req.Msg.GetConfig()
	if cfg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("config is required"))
	}

	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	readonly := readonlySections()

	// Update each section. Sections in `readonly` are silently skipped
	// — the form panels for them render a "coming soon" notice but the
	// proto fields are still on the wire, so old clients won't break.
	if cfg.RateLimit != nil {
		configData, _ := json.Marshal(rtconfig.RateLimitConfig{
			Enabled:           cfg.RateLimit.Enabled,
			RequestsPerMinute: int(cfg.RateLimit.RequestsPerMinute),
			Burst:             int(cfg.RateLimit.Burst),
			KeySource:         cfg.RateLimit.KeySource,
		})
		if _, err := s.repo.Update(ctx, tenantID, string(rtconfig.SectionRateLimit), configData, nil); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if cfg.LoadBalancer != nil && !readonly[string(rtconfig.SectionLoadBalancer)] {
		configData, _ := json.Marshal(rtconfig.LoadBalancerConfig{
			Enabled:   cfg.LoadBalancer.Enabled,
			Strategy:  cfg.LoadBalancer.Strategy,
			KeySource: cfg.LoadBalancer.KeySource,
		})
		if _, err := s.repo.Update(ctx, tenantID, string(rtconfig.SectionLoadBalancer), configData, nil); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if cfg.Features != nil {
		configData, _ := json.Marshal(rtconfig.FeaturesConfig{
			EnableStreaming:       cfg.Features.EnableStreaming,
			EnableEmbeddings:      cfg.Features.EnableEmbeddings,
			EnableFunctionCalling: cfg.Features.EnableFunctionCalling,
			EnableResponseCaching: cfg.Features.EnableResponseCaching,
			EnableSSE:             cfg.Features.EnableSse,
			EnableRequestLogging:  cfg.Features.EnableRequestLogging,
			EnableHealthChecks:    cfg.Features.EnableHealthChecks,
			EnableAgents:          cfg.Features.EnableAgents,
		})
		if _, err := s.repo.Update(ctx, tenantID, string(rtconfig.SectionFeatures), configData, nil); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if cfg.Cache != nil {
		configData, _ := json.Marshal(rtconfig.CacheConfig{
			Enabled:       cfg.Cache.Enabled,
			Type:          cfg.Cache.Type,
			TTL:           cfg.Cache.Ttl,
			MemoryMaxSize: int(cfg.Cache.MemoryMaxSize),
			RedisAddress:  cfg.Cache.RedisAddress,
			RedisDB:       int(cfg.Cache.RedisDb),
			RedisPoolSize: int(cfg.Cache.RedisPoolSize),
		})
		if _, err := s.repo.Update(ctx, tenantID, string(rtconfig.SectionCache), configData, nil); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	// Telemetry + CORS skipped: readonly until the wiring lands.

	// Return the updated full config
	return s.GetRuntimeConfig(ctx, connect.NewRequest(&configpb.GetRuntimeConfigRequest{}))
}

// GetRuntimeConfigSection returns a specific section of the runtime configuration
func (s *RuntimeConfigServer) GetRuntimeConfigSection(
	ctx context.Context,
	req *connect.Request[configpb.GetRuntimeConfigSectionRequest],
) (*connect.Response[configpb.ConfigSection], error) {
	section := req.Msg.GetSection()
	if section == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("section is required"))
	}

	if !rtconfig.IsValidSection(section) {
		return nil, connect.NewError(connect.CodeInvalidArgument, rtconfig.ErrInvalidSection)
	}

	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	configSection, err := s.repo.Get(ctx, tenantID, section)
	if err != nil {
		if errors.Is(err, rtconfig.ErrSectionNotFound) {
			// Return default config for the section
			defaultConfig, defaultErr := rtconfig.GetDefaultConfig(rtconfig.SectionName(section))
			if defaultErr != nil {
				return nil, connect.NewError(connect.CodeInternal, defaultErr)
			}
			configBytes, _ := json.Marshal(defaultConfig)
			yamlContent, _ := rtconfig.ConfigToYAML(configBytes)

			configStruct, _ := structpb.NewStruct(make(map[string]interface{}))
			json.Unmarshal(configBytes, &configStruct)

			return connect.NewResponse(&configpb.ConfigSection{
				Name:        section,
				Config:      configStruct,
				YamlContent: yamlContent,
				Version:     0,
			}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to protobuf
	configStruct, yamlContent, err := s.configToProto(configSection.Config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&configpb.ConfigSection{
		Name:        section,
		Config:      configStruct,
		YamlContent: yamlContent,
		UpdatedAt:   timestamppb.New(configSection.UpdatedAt),
		Version:     int32(configSection.Version),
	}), nil
}

// UpdateRuntimeConfigSection updates a specific section of the runtime configuration
func (s *RuntimeConfigServer) UpdateRuntimeConfigSection(
	ctx context.Context,
	req *connect.Request[configpb.UpdateRuntimeConfigSectionRequest],
) (*connect.Response[configpb.ConfigSection], error) {
	section := req.Msg.GetSection()
	if section == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("section is required"))
	}

	if !rtconfig.IsValidSection(section) {
		return nil, connect.NewError(connect.CodeInvalidArgument, rtconfig.ErrInvalidSection)
	}

	if readonlySections()[section] {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("section %q is read-only until its wiring lands; see docs/audits/runtime-config.md", section))
	}

	var configData json.RawMessage

	// Check if YAML content is provided
	if req.Msg.GetYamlContent() != "" {
		var err error
		configData, err = rtconfig.YAMLToConfig(req.Msg.GetYamlContent())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	} else if req.Msg.GetConfig() != nil {
		// Use the struct config
		configMap := req.Msg.GetConfig().AsMap()
		var err error
		configData, err = json.Marshal(configMap)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	} else {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("either config or yaml_content is required"))
	}

	// Validate the config
	if err := rtconfig.ValidateConfig(rtconfig.SectionName(section), configData); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Update the config
	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	updatedSection, err := s.repo.Update(ctx, tenantID, section, configData, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Publish event for hot-reload
	s.publishConfigUpdated(ctx, tenantID, section, updatedSection.Config, updatedSection.Version, "")

	// Convert to protobuf
	configStruct, yamlContent, err := s.configToProto(updatedSection.Config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&configpb.ConfigSection{
		Name:        section,
		Config:      configStruct,
		YamlContent: yamlContent,
		UpdatedAt:   timestamppb.New(updatedSection.UpdatedAt),
		Version:     int32(updatedSection.Version),
	}), nil
}

// ResetRuntimeConfigSection resets a configuration section to its default values
func (s *RuntimeConfigServer) ResetRuntimeConfigSection(
	ctx context.Context,
	req *connect.Request[configpb.ResetRuntimeConfigSectionRequest],
) (*connect.Response[configpb.ConfigSection], error) {
	section := req.Msg.GetSection()
	if section == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("section is required"))
	}

	if !rtconfig.IsValidSection(section) {
		return nil, connect.NewError(connect.CodeInvalidArgument, rtconfig.ErrInvalidSection)
	}

	if readonlySections()[section] {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("section %q is read-only until its wiring lands; see docs/audits/runtime-config.md", section))
	}

	// Reset to defaults
	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	updatedSection, err := s.repo.Reset(ctx, tenantID, section, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Publish event for hot-reload
	s.publishConfigReset(ctx, tenantID, section, updatedSection.Version, "")

	// Convert to protobuf
	configStruct, yamlContent, err := s.configToProto(updatedSection.Config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&configpb.ConfigSection{
		Name:        section,
		Config:      configStruct,
		YamlContent: yamlContent,
		UpdatedAt:   timestamppb.New(updatedSection.UpdatedAt),
		Version:     int32(updatedSection.Version),
	}), nil
}

// Helper function to convert JSON config to protobuf struct and YAML
func (s *RuntimeConfigServer) configToProto(config json.RawMessage) (*structpb.Struct, string, error) {
	// Convert to map
	var configMap map[string]interface{}
	if err := json.Unmarshal(config, &configMap); err != nil {
		return nil, "", fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Convert to protobuf struct
	configStruct, err := structpb.NewStruct(configMap)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create protobuf struct: %w", err)
	}

	// Convert to YAML
	yamlContent, err := rtconfig.ConfigToYAML(config)
	if err != nil {
		return nil, "", fmt.Errorf("failed to convert to YAML: %w", err)
	}

	return configStruct, yamlContent, nil
}
