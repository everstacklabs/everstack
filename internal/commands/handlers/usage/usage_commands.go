package usage

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	usagecore "github.com/everstacklabs/everstack/internal/usage"
	"github.com/google/uuid"
)

// RecordBillingUsageCommand records a normalized billing usage datapoint.
type RecordBillingUsageCommand struct {
	commands.BaseCommand
	IdempotencyKey string                 `json:"idempotency_key"`
	TenantID       string                 `json:"tenant_id"`
	ResourceType   string                 `json:"resource_type"`
	ResourceID     string                 `json:"resource_id,omitempty"`
	SourceType     string                 `json:"source_type"`
	SourceRef      string                 `json:"source_ref"`
	MetricType     string                 `json:"metric_type"`
	Quantity       float64                `json:"quantity"`
	Unit           string                 `json:"unit"`
	CostUSD        float64                `json:"cost_usd"`
	Currency       string                 `json:"currency,omitempty"`
	Status         string                 `json:"status,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	PeriodStart    *time.Time             `json:"period_start,omitempty"`
	PeriodEnd      *time.Time             `json:"period_end,omitempty"`
}

func NewRecordBillingUsageCommand(rec usagecore.BillingUsageRecord, userID, traceID string) *RecordBillingUsageCommand {
	return &RecordBillingUsageCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now().UTC(),
			UserID:    userID,
			TraceID:   traceID,
		},
		IdempotencyKey: rec.IdempotencyKey,
		TenantID:       rec.TenantID,
		ResourceType:   rec.ResourceType,
		ResourceID:     rec.ResourceID,
		SourceType:     rec.SourceType,
		SourceRef:      rec.SourceRef,
		MetricType:     rec.MetricType,
		Quantity:       rec.Quantity,
		Unit:           rec.Unit,
		CostUSD:        rec.CostUSD,
		Currency:       rec.Currency,
		Status:         rec.Status,
		Metadata:       rec.Metadata,
		PeriodStart:    rec.PeriodStart,
		PeriodEnd:      rec.PeriodEnd,
	}
}

func (c *RecordBillingUsageCommand) AggregateID() string { return c.IdempotencyKey }
func (c *RecordBillingUsageCommand) CommandType() string { return "RecordBillingUsage" }
func (c *RecordBillingUsageCommand) Validate() error {
	if c.IdempotencyKey == "" {
		return fmt.Errorf("idempotency_key cannot be empty")
	}
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id cannot be empty")
	}
	if c.ResourceType == "" {
		return fmt.Errorf("resource_type cannot be empty")
	}
	if c.SourceType == "" {
		return fmt.Errorf("source_type cannot be empty")
	}
	if c.SourceRef == "" {
		return fmt.Errorf("source_ref cannot be empty")
	}
	if c.MetricType == "" {
		return fmt.Errorf("metric_type cannot be empty")
	}
	if c.Unit == "" {
		return fmt.Errorf("unit cannot be empty")
	}
	return nil
}

// UsageMeteringCommand represents a request to record provider/model usage metrics.
type UsageMeteringCommand struct {
	commands.BaseCommand
	RequestID        string  `json:"request_id"`
	CorrelationID    string  `json:"correlation_id"`
	APIKeyHash       string  `json:"api_key_hash"`
	UserID           string  `json:"user_id,omitempty"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	CacheSavingsUSD  float64 `json:"cache_savings_usd"`
	CacheHit         bool    `json:"cache_hit"`
	CacheType        string  `json:"cache_type,omitempty"`
	LatencyMs        int64   `json:"latency_ms"`
	Success          bool    `json:"success"`
	ErrorCode        string  `json:"error_code,omitempty"`
}

func (c *UsageMeteringCommand) CommandType() string { return "usage.metering" }
func (c *UsageMeteringCommand) AggregateID() string { return c.RequestID }
func (c *UsageMeteringCommand) Validate() error     { return nil }

func NewUsageMeteringCommand(
	requestID, correlationID, apiKeyHash, userID string,
	provider, model string,
	inputTokens, outputTokens int64,
	estimatedCost, cacheSavings float64,
	cacheHit bool, cacheType string,
	latencyMs int64, success bool, errorCode string,
) *UsageMeteringCommand {
	return &UsageMeteringCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
		},
		RequestID:        requestID,
		CorrelationID:    correlationID,
		APIKeyHash:       apiKeyHash,
		UserID:           userID,
		Provider:         provider,
		Model:            model,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		TotalTokens:      inputTokens + outputTokens,
		EstimatedCostUSD: estimatedCost,
		CacheSavingsUSD:  cacheSavings,
		CacheHit:         cacheHit,
		CacheType:        cacheType,
		LatencyMs:        latencyMs,
		Success:          success,
		ErrorCode:        errorCode,
	}
}

// UsageLimitWarningCommand is emitted when usage approaches limits.
type UsageLimitWarningCommand struct {
	commands.BaseCommand
	InstanceID   string  `json:"instance_id"`
	LimitType    string  `json:"limit_type"`
	CurrentUsage int64   `json:"current_usage"`
	Limit        int64   `json:"limit"`
	Percentage   float64 `json:"percentage"`
	Message      string  `json:"message"`
}

func (c *UsageLimitWarningCommand) CommandType() string { return "usage.limit.warning" }
func (c *UsageLimitWarningCommand) AggregateID() string { return c.InstanceID }
func (c *UsageLimitWarningCommand) Validate() error     { return nil }

func NewUsageLimitWarningCommand(instanceID, limitType string, currentUsage, limit int64) *UsageLimitWarningCommand {
	pct := float64(currentUsage) / float64(limit) * 100
	return &UsageLimitWarningCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		InstanceID:   instanceID,
		LimitType:    limitType,
		CurrentUsage: currentUsage,
		Limit:        limit,
		Percentage:   pct,
		Message:      "Usage approaching limit",
	}
}

// UsageLimitExceededCommand is emitted when a usage limit is exceeded.
type UsageLimitExceededCommand struct {
	commands.BaseCommand
	InstanceID   string `json:"instance_id"`
	LimitType    string `json:"limit_type"`
	CurrentUsage int64  `json:"current_usage"`
	Limit        int64  `json:"limit"`
	Message      string `json:"message"`
}

func (c *UsageLimitExceededCommand) CommandType() string { return "usage.limit.exceeded" }
func (c *UsageLimitExceededCommand) AggregateID() string { return c.InstanceID }
func (c *UsageLimitExceededCommand) Validate() error     { return nil }

func NewUsageLimitExceededCommand(instanceID, limitType string, currentUsage, limit int64) *UsageLimitExceededCommand {
	return &UsageLimitExceededCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		InstanceID:   instanceID,
		LimitType:    limitType,
		CurrentUsage: currentUsage,
		Limit:        limit,
		Message:      "Usage limit exceeded",
	}
}
