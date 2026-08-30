package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

const billingUsageStream = "billing-usage"

// UsageCommandHandler converts usage commands into domain events.
type UsageCommandHandler struct{}

func NewUsageCommandHandler() *UsageCommandHandler { return &UsageCommandHandler{} }

func (h *UsageCommandHandler) CommandType() string {
	return "RecordBillingUsage|usage.metering|usage.limit.warning|usage.limit.exceeded"
}

func (h *UsageCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *RecordBillingUsageCommand:
		return h.handleRecordBillingUsage(c)
	case *UsageMeteringCommand:
		return h.handleUsageMetering(ctx, c)
	case *UsageLimitWarningCommand:
		return h.handleLimitWarning(ctx, c)
	case *UsageLimitExceededCommand:
		return h.handleLimitExceeded(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type %T", cmd)
	}
}

func (h *UsageCommandHandler) handleRecordBillingUsage(c *RecordBillingUsageCommand) ([]database.Event, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	payload := map[string]interface{}{
		"idempotency_key": c.IdempotencyKey,
		"tenant_id":       c.TenantID,
		"resource_type":   c.ResourceType,
		"resource_id":     c.ResourceID,
		"source_type":     c.SourceType,
		"source_ref":      c.SourceRef,
		"metric_type":     c.MetricType,
		"quantity":        c.Quantity,
		"unit":            c.Unit,
		"cost_usd":        c.CostUSD,
		"currency":        c.Currency,
		"status":          c.Status,
		"metadata":        c.Metadata,
	}
	if c.PeriodStart != nil && !c.PeriodStart.IsZero() {
		payload["period_start"] = c.PeriodStart.UTC().Format(time.RFC3339)
	}
	if c.PeriodEnd != nil && !c.PeriodEnd.IsZero() {
		payload["period_end"] = c.PeriodEnd.UTC().Format(time.RFC3339)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal billing usage payload: %w", err)
	}

	return []database.Event{{
		ID:        uuid.NewString(),
		Type:      "billing.usage_recorded",
		Stream:    billingUsageStream,
		Payload:   data,
		CreatedAt: time.Now().UTC().Unix(),
	}}, nil
}

func (h *UsageCommandHandler) handleUsageMetering(ctx context.Context, cmd *UsageMeteringCommand) ([]database.Event, error) {
	correlationID := correlation.GetCorrelationID(ctx)
	if correlationID == "" {
		correlationID = cmd.CorrelationID
	}
	now := time.Now()

	payload := map[string]interface{}{
		"request_id":         cmd.RequestID,
		"correlation_id":     correlationID,
		"api_key_hash":       cmd.APIKeyHash,
		"user_id":            cmd.UserID,
		"provider":           cmd.Provider,
		"model":              cmd.Model,
		"input_tokens":       cmd.InputTokens,
		"output_tokens":      cmd.OutputTokens,
		"total_tokens":       cmd.TotalTokens,
		"estimated_cost_usd": cmd.EstimatedCostUSD,
		"cache_savings_usd":  cmd.CacheSavingsUSD,
		"cache_hit":          cmd.CacheHit,
		"cache_type":         cmd.CacheType,
		"latency_ms":         cmd.LatencyMs,
		"success":            cmd.Success,
		"error_code":         cmd.ErrorCode,
		"timestamp":          now.Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)

	logger.WithFields(
		"command_id", cmd.ID,
		"provider", cmd.Provider,
		"model", cmd.Model,
		"tokens", cmd.TotalTokens,
		"cost_usd", cmd.EstimatedCostUSD,
		"cache_hit", cmd.CacheHit,
	).Debug("usage metering event generated")

	return []database.Event{{
		ID:        uuid.NewString(),
		Type:      "usage.request.completed",
		Stream:    "usage-metering",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *UsageCommandHandler) handleLimitWarning(ctx context.Context, cmd *UsageLimitWarningCommand) ([]database.Event, error) {
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	payload := map[string]interface{}{
		"instance_id":    cmd.InstanceID,
		"limit_type":     cmd.LimitType,
		"current_usage":  cmd.CurrentUsage,
		"limit":          cmd.Limit,
		"percentage":     cmd.Percentage,
		"message":        cmd.Message,
		"correlation_id": correlationID,
		"timestamp":      now.Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)

	return []database.Event{{
		ID:        uuid.NewString(),
		Type:      "usage.limit.warning",
		Stream:    "usage-alerts",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *UsageCommandHandler) handleLimitExceeded(ctx context.Context, cmd *UsageLimitExceededCommand) ([]database.Event, error) {
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	payload := map[string]interface{}{
		"instance_id":    cmd.InstanceID,
		"limit_type":     cmd.LimitType,
		"current_usage":  cmd.CurrentUsage,
		"limit":          cmd.Limit,
		"message":        cmd.Message,
		"correlation_id": correlationID,
		"timestamp":      now.Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)

	return []database.Event{{
		ID:        uuid.NewString(),
		Type:      "usage.limit.exceeded",
		Stream:    "usage-alerts",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
