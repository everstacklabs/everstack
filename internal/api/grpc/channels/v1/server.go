package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/channels"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	channelspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/channels/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/channels/v1/channelsconnect"
	"github.com/google/uuid"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// requireTenantID extracts the tenant id from context. The post-2026-05-06
// P0 contract is "context only, never trust the request body". Any
// channels handler that takes a tenant id off `req.Msg.TenantId` is a
// cross-tenant leak — see project_tenant_isolation_bugs.md. Returns
// `PermissionDenied` when context is empty so the FE / SDK gets the
// same shape as the rest of the platform.
func requireTenantID(ctx context.Context) (string, error) {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid, nil
	}
	return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
}

// Server implements the ChannelsService gRPC server.
type Server struct {
	ctx     context.Context
	store   channels.ChannelStore
	manager *channels.ChannelManager
	db      *sqlx.DB
}

// CreateServer creates a new channels Server. db is the read model the plan
// gates count against; a nil db disables the count-based gates (the
// availability gate still applies).
func CreateServer(ctx context.Context, store channels.ChannelStore, manager *channels.ChannelManager, db *sqlx.DB) *Server {
	return &Server{ctx: ctx, store: store, manager: manager, db: db}
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return channelsconnect.NewChannelsServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return channelspb.File_everstack_channels_v1_channels_service_proto
}

func (s *Server) AppName() string      { return channelsconnect.ChannelsServiceName }
func (s *Server) MethodPrefix() string { return channelsconnect.ChannelsServiceName }

func (s *Server) RegisterGateway(_ context.Context, _ *gwruntime.ServeMux, _ string, _ []grpc.DialOption) error {
	return nil
}

// ─── RPC Implementations ────────────────────────────────────────────

func (s *Server) CreateChannel(ctx context.Context, req *connect.Request[channelspb.CreateChannelRequest]) (*connect.Response[channelspb.CreateChannelResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg

	// One limit, one semantic: CHANNELS caps the platform connections a tenant
	// may configure, CHANNEL_BINDINGS (enforced in BindAgentChannel) caps the
	// agent↔channel wirings. Every plan reaches every platform; the volume
	// meter is MESSAGES_MONTHLY, so channel count is only a free-tier ceiling
	// (editions-and-billing.md, Phase 1c).
	//
	// Denials are ResourceExhausted, never PermissionDenied: the admin FE
	// transport bounces the browser to /login on auth-shaped codes
	// (apps/admin/src/lib/auth-redirect.ts), so an upgrade prompt returned as
	// PermissionDenied logs the user out instead of showing the limit.
	platform := platformToString(msg.Platform)
	monitor := enterprise.LicenseMonitorFromContext(ctx)

	slot, err := enterprise.ReserveResourceSlot(ctx, s.db, monitor,
		enterprise.UsageTypeChannels,
		`SELECT COUNT(*) FROM channel_configs WHERE tenant_id = $1`,
		[]interface{}{tenantID}, 1, "channel", tenantID)
	if err != nil {
		ent := enterprise.ResolveEntitlements(ctx, monitor)
		logger.WithFields("tier", ent.Tier, "source", ent.Source, "platform", platform).
			WithError(err).Info("channels: create blocked by channel limit")
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}
	defer slot.Release()

	var credsEncrypted []byte
	if msg.Credentials != nil {
		encrypted, err := s.manager.EncryptCredentials(msg.Credentials.AsMap())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("encrypt credentials: %w", err))
		}
		credsEncrypted = encrypted
	}

	platformConfigBytes := []byte("{}")
	if msg.PlatformConfig != nil {
		b, err := json.Marshal(msg.PlatformConfig.AsMap())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid platform_config: %w", err))
		}
		platformConfigBytes = b
	}

	record := &channels.ChannelConfigRecord{
		ID:                    uuid.New().String(),
		TenantID:              tenantID,
		AgentID:               optionalStringToNullString(msg.AgentId),
		Platform:              platform,
		Name:                  msg.Name,
		Enabled:               true,
		SessionMode:           sessionModeToString(msg.SessionMode),
		CredentialsEncrypted:  credsEncrypted,
		PlatformConfig:        platformConfigBytes,
		MaxMessagesPerMinute:  defaultInt32(msg.MaxMessagesPerMinute, 30),
		MaxSessionsPerUser:    defaultInt32(msg.MaxSessionsPerUser, 5),
		ResponseFormat:        defaultString(msg.ResponseFormat, "auto"),
		MaxResponseLength:     defaultInt32(msg.MaxResponseLength, 2000),
		MaxTokensPerDay:       msg.MaxTokensPerDay,
		IdleSessionTTLSeconds: defaultInt32(msg.IdleSessionTtlSeconds, 3600),
		CoalesceWindowMs:      defaultInt32(msg.CoalesceWindowMs, 3000),
		InstanceAffinity:      msg.InstanceAffinity,
	}
	if record.SessionMode == "" {
		record.SessionMode = "thread"
	}

	if err := s.store.CreateChannelConfig(ctx, record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create channel: %w", err))
	}

	// Hold the reservation until the row is countable so a concurrent
	// creator on the same tenant sees it.
	slot.Confirm(ctx)

	logger.WithFields("channel", record.Name, "platform", record.Platform).Info("channels: created channel config")

	// Hot-start the connector immediately
	if err := s.manager.ReloadConfig(ctx, record.ID, record.TenantID); err != nil {
		logger.WithFields("channel", record.Name).WithError(err).Warn("channels: created but failed to start connector")
	}

	return connect.NewResponse(&channelspb.CreateChannelResponse{Channel: recordToProto(record)}), nil
}

func (s *Server) GetChannel(ctx context.Context, req *connect.Request[channelspb.GetChannelRequest]) (*connect.Response[channelspb.GetChannelResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	record, err := s.store.GetChannelConfig(ctx, req.Msg.Id, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("channel not found"))
	}
	return connect.NewResponse(&channelspb.GetChannelResponse{Channel: recordToProto(record)}), nil
}

func (s *Server) UpdateChannel(ctx context.Context, req *connect.Request[channelspb.UpdateChannelRequest]) (*connect.Response[channelspb.UpdateChannelResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	existing, err := s.store.GetChannelConfig(ctx, msg.Id, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if existing == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("channel not found"))
	}

	if msg.Name != nil {
		existing.Name = *msg.Name
	}
	if msg.AgentId != nil {
		existing.AgentID = optionalStringToNullString(msg.AgentId)
	}
	if msg.Enabled != nil {
		existing.Enabled = *msg.Enabled
	}
	if msg.SessionMode != nil {
		existing.SessionMode = sessionModeToString(*msg.SessionMode)
	}
	if msg.Credentials != nil {
		encrypted, err := s.manager.EncryptCredentials(msg.Credentials.AsMap())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("encrypt credentials: %w", err))
		}
		existing.CredentialsEncrypted = encrypted
	}
	if msg.PlatformConfig != nil {
		b, _ := json.Marshal(msg.PlatformConfig.AsMap())
		existing.PlatformConfig = b
	}
	if msg.MaxMessagesPerMinute != nil {
		existing.MaxMessagesPerMinute = *msg.MaxMessagesPerMinute
	}
	if msg.MaxSessionsPerUser != nil {
		existing.MaxSessionsPerUser = *msg.MaxSessionsPerUser
	}
	if msg.ResponseFormat != nil {
		existing.ResponseFormat = *msg.ResponseFormat
	}
	if msg.MaxResponseLength != nil {
		existing.MaxResponseLength = *msg.MaxResponseLength
	}
	if msg.MaxTokensPerDay != nil {
		existing.MaxTokensPerDay = *msg.MaxTokensPerDay
	}
	if msg.IdleSessionTtlSeconds != nil {
		existing.IdleSessionTTLSeconds = *msg.IdleSessionTtlSeconds
	}
	if msg.CoalesceWindowMs != nil {
		existing.CoalesceWindowMs = *msg.CoalesceWindowMs
	}
	if msg.InstanceAffinity != nil {
		existing.InstanceAffinity = *msg.InstanceAffinity
	}

	if err := s.store.UpdateChannelConfig(ctx, existing); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := s.manager.ReloadConfig(ctx, msg.Id, tenantID); err != nil {
		logger.WithError(err).Warn("channels: failed to hot-reload connector after update")
	}

	return connect.NewResponse(&channelspb.UpdateChannelResponse{Channel: recordToProto(existing)}), nil
}

func (s *Server) DeleteChannel(ctx context.Context, req *connect.Request[channelspb.DeleteChannelRequest]) (*connect.Response[channelspb.DeleteChannelResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteChannelConfig(ctx, req.Msg.Id, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	_ = s.manager.ReloadConfig(ctx, req.Msg.Id, tenantID)
	return connect.NewResponse(&channelspb.DeleteChannelResponse{}), nil
}

func (s *Server) ListChannels(ctx context.Context, req *connect.Request[channelspb.ListChannelsRequest]) (*connect.Response[channelspb.ListChannelsResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	var platform *string
	if msg.Platform != nil && *msg.Platform != channelspb.Platform_PLATFORM_UNSPECIFIED {
		p := platformToString(*msg.Platform)
		platform = &p
	}
	var agentID *string
	if msg.AgentId != nil && *msg.AgentId != "" {
		agentID = msg.AgentId
	}
	var enabled *bool
	if msg.Enabled != nil {
		enabled = msg.Enabled
	}

	records, total, err := s.store.ListChannelConfigs(ctx, tenantID, platform, agentID, enabled, msg.Limit, msg.Offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protos := make([]*channelspb.ChannelConfig, 0, len(records))
	for _, r := range records {
		protos = append(protos, recordToProto(r))
	}
	return connect.NewResponse(&channelspb.ListChannelsResponse{Channels: protos, Total: total}), nil
}

func (s *Server) TestChannel(ctx context.Context, req *connect.Request[channelspb.TestChannelRequest]) (*connect.Response[channelspb.TestChannelResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	// Verify the caller owns the channel before exposing its connector
	// status. Without this check, anyone could probe arbitrary channel
	// IDs across tenants — the manager's status map is process-global.
	record, err := s.store.GetChannelConfig(ctx, req.Msg.Id, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("channel not found"))
	}
	status := s.manager.GetStatus(req.Msg.Id)
	return connect.NewResponse(&channelspb.TestChannelResponse{
		Success: status == channels.StatusConnected,
		Message: string(status),
	}), nil
}

func (s *Server) ListChannelStatuses(ctx context.Context, _ *connect.Request[channelspb.ListChannelStatusesRequest]) (*connect.Response[channelspb.ListChannelStatusesResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	// The manager's status map is process-global (one per gateway pod
	// across every tenant's connector). Filter to the calling tenant's
	// channels by intersecting the status map with the tenant's channel
	// configs — otherwise this endpoint enumerates every running
	// connector in the cluster.
	records, _, err := s.store.ListChannelConfigs(ctx, tenantID, nil, nil, nil, 0, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	owned := make(map[string]struct{}, len(records))
	for _, r := range records {
		owned[r.ID] = struct{}{}
	}
	statuses := s.manager.GetAllStatuses()
	infos := make([]*channelspb.ChannelStatusInfo, 0, len(owned))
	for id, status := range statuses {
		if _, ok := owned[id]; !ok {
			continue
		}
		infos = append(infos, &channelspb.ChannelStatusInfo{
			ChannelId: id,
			Status:    connectorStatusToProto(status),
		})
	}
	return connect.NewResponse(&channelspb.ListChannelStatusesResponse{Statuses: infos}), nil
}

func (s *Server) ListChannelSessions(ctx context.Context, req *connect.Request[channelspb.ListChannelSessionsRequest]) (*connect.Response[channelspb.ListChannelSessionsResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	// The session mappings table is keyed by channel_config_id and has
	// no tenant column; ownership is established via the parent channel.
	// Verify the caller owns the channel before exposing its sessions.
	owner, err := s.store.GetChannelConfig(ctx, req.Msg.ChannelId, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if owner == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("channel not found"))
	}
	mappings, total, err := s.store.ListSessionMappings(ctx, req.Msg.ChannelId, req.Msg.Limit, req.Msg.Offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	sessions := make([]*channelspb.ChannelSessionMapping, 0, len(mappings))
	for _, m := range mappings {
		sessions = append(sessions, &channelspb.ChannelSessionMapping{
			Id: m.ID, ChannelConfigId: m.ChannelConfigID,
			PlatformChannelRef: m.PlatformChannelRef, PlatformUserId: m.PlatformUserID,
			PlatformUserName: m.PlatformUserName, PlatformThreadRef: m.PlatformThreadRef,
			AgentSessionId: m.AgentSessionID,
			CreatedAt:      timestamppb.New(m.CreatedAt), LastMessageAt: timestamppb.New(m.LastMessageAt),
		})
	}
	return connect.NewResponse(&channelspb.ListChannelSessionsResponse{Sessions: sessions, Total: total}), nil
}

func (s *Server) ListPlatformChannels(ctx context.Context, req *connect.Request[channelspb.ListPlatformChannelsRequest]) (*connect.Response[channelspb.ListPlatformChannelsResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	if msg.ChannelConfigId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("channel_config_id is required"))
	}

	// Verify ownership using the context tenant id only — never the
	// request body's tenant_id (that field is now ignored on every
	// channels handler; left in the proto for backwards-compat).
	record, err := s.store.GetChannelConfig(ctx, msg.ChannelConfigId, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("channel config not found"))
	}

	// Get the live connector
	connector := s.manager.GetConnector(msg.ChannelConfigId)
	if connector == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("connector is not running"))
	}

	// Type-assert for ChannelLister
	lister, ok := connector.(channels.ChannelLister)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("platform does not support listing channels"))
	}

	platformChannels, err := lister.ListPlatformChannels(ctx)
	if err != nil {
		logger.WithFields("channel_config_id", msg.ChannelConfigId, "platform", record.Platform).
			WithError(err).Warn("channels: ListPlatformChannels failed")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list platform channels: %w", err))
	}

	logger.WithFields("channel_config_id", msg.ChannelConfigId, "platform", record.Platform, "count", len(platformChannels)).
		Info("channels: ListPlatformChannels succeeded")

	infos := make([]*channelspb.PlatformChannelInfo, 0, len(platformChannels))
	for _, ch := range platformChannels {
		infos = append(infos, &channelspb.PlatformChannelInfo{
			Id:   ch.ID,
			Name: ch.Name,
			Type: ch.Type,
		})
	}

	return connect.NewResponse(&channelspb.ListPlatformChannelsResponse{Channels: infos}), nil
}

// ─── Helpers ────────────────────────────────────────────────────────

func defaultInt32(v, d int32) int32 {
	if v <= 0 {
		return d
	}
	return v
}

func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func recordToProto(r *channels.ChannelConfigRecord) *channelspb.ChannelConfig {
	cfg := &channelspb.ChannelConfig{
		Id: r.ID, TenantId: r.TenantID, AgentId: nullStringToOptional(r.AgentID),
		Platform: stringToPlatform(r.Platform), Name: r.Name, Enabled: r.Enabled,
		SessionMode:          stringToSessionMode(r.SessionMode),
		MaxMessagesPerMinute: r.MaxMessagesPerMinute, MaxSessionsPerUser: r.MaxSessionsPerUser,
		ResponseFormat: r.ResponseFormat, MaxResponseLength: r.MaxResponseLength,
		MaxTokensPerDay: r.MaxTokensPerDay, IdleSessionTtlSeconds: r.IdleSessionTTLSeconds,
		CoalesceWindowMs: r.CoalesceWindowMs, InstanceAffinity: r.InstanceAffinity,
		CreatedAt: timestamppb.New(r.CreatedAt), UpdatedAt: timestamppb.New(r.UpdatedAt),
	}
	if len(r.PlatformConfig) > 0 {
		var pcMap map[string]interface{}
		if json.Unmarshal(r.PlatformConfig, &pcMap) == nil {
			if s, err := structpb.NewStruct(pcMap); err == nil {
				cfg.PlatformConfig = s
			}
		}
	}
	return cfg
}

func platformToString(p channelspb.Platform) string {
	switch p {
	case channelspb.Platform_PLATFORM_DISCORD:
		return "discord"
	case channelspb.Platform_PLATFORM_SLACK:
		return "slack"
	case channelspb.Platform_PLATFORM_TELEGRAM:
		return "telegram"
	default:
		return "unknown"
	}
}

func stringToPlatform(s string) channelspb.Platform {
	switch s {
	case "discord":
		return channelspb.Platform_PLATFORM_DISCORD
	case "slack":
		return channelspb.Platform_PLATFORM_SLACK
	case "telegram":
		return channelspb.Platform_PLATFORM_TELEGRAM
	default:
		return channelspb.Platform_PLATFORM_UNSPECIFIED
	}
}

func sessionModeToString(m channelspb.SessionMode) string {
	switch m {
	case channelspb.SessionMode_SESSION_MODE_SHARED:
		return "shared"
	case channelspb.SessionMode_SESSION_MODE_PER_USER:
		return "per_user"
	case channelspb.SessionMode_SESSION_MODE_THREAD:
		return "thread"
	default:
		return "thread"
	}
}

func stringToSessionMode(s string) channelspb.SessionMode {
	switch s {
	case "shared":
		return channelspb.SessionMode_SESSION_MODE_SHARED
	case "per_user":
		return channelspb.SessionMode_SESSION_MODE_PER_USER
	case "thread":
		return channelspb.SessionMode_SESSION_MODE_THREAD
	default:
		return channelspb.SessionMode_SESSION_MODE_UNSPECIFIED
	}
}

func optionalStringToNullString(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func ListChannelStatuses(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func nullStringToOptional(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func connectorStatusToProto(s channels.ConnectorStatus) channelspb.ChannelStatus {
	switch s {
	case channels.StatusConnected:
		return channelspb.ChannelStatus_CHANNEL_STATUS_CONNECTED
	case channels.StatusDisconnected:
		return channelspb.ChannelStatus_CHANNEL_STATUS_DISCONNECTED
	case channels.StatusConnecting:
		return channelspb.ChannelStatus_CHANNEL_STATUS_CONNECTING
	case channels.StatusError:
		return channelspb.ChannelStatus_CHANNEL_STATUS_ERROR
	default:
		return channelspb.ChannelStatus_CHANNEL_STATUS_UNSPECIFIED
	}
}
