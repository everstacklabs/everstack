package enterprise

import "time"

// The canonical count behind each creation-time cap. Every query binds the
// counted tenant ids as $1 (see LimitCheck) and is written against the schema
// as it exists in production (information_schema on the prod gateway DB,
// 2026-10-06): agent, channel, binding and message tables key tenants by
// uuid; dataset, eval and annotation tables by varchar. annotation_queues and
// agent_channel_bindings have no deleted_at column (rows are hard deleted),
// so no predicate on it may appear here.
//
// The prod over-limit report and the tests run these exact strings.
const (
	CountAgentsQuery = `SELECT COUNT(*) FROM agent_definitions
		WHERE tenant_id = ANY($1::uuid[]) AND deleted_at IS NULL`

	// Persistent agents are counted as defined, whatever their lifecycle
	// status, so a sleeping or terminated persistent agent still holds its
	// slot until it is deleted.
	CountPersistentAgentsQuery = `SELECT COUNT(*) FROM agent_definitions
		WHERE tenant_id = ANY($1::uuid[]) AND deleted_at IS NULL AND lifecycle_mode = 'persistent'`

	// $2 is the agent being started, excluded so another turn on an agent
	// that is already running is never refused.
	CountConcurrentRunningQuery = `SELECT COUNT(*) FROM agent_definitions
		WHERE tenant_id = ANY($1::uuid[]) AND deleted_at IS NULL AND lifecycle_status = 'running'
		  AND id::text <> $2`

	CountDatasetItemsQuery = `SELECT COUNT(*) FROM dataset_items
		WHERE tenant_id = ANY($1::text[])`

	// $2 is the start of the current UTC month (MonthStartUTC).
	CountEvalRunsMonthlyQuery = `SELECT COUNT(*) FROM eval_runs
		WHERE tenant_id = ANY($1::text[]) AND created_at >= $2`

	CountAnnotationQueuesQuery = `SELECT COUNT(*) FROM annotation_queues
		WHERE tenant_id = ANY($1::text[])`

	CountChannelsQuery = `SELECT COUNT(*) FROM channel_configs
		WHERE tenant_id = ANY($1::uuid[])`

	// Agent and legacy trooper bindings share one cap. Trooper bindings that
	// the unification migration already copied into agent bindings keep the
	// same id and are counted once.
	CountChannelBindingsQuery = `SELECT
		(SELECT COUNT(*) FROM agent_channel_bindings WHERE tenant_id = ANY($1::uuid[]))
		+ (SELECT COUNT(*) FROM trooper_channel_bindings AS t
		   WHERE t.tenant_id = ANY($1::uuid[])
		     AND NOT EXISTS (SELECT 1 FROM agent_channel_bindings AS a WHERE a.id = t.id))`
)

// MonthStartUTC is the start of the calendar month (UTC) containing now; the
// boundary for every monthly cap and for the Billing gauges.
func MonthStartUTC(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// AgentsLimit is the AGENTS cap check for a tenant.
func AgentsLimit(tenantID string) LimitCheck {
	return LimitCheck{Type: UsageTypeAgents, Resource: "agent", TenantID: tenantID,
		Query: CountAgentsQuery, Column: TenantUUID}
}

// PersistentAgentsLimit is the PERSISTENT_AGENTS cap check for a tenant.
func PersistentAgentsLimit(tenantID string) LimitCheck {
	return LimitCheck{Type: UsageTypePersistentAgents, Resource: "persistent agent", TenantID: tenantID,
		Query: CountPersistentAgentsQuery, Column: TenantUUID}
}

// ConcurrentRunningLimit is the CONCURRENT_RUNNING check for starting agentID.
func ConcurrentRunningLimit(tenantID, agentID string) LimitCheck {
	return LimitCheck{Type: UsageTypeConcurrentRunning, Resource: "concurrently running agent", TenantID: tenantID,
		Query: CountConcurrentRunningQuery, Column: TenantUUID, Args: []interface{}{agentID}}
}

// DatasetItemsLimit is the DATASET_ITEMS check for adding n items.
func DatasetItemsLimit(tenantID string, n int64) LimitCheck {
	return LimitCheck{Type: UsageTypeDatasetItems, Resource: "dataset item", TenantID: tenantID,
		Query: CountDatasetItemsQuery, Column: TenantText, Delta: n}
}

// EvalRunsMonthlyLimit is the EVAL_RUNS_MONTHLY check at time now.
func EvalRunsMonthlyLimit(tenantID string, now time.Time) LimitCheck {
	return LimitCheck{Type: UsageTypeEvalRunsMonthly, Resource: "monthly eval run", TenantID: tenantID,
		Query: CountEvalRunsMonthlyQuery, Column: TenantText, Args: []interface{}{MonthStartUTC(now)}}
}

// AnnotationQueuesLimit is the ANNOTATION_QUEUES cap check for a tenant.
func AnnotationQueuesLimit(tenantID string) LimitCheck {
	return LimitCheck{Type: UsageTypeAnnotationQueues, Resource: "annotation queue", TenantID: tenantID,
		Query: CountAnnotationQueuesQuery, Column: TenantText}
}

// ChannelsLimit is the CHANNELS cap check for a tenant.
func ChannelsLimit(tenantID string) LimitCheck {
	return LimitCheck{Type: UsageTypeChannels, Resource: "channel", TenantID: tenantID,
		Query: CountChannelsQuery, Column: TenantUUID}
}

// ChannelBindingsLimit is the CHANNEL_BINDINGS cap check for a tenant.
func ChannelBindingsLimit(tenantID string) LimitCheck {
	return LimitCheck{Type: UsageTypeChannelBindings, Resource: "channel binding", TenantID: tenantID,
		Query: CountChannelBindingsQuery, Column: TenantUUID}
}
