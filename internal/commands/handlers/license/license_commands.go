package license

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
)

// ActivateInstanceCommand requests activation of a gateway instance.
type ActivateInstanceCommand struct {
	commands.BaseCommand
	InstanceID      string    `json:"instance_id"`
	TenantID        string    `json:"tenant_id"`
	PlanTier        string    `json:"plan_tier"`
	ActivationToken string    `json:"activation_token"` // First 10 chars only for audit
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
}

func NewActivateInstanceCommand(instanceID, tenantID, planTier, activationToken string, expiresAt time.Time, userID, traceID string) *ActivateInstanceCommand {
	return &ActivateInstanceCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		InstanceID:      instanceID,
		TenantID:        tenantID,
		PlanTier:        planTier,
		ActivationToken: activationToken,
		ExpiresAt:       expiresAt,
	}
}

func (c *ActivateInstanceCommand) AggregateID() string { return c.InstanceID }
func (c *ActivateInstanceCommand) CommandType() string { return "ActivateInstance" }
func (c *ActivateInstanceCommand) Validate() error {
	if c.InstanceID == "" {
		return fmt.Errorf("instance_id cannot be empty")
	}
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id cannot be empty")
	}
	if c.PlanTier == "" {
		return fmt.Errorf("plan_tier cannot be empty")
	}
	return nil
}

// FailInstanceActivationCommand records a failed activation attempt.
type FailInstanceActivationCommand struct {
	commands.BaseCommand
	ActivationToken string `json:"activation_token"` // First 10 chars only
	ErrorReason     string `json:"error_reason"`
	RetryCount      int    `json:"retry_count"`
}

func NewFailInstanceActivationCommand(activationToken, errorReason string, retryCount int, traceID string) *FailInstanceActivationCommand {
	return &FailInstanceActivationCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			TraceID:   traceID,
		},
		ActivationToken: activationToken,
		ErrorReason:     errorReason,
		RetryCount:      retryCount,
	}
}

func (c *FailInstanceActivationCommand) AggregateID() string { return c.ID }
func (c *FailInstanceActivationCommand) CommandType() string { return "FailInstanceActivation" }
func (c *FailInstanceActivationCommand) Validate() error {
	if c.ActivationToken == "" {
		return fmt.Errorf("activation_token cannot be empty")
	}
	if c.ErrorReason == "" {
		return fmt.Errorf("error_reason cannot be empty")
	}
	return nil
}

// RefreshLicenseCommand requests refresh of license state from remote service.
type RefreshLicenseCommand struct {
	commands.BaseCommand
	InstanceID string    `json:"instance_id"`
	TenantID   string    `json:"tenant_id"`
	PlanTier   string    `json:"plan_tier"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

func NewRefreshLicenseCommand(instanceID, tenantID, planTier, status string, expiresAt time.Time, userID, traceID string) *RefreshLicenseCommand {
	return &RefreshLicenseCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		InstanceID: instanceID,
		TenantID:   tenantID,
		PlanTier:   planTier,
		Status:     status,
		ExpiresAt:  expiresAt,
	}
}

func (c *RefreshLicenseCommand) AggregateID() string { return c.InstanceID }
func (c *RefreshLicenseCommand) CommandType() string { return "RefreshLicense" }
func (c *RefreshLicenseCommand) Validate() error {
	if c.InstanceID == "" {
		return fmt.Errorf("instance_id cannot be empty")
	}
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id cannot be empty")
	}
	return nil
}

// InstanceDataMissingCommand is dispatched when the gateway detects that local
// instance data has been deleted but the device was previously activated.
// This is used for audit logging and tampering detection.
type InstanceDataMissingCommand struct {
	commands.BaseCommand
	DeviceFingerprint string    `json:"device_fingerprint"` // Device fingerprint (truncated for audit)
	BoundInstanceID   string    `json:"bound_instance_id"`  // The instance ID this device was bound to
	DetectedAt        time.Time `json:"detected_at"`
}

func NewInstanceDataMissingCommand(deviceFingerprint, boundInstanceID, _ /* unused */, traceID string) *InstanceDataMissingCommand {
	// Truncate device fingerprint to first 16 chars for audit purposes
	fpPrefix := deviceFingerprint
	if len(fpPrefix) > 16 {
		fpPrefix = fpPrefix[:16]
	}

	return &InstanceDataMissingCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			TraceID:   traceID,
		},
		DeviceFingerprint: fpPrefix,
		BoundInstanceID:   boundInstanceID,
		DetectedAt:        time.Now(),
	}
}

func (c *InstanceDataMissingCommand) AggregateID() string { return c.BoundInstanceID }
func (c *InstanceDataMissingCommand) CommandType() string { return "InstanceDataMissing" }
func (c *InstanceDataMissingCommand) Validate() error {
	if c.DeviceFingerprint == "" {
		return fmt.Errorf("device_fingerprint cannot be empty")
	}
	return nil
}

// SubscriptionStatusChangedCommand is dispatched when subscription status changes
// (e.g., user cancels from Stripe billing portal)
type SubscriptionStatusChangedCommand struct {
	commands.BaseCommand
	OrganizationID    string    `json:"organization_id"`
	InstanceID        string    `json:"instance_id"`
	PlanTier          string    `json:"plan_tier"`
	Status            string    `json:"status"` // "active", "canceled", "past_due"
	CancelAtPeriodEnd bool      `json:"cancel_at_period_end"`
	CurrentPeriodEnd  time.Time `json:"current_period_end,omitempty"`
	EventType         string    `json:"event_type"` // "subscription.canceled", "subscription.resumed", "subscription.updated"
}

func NewSubscriptionStatusChangedCommand(orgID, instanceID, planTier, status, eventType string, cancelAtPeriodEnd bool, currentPeriodEnd time.Time, traceID string) *SubscriptionStatusChangedCommand {
	return &SubscriptionStatusChangedCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			TraceID:   traceID,
		},
		OrganizationID:    orgID,
		InstanceID:        instanceID,
		PlanTier:          planTier,
		Status:            status,
		CancelAtPeriodEnd: cancelAtPeriodEnd,
		CurrentPeriodEnd:  currentPeriodEnd,
		EventType:         eventType,
	}
}

func (c *SubscriptionStatusChangedCommand) AggregateID() string { return c.OrganizationID }
func (c *SubscriptionStatusChangedCommand) CommandType() string { return "SubscriptionStatusChanged" }
func (c *SubscriptionStatusChangedCommand) Validate() error {
	if c.OrganizationID == "" {
		return fmt.Errorf("organization_id cannot be empty")
	}
	if c.EventType == "" {
		return fmt.Errorf("event_type cannot be empty")
	}
	return nil
}

// PreActivationRequestCommand logs pre-activation (fingerprint-only) requests
// for forensic analysis and internal investigations.
type PreActivationRequestCommand struct {
	commands.BaseCommand
	Fingerprint      string  `json:"fingerprint"`        // Device fingerprint (truncated for storage)
	ClientIP         string  `json:"client_ip"`          // Source IP for geo/abuse analysis
	UserAgent        string  `json:"user_agent"`         // Client identification
	RequestType      string  `json:"request_type"`       // "usage_report", "rate_limited", "invalid_format"
	Outcome          string  `json:"outcome"`            // "accepted", "rejected", "rate_limited"
	TotalRequests    int64   `json:"total_requests"`     // Request count from usage report
	TotalTokens      int64   `json:"total_tokens"`       // Token count from usage report
	EstimatedCostUsd float64 `json:"estimated_cost_usd"` // Estimated cost from usage report
	RejectionReason  string  `json:"rejection_reason"`   // If rejected, why
}

func NewPreActivationRequestCommand(
	fingerprint, clientIP, userAgent, requestType, outcome string,
	totalRequests, totalTokens int64, estimatedCostUsd float64,
	rejectionReason, traceID string,
) *PreActivationRequestCommand {
	// Truncate fingerprint to first 16 chars for storage
	fpPrefix := fingerprint
	if len(fpPrefix) > 16 {
		fpPrefix = fpPrefix[:16]
	}

	// Truncate user agent to avoid bloating event payloads
	ua := userAgent
	if len(ua) > 256 {
		ua = ua[:256]
	}

	return &PreActivationRequestCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			TraceID:   traceID,
		},
		Fingerprint:      fpPrefix,
		ClientIP:         clientIP,
		UserAgent:        ua,
		RequestType:      requestType,
		Outcome:          outcome,
		TotalRequests:    totalRequests,
		TotalTokens:      totalTokens,
		EstimatedCostUsd: estimatedCostUsd,
		RejectionReason:  rejectionReason,
	}
}

func (c *PreActivationRequestCommand) AggregateID() string { return c.Fingerprint }
func (c *PreActivationRequestCommand) CommandType() string { return "PreActivationRequest" }
func (c *PreActivationRequestCommand) Validate() error {
	if c.Fingerprint == "" {
		return fmt.Errorf("fingerprint cannot be empty")
	}
	if c.RequestType == "" {
		return fmt.Errorf("request_type cannot be empty")
	}
	if c.Outcome == "" {
		return fmt.Errorf("outcome cannot be empty")
	}
	return nil
}
