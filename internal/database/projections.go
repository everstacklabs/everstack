package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
	"github.com/everstacklabs/everstack/internal/usage"
	"github.com/jmoiron/sqlx"
)

// ProjectionManager manages read model projections from events.
type ProjectionManager struct {
	db                     *sqlx.DB
	bus                    *InMemoryEventBus
	storageCredentialStore storagecredentials.Store
}

func (pm *ProjectionManager) DB() *sqlx.DB { return pm.db }

func (pm *ProjectionManager) SetStorageCredentialStore(store storagecredentials.Store) {
	pm.storageCredentialStore = store
}

// NewProjectionManager creates a new projection manager.
func NewProjectionManager(db *sqlx.DB, bus *InMemoryEventBus) *ProjectionManager {
	return &ProjectionManager{
		db:  db,
		bus: bus,
	}
}

// Initialize sets up the projection handlers.
func (pm *ProjectionManager) Initialize(ctx context.Context) error {
	// Views are now managed by migrations (Postgres/ClickHouse). Skip runtime DDL.
	if err := pm.registerEventHandlers(); err != nil {
		return fmt.Errorf("failed to register event handlers: %w", err)
	}
	logger.Info("projection manager initialized successfully")
	return nil
}

// createViews is deprecated; views are installed via migrations.
func (pm *ProjectionManager) createViews(ctx context.Context) error {
	logger.Info("skipping runtime view creation; views are managed by migrations")
	return nil
}

// registerEventHandlers registers event handlers for maintaining projections.
func (pm *ProjectionManager) registerEventHandlers() error {
	// Chat session projection handler
	if err := pm.bus.Subscribe(
		"chat-session-projection",
		"chat.session.started",
		"chat-sessions",
		pm.handleChatSessionEvent,
	); err != nil {
		return err
	}

	// Chat completion handler
	if err := pm.bus.Subscribe(
		"chat-completion-projection",
		"chat.session.completed",
		"chat-sessions",
		pm.handleChatCompletionEvent,
	); err != nil {
		return err
	}

	// Chat session error handler
	if err := pm.bus.Subscribe(
		"chat-session-error-projection",
		"chat.session.error",
		"chat-sessions",
		pm.handleChatSessionErrorEvent,
	); err != nil {
		return err
	}

	// Model usage projection handler
	if err := pm.bus.Subscribe(
		"model-usage-projection",
		"model.selection.requested",
		"model-selections",
		pm.handleModelUsageEvent,
	); err != nil {
		return err
	}

	// Load balancer projection handler
	if err := pm.bus.Subscribe(
		"load-balancer-projection",
		"load_balancer.request.completed",
		"load-balancer",
		pm.handleLoadBalancerEvent,
	); err != nil {
		return err
	}

	// API key projections
	if err := pm.bus.Subscribe(
		"api-key-created-projection",
		"api.key.created",
		"api-keys",
		pm.handleApiKeyCreated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"api-key-revoked-projection",
		"api.key.revoked",
		"api-keys",
		pm.handleApiKeyRevoked,
	); err != nil {
		return err
	}

	// Functions projections
	if err := pm.bus.Subscribe(
		"function-created-projection",
		"function.created",
		"functions",
		pm.handleFunctionCreated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"function-updated-projection",
		"function.updated",
		"functions",
		pm.handleFunctionUpdated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"function-deleted-projection",
		"function.deleted",
		"functions",
		pm.handleFunctionDeleted,
	); err != nil {
		return err
	}

	// Workflows projections
	if err := pm.bus.Subscribe(
		"workflow-created-projection",
		"workflow.created",
		"workflows",
		pm.handleWorkflowCreated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"workflow-updated-projection",
		"workflow.updated",
		"workflows",
		pm.handleWorkflowUpdated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"workflow-deleted-projection",
		"workflow.deleted",
		"workflows",
		pm.handleWorkflowDeleted,
	); err != nil {
		return err
	}

	// Agent definitions projections
	if err := pm.bus.Subscribe(
		"agent-created-projection",
		"agent.created",
		"agents",
		pm.handleAgentCreated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-updated-projection",
		"agent.updated",
		"agents",
		pm.handleAgentUpdated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-deleted-projection",
		"agent.deleted",
		"agents",
		pm.handleAgentDeleted,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-provisioning-projection",
		"agent.provisioning",
		"agents",
		pm.handleAgentUpdated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-sleeping-projection",
		"agent.sleeping",
		"agents",
		pm.handleAgentUpdated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-waking-projection",
		"agent.waking",
		"agents",
		pm.handleAgentUpdated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-link-created-projection",
		"agent.link.created",
		"agents",
		pm.handleAgentLinkCreated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-link-deleted-projection",
		"agent.link.deleted",
		"agents",
		pm.handleAgentLinkDeleted,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-channel-bound-projection",
		"agent.channel.bound",
		"agents",
		pm.handleAgentChannelBound,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-channel-unbound-projection",
		"agent.channel.unbound",
		"agents",
		pm.handleAgentChannelUnbound,
	); err != nil {
		return err
	}

	// Agent session projections
	if err := pm.bus.Subscribe(
		"agent-session-created-projection",
		"agent.session.created",
		"agent-sessions",
		pm.handleAgentSessionCreated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-session-completed-projection",
		"agent.session.completed",
		"agent-sessions",
		pm.handleAgentSessionCompleted,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-session-cancelled-projection",
		"agent.session.cancelled",
		"agent-sessions",
		pm.handleAgentSessionCancelled,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"agent-turn-completed-projection",
		"agent.turn.completed",
		"agent-sessions",
		pm.handleAgentTurnCompleted,
	); err != nil {
		return err
	}

	// Billing usage projection (normalized usage records for pricing/billing).
	if err := pm.bus.Subscribe(
		"billing-usage-recorded-projection",
		"billing.usage_recorded",
		"billing-usage",
		pm.handleBillingUsageRecorded,
	); err != nil {
		return err
	}

	// Agent approval review projection
	if err := pm.bus.Subscribe(
		"agent-approval-requested-projection",
		"agent.approval.requested",
		"agent-sessions",
		pm.handleAgentApprovalRequested,
	); err != nil {
		return err
	}

	// Fallback routing event handlers
	if err := pm.bus.Subscribe(
		"model-not-found-projection",
		"model.not_found",
		"model-errors",
		pm.handleModelNotFoundEvent,
	); err != nil {
		return err
	}

	if err := pm.bus.Subscribe(
		"fallback-triggered-projection",
		"fallback.triggered",
		"fallback-routing",
		pm.handleFallbackTriggeredEvent,
	); err != nil {
		return err
	}

	if err := pm.bus.Subscribe(
		"fallback-succeeded-projection",
		"fallback.succeeded",
		"fallback-routing",
		pm.handleFallbackSucceededEvent,
	); err != nil {
		return err
	}

	if err := pm.bus.Subscribe(
		"fallback-failed-projection",
		"fallback.failed",
		"fallback-routing",
		pm.handleFallbackFailedEvent,
	); err != nil {
		return err
	}

	// Dataset projections
	if err := pm.bus.Subscribe("dataset-created-projection", "dataset.created", "datasets", pm.handleDatasetCreated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("dataset-updated-projection", "dataset.updated", "datasets", pm.handleDatasetUpdated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("dataset-deleted-projection", "dataset.deleted", "datasets", pm.handleDatasetDeleted); err != nil {
		return err
	}

	// Dataset item projections
	if err := pm.bus.Subscribe("dataset-item-created-projection", "dataset_item.created", "datasets", pm.handleDatasetItemCreated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("dataset-item-updated-projection", "dataset_item.updated", "datasets", pm.handleDatasetItemUpdated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("dataset-item-deleted-projection", "dataset_item.deleted", "datasets", pm.handleDatasetItemDeleted); err != nil {
		return err
	}

	// Score config projections
	if err := pm.bus.Subscribe("score-config-created-projection", "score_config.created", "datasets", pm.handleScoreConfigCreated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("score-config-updated-projection", "score_config.updated", "datasets", pm.handleScoreConfigUpdated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("score-config-deleted-projection", "score_config.deleted", "datasets", pm.handleScoreConfigDeleted); err != nil {
		return err
	}

	// Eval run projections
	if err := pm.bus.Subscribe("eval-run-created-projection", "eval_run.created", "eval_runs", pm.handleEvalRunCreated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("eval-run-cancelled-projection", "eval_run.cancelled", "eval_runs", pm.handleEvalRunCancelled); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("eval-run-deleted-projection", "eval_run.deleted", "eval_runs", pm.handleEvalRunDeleted); err != nil {
		return err
	}

	// Prompt library projections
	if err := pm.bus.Subscribe("prompt-created-projection", "prompt.created", "prompts", pm.handlePromptCreated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("prompt-updated-projection", "prompt.updated", "prompts", pm.handlePromptUpdated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("prompt-deleted-projection", "prompt.deleted", "prompts", pm.handlePromptDeleted); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("prompt-version-created-projection", "prompt_version.created", "prompts", pm.handlePromptVersionCreated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("prompt-version-labels-set-projection", "prompt_version.labels_set", "prompts", pm.handlePromptVersionLabelsSet); err != nil {
		return err
	}

	// Annotation queue projections
	if err := pm.bus.Subscribe("annotation-queue-created-projection", "annotation_queue.created", "annotation_queues", pm.handleAnnotationQueueCreated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("annotation-queue-updated-projection", "annotation_queue.updated", "annotation_queues", pm.handleAnnotationQueueUpdated); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("annotation-queue-deleted-projection", "annotation_queue.deleted", "annotation_queues", pm.handleAnnotationQueueDeleted); err != nil {
		return err
	}

	// Annotation queue item projections
	if err := pm.bus.Subscribe("annotation-item-added-projection", "annotation_queue_item.added", "annotation_queue_items", pm.handleAnnotationItemAdded); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("annotation-item-completed-projection", "annotation_queue_item.completed", "annotation_queue_items", pm.handleAnnotationItemCompleted); err != nil {
		return err
	}
	if err := pm.bus.Subscribe("annotation-item-skipped-projection", "annotation_queue_item.skipped", "annotation_queue_items", pm.handleAnnotationItemSkipped); err != nil {
		return err
	}

	// Storage config projections
	if err := pm.bus.SubscribeCritical("storage-config-created-projection", "storage_config.created", "storage_configs", pm.handleStorageConfigCreated); err != nil {
		return err
	}
	if err := pm.bus.SubscribeCritical("storage-config-updated-projection", "storage_config.updated", "storage_configs", pm.handleStorageConfigUpdated); err != nil {
		return err
	}
	if err := pm.bus.SubscribeCritical("storage-config-deleted-projection", "storage_config.deleted", "storage_configs", pm.handleStorageConfigDeleted); err != nil {
		return err
	}

	// Trooper projections
	if err := pm.bus.Subscribe(
		"trooper-created-projection",
		"trooper.created",
		"troopers",
		pm.handleTrooperCreated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"trooper-updated-projection",
		"trooper.updated",
		"troopers",
		pm.handleTrooperUpdated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"trooper-deleted-projection",
		"trooper.deleted",
		"troopers",
		pm.handleTrooperDeleted,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"trooper-link-created-projection",
		"trooper.link.created",
		"troopers",
		pm.handleTrooperLinkCreated,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"trooper-link-deleted-projection",
		"trooper.link.deleted",
		"troopers",
		pm.handleTrooperLinkDeleted,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"trooper-channel-bound-projection",
		"trooper.channel.bound",
		"troopers",
		pm.handleTrooperChannelBound,
	); err != nil {
		return err
	}
	if err := pm.bus.Subscribe(
		"trooper-channel-unbound-projection",
		"trooper.channel.unbound",
		"troopers",
		pm.handleTrooperChannelUnbound,
	); err != nil {
		return err
	}

	logger.Info("projection event handlers registered")
	return nil
}

// handleChatSessionEvent processes chat session events for projections.
func (pm *ProjectionManager) handleChatSessionEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal chat session event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"session_id", payload["session_id"],
	).Debug("processing chat session event for projection")

	// For now, the view handles this automatically
	// In a more complex system, you might update materialized tables here

	return nil
}

// handleChatCompletionEvent processes chat completion events.
func (pm *ProjectionManager) handleChatCompletionEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal chat completion event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"session_id", payload["session_id"],
	).Debug("processing chat completion event for projection")

	// Update completion status, duration, token usage, etc.
	// This could involve updating a materialized table for better performance

	return nil
}

// handleChatSessionErrorEvent processes chat session error events.
// These events track failed requests before processing starts.
func (pm *ProjectionManager) handleChatSessionErrorEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal chat.session.error event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"session_id", payload["session_id"],
		"requested_model", payload["requested_model"],
		"error_type", payload["error_type"],
		"error_message", payload["error_message"],
		"user_id", payload["user_id"],
		"api_key_hash", payload["api_key_hash"],
		"correlation_id", payload["correlation_id"],
	).Warn("chat session failed before processing")

	// TODO: Track session failure patterns for analytics:
	// - Count failures by error type
	// - Identify problematic models/configurations
	// - Alert on spike in error rates

	return nil
}

// handleModelUsageEvent processes model usage events.
func (pm *ProjectionManager) handleModelUsageEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal model usage event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"model", payload["requested_model"],
	).Debug("processing model usage event for projection")

	// Track model selection patterns for load balancer analytics

	return nil
}

// handleLoadBalancerEvent processes load balancer events.
func (pm *ProjectionManager) handleLoadBalancerEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal load balancer event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"strategy", payload["strategy"],
	).Debug("processing load balancer event for projection")

	// Track load balancer performance metrics

	return nil
}

// handleApiKeyCreated upserts API key rows.
func (pm *ProjectionManager) handleApiKeyCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal api key created: %w", err)
	}
	if pm.db == nil {
		return nil
	}
	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, name, hash, type, sensitive_id, user_id, org_id, instance_id, created_at, updated_at, revoked)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::timestamptz,$10::timestamptz,FALSE)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, hash=EXCLUDED.hash, type=EXCLUDED.type, sensitive_id=EXCLUDED.sensitive_id, user_id=EXCLUDED.user_id, org_id=EXCLUDED.org_id, instance_id=EXCLUDED.instance_id, updated_at=EXCLUDED.updated_at, revoked=FALSE, revoked_at=NULL
	`, payload["id"], payload["name"], payload["hash"], payload["type"], payload["sensitive_id"], payload["user_id"], payload["org_id"], payload["instance_id"], payload["created_at"], payload["updated_at"])
	return err
}

// handleApiKeyRevoked marks API key as revoked.
func (pm *ProjectionManager) handleApiKeyRevoked(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal api key revoked: %w", err)
	}
	if pm.db == nil {
		return nil
	}
	// Scope the UPDATE by org_id so a revoke event for one tenant cannot
	// flip the revoked bit on a same-id row in another tenant's namespace.
	// Older revoke events written before the command carried org_id will
	// have an empty org_id in the payload; we skip them to avoid an
	// unscoped UPDATE — a stale event re-applied during replay must not
	// silently delete keys.
	orgID, _ := payload["org_id"].(string)
	if orgID == "" {
		logger.Warnf("projections: api.key.revoked event missing org_id, skipping (id=%v)", payload["id"])
		return nil
	}
	_, err := pm.db.ExecContext(ctx, `
		UPDATE api_keys SET revoked=TRUE, revoked_at=$2::timestamptz, updated_at=$2::timestamptz WHERE id=$1 AND org_id=$3
	`, payload["id"], payload["revoked_at"], orgID)
	return err
}

// handleFunctionCreated inserts a new function row.
func (pm *ProjectionManager) handleFunctionCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal function created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	// Extract webhook config if present
	var webhookURL, webhookMethod *string
	var webhookHeaders []byte
	var webhookTimeoutMs *int32
	if webhook, ok := payload["webhook"].(map[string]interface{}); ok {
		if url, ok := webhook["url"].(string); ok {
			webhookURL = &url
		}
		if method, ok := webhook["method"].(string); ok {
			webhookMethod = &method
		}
		if headers, ok := webhook["headers"].(map[string]interface{}); ok {
			webhookHeaders, _ = json.Marshal(headers)
		}
		if timeout, ok := webhook["timeout_ms"].(float64); ok {
			t := int32(timeout)
			webhookTimeoutMs = &t
		}
	}

	// Extract proxy config if present
	var proxyBaseURL, proxyPath, proxyMethod *string
	var proxyQueryMapping, proxyHeaderMapping, proxyBodyMapping, proxyResponseMapping []byte
	if proxy, ok := payload["proxy"].(map[string]interface{}); ok {
		if baseURL, ok := proxy["base_url"].(string); ok {
			proxyBaseURL = &baseURL
		}
		if path, ok := proxy["path"].(string); ok {
			proxyPath = &path
		}
		if method, ok := proxy["method"].(string); ok {
			proxyMethod = &method
		}
		if qm, ok := proxy["query_mapping"].(map[string]interface{}); ok {
			proxyQueryMapping, _ = json.Marshal(qm)
		}
		if hm, ok := proxy["header_mapping"].(map[string]interface{}); ok {
			proxyHeaderMapping, _ = json.Marshal(hm)
		}
		if bm, ok := proxy["body_mapping"].(map[string]interface{}); ok {
			proxyBodyMapping, _ = json.Marshal(bm)
		}
		if rm, ok := proxy["response_mapping"].(map[string]interface{}); ok {
			proxyResponseMapping, _ = json.Marshal(rm)
		}
	}

	// Extract isolated config if present (Phase 2)
	var runtime, code *string
	var packages []string
	if isolated, ok := payload["isolated"].(map[string]interface{}); ok {
		if r, ok := isolated["runtime"].(string); ok {
			runtime = &r
		}
		if c, ok := isolated["code"].(string); ok {
			code = &c
		}
		if pkgs, ok := isolated["packages"].([]interface{}); ok {
			for _, p := range pkgs {
				if ps, ok := p.(string); ok {
					packages = append(packages, ps)
				}
			}
		}
	}

	var dockerHost *string
	if isolated, ok := payload["isolated"].(map[string]interface{}); ok {
		if dh, ok := isolated["docker_host"].(string); ok && dh != "" {
			dockerHost = &dh
		}
	}

	// Marshal parameters
	var parametersJSON []byte
	if params, ok := payload["parameters"].(map[string]interface{}); ok {
		parametersJSON, _ = json.Marshal(params)
	} else {
		parametersJSON = []byte("{}")
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO functions (
			id, tenant_id, name, description, mode, parameters,
			webhook_url, webhook_method, webhook_headers, webhook_timeout_ms,
			proxy_base_url, proxy_path, proxy_method, proxy_query_mapping, proxy_header_mapping, proxy_body_mapping, proxy_response_mapping,
			runtime, code, packages,
			timeout_ms, memory_mb, max_retries, enabled, created_at, updated_at, docker_host
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20,
			$21, $22, $23, $24, $25::timestamptz, $26::timestamptz, $27
		)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description, mode=EXCLUDED.mode, parameters=EXCLUDED.parameters,
			webhook_url=EXCLUDED.webhook_url, webhook_method=EXCLUDED.webhook_method, webhook_headers=EXCLUDED.webhook_headers, webhook_timeout_ms=EXCLUDED.webhook_timeout_ms,
			proxy_base_url=EXCLUDED.proxy_base_url, proxy_path=EXCLUDED.proxy_path, proxy_method=EXCLUDED.proxy_method,
			proxy_query_mapping=EXCLUDED.proxy_query_mapping, proxy_header_mapping=EXCLUDED.proxy_header_mapping, proxy_body_mapping=EXCLUDED.proxy_body_mapping, proxy_response_mapping=EXCLUDED.proxy_response_mapping,
			runtime=EXCLUDED.runtime, code=EXCLUDED.code, packages=EXCLUDED.packages,
			timeout_ms=EXCLUDED.timeout_ms, memory_mb=EXCLUDED.memory_mb, max_retries=EXCLUDED.max_retries, enabled=EXCLUDED.enabled, updated_at=EXCLUDED.updated_at, docker_host=EXCLUDED.docker_host
	`,
		payload["id"], payload["tenant_id"], payload["name"], payload["description"], payload["mode"], parametersJSON,
		webhookURL, webhookMethod, webhookHeaders, webhookTimeoutMs,
		proxyBaseURL, proxyPath, proxyMethod, proxyQueryMapping, proxyHeaderMapping, proxyBodyMapping, proxyResponseMapping,
		runtime, code, packages,
		payload["timeout_ms"], payload["memory_mb"], payload["max_retries"], payload["enabled"], payload["created_at"], payload["updated_at"],
		dockerHost,
	)
	return err
}

// handleFunctionUpdated updates an existing function row.
func (pm *ProjectionManager) handleFunctionUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal function updated: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	// Build dynamic update query based on which fields are present
	setClauses := []string{"updated_at = $2::timestamptz"}
	args := []interface{}{payload["id"], payload["updated_at"]}
	argIndex := 3

	if name, ok := payload["name"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, name)
		argIndex++
	}
	if desc, ok := payload["description"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, desc)
		argIndex++
	}
	if mode, ok := payload["mode"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("mode = $%d", argIndex))
		args = append(args, mode)
		argIndex++
	}
	if params, ok := payload["parameters"].(map[string]interface{}); ok {
		paramsJSON, _ := json.Marshal(params)
		setClauses = append(setClauses, fmt.Sprintf("parameters = $%d", argIndex))
		args = append(args, paramsJSON)
		argIndex++
	}
	if enabled, ok := payload["enabled"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", argIndex))
		args = append(args, enabled)
		argIndex++
	}
	if timeoutMs, ok := payload["timeout_ms"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("timeout_ms = $%d", argIndex))
		args = append(args, timeoutMs)
		argIndex++
	}
	if memoryMB, ok := payload["memory_mb"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("memory_mb = $%d", argIndex))
		args = append(args, memoryMB)
		argIndex++
	}
	if maxRetries, ok := payload["max_retries"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("max_retries = $%d", argIndex))
		args = append(args, maxRetries)
		argIndex++
	}

	// Handle webhook config update
	if webhook, ok := payload["webhook"].(map[string]interface{}); ok {
		if url, ok := webhook["url"].(string); ok {
			setClauses = append(setClauses, fmt.Sprintf("webhook_url = $%d", argIndex))
			args = append(args, url)
			argIndex++
		}
		if method, ok := webhook["method"].(string); ok {
			setClauses = append(setClauses, fmt.Sprintf("webhook_method = $%d", argIndex))
			args = append(args, method)
			argIndex++
		}
		if headers, ok := webhook["headers"].(map[string]interface{}); ok {
			headersJSON, _ := json.Marshal(headers)
			setClauses = append(setClauses, fmt.Sprintf("webhook_headers = $%d", argIndex))
			args = append(args, headersJSON)
			argIndex++
		}
		if timeout, ok := webhook["timeout_ms"].(float64); ok {
			setClauses = append(setClauses, fmt.Sprintf("webhook_timeout_ms = $%d", argIndex))
			args = append(args, int32(timeout))
			argIndex++
		}
	}

	// Handle proxy config update
	if proxy, ok := payload["proxy"].(map[string]interface{}); ok {
		if baseURL, ok := proxy["base_url"].(string); ok {
			setClauses = append(setClauses, fmt.Sprintf("proxy_base_url = $%d", argIndex))
			args = append(args, baseURL)
			argIndex++
		}
		if path, ok := proxy["path"].(string); ok {
			setClauses = append(setClauses, fmt.Sprintf("proxy_path = $%d", argIndex))
			args = append(args, path)
			argIndex++
		}
		if method, ok := proxy["method"].(string); ok {
			setClauses = append(setClauses, fmt.Sprintf("proxy_method = $%d", argIndex))
			args = append(args, method)
			argIndex++
		}
		if qm, ok := proxy["query_mapping"].(map[string]interface{}); ok {
			qmJSON, _ := json.Marshal(qm)
			setClauses = append(setClauses, fmt.Sprintf("proxy_query_mapping = $%d", argIndex))
			args = append(args, qmJSON)
			argIndex++
		}
		if hm, ok := proxy["header_mapping"].(map[string]interface{}); ok {
			hmJSON, _ := json.Marshal(hm)
			setClauses = append(setClauses, fmt.Sprintf("proxy_header_mapping = $%d", argIndex))
			args = append(args, hmJSON)
			argIndex++
		}
		if bm, ok := proxy["body_mapping"].(map[string]interface{}); ok {
			bmJSON, _ := json.Marshal(bm)
			setClauses = append(setClauses, fmt.Sprintf("proxy_body_mapping = $%d", argIndex))
			args = append(args, bmJSON)
			argIndex++
		}
		if rm, ok := proxy["response_mapping"].(map[string]interface{}); ok {
			rmJSON, _ := json.Marshal(rm)
			setClauses = append(setClauses, fmt.Sprintf("proxy_response_mapping = $%d", argIndex))
			args = append(args, rmJSON)
			argIndex++
		}
	}

	// Handle isolated config update (Phase 2)
	if isolated, ok := payload["isolated"].(map[string]interface{}); ok {
		if runtime, ok := isolated["runtime"].(string); ok {
			setClauses = append(setClauses, fmt.Sprintf("runtime = $%d", argIndex))
			args = append(args, runtime)
			argIndex++
		}
		if code, ok := isolated["code"].(string); ok {
			setClauses = append(setClauses, fmt.Sprintf("code = $%d", argIndex))
			args = append(args, code)
			argIndex++
		}
		if pkgs, ok := isolated["packages"].([]interface{}); ok {
			var packages []string
			for _, p := range pkgs {
				if ps, ok := p.(string); ok {
					packages = append(packages, ps)
				}
			}
			setClauses = append(setClauses, fmt.Sprintf("packages = $%d", argIndex))
			args = append(args, packages)
			argIndex++
		}
	}

	// Handle docker_host from isolated config
	if isolated, ok := payload["isolated"].(map[string]interface{}); ok {
		if dh, ok := isolated["docker_host"].(string); ok {
			setClauses = append(setClauses, fmt.Sprintf("docker_host = $%d", argIndex))
			args = append(args, dh)
			argIndex++
		}
	}

	query := fmt.Sprintf("UPDATE functions SET %s WHERE id = $1",
		joinStrings(setClauses, ", "))

	_, err := pm.db.ExecContext(ctx, query, args...)
	return err
}

// handleFunctionDeleted removes a function row.
func (pm *ProjectionManager) handleFunctionDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal function deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		DELETE FROM functions WHERE id = $1 AND tenant_id = $2
	`, payload["id"], payload["tenant_id"])
	return err
}

// handleWorkflowCreated inserts a new workflow row.
func (pm *ProjectionManager) handleWorkflowCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal workflow created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	// Marshal JSONB fields
	nodesJSON, _ := json.Marshal(payload["nodes"])
	edgesJSON, _ := json.Marshal(payload["edges"])
	viewportJSON, _ := json.Marshal(payload["viewport"])

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO workflows (id, tenant_id, name, description, nodes, edges, viewport, enabled, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::timestamptz, $11::timestamptz)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description, nodes=EXCLUDED.nodes, edges=EXCLUDED.edges,
			viewport=EXCLUDED.viewport, enabled=EXCLUDED.enabled, version=EXCLUDED.version, updated_at=EXCLUDED.updated_at
	`,
		payload["id"], payload["tenant_id"], payload["name"], payload["description"],
		nodesJSON, edgesJSON, viewportJSON,
		payload["enabled"], payload["version"],
		payload["created_at"], payload["updated_at"],
	)
	return err
}

// handleWorkflowUpdated updates an existing workflow row.
func (pm *ProjectionManager) handleWorkflowUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal workflow updated: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	setClauses := []string{"updated_at = $2::timestamptz", "version = version + 1"}
	args := []interface{}{payload["id"], payload["updated_at"]}
	argIndex := 3

	if name, ok := payload["name"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, name)
		argIndex++
	}
	if desc, ok := payload["description"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, desc)
		argIndex++
	}
	if nodes, ok := payload["nodes"]; ok {
		nodesJSON, _ := json.Marshal(nodes)
		setClauses = append(setClauses, fmt.Sprintf("nodes = $%d", argIndex))
		args = append(args, nodesJSON)
		argIndex++
	}
	if edges, ok := payload["edges"]; ok {
		edgesJSON, _ := json.Marshal(edges)
		setClauses = append(setClauses, fmt.Sprintf("edges = $%d", argIndex))
		args = append(args, edgesJSON)
		argIndex++
	}
	if viewport, ok := payload["viewport"]; ok {
		viewportJSON, _ := json.Marshal(viewport)
		setClauses = append(setClauses, fmt.Sprintf("viewport = $%d", argIndex))
		args = append(args, viewportJSON)
		argIndex++
	}
	if enabled, ok := payload["enabled"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", argIndex))
		args = append(args, enabled)
		argIndex++
	}

	query := fmt.Sprintf("UPDATE workflows SET %s WHERE id = $1",
		joinStrings(setClauses, ", "))

	_, err := pm.db.ExecContext(ctx, query, args...)
	return err
}

// handleWorkflowDeleted removes a workflow row.
func (pm *ProjectionManager) handleWorkflowDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal workflow deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		DELETE FROM workflows WHERE id = $1 AND tenant_id = $2
	`, payload["id"], payload["tenant_id"])
	return err
}

// joinStrings joins strings with a separator (helper function).
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

// handleModelNotFoundEvent processes model.not_found events.
// This tracks configuration issues where requested models are not activated.
// Events are automatically persisted to ClickHouse/Postgres via Writer.
func (pm *ProjectionManager) handleModelNotFoundEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal model.not_found event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"requested_model", payload["requested_model"],
		"user_id", payload["user_id"],
		"api_key_hash", payload["api_key_hash"],
		"correlation_id", payload["correlation_id"],
		"error_type", payload["error_type"],
	).Warn("model not found - configuration issue detected")

	// TODO: Create analytics views/queries to:
	// - Identify frequently requested but unavailable models
	// - Alert on repeated configuration issues
	// - Track by user/API key to identify patterns

	return nil
}

// handleFallbackTriggeredEvent processes fallback.triggered events.
// This tracks when fallback routing is initiated.
// Events are automatically persisted to ClickHouse/Postgres via Writer.
func (pm *ProjectionManager) handleFallbackTriggeredEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal fallback.triggered event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"requested_model", payload["requested_model"],
		"fallback_reason", payload["fallback_reason"],
		"user_id", payload["user_id"],
		"api_key_hash", payload["api_key_hash"],
		"correlation_id", payload["correlation_id"],
	).Info("fallback routing triggered")

	// TODO: Create analytics views/queries to:
	// - Monitor fallback frequency by model and reason
	// - Alert when fallback rate exceeds threshold
	// - Track patterns to identify provider issues

	return nil
}

// handleFallbackSucceededEvent processes fallback.succeeded events.
// This tracks successful fallback operations and their performance.
// Events are automatically persisted to ClickHouse/Postgres via Writer.
func (pm *ProjectionManager) handleFallbackSucceededEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal fallback.succeeded event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"requested_model", payload["requested_model"],
		"actual_model", payload["actual_model"],
		"fallback_reason", payload["fallback_reason"],
		"fallback_attempts", payload["fallback_attempts"],
		"duration_ms", payload["duration_ms"],
		"user_id", payload["user_id"],
		"api_key_hash", payload["api_key_hash"],
		"correlation_id", payload["correlation_id"],
		"success", payload["success"],
	).Info("fallback routing succeeded")

	// TODO: Create analytics views/queries to:
	// - Calculate cost differences between requested and actual models
	// - Track fallback performance (attempts, duration)
	// - Identify most effective fallback paths
	// - Monitor SLA impact of fallback routing

	return nil
}

// handleFallbackFailedEvent processes fallback.failed events.
// This tracks when all fallback attempts are exhausted.
// Events are automatically persisted to ClickHouse/Postgres via Writer.
func (pm *ProjectionManager) handleFallbackFailedEvent(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal fallback.failed event: %w", err)
	}

	logger.WithFields(
		"event_id", event.ID,
		"event_type", event.Type,
		"requested_model", payload["requested_model"],
		"fallback_reason", payload["fallback_reason"],
		"fallback_attempts", payload["fallback_attempts"],
		"last_error", payload["last_error"],
		"user_id", payload["user_id"],
		"api_key_hash", payload["api_key_hash"],
		"correlation_id", payload["correlation_id"],
		"success", payload["success"],
	).Error("fallback routing failed - all attempts exhausted")

	// TODO: Build alerting/monitoring on top of persisted events:
	// - Alert immediately on fallback failures (high priority)
	// - Track failure patterns to identify systemic issues
	// - Monitor impact on user experience
	// - Trigger automatic incident creation for repeated failures

	return nil
}

// ============================================================================
// Agent Projections
// ============================================================================

// handleAgentCreated inserts a new agent definition row.
func (pm *ProjectionManager) handleAgentCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	// Marshal config JSONB
	var configJSON []byte
	if cfg, ok := payload["config"]; ok {
		configJSON, _ = json.Marshal(cfg)
	} else {
		configJSON = []byte("{}")
	}

	// Extract tools array
	var tools []string
	if toolsRaw, ok := payload["tools"].([]interface{}); ok {
		for _, t := range toolsRaw {
			if ts, ok := t.(string); ok {
				tools = append(tools, ts)
			}
		}
	}

	// Marshal JSONB fields for sandbox and worker config
	envVarsJSON, _ := json.Marshal(payload["sandbox_env_vars"])
	if payload["sandbox_env_vars"] == nil {
		envVarsJSON = []byte("{}")
	}
	workerPoolJSON, _ := json.Marshal(payload["worker_pool_config"])
	if payload["worker_pool_config"] == nil {
		workerPoolJSON = []byte("{}")
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO agent_definitions (
			id, tenant_id, name, description, model, system_prompt, tools, config,
			max_turns, max_tool_calls_per_turn, mode, max_steps, task_permission_mode,
			hidden, color, working_directory, mention_alias, enabled,
			lifecycle_mode, lifecycle_status, icon,
			soul_md, identity_md, user_md, role_md,
			sandbox_image, sandbox_cpu_limit, sandbox_memory_mb, sandbox_disk_mb,
			sandbox_timeout_seconds, sandbox_network_mode, sandbox_allowed_hosts,
			sandbox_env_vars, sandbox_ssh_enabled, sandbox_git_repo_url, sandbox_git_branch,
			db_sqlite_path, db_lancedb_path, db_redb_path,
			max_concurrent_workers, worker_pool_config,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
		          $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34,
		          $35, $36, $37, $38, $39, $40, $41, $42::timestamptz, $43::timestamptz)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description, model=EXCLUDED.model,
			system_prompt=EXCLUDED.system_prompt, tools=EXCLUDED.tools, config=EXCLUDED.config,
			max_turns=EXCLUDED.max_turns, max_tool_calls_per_turn=EXCLUDED.max_tool_calls_per_turn,
			mode=EXCLUDED.mode, max_steps=EXCLUDED.max_steps,
			task_permission_mode=EXCLUDED.task_permission_mode, hidden=EXCLUDED.hidden,
			color=EXCLUDED.color, working_directory=EXCLUDED.working_directory,
			mention_alias=EXCLUDED.mention_alias, enabled=EXCLUDED.enabled,
			lifecycle_mode=EXCLUDED.lifecycle_mode, lifecycle_status=EXCLUDED.lifecycle_status,
			icon=EXCLUDED.icon, soul_md=EXCLUDED.soul_md, identity_md=EXCLUDED.identity_md,
			user_md=EXCLUDED.user_md, role_md=EXCLUDED.role_md,
			sandbox_image=EXCLUDED.sandbox_image, sandbox_cpu_limit=EXCLUDED.sandbox_cpu_limit,
			sandbox_memory_mb=EXCLUDED.sandbox_memory_mb, sandbox_disk_mb=EXCLUDED.sandbox_disk_mb,
			sandbox_timeout_seconds=EXCLUDED.sandbox_timeout_seconds,
			sandbox_network_mode=EXCLUDED.sandbox_network_mode,
			sandbox_allowed_hosts=EXCLUDED.sandbox_allowed_hosts,
			sandbox_env_vars=EXCLUDED.sandbox_env_vars, sandbox_ssh_enabled=EXCLUDED.sandbox_ssh_enabled,
			sandbox_git_repo_url=EXCLUDED.sandbox_git_repo_url, sandbox_git_branch=EXCLUDED.sandbox_git_branch,
			db_sqlite_path=EXCLUDED.db_sqlite_path, db_lancedb_path=EXCLUDED.db_lancedb_path,
			db_redb_path=EXCLUDED.db_redb_path, max_concurrent_workers=EXCLUDED.max_concurrent_workers,
			worker_pool_config=EXCLUDED.worker_pool_config, updated_at=EXCLUDED.updated_at
	`,
		payload["id"], payload["tenant_id"], payload["name"], payload["description"],
		payload["model"], payload["system_prompt"], tools, configJSON,
		payload["max_turns"], payload["max_tool_calls_per_turn"], payload["mode"], payload["max_steps"],
		payload["task_permission_mode"], payload["hidden"], payload["color"], payload["working_directory"],
		payload["mention_alias"], payload["enabled"],
		payload["lifecycle_mode"], payload["lifecycle_status"], payload["icon"],
		payload["soul_md"], payload["identity_md"], payload["user_md"], payload["role_md"],
		payload["sandbox_image"], payload["sandbox_cpu_limit"], payload["sandbox_memory_mb"], payload["sandbox_disk_mb"],
		payload["sandbox_timeout_seconds"], payload["sandbox_network_mode"], payload["sandbox_allowed_hosts"],
		envVarsJSON, payload["sandbox_ssh_enabled"], payload["sandbox_git_repo_url"], payload["sandbox_git_branch"],
		payload["db_sqlite_path"], payload["db_lancedb_path"], payload["db_redb_path"],
		payload["max_concurrent_workers"], workerPoolJSON,
		payload["created_at"], payload["updated_at"],
	)
	return err
}

// handleAgentUpdated updates an existing agent definition row.
func (pm *ProjectionManager) handleAgentUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.updated: %w", err)
	}
	if pm.db == nil {
		logger.Warn("handleAgentUpdated: db is nil, skipping projection")
		return nil
	}
	logger.WithFields(
		"agent_id", payload["id"],
		"has_config", payload["config"] != nil,
		"has_name", payload["name"] != nil,
	).Info("handleAgentUpdated: projecting agent update")

	setClauses := []string{"updated_at = $2::timestamptz"}
	args := []interface{}{payload["id"], payload["updated_at"]}
	argIndex := 3

	if name, ok := payload["name"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, name)
		argIndex++
	}
	if desc, ok := payload["description"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, desc)
		argIndex++
	}
	if model, ok := payload["model"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("model = $%d", argIndex))
		args = append(args, model)
		argIndex++
	}
	if sp, ok := payload["system_prompt"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("system_prompt = $%d", argIndex))
		args = append(args, sp)
		argIndex++
	}
	if toolsRaw, ok := payload["tools"].([]interface{}); ok {
		// Preserve an explicit empty JSON array as an empty PostgreSQL array.
		// A nil slice may otherwise be encoded as SQL NULL.
		tools := make([]string, 0, len(toolsRaw))
		for _, t := range toolsRaw {
			if ts, ok := t.(string); ok {
				tools = append(tools, ts)
			}
		}
		setClauses = append(setClauses, fmt.Sprintf("tools = $%d", argIndex))
		args = append(args, tools)
		argIndex++
	}
	if cfg, ok := payload["config"]; ok {
		cfgJSON, _ := json.Marshal(cfg)
		setClauses = append(setClauses, fmt.Sprintf("config = $%d", argIndex))
		args = append(args, cfgJSON)
		argIndex++
	}
	if mt, ok := payload["max_turns"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("max_turns = $%d", argIndex))
		args = append(args, mt)
		argIndex++
	}
	if mtc, ok := payload["max_tool_calls_per_turn"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("max_tool_calls_per_turn = $%d", argIndex))
		args = append(args, mtc)
		argIndex++
	}
	if enabled, ok := payload["enabled"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", argIndex))
		args = append(args, enabled)
		argIndex++
	}
	if mode, ok := payload["mode"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("mode = $%d", argIndex))
		args = append(args, mode)
		argIndex++
	}
	if maxSteps, ok := payload["max_steps"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("max_steps = $%d", argIndex))
		args = append(args, maxSteps)
		argIndex++
	}
	if taskPermissionMode, ok := payload["task_permission_mode"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("task_permission_mode = $%d", argIndex))
		args = append(args, taskPermissionMode)
		argIndex++
	}
	if hidden, ok := payload["hidden"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("hidden = $%d", argIndex))
		args = append(args, hidden)
		argIndex++
	}
	if color, ok := payload["color"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("color = $%d", argIndex))
		args = append(args, color)
		argIndex++
	}
	if workingDirectory, ok := payload["working_directory"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("working_directory = $%d", argIndex))
		args = append(args, workingDirectory)
		argIndex++
	}
	if mentionAlias, ok := payload["mention_alias"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("mention_alias = $%d", argIndex))
		args = append(args, mentionAlias)
		argIndex++
	}
	if v, ok := payload["lifecycle_mode"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("lifecycle_mode = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["lifecycle_status"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("lifecycle_status = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["icon"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("icon = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["soul_md"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("soul_md = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["identity_md"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("identity_md = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["user_md"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("user_md = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["role_md"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("role_md = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_image"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_image = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_cpu_limit"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_cpu_limit = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_memory_mb"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_memory_mb = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_disk_mb"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_disk_mb = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_timeout_seconds"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_timeout_seconds = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_network_mode"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_network_mode = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_allowed_hosts"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_allowed_hosts = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_env_vars"]; ok {
		envJSON, _ := json.Marshal(v)
		setClauses = append(setClauses, fmt.Sprintf("sandbox_env_vars = $%d", argIndex))
		args = append(args, envJSON)
		argIndex++
	}
	if v, ok := payload["sandbox_ssh_enabled"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_ssh_enabled = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_git_repo_url"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_git_repo_url = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["sandbox_git_branch"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("sandbox_git_branch = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["db_sqlite_path"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("db_sqlite_path = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["db_lancedb_path"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("db_lancedb_path = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["db_redb_path"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("db_redb_path = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["max_concurrent_workers"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("max_concurrent_workers = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}
	if v, ok := payload["worker_pool_config"]; ok {
		wpJSON, _ := json.Marshal(v)
		setClauses = append(setClauses, fmt.Sprintf("worker_pool_config = $%d", argIndex))
		args = append(args, wpJSON)
		argIndex++
	}

	query := fmt.Sprintf("UPDATE agent_definitions SET %s WHERE id = $1",
		joinStrings(setClauses, ", "))

	result, err := pm.db.ExecContext(ctx, query, args...)
	if err != nil {
		logger.WithFields(
			"agent_id", payload["id"],
			"error", err.Error(),
		).Error("handleAgentUpdated: SQL exec failed")
		return err
	}
	rowsAffected, _ := result.RowsAffected()
	logger.WithFields(
		"agent_id", payload["id"],
		"rows_affected", rowsAffected,
		"set_clauses", len(setClauses),
	).Info("handleAgentUpdated: projection complete")
	return nil
}

// handleAgentDeleted soft-deletes an agent definition by setting deleted_at.
// Sessions and turns are preserved for audit trail and history.
func (pm *ProjectionManager) handleAgentDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	agentID, _ := payload["id"].(string)
	deletedAt := payload["deleted_at"]

	_, err := pm.db.ExecContext(ctx, `
		UPDATE agent_definitions
		SET deleted_at = $2::timestamptz, enabled = FALSE, updated_at = $2::timestamptz
		WHERE id = $1 AND deleted_at IS NULL
	`, agentID, deletedAt)
	if err != nil {
		return err
	}

	// Clean up crons and webhooks scoped to this agent's sessions.
	// Disable rather than delete so history is preserved.
	pm.db.ExecContext(ctx, `
		UPDATE sandbox_crons SET enabled = FALSE, updated_at = NOW()
		WHERE session_id IN (SELECT id FROM agent_sessions WHERE agent_id = $1)
	`, agentID)
	pm.db.ExecContext(ctx, `
		UPDATE sandbox_webhooks SET enabled = FALSE, updated_at = NOW()
		WHERE session_id IN (SELECT id FROM agent_sessions WHERE agent_id = $1)
	`, agentID)

	return nil
}

func (pm *ProjectionManager) handleAgentLinkCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.link.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}
	configJSON, _ := json.Marshal(payload["config"])
	if payload["config"] == nil {
		configJSON = []byte("{}")
	}
	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO agent_links (id, tenant_id, source_agent_id, target_type, target_id, target_name, link_type, protocol, status, config, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::timestamptz, $12::timestamptz)
		ON CONFLICT (id) DO NOTHING
	`, payload["id"], payload["tenant_id"], payload["source_agent_id"], payload["target_type"],
		payload["target_id"], payload["target_name"], payload["link_type"], payload["protocol"],
		payload["status"], configJSON, payload["created_at"], payload["updated_at"])
	return err
}

func (pm *ProjectionManager) handleAgentLinkDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.link.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}
	_, err := pm.db.ExecContext(ctx, `DELETE FROM agent_links WHERE id = $1 AND tenant_id = $2`, payload["link_id"], payload["tenant_id"])
	return err
}

func (pm *ProjectionManager) handleAgentChannelBound(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.channel.bound: %w", err)
	}
	if pm.db == nil {
		return nil
	}
	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO agent_channel_bindings (id, tenant_id, agent_id, channel_config_id, enabled, created_at)
		VALUES ($1, $2, $3, $4, $5, $6::timestamptz)
		ON CONFLICT (agent_id, channel_config_id) DO NOTHING
	`, payload["id"], payload["tenant_id"], payload["agent_id"], payload["channel_config_id"],
		payload["enabled"], payload["created_at"])
	return err
}

func (pm *ProjectionManager) handleAgentChannelUnbound(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.channel.unbound: %w", err)
	}
	if pm.db == nil {
		return nil
	}
	_, err := pm.db.ExecContext(ctx, `
		DELETE FROM agent_channel_bindings WHERE agent_id = $1 AND channel_config_id = $2 AND tenant_id = $3
	`, payload["agent_id"], payload["channel_config_id"], payload["tenant_id"])
	return err
}

// handleAgentSessionCreated inserts a new agent session row.
func (pm *ProjectionManager) handleAgentSessionCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.session.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	var metadataJSON []byte
	if md, ok := payload["metadata"]; ok {
		metadataJSON, _ = json.Marshal(md)
	} else {
		metadataJSON = []byte("{}")
	}

	// trooper_id is optional — only set for trooper sessions
	var trooperID interface{}
	if wid, ok := payload["trooper_id"].(string); ok && wid != "" {
		trooperID = wid
	}

	// agent_id may be empty for trooper sessions
	var agentID interface{}
	if aid, ok := payload["agent_id"].(string); ok && aid != "" {
		agentID = aid
	}

	// revision_id is captured when the create-session command is handled. Do
	// not resolve the active revision here: projections are asynchronous and a
	// deploy between command handling and projection would pin the wrong code.
	var revisionID interface{}
	if rid, ok := payload["revision_id"].(string); ok && rid != "" {
		revisionID = rid
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO agent_sessions (
			id, tenant_id, agent_id, trooper_id, revision_id,
			status, turn_count, total_tokens, metadata, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10::timestamptz, $11::timestamptz
		)
		ON CONFLICT (id) DO UPDATE SET
			status=EXCLUDED.status, updated_at=EXCLUDED.updated_at
	`,
		payload["id"], payload["tenant_id"], agentID, trooperID, revisionID, payload["status"],
		payload["turn_count"], payload["total_tokens"], metadataJSON,
		payload["created_at"], payload["updated_at"],
	)
	return err
}

// handleAgentSessionCompleted updates session status to completed/failed.
func (pm *ProjectionManager) handleAgentSessionCompleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.session.completed: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		UPDATE agent_sessions
		SET status = $2, updated_at = $3::timestamptz, completed_at = $4::timestamptz
		WHERE id = $1
	`, payload["session_id"], payload["status"], payload["updated_at"], payload["completed_at"])
	return err
}

// handleAgentSessionCancelled updates session status to cancelled.
func (pm *ProjectionManager) handleAgentSessionCancelled(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.session.cancelled: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		UPDATE agent_sessions
		SET status = $2, updated_at = $3::timestamptz, completed_at = $4::timestamptz
		WHERE id = $1
	`, payload["session_id"], payload["status"], payload["updated_at"], payload["completed_at"])
	return err
}

// handleAgentApprovalRequested inserts an approval review row.
func (pm *ProjectionManager) handleAgentApprovalRequested(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.approval.requested: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	var toolCallsJSON []byte
	if tc, ok := payload["tool_calls"]; ok {
		toolCallsJSON, _ = json.Marshal(tc)
	} else {
		toolCallsJSON = []byte("[]")
	}

	defaultAction := "deny"
	if da, ok := payload["default_action"].(string); ok && da != "" {
		defaultAction = da
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO agent_approval_reviews (
			id, session_id, tenant_id, agent_id, turn_number, iteration,
			status, tool_calls, default_action, requested_at, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, $9::timestamptz, $10::timestamptz, NOW())
		ON CONFLICT (id) DO NOTHING
	`,
		payload["review_id"], payload["session_id"], payload["tenant_id"], payload["agent_id"],
		payload["turn_number"], payload["iteration"],
		toolCallsJSON, defaultAction,
		payload["requested_at"], payload["expires_at"],
	)
	return err
}

// handleAgentTurnCompleted inserts a turn row and updates session metrics.
func (pm *ProjectionManager) handleAgentTurnCompleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal agent.turn.completed: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	var toolCallsJSON []byte
	if tc, ok := payload["tool_calls"]; ok {
		toolCallsJSON, _ = json.Marshal(tc)
	} else {
		toolCallsJSON = []byte("[]")
	}

	// timeline is optional — a nil []byte persists as NULL, and the UI falls
	// back to the flat fields for turns recorded before this column existed.
	var timelineJSON []byte
	if tl, ok := payload["timeline"]; ok && tl != nil {
		timelineJSON, _ = json.Marshal(tl)
	}

	// Insert turn
	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO agent_session_turns (
			id, session_id, turn_number, status, user_input, assistant_output, tool_calls,
			prompt_tokens, completion_tokens, total_tokens,
			cache_read_input_tokens, cache_write_input_tokens,
			latency_ms, error, created_at, completed_at, timeline
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::timestamptz, $16::timestamptz, $17)
		ON CONFLICT (session_id, turn_number) DO UPDATE SET
			status=EXCLUDED.status, user_input=EXCLUDED.user_input, assistant_output=EXCLUDED.assistant_output,
			tool_calls=EXCLUDED.tool_calls,
			prompt_tokens=EXCLUDED.prompt_tokens, completion_tokens=EXCLUDED.completion_tokens,
			total_tokens=EXCLUDED.total_tokens,
			cache_read_input_tokens=EXCLUDED.cache_read_input_tokens,
			cache_write_input_tokens=EXCLUDED.cache_write_input_tokens,
			latency_ms=EXCLUDED.latency_ms, error=EXCLUDED.error,
			completed_at=EXCLUDED.completed_at,
			timeline=COALESCE(EXCLUDED.timeline, agent_session_turns.timeline)
	`,
		payload["id"], payload["session_id"], payload["turn_number"], payload["status"],
		payload["user_input"], payload["assistant_output"], toolCallsJSON,
		payload["prompt_tokens"], payload["completion_tokens"], payload["total_tokens"],
		payloadInt(payload["cache_read_input_tokens"]), payloadInt(payload["cache_write_input_tokens"]),
		payload["latency_ms"], payload["error"],
		payload["created_at"], payload["completed_at"], timelineJSON,
	)
	if err != nil {
		return err
	}

	// Update session metrics with optimistic concurrency guard.
	// Only increment turn_count if it matches the expected value to prevent
	// double-increments on event replay and reject concurrent duplicate turns.
	totalTokens := float64(0)
	if tt, ok := payload["total_tokens"].(float64); ok {
		totalTokens = tt
	}
	expectedTurnCount := float64(0)
	if etc, ok := payload["expected_turn_count"].(float64); ok {
		expectedTurnCount = etc
	}
	result, err := pm.db.ExecContext(ctx, `
		UPDATE agent_sessions
		SET turn_count = turn_count + 1,
		    total_tokens = total_tokens + $2,
		    updated_at = NOW()
		WHERE id = $1 AND turn_count = $3
	`, payload["session_id"], int32(totalTokens), int32(expectedTurnCount))
	if err != nil {
		return err
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		logger.WithFields(
			"session_id", payload["session_id"],
			"expected_turn_count", int32(expectedTurnCount),
		).Warn("agent turn completed: session metric update skipped (concurrent turn or replay)")
	}

	// Best-effort usage billing record for this turn.
	tenantID, model := pm.lookupSessionTenantAndModel(ctx, payload["session_id"])
	promptTokens := payloadInt(payload["prompt_tokens"])
	completionTokens := payloadInt(payload["completion_tokens"])
	totalTokensInt := payloadInt(payload["total_tokens"])
	cost := metrics.CalculateCost("", model, promptTokens, completionTokens, 0)
	createdAt := payloadTime(payload["created_at"])
	completedAt := payloadTime(payload["completed_at"])

	turnRef := fmt.Sprintf("%v:%v", payload["session_id"], payload["turn_number"])
	if err := usage.InsertBillingUsageRecord(ctx, pm.db, usage.BillingUsageRecord{
		IdempotencyKey: "agent-turn:" + turnRef,
		TenantID:       tenantID,
		ResourceType:   "agent_session",
		ResourceID:     fmt.Sprintf("%v", payload["session_id"]),
		SourceType:     "agent.turn.completed",
		SourceRef:      turnRef,
		MetricType:     "agent.tokens",
		Quantity:       float64(totalTokensInt),
		Unit:           "tokens",
		CostUSD:        cost.EstimatedUSD,
		Metadata: map[string]interface{}{
			"turn_id":           payload["id"],
			"turn_number":       payload["turn_number"],
			"status":            payload["status"],
			"model":             model,
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      totalTokensInt,
			"latency_ms":        payload["latency_ms"],
			"error":             payload["error"],
		},
		PeriodStart: createdAt,
		PeriodEnd:   completedAt,
	}); err != nil {
		logger.WithFields(
			"session_id", payload["session_id"],
			"turn_number", payload["turn_number"],
			"error", err.Error(),
		).Warn("agent turn completed: failed to persist billing usage record")
	}

	return nil
}

func (pm *ProjectionManager) lookupSessionTenantAndModel(ctx context.Context, sessionID interface{}) (string, string) {
	if pm.db == nil || sessionID == nil {
		return "", ""
	}

	var row struct {
		TenantID string `db:"tenant_id"`
		Model    string `db:"model"`
	}
	if err := pm.db.GetContext(ctx, &row, `
		SELECT s.tenant_id::text AS tenant_id, COALESCE(a.model, w.model, '') AS model
		FROM agent_sessions s
		LEFT JOIN agent_definitions a ON a.id = s.agent_id
		LEFT JOIN troopers w ON w.id = s.trooper_id
		WHERE s.id = $1
		LIMIT 1
	`, sessionID); err != nil {
		return "", ""
	}
	return row.TenantID, row.Model
}

func (pm *ProjectionManager) handleBillingUsageRecorded(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal billing.usage_recorded: %w", err)
	}

	metadata := map[string]interface{}{}
	if raw := payload["metadata"]; raw != nil {
		if m, ok := raw.(map[string]interface{}); ok {
			metadata = m
		}
	}

	rec := usage.BillingUsageRecord{
		IdempotencyKey: fmt.Sprintf("%v", payload["idempotency_key"]),
		TenantID:       fmt.Sprintf("%v", payload["tenant_id"]),
		ResourceType:   fmt.Sprintf("%v", payload["resource_type"]),
		ResourceID:     fmt.Sprintf("%v", payload["resource_id"]),
		SourceType:     fmt.Sprintf("%v", payload["source_type"]),
		SourceRef:      fmt.Sprintf("%v", payload["source_ref"]),
		MetricType:     fmt.Sprintf("%v", payload["metric_type"]),
		Quantity:       payloadFloat(payload["quantity"]),
		Unit:           fmt.Sprintf("%v", payload["unit"]),
		CostUSD:        payloadFloat(payload["cost_usd"]),
		Currency:       fmt.Sprintf("%v", payload["currency"]),
		Status:         fmt.Sprintf("%v", payload["status"]),
		Metadata:       metadata,
		PeriodStart:    payloadTime(payload["period_start"]),
		PeriodEnd:      payloadTime(payload["period_end"]),
	}

	if rec.ResourceID == "<nil>" {
		rec.ResourceID = ""
	}
	if rec.Currency == "<nil>" {
		rec.Currency = ""
	}
	if rec.Status == "<nil>" {
		rec.Status = ""
	}

	return usage.InsertBillingUsageRecord(ctx, pm.db, rec)
}

func payloadInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}

func payloadFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func payloadTime(v interface{}) *time.Time {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

func payloadString(v interface{}) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

func payloadBool(v interface{}) bool {
	if v == nil {
		return false
	}
	b, ok := v.(bool)
	if ok {
		return b
	}
	return false
}

func payloadJSON(v interface{}) []byte {
	if v == nil {
		return []byte("{}")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func payloadOptionalJSON(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return nil
	}
	return s
}

func payloadStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func joinSetClauses(clauses []string) string {
	result := ""
	for i, c := range clauses {
		if i > 0 {
			result += ", "
		}
		result += c
	}
	return result
}

// ============================================================================
// Dataset Projections
// ============================================================================

func (pm *ProjectionManager) handleDatasetCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal dataset.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO datasets (id, tenant_id, name, description, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::timestamptz, $7::timestamptz)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description,
			metadata=EXCLUDED.metadata, updated_at=EXCLUDED.updated_at
	`,
		payload["id"], payload["tenant_id"], payload["name"], payloadString(payload["description"]),
		payloadJSON(payload["metadata"]),
		payload["created_at"], payload["updated_at"],
	)
	return err
}

func (pm *ProjectionManager) handleDatasetUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal dataset.updated: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	setClauses := []string{"updated_at = $2::timestamptz"}
	args := []interface{}{payload["id"], payload["updated_at"]}
	argIndex := 3

	if name, ok := payload["name"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, name)
		argIndex++
	}
	if desc, ok := payload["description"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, desc)
		argIndex++
	}
	if meta, ok := payload["metadata"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("metadata = $%d", argIndex))
		args = append(args, payloadJSON(meta))
		argIndex++
	}

	query := fmt.Sprintf("UPDATE datasets SET %s WHERE id = $1", joinSetClauses(setClauses))
	_, err := pm.db.ExecContext(ctx, query, args...)
	return err
}

func (pm *ProjectionManager) handleDatasetDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal dataset.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx,
		"UPDATE datasets SET archived_at = $2::timestamptz WHERE id = $1",
		payload["id"], payload["deleted_at"],
	)
	return err
}

// ============================================================================
// Dataset Item Projections
// ============================================================================

func (pm *ProjectionManager) handleDatasetItemCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal dataset_item.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO dataset_items (
			id, dataset_id, tenant_id, input, expected_output, metadata,
			source_trace_id, source_observation_id, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::timestamptz, $11::timestamptz)
		ON CONFLICT (id) DO NOTHING
	`,
		payload["id"], payload["dataset_id"], payload["tenant_id"],
		payloadJSON(payload["input"]), payloadJSON(payload["expected_output"]),
		payloadJSON(payload["metadata"]),
		payloadString(payload["source_trace_id"]),
		payloadString(payload["source_observation_id"]),
		payloadString(payload["status"]),
		payload["created_at"], payload["updated_at"],
	)
	return err
}

func (pm *ProjectionManager) handleDatasetItemUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal dataset_item.updated: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	setClauses := []string{"updated_at = $2::timestamptz"}
	args := []interface{}{payload["id"], payload["updated_at"]}
	argIndex := 3

	if input, ok := payload["input"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("input = $%d", argIndex))
		args = append(args, payloadJSON(input))
		argIndex++
	}
	if eo, ok := payload["expected_output"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("expected_output = $%d", argIndex))
		args = append(args, payloadJSON(eo))
		argIndex++
	}
	if meta, ok := payload["metadata"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("metadata = $%d", argIndex))
		args = append(args, payloadJSON(meta))
		argIndex++
	}
	if status, ok := payload["status"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, status)
		argIndex++
	}

	query := fmt.Sprintf("UPDATE dataset_items SET %s WHERE id = $1", joinSetClauses(setClauses))
	_, err := pm.db.ExecContext(ctx, query, args...)
	return err
}

func (pm *ProjectionManager) handleDatasetItemDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal dataset_item.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, "DELETE FROM dataset_items WHERE id = $1 AND tenant_id = $2", payload["id"], payload["tenant_id"])
	return err
}

// ============================================================================
// Score Config Projections
// ============================================================================

func (pm *ProjectionManager) handleScoreConfigCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal score_config.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO score_configs (
			id, tenant_id, name, data_type, description, min_value, max_value,
			categories, eval_prompt, eval_model, is_archived,
			scorer_code, scorer_language, use_sandbox, dag_definition,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb, $16::timestamptz, $17::timestamptz)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, data_type=EXCLUDED.data_type, description=EXCLUDED.description,
			min_value=EXCLUDED.min_value, max_value=EXCLUDED.max_value,
			categories=EXCLUDED.categories, eval_prompt=EXCLUDED.eval_prompt,
			eval_model=EXCLUDED.eval_model,
			scorer_code=EXCLUDED.scorer_code, scorer_language=EXCLUDED.scorer_language,
			use_sandbox=EXCLUDED.use_sandbox, dag_definition=EXCLUDED.dag_definition,
			updated_at=EXCLUDED.updated_at
	`,
		payload["id"], payload["tenant_id"], payload["name"], payload["data_type"],
		payloadString(payload["description"]),
		payload["min_value"], payload["max_value"],
		payloadJSON(payload["categories"]),
		payloadString(payload["eval_prompt"]),
		payloadString(payload["eval_model"]),
		payloadBool(payload["is_archived"]),
		payloadString(payload["scorer_code"]),
		payloadString(payload["scorer_language"]),
		payloadBool(payload["use_sandbox"]),
		payloadOptionalJSON(payload["dag_definition"]),
		payload["created_at"], payload["updated_at"],
	)
	return err
}

func (pm *ProjectionManager) handleScoreConfigUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal score_config.updated: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	setClauses := []string{"updated_at = $2::timestamptz"}
	args := []interface{}{payload["id"], payload["updated_at"]}
	argIndex := 3

	for _, field := range []string{"name", "description", "eval_prompt", "eval_model", "scorer_code", "scorer_language"} {
		if v, ok := payload[field]; ok {
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", field, argIndex))
			args = append(args, v)
			argIndex++
		}
	}
	if minV, ok := payload["min_value"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("min_value = $%d", argIndex))
		args = append(args, minV)
		argIndex++
	}
	if maxV, ok := payload["max_value"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("max_value = $%d", argIndex))
		args = append(args, maxV)
		argIndex++
	}
	if cats, ok := payload["categories"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("categories = $%d", argIndex))
		args = append(args, payloadJSON(cats))
		argIndex++
	}
	if ia, ok := payload["is_archived"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("is_archived = $%d", argIndex))
		args = append(args, ia)
		argIndex++
	}
	if us, ok := payload["use_sandbox"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("use_sandbox = $%d", argIndex))
		args = append(args, us)
		argIndex++
	}
	if dagDefinition, ok := payload["dag_definition"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("dag_definition = $%d::jsonb", argIndex))
		args = append(args, payloadOptionalJSON(dagDefinition))
		argIndex++
	}

	query := fmt.Sprintf("UPDATE score_configs SET %s WHERE id = $1", joinSetClauses(setClauses))
	_, err := pm.db.ExecContext(ctx, query, args...)
	return err
}

func (pm *ProjectionManager) handleScoreConfigDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal score_config.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, "DELETE FROM score_configs WHERE id = $1 AND tenant_id = $2", payload["id"], payload["tenant_id"])
	return err
}

// ============================================================================
// Eval Run Projections
// ============================================================================

func (pm *ProjectionManager) handleEvalRunCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal eval_run.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO eval_runs (
			id, tenant_id, dataset_id, name, description, status,
			eval_target_type, eval_target_id, eval_config, scorer_config_ids,
			dataset_version_id, total_items, completed_items, failed_items, score_summary,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12, $13, $14, $15, $16::timestamptz, $17::timestamptz)
		ON CONFLICT (id) DO NOTHING
	`,
		payload["id"], payload["tenant_id"], payload["dataset_id"],
		payload["name"], payloadString(payload["description"]), payload["status"],
		payload["eval_target_type"], payloadString(payload["eval_target_id"]),
		payloadJSON(payload["eval_config"]),
		payloadStringSlice(payload["scorer_config_ids"]),
		payloadString(payload["dataset_version_id"]),
		payloadInt(payload["total_items"]),
		payloadInt(payload["completed_items"]),
		payloadInt(payload["failed_items"]),
		payloadJSON(payload["score_summary"]),
		payload["created_at"], payload["updated_at"],
	)
	return err
}

func (pm *ProjectionManager) handleEvalRunCancelled(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal eval_run.cancelled: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx,
		`UPDATE eval_runs
		SET status = $2,
			lease_owner = NULL,
			lease_expires_at = NULL,
			lease_epoch = lease_epoch + 1,
			updated_at = $3::timestamptz
		WHERE id = $1`,
		payload["id"], payload["status"], payload["updated_at"],
	)
	return err
}

func (pm *ProjectionManager) handleEvalRunDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal eval_run.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	// Delete eval run items first, then the run itself
	_, _ = pm.db.ExecContext(ctx, "DELETE FROM eval_run_items WHERE eval_run_id = $1 AND tenant_id = $2", payload["id"], payload["tenant_id"])
	_, err := pm.db.ExecContext(ctx, "DELETE FROM eval_runs WHERE id = $1 AND tenant_id = $2", payload["id"], payload["tenant_id"])
	return err
}

// ============================================================================
// Annotation Queue Projections
// ============================================================================

func (pm *ProjectionManager) handleAnnotationQueueCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal annotation_queue.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO annotation_queues (
			id, tenant_id, name, description, status,
			score_config_ids, assignment_mode, annotators, auto_populate_config,
			items_pending, items_completed, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::timestamptz, $13::timestamptz)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description, status=EXCLUDED.status,
			updated_at=EXCLUDED.updated_at
	`,
		payload["id"], payload["tenant_id"], payload["name"],
		payloadString(payload["description"]),
		payloadString(payload["status"]),
		payloadStringSlice(payload["score_config_ids"]),
		payloadString(payload["assignment_mode"]),
		payloadStringSlice(payload["annotators"]),
		payloadJSON(payload["auto_populate_config"]),
		payloadInt(payload["items_pending"]),
		payloadInt(payload["items_completed"]),
		payload["created_at"], payload["updated_at"],
	)
	return err
}

func (pm *ProjectionManager) handleAnnotationQueueUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal annotation_queue.updated: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	setClauses := []string{"updated_at = $2::timestamptz"}
	args := []interface{}{payload["id"], payload["updated_at"]}
	argIndex := 3

	for _, field := range []string{"name", "description", "status", "assignment_mode"} {
		if v, ok := payload[field]; ok {
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", field, argIndex))
			args = append(args, v)
			argIndex++
		}
	}
	if scIDs, ok := payload["score_config_ids"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("score_config_ids = $%d", argIndex))
		args = append(args, payloadStringSlice(scIDs))
		argIndex++
	}
	if ann, ok := payload["annotators"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("annotators = $%d", argIndex))
		args = append(args, payloadStringSlice(ann))
		argIndex++
	}

	query := fmt.Sprintf("UPDATE annotation_queues SET %s WHERE id = $1", joinSetClauses(setClauses))
	_, err := pm.db.ExecContext(ctx, query, args...)
	return err
}

func (pm *ProjectionManager) handleAnnotationQueueDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal annotation_queue.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, "DELETE FROM annotation_queues WHERE id = $1 AND tenant_id = $2", payload["id"], payload["tenant_id"])
	return err
}

func (pm *ProjectionManager) handleAnnotationItemAdded(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal annotation_queue_item.added: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO annotation_queue_items (
			id, queue_id, tenant_id, trace_id, observation_id,
			status, priority, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::timestamptz, $9::timestamptz)
		ON CONFLICT (id) DO NOTHING
	`,
		payload["id"], payload["queue_id"], payload["tenant_id"],
		payloadString(payload["trace_id"]),
		payloadString(payload["observation_id"]),
		payloadString(payload["status"]),
		payloadInt(payload["priority"]),
		payload["created_at"], payload["updated_at"],
	)
	if err != nil {
		return err
	}

	// Increment pending count on the queue
	_, err = pm.db.ExecContext(ctx,
		"UPDATE annotation_queues SET items_pending = items_pending + 1 WHERE id = $1",
		payload["queue_id"],
	)
	return err
}

func (pm *ProjectionManager) handleAnnotationItemCompleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal annotation_queue_item.completed: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	result, err := pm.db.ExecContext(ctx, `
		UPDATE annotation_queue_items
		SET
			status = 'completed',
			completed_by = $2,
			completed_at = $3::timestamptz,
			updated_at = $4::timestamptz,
			assigned_to = CASE WHEN assigned_to = '' AND $5 <> '' THEN $5 ELSE assigned_to END,
			assigned_at = CASE WHEN assigned_to = '' AND $5 <> '' THEN $3::timestamptz ELSE assigned_at END
		WHERE id = $1
			AND tenant_id = $6
			AND (assigned_to = $5 OR assigned_to = '')
			AND EXISTS (
				SELECT 1
				FROM annotation_queues
				WHERE annotation_queues.id = annotation_queue_items.queue_id
					AND annotation_queues.tenant_id = $6
					AND (
						COALESCE(cardinality(annotation_queues.annotators), 0) = 0
						OR ($5 <> '' AND $5 = ANY(annotation_queues.annotators))
					)
			)
	`,
		payload["item_id"], payloadString(payload["completed_by"]),
		payload["completed_at"], payload["updated_at"],
		payloadString(payload["user_id"]), payload["tenant_id"],
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify annotation_queue_item.completed projection update: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("annotation_queue_item.completed projection update affected no rows")
	}
	return nil
}

func (pm *ProjectionManager) handleAnnotationItemSkipped(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal annotation_queue_item.skipped: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	result, err := pm.db.ExecContext(ctx, `
		UPDATE annotation_queue_items
		SET
			status = 'skipped',
			completed_at = $2::timestamptz,
			updated_at = $3::timestamptz,
			assigned_to = CASE WHEN assigned_to = '' AND $4 <> '' THEN $4 ELSE assigned_to END,
			assigned_at = CASE WHEN assigned_to = '' AND $4 <> '' THEN $2::timestamptz ELSE assigned_at END
		WHERE id = $1
			AND tenant_id = $5
			AND (assigned_to = $4 OR assigned_to = '')
			AND EXISTS (
				SELECT 1
				FROM annotation_queues
				WHERE annotation_queues.id = annotation_queue_items.queue_id
					AND annotation_queues.tenant_id = $5
					AND (
						COALESCE(cardinality(annotation_queues.annotators), 0) = 0
						OR ($4 <> '' AND $4 = ANY(annotation_queues.annotators))
					)
			)
	`,
		payload["item_id"], payload["completed_at"], payload["updated_at"],
		payloadString(payload["user_id"]), payload["tenant_id"],
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify annotation_queue_item.skipped projection update: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("annotation_queue_item.skipped projection update affected no rows")
	}
	return nil
}

// ============================================================================
// Storage Config Projections
// ============================================================================

func storageProjectionIdentity(payload map[string]interface{}) (string, string, error) {
	id := strings.TrimSpace(payloadString(payload["id"]))
	if id == "" {
		return "", "", fmt.Errorf("storage projection payload is missing id")
	}
	tenantID := strings.TrimSpace(payloadString(payload["tenant_id"]))
	if tenantID == "" {
		return "", "", fmt.Errorf("storage projection payload is missing tenant_id")
	}
	return id, tenantID, nil
}

func (pm *ProjectionManager) rejectLegacyStorageCredentialEventAfterCutover(ctx context.Context, payload map[string]interface{}) error {
	_, hasAccessKey := payload["access_key_id"]
	_, hasSecretKey := payload["secret_access_key"]
	if !hasAccessKey && !hasSecretKey {
		return nil
	}
	var enabled bool
	if err := pm.db.GetContext(ctx, &enabled,
		"SELECT cutover_enabled FROM object_storage_credential_state WHERE singleton = TRUE",
	); err != nil {
		return fmt.Errorf("read storage credential cutover state: %w", err)
	}
	if enabled {
		return fmt.Errorf("legacy plaintext storage credential event rejected after cutover")
	}
	return nil
}

func (pm *ProjectionManager) handleStorageConfigCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal storage_config.created: %w", err)
	}
	id, tenantID, err := storageProjectionIdentity(payload)
	if err != nil {
		return err
	}
	if pm.db == nil {
		return nil
	}
	if err := pm.rejectLegacyStorageCredentialEventAfterCutover(ctx, payload); err != nil {
		return err
	}

	result, err := pm.db.ExecContext(ctx, `
		INSERT INTO object_storage_configs (
			id, tenant_id, provider, endpoint, region, bucket,
			credential_ref, access_key_id, secret_access_key, path_prefix,
			is_default, enabled, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, $11, $12, $13::timestamptz, $14::timestamptz)
		ON CONFLICT (id) DO UPDATE SET
			provider=EXCLUDED.provider, endpoint=EXCLUDED.endpoint, region=EXCLUDED.region,
			bucket=EXCLUDED.bucket, credential_ref=EXCLUDED.credential_ref,
			access_key_id=CASE WHEN EXCLUDED.credential_ref IS NULL THEN EXCLUDED.access_key_id ELSE '' END,
			secret_access_key=CASE WHEN EXCLUDED.credential_ref IS NULL THEN EXCLUDED.secret_access_key ELSE '' END,
			path_prefix=EXCLUDED.path_prefix,
			is_default=EXCLUDED.is_default, enabled=EXCLUDED.enabled, updated_at=EXCLUDED.updated_at
		WHERE object_storage_configs.tenant_id = EXCLUDED.tenant_id
	`,
		id, tenantID, payload["provider"],
		payloadString(payload["endpoint"]),
		payloadString(payload["region"]),
		payloadString(payload["bucket"]),
		payloadString(payload["credential_ref"]),
		payloadString(payload["access_key_id"]),
		payloadString(payload["secret_access_key"]),
		payloadString(payload["path_prefix"]),
		payloadBool(payload["is_default"]),
		payloadBool(payload["enabled"]),
		payload["created_at"], payload["updated_at"],
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify storage_config.created projection: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("storage_config.created projection affected no rows for tenant %s", tenantID)
	}
	return nil
}

func (pm *ProjectionManager) handleStorageConfigUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal storage_config.updated: %w", err)
	}
	id, tenantID, err := storageProjectionIdentity(payload)
	if err != nil {
		return err
	}
	if pm.db == nil {
		return nil
	}
	if err := pm.rejectLegacyStorageCredentialEventAfterCutover(ctx, payload); err != nil {
		return err
	}

	setClauses := []string{"updated_at = $2::timestamptz"}
	args := []interface{}{id, payload["updated_at"]}
	argIndex := 3

	if v, ok := payload["credential_ref"]; ok {
		setClauses = append(setClauses,
			fmt.Sprintf("credential_ref = $%d", argIndex),
			"access_key_id = ''",
			"secret_access_key = ''",
		)
		args = append(args, v)
		argIndex++
	} else {
		legacyCredentialUpdate := false
		if accessKey, ok := payload["access_key_id"]; ok {
			legacyCredentialUpdate = true
			setClauses = append(setClauses, fmt.Sprintf("access_key_id = $%d", argIndex))
			args = append(args, accessKey)
			argIndex++
		}
		if secretKey, ok := payload["secret_access_key"]; ok {
			legacyCredentialUpdate = true
			setClauses = append(setClauses, fmt.Sprintf("secret_access_key = $%d", argIndex))
			args = append(args, secretKey)
			argIndex++
		}
		if legacyCredentialUpdate {
			setClauses = append(setClauses, "credential_ref = NULL")
		}
	}
	for _, field := range []string{"endpoint", "region", "bucket", "path_prefix"} {
		if v, ok := payload[field]; ok {
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", field, argIndex))
			args = append(args, v)
			argIndex++
		}
	}
	if v, ok := payload["enabled"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", argIndex))
		args = append(args, v)
		argIndex++
	}

	args = append(args, tenantID)
	query := fmt.Sprintf("UPDATE object_storage_configs SET %s WHERE id = $1 AND tenant_id = $%d", joinSetClauses(setClauses), argIndex)
	if reference, ok := payload["credential_ref"]; ok {
		return pm.applyStorageCredentialReferenceUpdate(
			ctx, query, args, id, tenantID, payloadString(reference), payloadString(payload["previous_credential_ref"]),
		)
	}
	result, err := pm.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify storage_config.updated projection: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("storage_config.updated projection affected no rows for tenant %s", tenantID)
	}
	return nil
}

var errStaleStorageCredentialProjection = errors.New("storage credential rotation was superseded by a newer rotation")

func (pm *ProjectionManager) applyStorageCredentialReferenceUpdate(ctx context.Context, query string, args []interface{}, id, tenantID, newReference, eventPreviousReference string) error {
	tx, err := pm.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin storage credential projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var previous struct {
		Reference  string `db:"credential_ref"`
		Backend    string `db:"backend"`
		Generation int64  `db:"generation"`
	}
	if err := tx.GetContext(ctx, &previous, `
		SELECT COALESCE(c.credential_ref, '') AS credential_ref,
		       COALESCE(credentials.backend, '') AS backend,
		       COALESCE(credentials.generation, 0) AS generation
		FROM object_storage_configs c
		LEFT JOIN object_storage_credentials credentials
		  ON credentials.id = c.credential_ref AND credentials.tenant_id = c.tenant_id
		WHERE c.id = $1 AND c.tenant_id = $2
		FOR UPDATE OF c
	`,
		id, tenantID,
	); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("storage_config.updated projection affected no rows for tenant %s", tenantID)
	} else if err != nil {
		return fmt.Errorf("resolve previous storage credential reference: %w", err)
	}
	var next struct {
		Backend    string `db:"backend"`
		Generation int64  `db:"generation"`
	}
	if err := tx.GetContext(ctx, &next, `
		SELECT backend, generation
		FROM object_storage_credentials
		WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL
	`, newReference, tenantID); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("new storage credential reference is unavailable")
	} else if err != nil {
		return fmt.Errorf("resolve new storage credential reference: %w", err)
	}

	if previous.Generation > next.Generation {
		if next.Backend == "postgres" {
			if err := revokePostgresCredentialReferenceTx(ctx, tx, newReference, tenantID); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit stale storage credential projection: %w", err)
		}
		if next.Backend != "postgres" {
			if err := pm.revokeStorageCredentialAfterProjection(ctx, tenantID, newReference); err != nil {
				return err
			}
		}
		return errStaleStorageCredentialProjection
	}

	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify storage_config.updated projection: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("storage_config.updated projection affected no rows for tenant %s", tenantID)
	}

	oldReference := strings.TrimSpace(previous.Reference)
	externalReference := ""
	if oldReference != "" && oldReference != newReference {
		if previous.Backend == "postgres" {
			if err := revokePostgresCredentialReferenceTx(ctx, tx, oldReference, tenantID); err != nil {
				return err
			}
		} else {
			externalReference = oldReference
		}
	}
	if eventPreviousReference = strings.TrimSpace(eventPreviousReference); oldReference == newReference && eventPreviousReference != "" && eventPreviousReference != newReference {
		externalReference = eventPreviousReference
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit storage credential projection: %w", err)
	}
	if externalReference != "" {
		if err := pm.revokeStorageCredentialAfterProjection(ctx, tenantID, externalReference); err != nil {
			return err
		}
	}
	return nil
}

func revokePostgresCredentialReferenceTx(ctx context.Context, tx *sqlx.Tx, reference, tenantID string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE object_storage_credentials
		 SET revoked_at = COALESCE(revoked_at, NOW()), ciphertext = '\x'::bytea, key_id = 'revoked'
		 WHERE id = $1 AND tenant_id = $2 AND backend = 'postgres'
		   AND NOT EXISTS (SELECT 1 FROM object_storage_configs WHERE credential_ref = $1 AND tenant_id = $2)
		   AND NOT EXISTS (SELECT 1 FROM tenant_volume_buckets WHERE credential_ref = $1 AND tenant_id = $2)`,
		reference, tenantID,
	); err != nil {
		return fmt.Errorf("revoke replaced storage credential reference: %w", err)
	}
	return nil
}

func (pm *ProjectionManager) revokeStorageCredentialAfterProjection(ctx context.Context, tenantID, reference string) error {
	if pm.storageCredentialStore == nil {
		return fmt.Errorf("storage credential revoker is not configured")
	}
	if err := pm.storageCredentialStore.Revoke(ctx, tenantID, reference); err != nil && !errors.Is(err, storagecredentials.ErrCredentialNotFound) {
		return fmt.Errorf("revoke storage credential reference: %w", err)
	}
	return nil
}

func (pm *ProjectionManager) handleStorageConfigDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal storage_config.deleted: %w", err)
	}
	id, tenantID, err := storageProjectionIdentity(payload)
	if err != nil {
		return err
	}
	if pm.db == nil {
		return nil
	}

	tx, err := pm.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin storage config deletion projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var stored struct {
		TenantID      string `db:"tenant_id"`
		CredentialRef string `db:"credential_ref"`
		Backend       string `db:"backend"`
	}
	err = tx.GetContext(ctx, &stored,
		`SELECT c.tenant_id, COALESCE(c.credential_ref, '') AS credential_ref,
		        COALESCE(credentials.backend, '') AS backend
		 FROM object_storage_configs c
		 LEFT JOIN object_storage_credentials credentials
		   ON credentials.id = c.credential_ref AND credentials.tenant_id = c.tenant_id
		 WHERE c.id = $1
		 FOR UPDATE OF c`,
		id,
	)
	if err == sql.ErrNoRows {
		// Projection replays may see an event that has already been applied.
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("rollback replayed storage config deletion: %w", rollbackErr)
		}
		if reference := strings.TrimSpace(payloadString(payload["credential_ref"])); reference != "" {
			return pm.revokeStorageCredentialAfterProjection(ctx, tenantID, reference)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to resolve storage_config.deleted projection tenant: %w", err)
	}
	if stored.TenantID != tenantID {
		return fmt.Errorf("storage_config.deleted projection tenant mismatch for config %s", id)
	}

	result, err := tx.ExecContext(ctx, "DELETE FROM object_storage_configs WHERE id = $1 AND tenant_id = $2", id, tenantID)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify storage_config.deleted projection: %w", err)
	}
	if rowsAffected == 0 {
		// A concurrent replay may have deleted the same owned row after the
		// tenant check. The desired state is already reached.
		return tx.Commit()
	}
	externalReference := ""
	if stored.CredentialRef != "" {
		if stored.Backend == "postgres" {
			if err := revokePostgresCredentialReferenceTx(ctx, tx, stored.CredentialRef, tenantID); err != nil {
				return err
			}
		} else {
			externalReference = stored.CredentialRef
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit storage config deletion projection: %w", err)
	}
	if externalReference != "" {
		if err := pm.revokeStorageCredentialAfterProjection(ctx, tenantID, externalReference); err != nil {
			return err
		}
	}
	return nil
}

// ─── Trooper Projections ─────────────────────────────────────────────────────

func (pm *ProjectionManager) handleTrooperCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal trooper.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	var agentConfigJSON []byte
	if cfg, ok := payload["agent_config"]; ok {
		agentConfigJSON, _ = json.Marshal(cfg)
	} else {
		agentConfigJSON = []byte("{}")
	}

	var envVarsJSON []byte
	if ev, ok := payload["sandbox_env_vars"]; ok {
		envVarsJSON, _ = json.Marshal(ev)
	} else {
		envVarsJSON = []byte("{}")
	}

	var workerPoolJSON []byte
	if wp, ok := payload["worker_pool_config"]; ok {
		workerPoolJSON, _ = json.Marshal(wp)
	} else {
		workerPoolJSON = []byte("{}")
	}

	var tools []string
	if toolsRaw, ok := payload["tools"].([]interface{}); ok {
		for _, t := range toolsRaw {
			if ts, ok := t.(string); ok {
				tools = append(tools, ts)
			}
		}
	}

	var allowedHosts []string
	if hostsRaw, ok := payload["sandbox_allowed_hosts"].([]interface{}); ok {
		for _, h := range hostsRaw {
			if hs, ok := h.(string); ok {
				allowedHosts = append(allowedHosts, hs)
			}
		}
	}

	now := time.Now().Format(time.RFC3339)

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO troopers (
			id, tenant_id, name, description, status,
			model, system_prompt, tools, agent_config, max_turns, max_tool_calls_per_turn, max_steps,
			soul_md, identity_md, user_md, role_md,
			sandbox_image, sandbox_cpu_limit, sandbox_memory_mb, sandbox_disk_mb,
			sandbox_timeout_seconds, sandbox_network_mode, sandbox_allowed_hosts, sandbox_env_vars,
			sandbox_ssh_enabled, sandbox_git_repo_url, sandbox_git_branch,
			db_sqlite_path, db_lancedb_path, db_redb_path,
			max_concurrent_workers, worker_pool_config,
			color, icon,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, 'created',
			$5, $6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15,
			$16, $17, $18, $19,
			$20, $21, $22, $23,
			$24, $25, $26,
			$27, $28, $29,
			$30, $31,
			$32, $33,
			$34::timestamptz, $35::timestamptz
		) ON CONFLICT (id) DO NOTHING`,
		getString(payload, "id"),
		getString(payload, "tenant_id"),
		getString(payload, "name"),
		getString(payload, "description"),
		getString(payload, "model"),
		getString(payload, "system_prompt"),
		pqStringArray(tools),
		agentConfigJSON,
		getInt32(payload, "max_turns"),
		getInt32(payload, "max_tool_calls_per_turn"),
		getOptionalInt32(payload, "max_steps"),
		getString(payload, "soul_md"),
		getString(payload, "identity_md"),
		getString(payload, "user_md"),
		getString(payload, "role_md"),
		getString(payload, "sandbox_image"),
		getFloat64(payload, "sandbox_cpu_limit"),
		getInt64(payload, "sandbox_memory_mb"),
		getInt64(payload, "sandbox_disk_mb"),
		getInt32(payload, "sandbox_timeout_seconds"),
		getString(payload, "sandbox_network_mode"),
		pqStringArray(allowedHosts),
		envVarsJSON,
		getBool(payload, "sandbox_ssh_enabled"),
		getOptionalString(payload, "sandbox_git_repo_url"),
		getOptionalString(payload, "sandbox_git_branch"),
		getString(payload, "db_sqlite_path"),
		getString(payload, "db_lancedb_path"),
		getString(payload, "db_redb_path"),
		getInt32(payload, "max_concurrent_workers"),
		workerPoolJSON,
		getOptionalString(payload, "color"),
		getOptionalString(payload, "icon"),
		now, now,
	)
	return err
}

func (pm *ProjectionManager) handleTrooperUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal trooper.updated: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	id := getString(payload, "id")
	now := time.Now().Format(time.RFC3339)

	// Build dynamic UPDATE from non-nil fields
	sets := []string{"updated_at = $1::timestamptz"}
	args := []interface{}{now}
	argIdx := 2

	stringFields := map[string]string{
		"name": "name", "description": "description", "model": "model",
		"system_prompt": "system_prompt", "status": "status",
		"soul_md": "soul_md", "identity_md": "identity_md", "user_md": "user_md", "role_md": "role_md",
		"sandbox_image": "sandbox_image", "sandbox_network_mode": "sandbox_network_mode",
		"sandbox_git_repo_url": "sandbox_git_repo_url", "sandbox_git_branch": "sandbox_git_branch",
		"db_sqlite_path": "db_sqlite_path", "db_lancedb_path": "db_lancedb_path", "db_redb_path": "db_redb_path",
		"color": "color", "icon": "icon", "sandbox_id": "sandbox_id",
	}
	for jsonKey, col := range stringFields {
		if v, ok := payload[jsonKey]; ok && v != nil {
			sets = append(sets, fmt.Sprintf("%s = $%d", col, argIdx))
			args = append(args, v)
			argIdx++
		}
	}

	_, err := pm.db.ExecContext(ctx, fmt.Sprintf(
		"UPDATE troopers SET %s WHERE id = $%d",
		strings.Join(sets, ", "), argIdx,
	), append(args, id)...)
	return err
}

func (pm *ProjectionManager) handleTrooperDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal trooper.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	now := time.Now().Format(time.RFC3339)
	_, err := pm.db.ExecContext(ctx,
		"UPDATE troopers SET deleted_at = $1::timestamptz, updated_at = $1::timestamptz WHERE id = $2",
		now, getString(payload, "id"),
	)
	return err
}

func (pm *ProjectionManager) handleTrooperLinkCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal trooper.link.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	var configJSON []byte
	if cfg, ok := payload["config"]; ok {
		configJSON, _ = json.Marshal(cfg)
	} else {
		configJSON = []byte("{}")
	}

	now := time.Now().Format(time.RFC3339)
	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO trooper_links (
			id, tenant_id, source_trooper_id, target_type, target_id, target_name,
			link_type, protocol, status, config, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active', $9, $10::timestamptz, $11::timestamptz)
		ON CONFLICT (id) DO NOTHING`,
		getString(payload, "id"),
		getString(payload, "tenant_id"),
		getString(payload, "source_trooper_id"),
		getString(payload, "target_type"),
		getString(payload, "target_id"),
		getOptionalString(payload, "target_name"),
		getString(payload, "link_type"),
		getString(payload, "protocol"),
		configJSON,
		now, now,
	)
	return err
}

func (pm *ProjectionManager) handleTrooperLinkDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal trooper.link.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx,
		"DELETE FROM trooper_links WHERE id = $1 AND tenant_id = $2",
		getString(payload, "id"), getString(payload, "tenant_id"),
	)
	return err
}

func (pm *ProjectionManager) handleTrooperChannelBound(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal trooper.channel.bound: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	now := time.Now().Format(time.RFC3339)
	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO trooper_channel_bindings (
			id, tenant_id, trooper_id, channel_config_id, enabled, created_at
		) VALUES ($1, $2, $3, $4, TRUE, $5::timestamptz)
		ON CONFLICT (trooper_id, channel_config_id) DO NOTHING`,
		getString(payload, "id"),
		getString(payload, "tenant_id"),
		getString(payload, "trooper_id"),
		getString(payload, "channel_config_id"),
		now,
	)
	return err
}

func (pm *ProjectionManager) handleTrooperChannelUnbound(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal trooper.channel.unbound: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx,
		"DELETE FROM trooper_channel_bindings WHERE trooper_id = $1 AND channel_config_id = $2 AND tenant_id = $3",
		getString(payload, "trooper_id"),
		getString(payload, "channel_config_id"),
		getString(payload, "tenant_id"),
	)
	return err
}

// ─── Projection Helpers ────────────────────────────────────────────────────────

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getOptionalString(m map[string]interface{}, key string) *string {
	if v, ok := m[key].(string); ok {
		return &v
	}
	return nil
}

func getInt32(m map[string]interface{}, key string) int32 {
	if v, ok := m[key].(float64); ok {
		return int32(v)
	}
	return 0
}

func getOptionalInt32(m map[string]interface{}, key string) *int32 {
	if v, ok := m[key].(float64); ok {
		i := int32(v)
		return &i
	}
	return nil
}

func getInt64(m map[string]interface{}, key string) int64 {
	if v, ok := m[key].(float64); ok {
		return int64(v)
	}
	return 0
}

func getFloat64(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func pqStringArray(arr []string) interface{} {
	if arr == nil {
		return "{}"
	}
	return "{" + strings.Join(arr, ",") + "}"
}

// ============================================================================
// Prompt Library Projections
// ============================================================================

func (pm *ProjectionManager) handlePromptCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal prompt.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO prompts (id, tenant_id, name, description, tags, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::timestamptz, $7::timestamptz)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description,
			tags=EXCLUDED.tags, updated_at=EXCLUDED.updated_at
	`,
		payload["id"], payload["tenant_id"], payload["name"], payloadString(payload["description"]),
		payloadJSON(payload["tags"]),
		payload["created_at"], payload["updated_at"],
	)
	return err
}

func (pm *ProjectionManager) handlePromptUpdated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal prompt.updated: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	setClauses := []string{"updated_at = $3::timestamptz"}
	args := []interface{}{payload["id"], payload["tenant_id"], payload["updated_at"]}
	argIndex := 4

	if name, ok := payload["name"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, name)
		argIndex++
	}
	if desc, ok := payload["description"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, desc)
		argIndex++
	}
	if tags, ok := payload["tags"]; ok {
		setClauses = append(setClauses, fmt.Sprintf("tags = $%d", argIndex))
		args = append(args, payloadJSON(tags))
		argIndex++
	}

	query := fmt.Sprintf("UPDATE prompts SET %s WHERE id = $1 AND tenant_id = $2", joinSetClauses(setClauses))
	_, err := pm.db.ExecContext(ctx, query, args...)
	return err
}

func (pm *ProjectionManager) handlePromptDeleted(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal prompt.deleted: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx,
		"UPDATE prompts SET archived_at = $3::timestamptz WHERE id = $1 AND tenant_id = $2",
		payload["id"], payload["tenant_id"], payload["deleted_at"],
	)
	return err
}

func (pm *ProjectionManager) handlePromptVersionCreated(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal prompt_version.created: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	_, err := pm.db.ExecContext(ctx, `
		INSERT INTO prompt_versions (
			id, prompt_id, tenant_id, version, messages, config, labels,
			commit_message, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::timestamptz)
		ON CONFLICT (id) DO NOTHING
	`,
		payload["id"], payload["prompt_id"], payload["tenant_id"], payload["version"],
		payloadJSON(payload["messages"]), payloadJSON(payload["config"]),
		payloadJSON(payload["labels"]),
		payloadString(payload["commit_message"]), payloadString(payload["created_by"]),
		payload["created_at"],
	)
	if err != nil {
		return err
	}

	// A new version is a meaningful change to the prompt itself.
	_, err = pm.db.ExecContext(ctx,
		"UPDATE prompts SET updated_at = $3::timestamptz WHERE id = $1 AND tenant_id = $2",
		payload["prompt_id"], payload["tenant_id"], payload["created_at"],
	)
	return err
}

func (pm *ProjectionManager) handlePromptVersionLabelsSet(ctx context.Context, event Event) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal prompt_version.labels_set: %w", err)
	}
	if pm.db == nil {
		return nil
	}

	labelsJSON := payloadJSON(payload["labels"])

	// A label belongs to at most one version of a prompt: strip the labels
	// being assigned from every version first, then set the target's labels.
	_, err := pm.db.ExecContext(ctx, `
		UPDATE prompt_versions
		SET labels = COALESCE((
			SELECT jsonb_agg(elem) FROM jsonb_array_elements_text(labels) AS elem
			WHERE elem NOT IN (SELECT jsonb_array_elements_text($3::jsonb))
		), '[]'::jsonb)
		WHERE prompt_id = $1 AND tenant_id = $2
	`, payload["prompt_id"], payload["tenant_id"], labelsJSON)
	if err != nil {
		return err
	}

	_, err = pm.db.ExecContext(ctx, `
		UPDATE prompt_versions SET labels = $4::jsonb
		WHERE prompt_id = $1 AND tenant_id = $2 AND version = $3
	`, payload["prompt_id"], payload["tenant_id"], payload["version"], labelsJSON)
	if err != nil {
		return err
	}

	_, err = pm.db.ExecContext(ctx,
		"UPDATE prompts SET updated_at = $3::timestamptz WHERE id = $1 AND tenant_id = $2",
		payload["prompt_id"], payload["tenant_id"], payload["updated_at"],
	)
	return err
}
