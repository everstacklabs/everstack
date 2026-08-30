package v1

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	licenseCmd "github.com/everstacklabs/everstack/internal/commands/handlers/license"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	licpkg "github.com/everstacklabs/everstack/internal/license"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	licenseconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
	"github.com/golang-jwt/jwt/v5"
)

// CallbackSecretStore provides in-memory storage for upgrade callback secrets
type CallbackSecretStore struct {
	mu      sync.RWMutex
	secrets map[string]*callbackSecretEntry
}

type callbackSecretEntry struct {
	PlanTier      string
	BillingPeriod string
	ExpiresAt     time.Time
}

var callbackSecretStore = &CallbackSecretStore{
	secrets: make(map[string]*callbackSecretEntry),
}

func (s *CallbackSecretStore) Store(secret, planTier, billingPeriod string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt := time.Now().Add(30 * time.Minute)
	s.secrets[secret] = &callbackSecretEntry{PlanTier: planTier, BillingPeriod: billingPeriod, ExpiresAt: expiresAt}
	s.cleanup()
	return expiresAt
}

func (s *CallbackSecretStore) Get(secret string) (*callbackSecretEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.secrets[secret]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.ExpiresAt) {
		return entry, false
	}
	return entry, true
}

func (s *CallbackSecretStore) Delete(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, secret)
}

func (s *CallbackSecretStore) cleanup() {
	now := time.Now()
	for k, v := range s.secrets {
		if now.After(v.ExpiresAt) {
			delete(s.secrets, k)
		}
	}
}

// StoreUpgradeCallbackSecret stores a callback secret for the upgrade flow
func (s *Server) StoreUpgradeCallbackSecret(ctx context.Context, req *connect.Request[gatewaypb.StoreUpgradeCallbackSecretRequest]) (*connect.Response[gatewaypb.StoreUpgradeCallbackSecretResponse], error) {
	logger := logger.WithFields("procedure", "StoreUpgradeCallbackSecret")
	if req.Msg.GetCallbackSecret() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("callback_secret is required"))
	}
	if req.Msg.GetPlanTier() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("plan_tier is required"))
	}
	billingPeriod := req.Msg.GetBillingPeriod()
	if billingPeriod == "" {
		billingPeriod = "monthly"
	}
	expiresAt := callbackSecretStore.Store(req.Msg.GetCallbackSecret(), req.Msg.GetPlanTier(), billingPeriod)
	logger.WithFields(map[string]interface{}{
		"plan_tier": req.Msg.GetPlanTier(), "billing_period": billingPeriod,
		"expires_at": expiresAt.Format(time.RFC3339),
	}).Info("stored upgrade callback secret")
	return connect.NewResponse(&gatewaypb.StoreUpgradeCallbackSecretResponse{
		Success: true, ExpiresAt: expiresAt.Format(time.RFC3339),
	}), nil
}

// GetUpgradeCallbackSecret retrieves a callback secret for verification
func (s *Server) GetUpgradeCallbackSecret(ctx context.Context, req *connect.Request[gatewaypb.GetUpgradeCallbackSecretRequest]) (*connect.Response[gatewaypb.GetUpgradeCallbackSecretResponse], error) {
	if req.Msg.GetCallbackSecret() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("callback_secret is required"))
	}
	entry, found := callbackSecretStore.Get(req.Msg.GetCallbackSecret())
	resp := &gatewaypb.GetUpgradeCallbackSecretResponse{Found: found}
	if entry != nil {
		resp.PlanTier = entry.PlanTier
		resp.BillingPeriod = entry.BillingPeriod
		resp.Expired = !found && entry != nil
	}
	return connect.NewResponse(resp), nil
}

// ActivationCallbackClaims represents the JWT claims for activation callbacks
type ActivationCallbackClaims struct {
	ActivationToken string `json:"activation_token"`
	Nonce           string `json:"nonce"`
	jwt.RegisteredClaims
}

// ActivationCallback handles activation callbacks from cloud after successful payment
func (s *Server) ActivationCallback(ctx context.Context, req *connect.Request[gatewaypb.ActivationCallbackRequest]) (*connect.Response[gatewaypb.ActivationCallbackResponse], error) {
	logger := logger.WithFields("procedure", "ActivationCallback")

	if req.Msg.GetSignedPayload() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("signed_payload is required"))
	}
	if req.Msg.GetCallbackSecret() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("callback_secret is required"))
	}

	if _, found := callbackSecretStore.Get(req.Msg.GetCallbackSecret()); !found {
		logger.Warn("callback secret not found or expired")
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid or expired callback secret"))
	}

	// Verify JWT
	cloudPublicKeyB64, ok := s.ctx.Value(contextkeys.CloudPublicKey).(string)
	if !ok || cloudPublicKeyB64 == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("cloud public key not configured"))
	}
	publicKeyBytes, err := base64.StdEncoding.DecodeString(cloudPublicKeyB64)
	if err != nil || len(publicKeyBytes) != ed25519.PublicKeySize {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid cloud public key"))
	}
	publicKey := ed25519.PublicKey(publicKeyBytes)

	token, err := jwt.ParseWithClaims(req.Msg.GetSignedPayload(), &ActivationCallbackClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return publicKey, nil
	})
	if err != nil {
		logger.WithError(err).Warn("failed to verify JWT signature")
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid signature"))
	}
	claims, ok := token.Claims.(*ActivationCallbackClaims)
	if !ok || !token.Valid {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid token claims"))
	}
	if claims.Nonce != req.Msg.GetCallbackSecret() {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("nonce mismatch"))
	}

	activationToken := claims.ActivationToken
	if activationToken == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("missing activation token"))
	}

	logger.Info("activation callback JWT verified, proceeding with activation")

	instanceMgr := enterprise.InstanceManagerFromContext(s.ctx)

	licenseServiceURL, ok := s.ctx.Value(contextkeys.LicenseServiceURL).(string)
	if !ok || licenseServiceURL == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("license service URL not configured"))
	}

	// Get instance ID
	instanceID, _, err := instanceMgr.GetInstanceId(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to get instance ID"))
	}
	if instanceID == "" {
		instanceID, _ = instanceMgr.GetLocalInstanceId(ctx)
	}
	if instanceID == "" {
		instanceID, err = instanceMgr.EnsureLocalInstanceId(ctx)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to ensure local instance ID"))
		}
	}
	if instanceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("gateway instance ID not found"))
	}

	// Call License Service to activate
	httpClient := &http.Client{Timeout: 60 * time.Second}
	licenseClient := licenseconnect.NewInstanceServiceClient(httpClient, licenseServiceURL)

	deviceFingerprint := ""
	if fp, ok := s.ctx.Value(contextkeys.DeviceFingerprint).(string); ok {
		deviceFingerprint = fp
	}

	activateCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	activateResp, err := licenseClient.ActivateInstance(activateCtx, connect.NewRequest(&licv1.ActivateInstanceRequest{
		ActivationToken: activationToken, DeviceFingerprintHash: deviceFingerprint,
		InstanceId: instanceID,
	}))
	if err != nil {
		logger.WithError(err).Error("license service activation failed")
		return nil, connect.NewError(connect.CodeInternal, errors.New("activation failed"))
	}
	if activateResp == nil || activateResp.Msg == nil || activateResp.Msg.GetLicenseState() == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("empty response from license service"))
	}

	activatedInstanceID := activateResp.Msg.GetInstanceId()
	refreshToken := activateResp.Msg.GetRefreshToken()
	licenseState := activateResp.Msg.GetLicenseState()
	signingKey := activateResp.Msg.GetSigningKey()
	licenseJWT := activateResp.Msg.GetLicenseJwt()
	licensePublicKey := activateResp.Msg.GetLicensePublicKey()

	// Store activation data
	storeOpts := enterprise.StoreActivationOpts{LicenseJWT: licenseJWT, LicensePublicKey: licensePublicKey}
	if err := instanceMgr.StoreActivation(ctx, activationToken, activatedInstanceID, refreshToken, signingKey, licenseState, storeOpts); err != nil {
		logger.WithError(err).Error("failed to store activation locally")
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to store activation"))
	}

	// Dispatch activation event
	if sys, err := cqrs.GetSystemFromContext(s.ctx); err == nil && sys != nil && sys.CommandBus != nil {
		expiresAt := licenseState.ExpiresAt.AsTime()
		cmd := licenseCmd.NewActivateInstanceCommand(activatedInstanceID, licenseState.TenantId, licenseState.PlanTier.String(), "", expiresAt, "", "")
		_ = sys.CommandBus.Dispatch(ctx, cmd)
	}

	// Update enforcer
	tier, status, isPaid, expiresAt, trialExpires := convertProtoLicenseState(licenseState)
	le := enterprise.LicenseEnforcerFromContext(s.ctx)
	le.SetCachedState(&enterprise.LicenseState{
		Active: status == "active", Status: status, Tier: tier,
		IsPaid: isPaid, ExpiresAt: expiresAt, TrialExpires: trialExpires,
	})

	// A configured license public key is the trust anchor. Ignore an
	// activation-supplied key when that anchor is pinned.
	if licenseJWT != "" && !le.IsKeyPinned() && licensePublicKey != "" {
		if verifier, err := licpkg.NewVerifier(licensePublicKey); err == nil {
			le.SetVerifier(verifier)
			if claims, err := verifier.Verify(licenseJWT); err == nil {
				le.SetCachedJWT(licenseJWT, claims)
			}
		}
	}

	// Update monitor
	monitor := enterprise.LicenseMonitorFromContext(s.ctx)
	monitor.SetOrganizationAndInstanceID(licenseState.TenantId, activatedInstanceID)
	monitor.Refresh()

	if signingKey != "" {
		signingKeyBytes, err := base64.StdEncoding.DecodeString(signingKey)
		if err == nil && len(signingKeyBytes) > 0 {
			monitor.SetM2MCredentials(signingKeyBytes)
			if pm, ok := monitor.(enterprise.PersistentMonitor); ok {
				pm.SetSyncerCredentials(activatedInstanceID, refreshToken, signingKeyBytes)
			}
		}
	}

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

	callbackSecretStore.Delete(req.Msg.GetCallbackSecret())

	logger.WithFields(map[string]interface{}{
		"instance_id": activatedInstanceID, "tenant_id": licenseState.TenantId,
		"plan_tier": licenseState.PlanTier.String(),
	}).Info("gateway activated via callback successfully")

	return connect.NewResponse(&gatewaypb.ActivationCallbackResponse{
		Success: true, InstanceId: activatedInstanceID,
		PlanTier: licenseState.PlanTier.String(), Message: "Gateway activated successfully",
	}), nil
}

// Classic gRPC wrappers
func (g *GrpcServer) StoreUpgradeCallbackSecret(ctx context.Context, req *gatewaypb.StoreUpgradeCallbackSecretRequest) (*gatewaypb.StoreUpgradeCallbackSecretResponse, error) {
	resp, err := g.base.StoreUpgradeCallbackSecret(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetUpgradeCallbackSecret(ctx context.Context, req *gatewaypb.GetUpgradeCallbackSecretRequest) (*gatewaypb.GetUpgradeCallbackSecretResponse, error) {
	resp, err := g.base.GetUpgradeCallbackSecret(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ActivationCallback(ctx context.Context, req *gatewaypb.ActivationCallbackRequest) (*gatewaypb.ActivationCallbackResponse, error) {
	resp, err := g.base.ActivationCallback(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// SubscriptionStatusCallbackClaims represents the JWT claims for subscription status callbacks
type SubscriptionStatusCallbackClaims struct {
	OrganizationID    string `json:"organization_id"`
	CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
	CurrentPeriodEnd  string `json:"current_period_end,omitempty"`
	Status            string `json:"status"`
	PlanTier          string `json:"plan_tier"`
	EventType         string `json:"event_type"`
	jwt.RegisteredClaims
}

// SubscriptionStatusCallback handles subscription status updates from billing service
func (s *Server) SubscriptionStatusCallback(ctx context.Context, req *connect.Request[gatewaypb.SubscriptionStatusCallbackRequest]) (*connect.Response[gatewaypb.SubscriptionStatusCallbackResponse], error) {
	logger := logger.WithFields("procedure", "SubscriptionStatusCallback")

	if req.Msg.GetSignedPayload() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("signed_payload is required"))
	}

	cloudPublicKeyB64, ok := s.ctx.Value(contextkeys.CloudPublicKey).(string)
	if !ok || cloudPublicKeyB64 == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("cloud public key not configured"))
	}
	publicKeyBytes, err := base64.StdEncoding.DecodeString(cloudPublicKeyB64)
	if err != nil || len(publicKeyBytes) != ed25519.PublicKeySize {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid cloud public key"))
	}
	publicKey := ed25519.PublicKey(publicKeyBytes)

	token, err := jwt.ParseWithClaims(req.Msg.GetSignedPayload(), &SubscriptionStatusCallbackClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return publicKey, nil
	})
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid signature"))
	}
	claims, ok := token.Claims.(*SubscriptionStatusCallbackClaims)
	if !ok || !token.Valid {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid token claims"))
	}

	logger.WithFields(map[string]interface{}{
		"organization_id": claims.OrganizationID, "event_type": claims.EventType,
		"cancel_at_period_end": claims.CancelAtPeriodEnd, "status": claims.Status,
		"plan_tier": claims.PlanTier,
	}).Info("subscription status callback received")

	instanceID := enterprise.LicenseMonitorFromContext(s.ctx).GetInstanceID()

	var currentPeriodEnd time.Time
	if claims.CurrentPeriodEnd != "" {
		if parsed, err := time.Parse(time.RFC3339, claims.CurrentPeriodEnd); err == nil {
			currentPeriodEnd = parsed
		}
	}

	// Refresh enforcer and monitor
	enterprise.LicenseEnforcerFromContext(s.ctx).RefreshNow()
	enterprise.LicenseMonitorFromContext(s.ctx).Refresh()

	// Dispatch subscription status event
	if sys, err := cqrs.GetSystemFromContext(s.ctx); err == nil && sys != nil && sys.CommandBus != nil {
		cmd := licenseCmd.NewSubscriptionStatusChangedCommand(
			claims.OrganizationID, instanceID, claims.PlanTier, claims.Status,
			claims.EventType, claims.CancelAtPeriodEnd, currentPeriodEnd, "",
		)
		_ = sys.CommandBus.Dispatch(ctx, cmd)
	}

	return connect.NewResponse(&gatewaypb.SubscriptionStatusCallbackResponse{
		Success: true, Message: "Subscription status update received",
	}), nil
}

func (g *GrpcServer) SubscriptionStatusCallback(ctx context.Context, req *gatewaypb.SubscriptionStatusCallbackRequest) (*gatewaypb.SubscriptionStatusCallbackResponse, error) {
	resp, err := g.base.SubscriptionStatusCallback(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
