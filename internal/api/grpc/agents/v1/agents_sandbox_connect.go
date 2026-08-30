package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox"
	sandboxcp "github.com/everstacklabs/everstack/internal/sandbox/controlplane"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// CreateSandbox implements AgentsServiceHandler.CreateSandbox via ConnectRPC.
func (s *Server) CreateSandbox(ctx context.Context, req *connect.Request[agentspb.CreateSandboxRequest]) (*connect.Response[agentspb.CreateSandboxResponse], error) {
	msg := req.Msg
	// Diagnostic: log every CreateSandbox entry so we can tell from the
	// gateway log alone whether the handler is being reached vs hanging in
	// an interceptor / middleware. Includes tenant + template so we can
	// correlate with the FE network panel.
	logger.WithFields(
		"request_tenant_id", msg.GetTenantId(),
		"session_id", msg.GetSessionId(),
		"template_id", msg.GetTemplateId(),
		"image", msg.GetImage(),
	).Info("CreateSandbox: handler entered")

	sessionID := msg.GetSessionId()
	if sessionID == "" {
		sessionID = "manual_" + uuid.New().String()
	}
	logger.WithFields("sandbox_id", "sbx_"+sessionID).Info("CreateSandbox: resolving tenant")
	tenantID, _ := s.resolveTenantID(ctx, msg.GetTenantId())
	logger.WithFields("tenant_id", tenantID, "session_id", sessionID).Info("CreateSandbox: tenant resolved, building config")
	spanCtx, span := telemetry.StartSandboxCreateSpan(ctx, sessionID, "", "",
		telemetry.WithTenantID(tenantID),
	)
	ctx = spanCtx
	defer span.End()
	telemetry.AddSpanEvent(span, attrs.EventSandboxCreate)
	if s.sandboxMgr == nil {
		err := connect.NewError(connect.CodeUnavailable, errSandboxNotEnabled)
		telemetry.RecordError(span, err)
		return nil, err
	}
	if err := s.sandboxMgr.RequireSandboxBilling(tenantID); err != nil {
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	// Start from template config if provided, otherwise use defaults
	var cfg sandbox.SandboxConfig
	if msg.GetTemplateId() != "" {
		span.SetAttributes(attribute.String("sandbox.template_id", msg.GetTemplateId()))
		t := sandbox.GetTemplate(msg.GetTemplateId())
		if t == nil {
			err := connect.NewError(connect.CodeInvalidArgument, errUnknownTemplate(msg.GetTemplateId()))
			telemetry.RecordError(span, err)
			return nil, err
		}
		cfg = sandbox.TemplateToSandboxConfig(t)
	} else {
		cfg = sandbox.DefaultSandboxConfig()
		cfg.Enabled = true
	}

	// Override template defaults with explicit request fields
	if msg.GetImage() != "" {
		cfg.Image = msg.GetImage()
	}
	if msg.GetCpuLimit() > 0 && msg.GetCpuLimit() <= 8 {
		cfg.CPULimit = msg.GetCpuLimit()
	}
	if msg.GetMemoryMb() >= 64 && msg.GetMemoryMb() <= 8192 {
		cfg.MemoryMB = msg.GetMemoryMb()
	}
	if msg.GetDiskMb() >= 64 && msg.GetDiskMb() <= sandbox.MaxSandboxDiskMB {
		cfg.DiskMB = msg.GetDiskMb()
	}
	if msg.GetTimeoutSeconds() >= 30 && msg.GetTimeoutSeconds() <= 3600 {
		cfg.TimeoutSeconds = int(msg.GetTimeoutSeconds())
	}
	switch msg.GetNetworkMode() {
	case "deny", "whitelist", "allow":
		cfg.NetworkMode = msg.GetNetworkMode()
	}
	if msg.GetIdleRetentionSeconds() != 0 {
		cfg.IdleRetentionSeconds = int(msg.GetIdleRetentionSeconds())
	}
	// Daytona-style minute intervals (canonical; win over the legacy
	// day/second fields when both are supplied). Zero semantics are
	// per-field, documented on the proto.
	if v := msg.GetAutoStopInterval(); v != 0 {
		mins := int(v)
		if mins < 0 {
			mins = 0 // negative request = auto-stop disabled
		}
		cfg.AutoStopMinutes = &mins
	}
	if v := msg.GetAutoArchiveInterval(); v != 0 {
		mins := int(v)
		if mins < 0 {
			mins = 0
		}
		cfg.AutoArchiveMinutes = &mins
	}
	if msg.AutoDeleteInterval != nil {
		mins := int(msg.GetAutoDeleteInterval())
		if mins < -1 {
			mins = -1
		}
		cfg.AutoDeleteMinutes = &mins
	}
	if msg.GetName() != "" {
		cfg.Name = msg.GetName()
	}
	if msg.GetGitRepoUrl() != "" {
		cfg.GitRepoURL = msg.GetGitRepoUrl()
	}
	if msg.GetGitBranch() != "" {
		cfg.GitBranch = msg.GetGitBranch()
	}
	if msg.GetGitInstallationId() > 0 {
		cfg.GitInstallationID = msg.GetGitInstallationId()
	}
	cfg.SSHEnabled = msg.GetSshEnabled()
	if reqLabels := msg.GetLabels(); len(reqLabels) > 0 {
		cfg.Labels = reqLabels
	}
	// Auto-lifecycle intervals. Use proto defaults if not supplied:
	// archive after 7 days, never auto-delete (-1).
	cfg.AutoArchiveAfterDays = int(msg.GetAutoArchiveAfterDays())
	if cfg.AutoArchiveAfterDays == 0 && msg.GetAutoArchiveAfterDays() == 0 {
		cfg.AutoArchiveAfterDays = 7 // server-side default
	}
	cfg.AutoDeleteAfterDays = int(msg.GetAutoDeleteAfterDays())
	if msg.GetAutoDeleteAfterDays() == 0 {
		cfg.AutoDeleteAfterDays = -1 // 0 in proto means "not set" for this field; -1 = never
	}

	// If snapshot_id is specified, resolve the snapshot's base_image and
	// use it as the sandbox image. This gives the sandbox the pre-baked
	// environment from the snapshot without needing explicit image knowledge.
	if snapID := msg.GetSnapshotId(); snapID != "" && s.snapshotRepo != nil {
		snapImage, snapErr := s.snapshotRepo.ImageForSnapshot(ctx, tenantID, snapID)
		if snapErr != nil {
			err := connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("snapshot_id %q: %w", snapID, snapErr))
			return nil, err
		}
		cfg.Image = snapImage
		cfg.SnapshotID = snapID
	}

	if len(msg.GetNetworkAllowCidrs()) > 0 && !msg.GetNetworkBlockAll() {
		err := connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("network_allow_cidrs requires network_block_all=true"))
		telemetry.RecordError(span, err)
		return nil, err
	}
	if msg.GetNetworkBlockAll() {
		allowCIDRs, cidrErr := sandbox.NormalizeNetworkAllowCIDRs(msg.GetNetworkAllowCidrs())
		if cidrErr != nil {
			err := connect.NewError(connect.CodeInvalidArgument, cidrErr)
			telemetry.RecordError(span, err)
			return nil, err
		}
		cfg.NetworkBlockAll = true
		cfg.NetworkAllowCIDRs = allowCIDRs
	}

	// Tailscale VPN: inject auth key as env var for sandbox-agent.
	if tsKey := msg.GetTailscaleAuthKey(); tsKey != "" {
		if cfg.EnvVars == nil {
			cfg.EnvVars = make(map[string]string)
		}
		cfg.EnvVars["SANDBOX_TAILSCALE_AUTH_KEY"] = tsKey
		cfg.TailscaleAuthKey = tsKey
	}

	// Storage mounts: serialize to JSON and inject as env var.
	if protoMounts := msg.GetMounts(); len(protoMounts) > 0 {
		mounts := make([]sandbox.StorageMountConfig, 0, len(protoMounts))
		for _, m := range protoMounts {
			mountType := strings.ToLower(strings.TrimSpace(m.GetType()))
			bucket := strings.TrimSpace(m.GetBucket())
			// everstack-volume (bucket = volume_id) is rewritten into a
			// concrete, credentialed r2 mount against the tenant's own bucket,
			// with a tenant-isolation check. Invalid mount arguments fail the
			// request; a volume not owned by this tenant is dropped
			// (resolveVolumeMount returns false), never mounted.
			if mountType == "everstack-volume" {
				if bucket == "" {
					return nil, connect.NewError(connect.CodeInvalidArgument, sandboxcp.ErrVolumeIDRequired)
				}
				mountPath, pathErr := sandboxcp.NormalizeVolumeMountPath(m.GetMountPath())
				if pathErr != nil {
					return nil, connect.NewError(connect.CodeInvalidArgument, pathErr)
				}
				if rewritten, ok := s.resolveVolumeMount(ctx, tenantID, bucket, mountPath, m.GetReadOnly()); ok {
					mounts = append(mounts, rewritten)
				}
				continue
			}
			switch mountType {
			case "s3", "r2", "gcs", "azure":
			default:
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported storage mount type %q", m.GetType()))
			}
			if bucket == "" {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("storage mount bucket is required"))
			}
			mountPath, pathErr := sandboxcp.NormalizeVolumeMountPath(m.GetMountPath())
			if pathErr != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, pathErr)
			}
			subPath, subPathErr := sandboxcp.NormalizeVolumeSubPath(m.GetSubpath())
			if subPathErr != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, subPathErr)
			}
			mounts = append(mounts, sandbox.StorageMountConfig{
				Type:      mountType,
				Bucket:    bucket,
				MountPath: mountPath,
				Endpoint:  strings.TrimSpace(m.GetEndpoint()),
				SubPath:   subPath,
				ReadOnly:  m.GetReadOnly(),
			})
		}
		if mountJSON, merr := json.Marshal(mounts); merr == nil {
			if cfg.EnvVars == nil {
				cfg.EnvVars = make(map[string]string)
			}
			cfg.EnvVars["SANDBOX_MOUNTS_JSON"] = string(mountJSON)
			cfg.StorageMounts = mounts
		}
	}

	// Computer Use: inject env var so sandbox-agent starts Xvfb + XFCE4.
	if msg.GetComputerUse() {
		if cfg.EnvVars == nil {
			cfg.EnvVars = make(map[string]string)
		}
		cfg.EnvVars["SANDBOX_COMPUTER_USE"] = "1"
		cfg.ComputerUse = true
	}

	// The async lifecycle path writes directly to the reconciler repository
	// instead of passing through SandboxManager.GetOrCreate. Apply the same
	// tenant caps and managed fixed-size policy before a pending allocation is
	// accepted so API clients cannot bypass the priced machine catalog.
	cfg = s.sandboxMgr.ClampToGlobalLimitsForTenant(cfg, tenantID)
	if err := s.sandboxMgr.ValidateSandboxMachineProfile(cfg, tenantID); err != nil {
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	s.injectPortExposureDomain(&cfg)

	// Async reconciler path: when the lifecycle repo is wired, write a
	// pending DB row and return immediately. The reconciler picks the
	// row up on the next 200ms tick, drives it through creating →
	// running, and the FE sees the transition via SSE (phase 2) or the
	// existing list polling (phase 1). No sync RPC, no 90s timeout, no
	// deadlock surface.
	//
	// See docs/design/sandbox-reconciler.md.
	if s.LifecycleRepoEnabled() {
		return s.createSandboxAsync(ctx, span, sessionID, tenantID, cfg)
	}

	// Sync path: blocks until the backend reports the sandbox is running.
	// context.WithoutCancel keeps tracing/auth values but discards the
	// client's deadline + cancel; we add our own upper bound so a wedged
	// firecracker-agent gRPC call can't pin the RPC indefinitely.
	//
	// 90s budget. Typical cold-boot on fcagent is <30s. The previous
	// 5-min ceiling was so generous that "sandbox didn't create" looked
	// indistinguishable from "RPC quietly succeeding eventually" — users
	// gave up before the timeout fired. Cut it to 90s so a real failure
	// surfaces inside the user's attention span; the placeholder DB row
	// (persisted before this call returns) carries any pending state for
	// the polling FE in the meantime.
	createCtx, createCancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer createCancel()
	logger.WithFields("session_id", sessionID, "tenant_id", tenantID, "image", cfg.Image).
		Info("CreateSandbox: calling sandboxMgr.GetOrCreate")
	inst, err := s.sandboxMgr.GetOrCreate(createCtx, sessionID, tenantID, cfg)
	logger.WithFields("session_id", sessionID, "tenant_id", tenantID, "err", fmt.Sprintf("%v", err)).
		Info("CreateSandbox: GetOrCreate returned")
	if err != nil {
		logger.WithFields("error", err.Error()).Error("CreateSandbox: failed to create sandbox")
		telemetry.RecordError(span, err)
		if errors.Is(err, sandbox.ErrSandboxBillingRequired) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if errors.Is(err, sandbox.ErrConcurrentSandboxLimit) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
		if errors.Is(err, sandbox.ErrUnsupportedSandboxSize) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	span.SetAttributes(
		attribute.String(attrs.SandboxID, inst.ID),
		attribute.String(attrs.SandboxBackend, inst.Backend),
		attribute.String(attrs.SandboxImage, inst.Config.Image),
		attribute.String(attrs.AgentSessionID, sessionID),
		attribute.String(attrs.TenantID, tenantID),
	)
	telemetry.AddSpanEvent(span, attrs.EventSandboxReady, attribute.String("sandbox.status", string(inst.Status)))

	// Auto-grant SSH access to the sandbox creator
	if s.sshKeyStore != nil {
		userID := s.resolveUserID(ctx)
		if err := s.sshKeyStore.GrantAccess(ctx, inst.ID, userID, tenantID, userID); err != nil {
			logger.WithFields("sandbox_id", inst.ID, "user_id", userID, "tenant_id", tenantID, "error", err.Error()).
				Warn("CreateSandbox: failed to auto-grant SSH access")
		} else {
			logger.WithFields("sandbox_id", inst.ID, "user_id", userID).
				Info("CreateSandbox: auto-granted SSH access")
		}
	}

	return connect.NewResponse(&agentspb.CreateSandboxResponse{
		Id:          inst.ID,
		SessionId:   sessionID,
		TenantId:    tenantID,
		ContainerId: inst.ContainerID,
		Status:      string(inst.Status),
		Backend:     publicBackendLabel(inst.Backend),
		Image:       inst.Config.Image,
		CreatedAt:   inst.CreatedAt.Format(time.RFC3339),
		ExpiresAt:   inst.ExpiresAt.Format(time.RFC3339),
		Name:        inst.Name,
	}), nil
}

// createSandboxAsync writes a pending sandbox_instances row and returns
// immediately. The reconciler picks the row up on the next tick and
// drives it through creating → running. The FE sees state changes via
// SSE (phase 2) or list polling (phase 1).
//
// On success the response carries the same shape as the sync path so
// callers don't have to distinguish — the only differences are status
// (always "pending") and an empty container_id at this point.
func (s *Server) createSandboxAsync(
	ctx context.Context,
	span trace.Span,
	sessionID, tenantID string,
	cfg sandbox.SandboxConfig,
) (*connect.Response[agentspb.CreateSandboxResponse], error) {
	if s.lifecycleRepo == nil {
		err := connect.NewError(connect.CodeInternal, errors.New("lifecycle repo not configured"))
		telemetry.RecordError(span, err)
		return nil, err
	}

	// Marshal config the same way the legacy persistInstance does so
	// the reconciler reads back identical JSON. Image lives separately
	// in the config so the reconciler step's unmarshalConfig can pick
	// it up.
	// Inject network policy env vars so the sandbox-agent can apply
	// nftables/iptables rules at boot. We merge into EnvVars rather than
	// replacing it so any caller-supplied env vars are preserved.
	envVars := cfg.EnvVars
	if cfg.NetworkBlockAll {
		if envVars == nil {
			envVars = make(map[string]string)
		}
		envVars["SANDBOX_NETWORK_BLOCK_ALL"] = "1"
		if len(cfg.NetworkAllowCIDRs) > 0 {
			envVars["SANDBOX_NETWORK_ALLOW_CIDRS"] = strings.Join(cfg.NetworkAllowCIDRs, ",")
		}
	}

	instCfg := sandbox.InstanceConfig{
		Image:             cfg.Image,
		CPULimit:          cfg.CPULimit,
		MemoryMB:          cfg.MemoryMB,
		DiskMB:            cfg.DiskMB,
		TimeoutSeconds:    cfg.TimeoutSeconds,
		NetworkMode:       sandbox.NetworkMode(cfg.NetworkMode),
		AllowedHosts:      cfg.AllowedHosts,
		EnvVars:           envVars,
		WorkDir:           "/workspace",
		TenantID:          tenantID,
		SessionID:         sessionID,
		Name:              cfg.Name,
		GitRepoURL:        cfg.GitRepoURL,
		GitBranch:         cfg.GitBranch,
		GitInstallationID: cfg.GitInstallationID,
		SSHEnabled:        cfg.SSHEnabled,
		BrowserSidecar:    cfg.BrowserSidecar,
	}
	configJSON, err := json.Marshal(instCfg)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal config: %w", err))
	}

	// Marshal labels; default to empty object so the NOT NULL column is satisfied.
	labelsJSON := []byte("{}")
	if reqLabels := cfg.Labels; len(reqLabels) > 0 {
		if b, merr := json.Marshal(reqLabels); merr == nil {
			labelsJSON = b
		}
	}

	// Resolve auto-lifecycle defaults. A value of 0 in the proto for
	// auto_archive_after_days means "not supplied" (proto int32 default);
	// in that case we apply the server-side default of 7 days. For
	// auto_delete_after_days, 0 from proto means "not supplied" and we
	// default to -1 (never). If the caller explicitly set 0 for
	// auto_archive, we treat it as "disabled".
	autoArchiveDays := cfg.AutoArchiveAfterDays
	if autoArchiveDays == 0 {
		autoArchiveDays = 7 // server default: archive after 7 days of sleeping
	}
	autoDeleteDays := cfg.AutoDeleteAfterDays
	if autoDeleteDays == 0 {
		autoDeleteDays = -1 // server default: never auto-delete
	}

	// Daytona-style minute intervals (the canonical columns going
	// forward; the day-granularity fields above are kept in lockstep
	// for legacy readers). auto_stop derives from the legacy
	// idle_retention_seconds knob: 0 = "not supplied" (NULL, plan-tier
	// default applies), negative = disabled, positive = ceiling to
	// whole minutes.
	var autoStopMinutes sql.NullInt64
	if cfg.IdleRetentionSeconds != 0 {
		mins := int64(0) // negative request = never auto-stop
		if cfg.IdleRetentionSeconds > 0 {
			mins = int64((cfg.IdleRetentionSeconds + 59) / 60)
			if mins < 1 {
				mins = 1
			}
		}
		autoStopMinutes = sql.NullInt64{Int64: mins, Valid: true}
	}
	autoArchiveMinutes := sql.NullInt64{Int64: 0, Valid: true}
	if autoArchiveDays > 0 {
		autoArchiveMinutes = sql.NullInt64{Int64: int64(autoArchiveDays) * 1440, Valid: true}
	}
	autoDeleteMinutes := sql.NullInt64{Int64: -1, Valid: true}
	if autoDeleteDays >= 0 {
		autoDeleteMinutes = sql.NullInt64{Int64: int64(autoDeleteDays) * 1440, Valid: true}
	}
	// Explicit minute intervals from the request override the values
	// derived from the legacy fields above.
	if cfg.AutoStopMinutes != nil {
		autoStopMinutes = sql.NullInt64{Int64: int64(*cfg.AutoStopMinutes), Valid: true}
	}
	if cfg.AutoArchiveMinutes != nil {
		autoArchiveMinutes = sql.NullInt64{Int64: int64(*cfg.AutoArchiveMinutes), Valid: true}
	}
	if cfg.AutoDeleteMinutes != nil {
		autoDeleteMinutes = sql.NullInt64{Int64: int64(*cfg.AutoDeleteMinutes), Valid: true}
	}

	now := time.Now()
	row := sandboxlc.Row{
		ID:                   "sbx_" + sessionID,
		TenantID:             tenantID,
		SessionID:            sessionID,
		Status:               sandboxlc.StatePending,
		LifecycleState:       sandboxlc.StatePending,
		DesiredState:         sandboxlc.DesireRunning,
		Backend:              backendNameOrEmpty(s.sandboxMgr),
		Image:                cfg.Image,
		Name:                 cfg.Name,
		Config:               configJSON,
		Labels:               labelsJSON,
		AutoArchiveAfterDays: autoArchiveDays,
		AutoDeleteAfterDays:  autoDeleteDays,
		AutoStopMinutes:      autoStopMinutes,
		AutoArchiveMinutes:   autoArchiveMinutes,
		AutoDeleteMinutes:    autoDeleteMinutes,
		ReconcileAfter:       now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	insCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.sandboxMgr.RequireConcurrentSandboxSlot(insCtx, tenantID, row.ID); err != nil {
		telemetry.RecordError(span, err)
		if errors.Is(err, sandbox.ErrConcurrentSandboxLimit) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	concurrentLimit := s.sandboxMgr.ConcurrentSandboxLimit(tenantID)
	inserted, err := s.lifecycleRepo.InsertPendingWithLimit(insCtx, row, concurrentLimit)
	if errors.Is(err, sandboxlc.ErrTerminalRow) {
		// A previous sandbox with this id terminated; can't resurrect.
		// Append a uniqueness suffix and try once more — most callers
		// use a fresh sessionID per Create so this is rare, but we
		// avoid hard-failing on the user's click.
		row.ID = row.ID + "_" + uuid.New().String()[:6]
		inserted, err = s.lifecycleRepo.InsertPendingWithLimit(insCtx, row, concurrentLimit)
	}
	if err != nil {
		logger.WithFields("sandbox_id", row.ID, "error", err.Error()).
			Error("createSandboxAsync: InsertPending failed")
		telemetry.RecordError(span, err)
		if errors.Is(err, sandbox.ErrConcurrentSandboxLimit) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	span.SetAttributes(
		attribute.String(attrs.SandboxID, inserted.ID),
		attribute.String(attrs.SandboxBackend, inserted.Backend),
		attribute.String(attrs.SandboxImage, inserted.Image),
		attribute.String(attrs.AgentSessionID, sessionID),
		attribute.String(attrs.TenantID, tenantID),
	)
	telemetry.AddSpanEvent(span, attrs.EventSandboxCreate)

	logger.WithFields(
		"sandbox_id", inserted.ID,
		"session_id", sessionID,
		"tenant_id", tenantID,
	).Info("CreateSandbox: pending row inserted, returning to caller (reconciler will converge)")

	// Auto-grant SSH access to the sandbox creator. The sync path and
	// RecreateSandbox do this after the backend.Create returns; the
	// async path needs the same grant or every sandbox the FE creates
	// surfaces "SSH: unavailable" until the user manually runs
	// GrantSandboxSSHAccess. The grant table is independent of the
	// sandbox runtime — writing it here, before the reconciler has
	// driven the row to running, is fine because GetSandboxSSHInfo
	// only reads the grant after confirming the sandbox is running.
	if s.sshKeyStore != nil {
		userID := s.resolveUserID(ctx)
		if err := s.sshKeyStore.GrantAccess(ctx, inserted.ID, userID, tenantID, userID); err != nil {
			logger.WithFields(
				"sandbox_id", inserted.ID,
				"user_id", userID,
				"tenant_id", tenantID,
				"error", err.Error(),
			).Warn("createSandboxAsync: failed to auto-grant SSH access")
		} else {
			logger.WithFields(
				"sandbox_id", inserted.ID,
				"user_id", userID,
			).Info("createSandboxAsync: auto-granted SSH access")
		}
	}

	return connect.NewResponse(&agentspb.CreateSandboxResponse{
		Id:          inserted.ID,
		SessionId:   sessionID,
		TenantId:    tenantID,
		ContainerId: "",
		Status:      string(inserted.Status),
		Backend:     publicBackendLabel(inserted.Backend),
		Image:       inserted.Image,
		CreatedAt:   inserted.CreatedAt.Format(time.RFC3339),
		ExpiresAt:   "",
		Name:        inserted.Name,
	}), nil
}

func backendNameOrEmpty(m *sandbox.SandboxManager) string {
	if m == nil {
		return ""
	}
	return m.BackendName()
}

// RecreateSandbox implements AgentsServiceHandler.RecreateSandbox via ConnectRPC.
func (s *Server) RecreateSandbox(ctx context.Context, req *connect.Request[agentspb.RecreateSandboxRequest]) (*connect.Response[agentspb.CreateSandboxResponse], error) {
	msg := req.Msg
	// Tenant id comes from context, not the request body. The earlier shape
	// here ("prefer body, fall back to context") let a caller recreate a
	// sandbox belonging to another tenant by setting tenant_id in the
	// request — the same body-trust pattern that produced the 2026-05-06
	// cross-tenant incident. resolveTenantID drops the body arg on the floor
	// and refuses to fall back unless there is exactly one organization in
	// the entire DB (genuinely single-tenant self-hosted).
	tenantID, err := s.resolveTenantID(ctx, msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	sessionID := msg.GetSessionId()
	if sessionID == "" {
		sessionID = "manual_" + uuid.New().String()
	}
	spanCtx, span := telemetry.StartSandboxCreateSpan(ctx, sessionID, msg.GetSandboxId(), "",
		telemetry.WithTenantID(tenantID),
	)
	ctx = spanCtx
	defer span.End()
	telemetry.AddSpanEvent(span, attrs.EventSandboxCreate)
	if msg.GetSandboxId() == "" {
		err := connect.NewError(connect.CodeInvalidArgument, errSandboxIDRequired)
		telemetry.RecordError(span, err)
		return nil, err
	}
	if s.sandboxMgr == nil {
		err := connect.NewError(connect.CodeUnavailable, errSandboxNotEnabled)
		telemetry.RecordError(span, err)
		return nil, err
	}
	inst, err := sandboxcp.NewLifecycleService(s.lifecycleRepo, s.sandboxMgr).Recreate(ctx, sandboxcp.RecreateRequest{
		SandboxID: msg.GetSandboxId(),
		SessionID: sessionID,
		TenantID:  tenantID,
	})
	if err != nil {
		logger.WithFields("error", err.Error()).Error("RecreateSandbox: failed to recreate sandbox")
		telemetry.RecordError(span, err)
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, mapLifecycleError(err)
	}
	span.SetAttributes(
		attribute.String(attrs.SandboxID, inst.ID),
		attribute.String(attrs.SandboxBackend, inst.Backend),
		attribute.String(attrs.SandboxImage, inst.Config.Image),
		attribute.String(attrs.AgentSessionID, sessionID),
		attribute.String(attrs.TenantID, tenantID),
	)
	telemetry.AddSpanEvent(span, attrs.EventSandboxReady, attribute.String("sandbox.status", string(inst.Status)))

	// Auto-grant SSH access to the sandbox creator
	if s.sshKeyStore != nil {
		userID := s.resolveUserID(ctx)
		if err := s.sshKeyStore.GrantAccess(ctx, inst.ID, userID, tenantID, userID); err != nil {
			logger.WithFields("sandbox_id", inst.ID, "user_id", userID, "tenant_id", tenantID, "error", err.Error()).
				Warn("RecreateSandbox: failed to auto-grant SSH access")
		}
	}

	return connect.NewResponse(&agentspb.CreateSandboxResponse{
		Id:          inst.ID,
		SessionId:   sessionID,
		TenantId:    tenantID,
		ContainerId: inst.ContainerID,
		Status:      string(inst.Status),
		Backend:     publicBackendLabel(inst.Backend),
		Image:       inst.Config.Image,
		CreatedAt:   inst.CreatedAt.Format(time.RFC3339),
		ExpiresAt:   inst.ExpiresAt.Format(time.RFC3339),
		Name:        inst.Name,
	}), nil
}

// ListSandboxTemplates implements AgentsServiceHandler.ListSandboxTemplates via ConnectRPC.
func (s *Server) ListSandboxTemplates(_ context.Context, _ *connect.Request[agentspb.ListSandboxTemplatesRequest]) (*connect.Response[agentspb.ListSandboxTemplatesResponse], error) {
	templates := sandbox.ListTemplates()
	out := make([]*agentspb.SandboxTemplate, 0, len(templates))
	for _, t := range templates {
		out = append(out, sandboxTemplateToProto(t))
	}
	return connect.NewResponse(&agentspb.ListSandboxTemplatesResponse{
		Templates: out,
	}), nil
}

// GetSandboxTemplate implements AgentsServiceHandler.GetSandboxTemplate via ConnectRPC.
func (s *Server) GetSandboxTemplate(_ context.Context, req *connect.Request[agentspb.GetSandboxTemplateRequest]) (*connect.Response[agentspb.GetSandboxTemplateResponse], error) {
	t := sandbox.GetTemplate(req.Msg.GetTemplateId())
	if t == nil {
		return nil, connect.NewError(connect.CodeNotFound, errTemplateNotFound)
	}
	return connect.NewResponse(&agentspb.GetSandboxTemplateResponse{
		Template: sandboxTemplateToProto(*t),
	}), nil
}

// SetupMemory implements AgentsServiceHandler.SetupMemory via ConnectRPC.
func (s *Server) SetupMemory(ctx context.Context, _ *connect.Request[agentspb.SetupMemoryRequest]) (*connect.Response[agentspb.SetupMemoryResponse], error) {
	spanCtx, span := telemetry.StartGatewaySpan(ctx, "agents.memory.setup")
	ctx = spanCtx
	defer span.End()
	telemetry.AddSpanEvent(span, attrs.EventRequestReceived)

	if s.db == nil {
		err := connect.NewError(connect.CodeUnavailable, errDatabaseNotAvailable)
		telemetry.RecordError(span, err)
		return nil, err
	}

	if err := memory.EnsurePgVector(ctx, s.db); err != nil {
		logger.WithFields("error", err.Error()).Error("SetupMemory: pgvector setup failed")
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Create the store and wire it up if not already done
	if s.memoryStore == nil {
		pgStore, err := memory.NewPgVectorStore(s.db)
		if err != nil {
			logger.WithFields("error", err.Error()).Error("SetupMemory: store creation failed")
			telemetry.RecordError(span, err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		memStore := memory.NewTracedVectorStore(pgStore)

		if s.engine != nil {
			registry, router, providerErr := s.engine.ProvidersForContext(ctx)
			if providerErr != nil {
				telemetry.RecordError(span, providerErr)
				return nil, connect.NewError(connect.CodeInternal, providerErr)
			}
			memEmbedder := memory.NewTracedEmbedder(memory.NewEmbedder(registry, router))
			s.SetMemoryBackend(memStore, memEmbedder)
		} else {
			s.memoryStore = memStore
		}
	}
	span.SetAttributes(attribute.String("memory.backend", "pgvector"))
	telemetry.AddSpanEvent(span, attrs.EventRequestComplete)

	return connect.NewResponse(&agentspb.SetupMemoryResponse{
		Success: true,
		Message: "pgvector memory backend initialized",
		Backend: "pgvector",
	}), nil
}

// ── helpers ─────────────────────────────────────────────────────────

func sandboxTemplateToProto(t sandbox.Template) *agentspb.SandboxTemplate {
	return &agentspb.SandboxTemplate{
		Id:             t.ID,
		Name:           t.Name,
		Slug:           t.Slug,
		Description:    t.Description,
		Icon:           t.Icon,
		IconColor:      t.IconColor,
		Image:          t.Image,
		CpuLimit:       t.CPULimit,
		MemoryMb:       int64(t.MemoryMB),
		DiskMb:         int64(t.DiskMB),
		TimeoutSeconds: int32(t.TimeoutSecs),
		NetworkMode:    t.NetworkMode,
		EnvVars:        t.EnvVars,
		WorkDir:        t.WorkDir,
		Tags:           t.Tags,
	}
}

// Sentinel errors for ConnectRPC error responses.
var (
	errSandboxNotEnabled    = fmt.Errorf("sandbox feature is not enabled")
	errSandboxIDRequired    = fmt.Errorf("sandboxId is required")
	errTemplateNotFound     = fmt.Errorf("template not found")
	errDatabaseNotAvailable = fmt.Errorf("database not available")
)

func errUnknownTemplate(id string) error {
	return fmt.Errorf("unknown template: %s", id)
}
