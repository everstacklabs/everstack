package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/redis/go-redis/v9"
)

// AgentWaker can wake a sleeping agent's sandbox.
type AgentWaker interface {
	Wake(ctx context.Context, agentID string) error
}

// AgentMessageBus handles cross-agent message delivery.
// It writes messages to the agent_messages DB table and delivers them
// to active sessions via the SessionManager's peer channels.
// For cross-instance delivery, it publishes to Redis pub/sub.
type AgentMessageBus struct {
	db             *sqlx.DB
	sessionManager *SessionManager
	redisClient    *redis.Client
	instanceID     string
	waker          AgentWaker // optional; auto-wakes sleeping agents on message delivery
}

// NewAgentMessageBus creates a new message bus.
func NewAgentMessageBus(db *sqlx.DB, sm *SessionManager, rc *redis.Client, instanceID string) *AgentMessageBus {
	return &AgentMessageBus{
		db:             db,
		sessionManager: sm,
		redisClient:    rc,
		instanceID:     instanceID,
	}
}

// SetWaker sets the agent waker for auto-waking sleeping agents on message delivery.
func (bus *AgentMessageBus) SetWaker(w AgentWaker) {
	bus.waker = w
}

// SendMessage writes a message to the DB and attempts immediate delivery.
// If the target agent has an active session on this instance, the message
// is delivered via the peer channel. Otherwise, it's published to Redis
// for cross-instance delivery (or left queued in DB for later retrieval).
func (bus *AgentMessageBus) SendMessage(ctx context.Context, msg PeerMessage, recipientAgentID, tenantID string) error {
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.SentAt.IsZero() {
		msg.SentAt = time.Now()
	}
	if msg.MessageType == "" {
		msg.MessageType = "message"
	}

	// Persist to DB
	if bus.db != nil {
		dbCtx := ctx
		if tenantID != "" {
			dbCtx = database.WithTenantSchema(ctx, tenantID)
		}

		var payload sql.NullString
		if msg.MessageType == "delegation" {
			payloadJSON, _ := json.Marshal(map[string]interface{}{
				"thread_id": msg.ThreadID,
			})
			payload = sql.NullString{String: string(payloadJSON), Valid: true}
		}

		var threadID sql.NullString
		if msg.ThreadID != "" {
			threadID = sql.NullString{String: msg.ThreadID, Valid: true}
		}

		_, err := bus.db.ExecContext(dbCtx, `
			INSERT INTO agent_messages (id, sender_agent_id, recipient_agent_id, tenant_id, thread_id, content, message_type, status, payload, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9)
		`, msg.ID, msg.SenderAgentID, recipientAgentID, tenantID, threadID, msg.Content, msg.MessageType, payload, msg.SentAt)
		if err != nil {
			return fmt.Errorf("failed to persist agent message: %w", err)
		}
	}

	// Try local delivery first
	if bus.sessionManager.DeliverPeerMessage(recipientAgentID, msg) {
		// Mark as delivered in DB
		if bus.db != nil {
			go bus.markDelivered(tenantID, msg.ID)
		}
		return nil
	}

	// Auto-wake sleeping agents and retry delivery via primary session.
	// Tenant predicate is mandatory — agent_definitions is a shared
	// table; without it any caller could read/mutate another tenant's
	// agent lifecycle_status by guessing an agent id.
	if bus.waker != nil && bus.db != nil && tenantID != "" {
		var lifecycleStatus string
		_ = bus.db.GetContext(ctx, &lifecycleStatus,
			`SELECT lifecycle_status FROM agent_definitions WHERE id = $1 AND tenant_id = $2`, recipientAgentID, tenantID)
		if lifecycleStatus == "sleeping" {
			logger.WithFields("recipient_agent_id", recipientAgentID).
				Info("agent_message_bus: auto-waking sleeping agent for message delivery")
			if err := bus.waker.Wake(ctx, recipientAgentID); err != nil {
				logger.WithFields("recipient_agent_id", recipientAgentID, "error", err.Error()).
					Warn("agent_message_bus: failed to auto-wake agent")
			} else {
				// Update lifecycle_status to idle after wake
				bus.db.ExecContext(ctx, `UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, recipientAgentID, tenantID)
			}
		}
	}

	// Try cross-instance delivery via Redis
	if bus.redisClient != nil {
		envelope := redisMessageEnvelope{
			RecipientAgentID: recipientAgentID,
			TenantID:         tenantID,
			Message:          msg,
		}
		data, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("failed to marshal message for Redis: %w", err)
		}
		// Scope channel by tenantID to prevent cross-tenant message leakage.
		channel := fmt.Sprintf("agents:messages:%s", bus.instanceID)
		if tenantID != "" {
			channel = fmt.Sprintf("agents:messages:%s:%s", tenantID, bus.instanceID)
		}
		if err := bus.redisClient.Publish(ctx, channel, data).Err(); err != nil {
			logger.WithFields("error", err.Error()).
				Warn("agent_message_bus: failed to publish to Redis, message queued in DB")
		}
	}

	return nil
}

// CheckMessages retrieves pending messages for an agent from the DB.
func (bus *AgentMessageBus) CheckMessages(ctx context.Context, agentID, tenantID string, threadID string, status string) ([]PeerMessage, error) {
	if bus.db == nil {
		return nil, nil
	}

	dbCtx := ctx
	if tenantID != "" {
		dbCtx = database.WithTenantSchema(ctx, tenantID)
	}

	if status == "" {
		status = "pending"
	}

	type row struct {
		ID            string         `db:"id"`
		SenderAgentID string         `db:"sender_agent_id"`
		ThreadID      sql.NullString `db:"thread_id"`
		Content       string         `db:"content"`
		MessageType   string         `db:"message_type"`
		CreatedAt     time.Time      `db:"created_at"`
	}

	var rows []row
	var err error

	if threadID != "" {
		err = bus.db.SelectContext(dbCtx, &rows, `
			SELECT id, sender_agent_id, thread_id, content, message_type, created_at
			FROM agent_messages
			WHERE recipient_agent_id = $1 AND status = $2 AND thread_id = $3
			ORDER BY created_at ASC
			LIMIT 50
		`, agentID, status, threadID)
	} else {
		err = bus.db.SelectContext(dbCtx, &rows, `
			SELECT id, sender_agent_id, thread_id, content, message_type, created_at
			FROM agent_messages
			WHERE recipient_agent_id = $1 AND status = $2
			ORDER BY created_at ASC
			LIMIT 50
		`, agentID, status)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query agent messages: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	// Mark messages as read
	ids := make([]string, len(rows))
	messages := make([]PeerMessage, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		messages[i] = PeerMessage{
			ID:            r.ID,
			SenderAgentID: r.SenderAgentID,
			ThreadID:      r.ThreadID.String,
			Content:       r.Content,
			MessageType:   r.MessageType,
			SentAt:        r.CreatedAt,
		}
	}

	// Resolve sender names (tenant-scoped)
	for i := range messages {
		messages[i].SenderName = bus.resolveAgentName(dbCtx, messages[i].SenderAgentID, tenantID)
	}

	// Mark as read in background — tenant predicate prevents a buggy
	// caller from accidentally marking another tenant's messages.
	go func() {
		bgCtx := dbCtx
		for _, id := range ids {
			_, _ = bus.db.ExecContext(bgCtx, `
				UPDATE agent_messages SET status = 'read', read_at = NOW() WHERE id = $1 AND tenant_id = $2
			`, id, tenantID)
		}
	}()

	return messages, nil
}

func (bus *AgentMessageBus) markDelivered(tenantID, messageID string) {
	if tenantID == "" {
		return
	}
	ctx := database.WithTenantSchema(context.Background(), tenantID)
	_, _ = bus.db.ExecContext(ctx, `
		UPDATE agent_messages SET status = 'delivered', delivered_at = NOW() WHERE id = $1 AND tenant_id = $2
	`, messageID, tenantID)
}

func (bus *AgentMessageBus) resolveAgentName(ctx context.Context, agentID, tenantID string) string {
	if bus.db == nil {
		return agentID
	}
	if tenantID == "" {
		return agentID
	}
	var name string
	err := bus.db.GetContext(ctx, &name, `SELECT name FROM agent_definitions WHERE id = $1 AND tenant_id = $2`, agentID, tenantID)
	if err != nil {
		return agentID
	}
	return name
}

type redisMessageEnvelope struct {
	RecipientAgentID string      `json:"recipient_agent_id"`
	TenantID         string      `json:"tenant_id"`
	Message          PeerMessage `json:"message"`
}

// StartRedisSubscriber listens for cross-instance messages and delivers them
// to local agent sessions. Call this once per SessionManager instance.
// Uses a pattern subscription (agents:messages:*:{instanceID}) to receive
// messages from all tenants routed to this instance.
func (bus *AgentMessageBus) StartRedisSubscriber(ctx context.Context) {
	if bus.redisClient == nil {
		return
	}

	// Subscribe to a pattern that matches tenant-scoped channels for this instance.
	// Pattern: agents:messages:*:{instanceID} catches agents:messages:{tenantID}:{instanceID}
	pattern := fmt.Sprintf("agents:messages:*:%s", bus.instanceID)
	sub := bus.redisClient.PSubscribe(ctx, pattern)

	go func() {
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case redisMsg, ok := <-ch:
				if !ok {
					return
				}
				var envelope redisMessageEnvelope
				if err := json.Unmarshal([]byte(redisMsg.Payload), &envelope); err != nil {
					logger.WithFields("error", err.Error()).
						Warn("agent_message_bus: failed to unmarshal Redis message")
					continue
				}
				if bus.sessionManager.DeliverPeerMessage(envelope.RecipientAgentID, envelope.Message) {
					bus.markDelivered(envelope.TenantID, envelope.Message.ID)
				}
			}
		}
	}()
}
