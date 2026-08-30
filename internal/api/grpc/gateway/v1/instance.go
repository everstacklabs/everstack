package v1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/billingcredit"
	"github.com/everstacklabs/everstack/internal/billingidentity"
	licenseCmd "github.com/everstacklabs/everstack/internal/commands/handlers/license"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/retrypolicy"
	"github.com/everstacklabs/everstack/internal/services/trial"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	licenseconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
	"github.com/everstacklabs/everstack/pkg/tenant"
	"github.com/jmoiron/sqlx"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// isManagedInstance returns true when the gateway is running in managed/cloud
// mode. Checks three orthogonal signals:
//  1. SharedGatewayMode on the server context (multi-tenant shared gateway)
//  2. TenantConfig on the request context (per-request tenant middleware)
//  3. EVS_AUTH_SERVICE_URL env var (single-tenant managed deployment)
func (s *Server) isManagedInstance(ctx context.Context) (*tenant.Config, bool) {
	if isShared, _ := s.ctx.Value(contextkeys.SharedGatewayMode).(bool); isShared {
		tc := tenant.ConfigFromContext(ctx)
		return tc, true
	}
	if tc := tenant.ConfigFromContext(ctx); tc != nil {
		return tc, true
	}
	if os.Getenv("EVS_AUTH_SERVICE_URL") != "" {
		return nil, true
	}
	return nil, false
}

// ActivateGatewayInstance handles activation from the Admin UI
func (s *Server) ActivateGatewayInstance(ctx context.Context, req *connect.Request[gatewaypb.ActivateGatewayInstanceRequest]) (*connect.Response[gatewaypb.ActivateGatewayInstanceResponse], error) {
	logger := logger.WithFields("procedure", "ActivateGatewayInstance")

	if req.Msg.GetActivationToken() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errMissingField("activation_token"))
	}

	instanceMgr := enterprise.InstanceManagerFromContext(s.ctx)

	// Get license service URL from server context
	licenseServiceURL, ok := s.ctx.Value(contextkeys.LicenseServiceURL).(string)
	if !ok || licenseServiceURL == "" {
		logger.Error("license service URL not available in context")
		return nil, connect.NewError(connect.CodeInternal, errInternal("license service URL not configured"))
	}

	// Check if already activated (skip this check if instance_id is provided for upgrade)
	isUpgrade := req.Msg.GetInstanceId() != ""
	if !isUpgrade {
		isActivated, err := instanceMgr.IsActivated(ctx)
		if err != nil {
			logger.WithError(err).Error("failed to check activation status")
		} else if isActivated {
			return nil, connect.NewError(connect.CodeAlreadyExists, errAlreadyActivated())
		}
	}

	// Call License Service to activate
	httpClient := &http.Client{Timeout: 60 * time.Second}
	licenseClient := licenseconnect.NewInstanceServiceClient(httpClient, licenseServiceURL)

	// Get device fingerprint: prefer request value, fall back to server context
	deviceFingerprint := req.Msg.GetDeviceFingerprintHash()
	if deviceFingerprint == "" {
		if fp, ok := s.ctx.Value(contextkeys.DeviceFingerprint).(string); ok && fp != "" {
			deviceFingerprint = fp
			logger.Debug("using device fingerprint from server context")
		}
	}

	activateReq := connect.NewRequest(&licv1.ActivateInstanceRequest{
		ActivationToken:       req.Msg.GetActivationToken(),
		DeviceFingerprintHash: deviceFingerprint,
		InstanceId:            req.Msg.GetInstanceId(),
	})

	activateCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	const maxRetries = 3
	var activateResp *connect.Response[licv1.ActivateInstanceResponse]
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			logger.WithFields(map[string]interface{}{
				"attempt":         attempt + 1,
				"backoff_seconds": backoff.Seconds(),
			}).Info("retrying license service activation")
			select {
			case <-activateCtx.Done():
				return nil, connect.NewError(connect.CodeDeadlineExceeded, activateCtx.Err())
			case <-time.After(backoff):
			}
		}

		activateResp, err = licenseClient.ActivateInstance(activateCtx, activateReq)
		if err == nil {
			break
		}

		if !retrypolicy.IsRetryable(err) || attempt == maxRetries-1 {
			logger.WithError(err).WithFields(map[string]interface{}{
				"attempt":   attempt + 1,
				"retryable": retrypolicy.IsRetryable(err),
			}).Error("license service activation failed")
			if connectErr, ok := err.(*connect.Error); ok {
				return nil, connectErr
			}
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}

		logger.WithError(err).WithFields(map[string]interface{}{
			"attempt": attempt + 1,
		}).Warn("license service activation failed, will retry")
	}

	if activateResp == nil || activateResp.Msg == nil || activateResp.Msg.GetLicenseState() == nil {
		logger.Error("empty response from license service")
		return nil, connect.NewError(connect.CodeInternal, errInternal("empty response from license service"))
	}

	instanceID := activateResp.Msg.GetInstanceId()
	refreshToken := activateResp.Msg.GetRefreshToken()
	licenseState := activateResp.Msg.GetLicenseState()
	signingKey := activateResp.Msg.GetSigningKey()

	// Persist the license JWT + public key alongside the activation. Without
	// them, signed_payload stores "{}" and a restart cannot reconstruct the
	// license state offline (the grace window anchors on the persisted JWT's
	// exp claim — editions-and-billing.md section 5).
	if err := instanceMgr.StoreActivation(ctx, req.Msg.GetActivationToken(), instanceID, refreshToken, signingKey, licenseState,
		enterprise.StoreActivationOpts{
			LicenseJWT:       activateResp.Msg.GetLicenseJwt(),
			LicensePublicKey: activateResp.Msg.GetLicensePublicKey(),
		}); err != nil {
		logger.WithError(err).Error("failed to store activation locally")
		return nil, connect.NewError(connect.CodeInternal, errInternal("failed to store activation"))
	}

	// Dispatch user-facing activation event
	if sys, err := cqrs.GetSystemFromContext(s.ctx); err == nil && sys != nil && sys.CommandBus != nil {
		expiresAt := licenseState.ExpiresAt.AsTime()
		userEvent := licenseCmd.NewActivateInstanceCommand(
			instanceID, licenseState.TenantId, licenseState.PlanTier.String(),
			"", expiresAt, "", "",
		)
		if err := sys.CommandBus.Dispatch(ctx, userEvent); err != nil {
			logger.WithError(err).Warn("failed to dispatch user-facing activation event")
		}
	}

	// Convert license state and update enforcer cache
	tier, status, isPaid, expiresAt, trialExpires := convertProtoLicenseState(licenseState)
	enforcerState := &enterprise.LicenseState{
		Active: status == "active", Status: status, Tier: tier,
		IsPaid: isPaid, ExpiresAt: expiresAt, TrialExpires: trialExpires,
	}

	le := enterprise.LicenseEnforcerFromContext(s.ctx)
	le.SetCachedState(enforcerState)

	// Update monitor
	monitor := enterprise.LicenseMonitorFromContext(s.ctx)
	monitor.SetOrganizationAndInstanceID(licenseState.TenantId, instanceID)
	monitor.Refresh()

	// Set M2M credentials
	if signingKey != "" {
		signingKeyBytes, err := base64.StdEncoding.DecodeString(signingKey)
		if err == nil && len(signingKeyBytes) > 0 {
			monitor.SetM2MCredentials(signingKeyBytes)
			if pm, ok := monitor.(enterprise.PersistentMonitor); ok {
				pm.SetSyncerCredentials(instanceID, refreshToken, signingKeyBytes)
				logger.Debug("license_monitor: set usage syncer credentials after activation")
			}
		}
	}

	// Store available features
	if protoFeatures := activateResp.Msg.GetAvailableFeatures(); len(protoFeatures) > 0 {
		features := make(map[string]*enterprise.FeatureRelease, len(protoFeatures))
		for key, pf := range protoFeatures {
			features[key] = &enterprise.FeatureRelease{
				Name: pf.GetName(), Description: pf.GetDescription(),
				Status: pf.GetStatus(), Categories: pf.GetCategories(),
			}
		}
		monitor.SetAvailableFeatures(features)
	}

	logger.WithFields(map[string]interface{}{"instance_id": instanceID, "tenant_id": licenseState.TenantId}).Info("gateway activated successfully")

	resp := &gatewaypb.ActivateGatewayInstanceResponse{
		Status: "activated", InstanceId: instanceID,
		TenantId: licenseState.TenantId, PlanTier: licenseState.PlanTier.String(),
		ActivatedAt: timestamppb.Now(),
	}
	if licenseState.ExpiresAt != nil {
		resp.ExpiresAt = licenseState.ExpiresAt
	}
	return connect.NewResponse(resp), nil
}

// GetGatewayInstanceStatus returns the current activation status
func (s *Server) GetGatewayInstanceStatus(ctx context.Context, req *connect.Request[gatewaypb.GetGatewayInstanceStatusRequest]) (*connect.Response[gatewaypb.GetGatewayInstanceStatusResponse], error) {
	logger := logger.WithFields("procedure", "GetGatewayInstanceStatus")

	if tc, managed := s.isManagedInstance(ctx); managed {
		instanceID := os.Getenv("EVS_INSTANCE_ID")
		if tc != nil {
			instanceID = tc.InstanceID
		}
		return connect.NewResponse(&gatewaypb.GetGatewayInstanceStatusResponse{
			Status: "active", Activated: true, InstanceId: instanceID,
			Message: "Managed instance (cloud)",
		}), nil
	}

	instanceMgr := enterprise.InstanceManagerFromContext(s.ctx)

	isActivated, err := instanceMgr.IsActivated(ctx)
	if err != nil {
		logger.WithError(err).Error("failed to check activation status")
		return nil, connect.NewError(connect.CodeInternal, errInternal("failed to check activation status"))
	}

	if !isActivated {
		localInstanceId, err := instanceMgr.GetLocalInstanceId(ctx)
		if err != nil {
			logger.WithError(err).Warn("failed to get local instance ID")
		}
		return connect.NewResponse(&gatewaypb.GetGatewayInstanceStatusResponse{
			Status: "inactive", Activated: false, InstanceId: localInstanceId,
			Message: "Gateway not activated. Please provide an activation token.",
		}), nil
	}

	info, err := instanceMgr.GetActiveInstance(ctx)
	if err != nil {
		logger.WithError(err).Error("failed to get instance info")
		return nil, connect.NewError(connect.CodeInternal, errInternal("failed to get instance info"))
	}

	var licenseState licv1.LicenseState
	if err := json.Unmarshal(info.LicenseState, &licenseState); err != nil {
		logger.WithError(err).Warn("failed to parse license state")
		return connect.NewResponse(&gatewaypb.GetGatewayInstanceStatusResponse{
			Status: "active", Activated: true, InstanceId: info.InstanceID,
			ActivatedAt: timestamppb.New(info.ActivatedAt), Message: "Gateway is activated",
		}), nil
	}

	return connect.NewResponse(&gatewaypb.GetGatewayInstanceStatusResponse{
		Status: "active", Activated: true, InstanceId: info.InstanceID,
		ActivatedAt: timestamppb.New(info.ActivatedAt), LicenseState: &licenseState,
		Message: "Gateway is activated and operational",
	}), nil
}

// Classic gRPC wrappers
func (g *GrpcServer) ActivateGatewayInstance(ctx context.Context, req *gatewaypb.ActivateGatewayInstanceRequest) (*gatewaypb.ActivateGatewayInstanceResponse, error) {
	resp, err := g.base.ActivateGatewayInstance(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetGatewayInstanceStatus(ctx context.Context, req *gatewaypb.GetGatewayInstanceStatusRequest) (*gatewaypb.GetGatewayInstanceStatusResponse, error) {
	resp, err := g.base.GetGatewayInstanceStatus(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// GetTrialStatus returns the current trial mode status
func (s *Server) GetTrialStatus(ctx context.Context, req *connect.Request[gatewaypb.GetTrialStatusRequest]) (*connect.Response[gatewaypb.GetTrialStatusResponse], error) {
	if _, managed := s.isManagedInstance(ctx); managed {
		return connect.NewResponse(&gatewaypb.GetTrialStatusResponse{
			Mode: "licensed", Active: false, Message: "Managed instance (cloud)",
		}), nil
	}

	le := enterprise.LicenseEnforcerFromContext(s.ctx)

	// Check if there's an active license
	if cached := le.GetCached(); cached != nil && cached.Active {
		return connect.NewResponse(&gatewaypb.GetTrialStatusResponse{
			Mode: "licensed", Active: false, Message: "Gateway is operating with a valid license",
		}), nil
	}

	if !le.IsInTrialMode() {
		return connect.NewResponse(&gatewaypb.GetTrialStatusResponse{
			Mode: "disabled", Active: false, Message: "No license or trial available",
		}), nil
	}

	// Trial is active — get details via trial manager
	trialMgr, ok := le.GetTrialManager().(*trial.Manager)
	if !ok || trialMgr == nil {
		return connect.NewResponse(&gatewaypb.GetTrialStatusResponse{
			Mode: "trial", Active: true, Message: "Gateway is running in trial mode",
		}), nil
	}

	status := trialMgr.GetStatus()
	resp := &gatewaypb.GetTrialStatusResponse{
		Mode: "trial", Active: trialMgr.IsActive(),
	}
	if trialMgr.IsExpired() {
		resp.Expired = true
		resp.Message = "Trial period has expired"
	} else {
		resp.Message = "Gateway is running in trial mode"
	}

	if createdAt, ok := status["created_at"].(string); ok && createdAt != "" {
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			resp.CreatedAt = timestamppb.New(t)
		}
	}
	if expiresAt, ok := status["expires_at"].(string); ok && expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			resp.ExpiresAt = timestamppb.New(t)
		}
	}
	if v, ok := status["days_remaining"].(int); ok {
		resp.DaysRemaining = int32(v)
	}
	if v, ok := status["daily_used"].(int); ok {
		resp.DailyUsed = int32(v)
	}
	if v, ok := status["daily_limit"].(int); ok {
		resp.DailyLimit = int32(v)
	}
	if v, ok := status["total_requests"].(int64); ok {
		resp.TotalRequests = v
	}
	if v, ok := status["fingerprint_hash"].(string); ok {
		resp.FingerprintHash = v
	}
	if v, ok := status["tokens_used"].(int64); ok {
		resp.TokensUsed = v
	}
	if v, ok := status["token_limit"].(int64); ok {
		resp.TokenLimit = v
	}
	if v, ok := status["rpm_limit"].(int64); ok {
		resp.RpmLimit = v
	}
	return connect.NewResponse(resp), nil
}

func (g *GrpcServer) GetTrialStatus(ctx context.Context, req *gatewaypb.GetTrialStatusRequest) (*gatewaypb.GetTrialStatusResponse, error) {
	resp, err := g.base.GetTrialStatus(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// GetLicenseMonitorStatus returns the Gateway's internal license and usage state
func (s *Server) GetLicenseMonitorStatus(ctx context.Context, req *connect.Request[gatewaypb.GetLicenseMonitorStatusRequest]) (*connect.Response[gatewaypb.GetLicenseMonitorStatusResponse], error) {
	if tc, managed := s.isManagedInstance(ctx); managed {
		tenantID := contextkeys.GetTenantID(ctx)
		instanceID := ""
		if requestInstance, ok := contextkeys.RequestInstanceScopeFromContext(ctx); ok {
			tenantID = requestInstance.OrganizationID
			instanceID = requestInstance.InstanceID
		}
		if tc != nil && tc.OrganizationID != "" {
			tenantID = tc.OrganizationID
			instanceID = tc.InstanceID
		}
		return connect.NewResponse(&gatewaypb.GetLicenseMonitorStatusResponse{
			License: &gatewaypb.LicenseInfo{
				Active: true, Tier: "cloud", Status: "active", IsPaid: true,
				TenantId:              tenantID,
				InstanceId:            instanceID,
				SandboxBillingEnabled: s.managedSandboxBillingEnabled(ctx, tenantID),
			},
			Gateway: &gatewaypb.GatewayStatus{Locked: false},
		}), nil
	}

	monitor := enterprise.LicenseMonitorFromContext(s.ctx)
	return connect.NewResponse(buildLicenseMonitorResponse(monitor)), nil
}

func (s *Server) managedSandboxBillingEnabled(ctx context.Context, tenantID string) bool {
	if tenantID == "" {
		return false
	}
	// Sandbox entitlement lives in billing.* on the platform DB. On cloud the
	// gateway PrimaryDB is the per-tenant gateway DB and lacks that schema, so
	// prefer BillingDB; fall back to PrimaryDB for CE where both collapse to one.
	db, ok := s.ctx.Value(contextkeys.BillingDB).(*sqlx.DB)
	if !ok || db == nil {
		db, ok = s.ctx.Value(contextkeys.PrimaryDB).(*sqlx.DB)
	}
	if !ok || db == nil {
		return false
	}
	organization, err := billingidentity.ResolveActiveOrganization(ctx, db, tenantID)
	if err != nil {
		logger.WithFields("tenant_id", tenantID, "error", err.Error()).
			Warn("gateway: failed to resolve organization plan for sandbox billing")
		return false
	}
	access, err := billingcredit.Resolve(ctx, db, organization.ID, organization.Tier)
	if err != nil {
		logger.WithFields("tenant_id", tenantID, "error", err.Error()).
			Warn("gateway: sandbox billing entitlement lookup failed closed")
		return false
	}
	return access.Allowed
}

func (g *GrpcServer) GetLicenseMonitorStatus(ctx context.Context, req *gatewaypb.GetLicenseMonitorStatusRequest) (*gatewaypb.GetLicenseMonitorStatusResponse, error) {
	resp, err := g.base.GetLicenseMonitorStatus(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// RefreshLicenseMonitor forces a refresh of the license state and returns updated status
func (s *Server) RefreshLicenseMonitor(ctx context.Context, req *connect.Request[gatewaypb.RefreshLicenseMonitorRequest]) (*connect.Response[gatewaypb.RefreshLicenseMonitorResponse], error) {
	enterprise.LicenseEnforcerFromContext(s.ctx).RefreshNow()

	monitor := enterprise.LicenseMonitorFromContext(s.ctx)
	monitor.Refresh()

	status := buildLicenseMonitorResponse(monitor)
	return connect.NewResponse(&gatewaypb.RefreshLicenseMonitorResponse{
		License: status.License, Usage: status.Usage,
		Gateway: status.Gateway, AvailableFeatures: status.AvailableFeatures,
	}), nil
}

func (g *GrpcServer) RefreshLicenseMonitor(ctx context.Context, req *gatewaypb.RefreshLicenseMonitorRequest) (*gatewaypb.RefreshLicenseMonitorResponse, error) {
	resp, err := g.base.RefreshLicenseMonitor(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// buildLicenseMonitorResponse creates the response from monitor state
func buildLicenseMonitorResponse(monitor enterprise.LicenseMonitor) *gatewaypb.GetLicenseMonitorStatusResponse {
	state := monitor.GetLicenseState()
	usage := monitor.GetUsageStats()
	locked, lockReason := monitor.IsLocked()
	spendBlocked, spendBlockedReason := monitor.IsSpendBlocked()

	resp := &gatewaypb.GetLicenseMonitorStatusResponse{
		Usage: &gatewaypb.UsageInfo{
			Rpm: usage.RPM, Rps: usage.RPS, Rph: usage.RPH,
			TotalRequests: usage.TotalRequests, LastReset: timestamppb.New(usage.LastReset),
			RequestsInMin: usage.RequestsInMin, RequestsInSec: usage.RequestsInSec,
			RequestsInHour:   usage.RequestsInHour,
			TotalInputTokens: usage.TotalInputTokens, TotalOutputTokens: usage.TotalOutputTokens,
			TotalTokens:      usage.TotalTokens,
			EstimatedCostUsd: usage.EstimatedCostUSD, CacheSavingsUsd: usage.CacheSavingsUSD,
			CacheHits: usage.CacheHits, CacheMisses: usage.CacheMisses,
		},
		Gateway: &gatewaypb.GatewayStatus{
			Locked: locked, LockReason: lockReason,
			Features:           []*gatewaypb.FeatureStatus{},
			SpendBlocked:       spendBlocked,
			SpendBlockedReason: spendBlockedReason,
			Edition:            enterprise.Edition(),
		},
	}

	if state != nil {
		resp.License = &gatewaypb.LicenseInfo{
			Active: state.Active, Tier: state.Tier, Status: state.Status,
			IsPaid: state.IsPaid, FetchedAt: timestamppb.New(state.FetchedAt),
			TenantId: monitor.GetOrganizationID(), InstanceId: monitor.GetInstanceID(),
			SandboxBillingEnabled: state.SandboxBillingEnabled,
		}
		if state.ExpiresAt != nil {
			resp.License.ExpiresAt = timestamppb.New(*state.ExpiresAt)
		}
		if state.TrialExpires != nil {
			resp.License.TrialExpires = timestamppb.New(*state.TrialExpires)
		}
		for _, limit := range state.UsageLimits {
			resp.License.UsageLimits = append(resp.License.UsageLimits, &gatewaypb.UsageLimit{
				Type: string(limit.Type), Limit: limit.Limit,
			})
		}
	}

	availableFeatures := monitor.GetAvailableFeatures()
	if len(availableFeatures) > 0 {
		resp.AvailableFeatures = make(map[string]*gatewaypb.FeatureRelease, len(availableFeatures))
		for key, f := range availableFeatures {
			resp.AvailableFeatures[key] = &gatewaypb.FeatureRelease{
				Name: f.Name, Description: f.Description,
				Status: f.Status, Categories: f.Categories,
			}
		}
	}
	return resp
}

// GetPlans returns available license plans from the license service
func (s *Server) GetPlans(ctx context.Context, req *connect.Request[gatewaypb.GetPlansRequest]) (*connect.Response[gatewaypb.GetPlansResponse], error) {
	licenseServiceURL, ok := s.ctx.Value(contextkeys.LicenseServiceURL).(string)
	if !ok || licenseServiceURL == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("license service URL not configured"))
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	licenseClient := licenseconnect.NewLicenseServiceClient(httpClient, licenseServiceURL)

	plansResp, err := licenseClient.GetPlans(ctx, connect.NewRequest(&licv1.GetPlansRequest{}))
	if err != nil {
		logger.WithError(err).Warn("failed to fetch plans from license service")
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to fetch plans from license service"))
	}

	resp := &gatewaypb.GetPlansResponse{
		Plans: make([]*gatewaypb.Plan, 0, len(plansResp.Msg.GetPlans())),
	}
	for _, p := range plansResp.Msg.GetPlans() {
		plan := &gatewaypb.Plan{
			Tier: p.GetTier(), Name: p.GetName(), Description: p.GetDescription(),
			TrialDurationDays: p.GetTrialDurationDays(), Highlight: p.GetHighlight(),
			SeatLimit:   p.GetSeatLimit(),
			Features:    make([]*gatewaypb.PlanFeature, 0, len(p.GetFeatures())),
			UsageLimits: make([]*gatewaypb.PlanUsageLimit, 0, len(p.GetUsageLimits())),
		}
		if p.GetPricing() != nil {
			plan.Pricing = &gatewaypb.PlanPricing{
				Monthly:    p.GetPricing().GetMonthly(),
				Yearly:     p.GetPricing().GetYearly(),
				Discounted: p.GetPricing().GetDiscounted(),
				Suggested:  p.GetPricing().GetSuggested(),
			}
			if ps := p.GetPricing().GetPerSeat(); ps != nil {
				plan.Pricing.PerSeat = &gatewaypb.PerSeatPricing{
					Monthly: ps.GetMonthly(),
					Yearly:  ps.GetYearly(),
					SubText: ps.GetSubText(),
				}
			}
		}
		for _, f := range p.GetFeatures() {
			plan.Features = append(plan.Features, &gatewaypb.PlanFeature{
				Name: f.GetName(), Enabled: f.GetEnabled(),
			})
		}
		for _, u := range p.GetUsageLimits() {
			plan.UsageLimits = append(plan.UsageLimits, &gatewaypb.PlanUsageLimit{
				Type: u.GetType(), Value: u.GetValue(), SubText: u.GetSubText(),
			})
		}
		resp.Plans = append(resp.Plans, plan)
	}
	return connect.NewResponse(resp), nil
}

func (g *GrpcServer) GetPlans(ctx context.Context, req *gatewaypb.GetPlansRequest) (*gatewaypb.GetPlansResponse, error) {
	resp, err := g.base.GetPlans(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// convertProtoLicenseState converts proto license state to enterprise types
func convertProtoLicenseState(ls *licv1.LicenseState) (tier, status string, isPaid bool, expiresAt, trialExpires *time.Time) {
	tier = "free"
	switch ls.PlanTier {
	case licv1.LicenseType_LICENSE_TYPE_BASIC:
		tier = "basic"
	case licv1.LicenseType_LICENSE_TYPE_PRO:
		tier = "pro"
	case licv1.LicenseType_LICENSE_TYPE_ENTERPRISE:
		tier = "enterprise"
	}
	isPaid = tier != "free"

	status = "active"
	switch ls.Status {
	case licv1.LicenseStatus_LICENSE_STATUS_INACTIVE:
		status = "inactive"
	case licv1.LicenseStatus_LICENSE_STATUS_EXPIRED:
		status = "expired"
	case licv1.LicenseStatus_LICENSE_STATUS_SUSPENDED:
		status = "suspended"
	case licv1.LicenseStatus_LICENSE_STATUS_CANCELLED:
		status = "cancelled"
	}

	if ls.ExpiresAt != nil {
		t := ls.ExpiresAt.AsTime()
		expiresAt = &t
		if !isPaid {
			trialExpires = &t
		}
	}
	return
}

// Error helpers
func errMissingField(field string) error { return errors.New("missing required field: " + field) }
func errInternal(message string) error   { return errors.New(message) }
func errAlreadyActivated() error         { return errors.New("gateway is already activated") }
