package license

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	LicenseStream = "licenses"
)

// LicenseCommandHandler handles license activation and refresh commands.
type LicenseCommandHandler struct{}

func NewLicenseCommandHandler() *LicenseCommandHandler { return &LicenseCommandHandler{} }

func (h *LicenseCommandHandler) CommandType() string {
	return "ActivateInstance|FailInstanceActivation|RefreshLicense|InstanceDataMissing|SubscriptionStatusChanged|PreActivationRequest"
}

func (h *LicenseCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *ActivateInstanceCommand:
		return h.handleActivateInstance(ctx, c)
	case *FailInstanceActivationCommand:
		return h.handleFailInstanceActivation(ctx, c)
	case *RefreshLicenseCommand:
		return h.handleRefreshLicense(ctx, c)
	case *InstanceDataMissingCommand:
		return h.handleInstanceDataMissing(ctx, c)
	case *SubscriptionStatusChangedCommand:
		return h.handleSubscriptionStatusChanged(ctx, c)
	case *PreActivationRequestCommand:
		return h.handlePreActivationRequest(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func (h *LicenseCommandHandler) handleActivateInstance(ctx context.Context, cmd *ActivateInstanceCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", cmd.ID,
		"instance_id", cmd.InstanceID,
		"tenant_id", cmd.TenantID,
		"plan_tier", cmd.PlanTier,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing instance activation command")

	// Truncate activation token to first 10 chars for audit purposes
	tokenPrefix := cmd.ActivationToken
	if len(tokenPrefix) > 10 {
		tokenPrefix = tokenPrefix[:10]
	}

	payload := map[string]interface{}{
		"instance_id":      cmd.InstanceID,
		"tenant_id":        cmd.TenantID,
		"plan_tier":        cmd.PlanTier,
		"activation_token": tokenPrefix,
		"activated_at":     now.Format(time.RFC3339),
		"correlation_id":   correlationID,
	}

	if !cmd.ExpiresAt.IsZero() {
		payload["expires_at"] = cmd.ExpiresAt.Format(time.RFC3339)
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "instance.activated",
		Stream:    LicenseStream,
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *LicenseCommandHandler) handleFailInstanceActivation(ctx context.Context, cmd *FailInstanceActivationCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", cmd.ID,
		"activation_token", cmd.ActivationToken,
		"error_reason", cmd.ErrorReason,
		"retry_count", cmd.RetryCount,
		"correlation_id", correlationID,
	).Debug("processing instance activation failure command")

	// Truncate activation token to first 10 chars for audit purposes
	tokenPrefix := cmd.ActivationToken
	if len(tokenPrefix) > 10 {
		tokenPrefix = tokenPrefix[:10]
	}

	payload := map[string]interface{}{
		"activation_token": tokenPrefix,
		"error_reason":     cmd.ErrorReason,
		"retry_count":      cmd.RetryCount,
		"failed_at":        now.Format(time.RFC3339),
		"correlation_id":   correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "instance.activation_failed",
		Stream:    LicenseStream,
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *LicenseCommandHandler) handleRefreshLicense(ctx context.Context, cmd *RefreshLicenseCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", cmd.ID,
		"instance_id", cmd.InstanceID,
		"tenant_id", cmd.TenantID,
		"plan_tier", cmd.PlanTier,
		"status", cmd.Status,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing license refresh command")

	payload := map[string]interface{}{
		"instance_id":    cmd.InstanceID,
		"tenant_id":      cmd.TenantID,
		"plan_tier":      cmd.PlanTier,
		"status":         cmd.Status,
		"refreshed_at":   now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	if !cmd.ExpiresAt.IsZero() {
		payload["expires_at"] = cmd.ExpiresAt.Format(time.RFC3339)
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "license.refreshed",
		Stream:    LicenseStream,
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *LicenseCommandHandler) handleInstanceDataMissing(ctx context.Context, cmd *InstanceDataMissingCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", cmd.ID,
		"bound_instance_id", cmd.BoundInstanceID,
		"device_fingerprint", cmd.DeviceFingerprint,
		"correlation_id", correlationID,
	).Warn("processing instance data missing command - possible tampering or data loss")

	payload := map[string]interface{}{
		"bound_instance_id":  cmd.BoundInstanceID,
		"device_fingerprint": cmd.DeviceFingerprint,
		"detected_at":        cmd.DetectedAt.Format(time.RFC3339),
		"action_taken":       "degraded_to_free",
		"correlation_id":     correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "license.instance_data_missing",
		Stream:    LicenseStream,
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *LicenseCommandHandler) handleSubscriptionStatusChanged(ctx context.Context, cmd *SubscriptionStatusChangedCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", cmd.ID,
		"organization_id", cmd.OrganizationID,
		"instance_id", cmd.InstanceID,
		"plan_tier", cmd.PlanTier,
		"status", cmd.Status,
		"event_type", cmd.EventType,
		"cancel_at_period_end", cmd.CancelAtPeriodEnd,
		"correlation_id", correlationID,
	).Info("processing subscription status changed command")

	payload := map[string]interface{}{
		"organization_id":      cmd.OrganizationID,
		"instance_id":          cmd.InstanceID,
		"plan_tier":            cmd.PlanTier,
		"status":               cmd.Status,
		"cancel_at_period_end": cmd.CancelAtPeriodEnd,
		"event_type":           cmd.EventType,
		"changed_at":           now.Format(time.RFC3339),
		"correlation_id":       correlationID,
	}

	if !cmd.CurrentPeriodEnd.IsZero() {
		payload["current_period_end"] = cmd.CurrentPeriodEnd.Format(time.RFC3339)
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      cmd.EventType, // "subscription.canceled", "subscription.resumed", etc.
		Stream:    LicenseStream,
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *LicenseCommandHandler) handlePreActivationRequest(ctx context.Context, cmd *PreActivationRequestCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", cmd.ID,
		"fingerprint", cmd.Fingerprint,
		"request_type", cmd.RequestType,
		"outcome", cmd.Outcome,
		"client_ip", cmd.ClientIP,
		"correlation_id", correlationID,
	).Debug("processing pre-activation request audit command")

	payload := map[string]interface{}{
		"fingerprint":        cmd.Fingerprint,
		"client_ip":          cmd.ClientIP,
		"user_agent":         cmd.UserAgent,
		"request_type":       cmd.RequestType,
		"outcome":            cmd.Outcome,
		"total_requests":     cmd.TotalRequests,
		"total_tokens":       cmd.TotalTokens,
		"estimated_cost_usd": cmd.EstimatedCostUsd,
		"recorded_at":        now.Format(time.RFC3339),
		"correlation_id":     correlationID,
	}

	if cmd.RejectionReason != "" {
		payload["rejection_reason"] = cmd.RejectionReason
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "preactivation.request",
		Stream:    LicenseStream,
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
