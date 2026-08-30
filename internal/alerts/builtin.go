package alerts

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

// BuiltinRule defines a pre-configured alert rule from alerts.yaml.
type BuiltinRule struct {
	Key             string
	Name            string
	Description     string
	Category        string
	Severity        string
	Metric          string
	Operator        string
	Threshold       float64
	DurationSeconds int32
}

// builtinGatewayAlerts are the gateway-level built-in rules that can be
// evaluated against ClickHouse otel_traces data.
var builtinGatewayAlerts = []BuiltinRule{
	{
		Key:             "high_error_rate",
		Name:            "High Error Rate",
		Description:     "High error rate from LLM providers",
		Category:        "performance",
		Severity:        "warning",
		Metric:          "error_rate",
		Operator:        ">",
		Threshold:       0.05,
		DurationSeconds: 300,
	},
	{
		Key:             "high_latency",
		Name:            "High Response Latency",
		Description:     "High average response latency from LLM providers",
		Category:        "performance",
		Severity:        "warning",
		Metric:          "avg_latency_ms",
		Operator:        ">",
		Threshold:       2000,
		DurationSeconds: 300,
	},
	{
		Key:             "low_throughput",
		Name:            "Low Throughput",
		Description:     "Low request throughput over evaluation window",
		Category:        "performance",
		Severity:        "info",
		Metric:          "request_count",
		Operator:        "<",
		Threshold:       10,
		DurationSeconds: 900,
	},
	{
		Key:             "high_cost",
		Name:            "High Hourly Cost",
		Description:     "API costs exceeding hourly threshold",
		Category:        "cost",
		Severity:        "warning",
		Metric:          "hourly_cost",
		Operator:        ">",
		Threshold:       100,
		DurationSeconds: 600,
	},
	{
		Key:             "cost_limit_reached",
		Name:            "Cost Limit Reached",
		Description:     "Accumulated cost has reached the budget limit",
		Category:        "cost",
		Severity:        "critical",
		Metric:          "hourly_cost",
		Operator:        ">",
		Threshold:       500,
		DurationSeconds: 60,
	},
	{
		Key:             "model_provider_outage",
		Name:            "Model Provider Outage",
		Description:     "LLM provider returning high error rate indicating possible outage",
		Category:        "provider",
		Severity:        "critical",
		Metric:          "error_rate",
		Operator:        ">",
		Threshold:       0.5,
		DurationSeconds: 60,
	},
	{
		Key:             "rate_limit_exceeded",
		Name:            "Rate Limit Hit",
		Description:     "Rate limit exceeded for model provider (high 429 rate)",
		Category:        "provider",
		Severity:        "warning",
		Metric:          "error_rate",
		Operator:        ">",
		Threshold:       0.2,
		DurationSeconds: 60,
	},
}

// builtinOutcomeAlerts are agent-outcome-level built-in rules that query
// the otel_trace_scores table (populated by the auto-scoring pipeline).
// Metric format: score.<aggregation>.<score_name>
var builtinOutcomeAlerts = []BuiltinRule{
	{
		Key:             "low_task_completion",
		Name:            "Low Task Completion Rate",
		Description:     "Agent turns are failing to complete naturally (hitting limits or stalling)",
		Category:        "performance",
		Severity:        "warning",
		Metric:          "score.rate_true.task_completion.finished",
		Operator:        "<",
		Threshold:       0.7,
		DurationSeconds: 600,
	},
	{
		Key:             "high_tool_failure",
		Name:            "High Tool Failure Rate",
		Description:     "Agent tool calls are failing at an elevated rate",
		Category:        "performance",
		Severity:        "warning",
		Metric:          "score.avg.tool_quality.success_rate",
		Operator:        "<",
		Threshold:       0.8,
		DurationSeconds: 600,
	},
	{
		Key:             "agent_looping",
		Name:            "Agent Looping Detected",
		Description:     "Agent is repeatedly making identical tool calls (stuck in a loop)",
		Category:        "performance",
		Severity:        "critical",
		Metric:          "score.rate_true.loop_health.looping",
		Operator:        ">",
		Threshold:       0.1,
		DurationSeconds: 300,
	},
	{
		Key:             "agent_stalling",
		Name:            "Agent Stalling",
		Description:     "Agent turns are ending due to timeouts or max iteration limits",
		Category:        "performance",
		Severity:        "warning",
		Metric:          "score.rate_true.loop_health.stalled",
		Operator:        ">",
		Threshold:       0.2,
		DurationSeconds: 600,
	},
	{
		Key:             "policy_violations",
		Name:            "Policy Compliance Violations",
		Description:     "Agent output is failing policy compliance checks (PII, blocked content)",
		Category:        "custom",
		Severity:        "critical",
		Metric:          "score.rate_false.policy.compliant",
		Operator:        ">",
		Threshold:       0.05,
		DurationSeconds: 300,
	},
	{
		Key:             "low_iteration_efficiency",
		Name:            "Low Iteration Efficiency",
		Description:     "Agent is wasting iterations on retries and errors",
		Category:        "performance",
		Severity:        "warning",
		Metric:          "score.avg.task_completion.efficiency",
		Operator:        "<",
		Threshold:       0.6,
		DurationSeconds: 900,
	},
	{
		Key:             "sandbox_failures",
		Name:            "Sandbox Execution Failures",
		Description:     "Sandbox tool executions are failing at an elevated rate",
		Category:        "performance",
		Severity:        "warning",
		Metric:          "score.avg.sandbox_hygiene.exit_code_rate",
		Operator:        "<",
		Threshold:       0.7,
		DurationSeconds: 600,
	},
}

// builtinRegressionAlerts are eval regression alerts, fired by the eval runner
// when a completed run's scores drop below its baseline.
var builtinRegressionAlerts = []BuiltinRule{
	{
		Key:             "eval_regression",
		Name:            "Eval Score Regression",
		Description:     "An evaluation run's scores have regressed compared to the baseline",
		Category:        "regression",
		Severity:        "warning",
		Metric:          "eval.regression",
		Operator:        ">",
		Threshold:       0,
		DurationSeconds: 0,
	},
}

// AllBuiltinRules returns gateway-level, outcome-level, and regression built-in rules.
func AllBuiltinRules() []BuiltinRule {
	all := make([]BuiltinRule, 0, len(builtinGatewayAlerts)+len(builtinOutcomeAlerts)+len(builtinRegressionAlerts))
	all = append(all, builtinGatewayAlerts...)
	all = append(all, builtinOutcomeAlerts...)
	all = append(all, builtinRegressionAlerts...)
	return all
}

// SeedBuiltinRules inserts built-in rules for a tenant, skipping any that
// already exist (matched by builtin_key). Seeds both gateway-level and
// outcome-level alert rules.
func SeedBuiltinRules(ctx context.Context, store AlertStore, tenantID string) (int32, error) {
	var seeded int32

	for _, b := range AllBuiltinRules() {
		existing, err := store.GetAlertRuleByBuiltinKey(ctx, tenantID, b.Key)
		if err != nil {
			return seeded, err
		}
		if existing != nil {
			continue
		}

		record := &AlertRuleRecord{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			Name:            b.Name,
			Description:     b.Description,
			Category:        b.Category,
			Severity:        b.Severity,
			BuiltinKey:      sql.NullString{String: b.Key, Valid: true},
			Metric:          b.Metric,
			Operator:        b.Operator,
			Threshold:       b.Threshold,
			DurationSeconds: b.DurationSeconds,
			Filters:         []byte("{}"),
			Enabled:         true,
		}

		if err := store.CreateAlertRule(ctx, record); err != nil {
			return seeded, err
		}
		seeded++
	}

	return seeded, nil
}
