package channels

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// SessionResolution is the result of resolving a platform message to a session.
type SessionResolution struct {
	SessionID    string
	IsNew        bool
	PlatformUser string
}

// ChannelRouter resolves inbound platform messages to agent sessions
// based on session mode (shared, per_user, thread).
type ChannelRouter struct {
	store ChannelStore

	// In-memory message coalescing buffer
	coalesceMu sync.Mutex
	coalesce   map[string]*coalesceBuffer
}

type coalesceBuffer struct {
	messages []coalescedMsg
	timer    *time.Timer
	flush    func()
}

type coalescedMsg struct {
	UserName string
	Text     string
	Time     time.Time
}

// NewChannelRouter creates a new ChannelRouter.
func NewChannelRouter(store ChannelStore) *ChannelRouter {
	return &ChannelRouter{
		store:    store,
		coalesce: make(map[string]*coalesceBuffer),
	}
}

// ResolveSession finds or creates an agent session for the given inbound message.
// Returns the session ID and whether it was newly created.
func (r *ChannelRouter) ResolveSession(ctx context.Context, msg InboundMessage) (*SessionResolution, error) {
	var lookupUserID string
	var lookupThreadRef string

	switch msg.SessionMode {
	case SessionModeShared:
		// One session per channel — all users share it
		lookupUserID = ""
		lookupThreadRef = ""
	case SessionModePerUser:
		// Each user gets their own session
		lookupUserID = msg.PlatformUserID
		lookupThreadRef = ""
	case SessionModeThread:
		// Main channel is shared; threads create per-user sessions
		if msg.PlatformThreadRef != "" {
			lookupUserID = msg.PlatformUserID
			lookupThreadRef = msg.PlatformThreadRef
		} else {
			lookupUserID = ""
			lookupThreadRef = ""
		}
	default:
		// Default to thread mode
		lookupUserID = ""
		lookupThreadRef = ""
	}

	// Look up existing mapping
	mapping, err := r.store.FindSessionMapping(ctx, msg.ChannelConfigID, msg.PlatformChannelRef, lookupUserID, lookupThreadRef)
	if err != nil {
		return nil, fmt.Errorf("find session mapping: %w", err)
	}

	if mapping != nil {
		// Update last message time
		if err := r.store.UpdateMappingLastMessage(ctx, mapping.ID); err != nil {
			logger.WithError(err).Warn("channels: failed to update mapping last_message_at")
		}
		return &SessionResolution{
			SessionID:    mapping.AgentSessionID,
			IsNew:        false,
			PlatformUser: msg.PlatformUserName,
		}, nil
	}

	// No existing session — a new one needs to be created
	// The caller (ChannelManager) handles actual session creation via the agent API
	newSessionID := uuid.New().String()
	return &SessionResolution{
		SessionID:    newSessionID,
		IsNew:        true,
		PlatformUser: msg.PlatformUserName,
	}, nil
}

// RecordMapping saves a new channel→session mapping after session creation.
func (r *ChannelRouter) RecordMapping(ctx context.Context, msg InboundMessage, sessionID string) error {
	var userID, threadRef string
	switch msg.SessionMode {
	case SessionModeShared:
		userID = ""
		threadRef = ""
	case SessionModePerUser:
		userID = msg.PlatformUserID
		threadRef = ""
	case SessionModeThread:
		if msg.PlatformThreadRef != "" {
			userID = msg.PlatformUserID
			threadRef = msg.PlatformThreadRef
		}
	}

	return r.store.CreateSessionMapping(ctx, &SessionMappingRecord{
		ID:                 uuid.New().String(),
		ChannelConfigID:    msg.ChannelConfigID,
		PlatformChannelRef: msg.PlatformChannelRef,
		PlatformUserID:     userID,
		PlatformUserName:   msg.PlatformUserName,
		PlatformThreadRef:  threadRef,
		AgentSessionID:     sessionID,
	})
}

// CoalesceKey builds the key for message coalescing.
func (r *ChannelRouter) CoalesceKey(msg InboundMessage) string {
	switch msg.SessionMode {
	case SessionModeShared:
		return fmt.Sprintf("%s:%s", msg.ChannelConfigID, msg.PlatformChannelRef)
	case SessionModePerUser:
		return fmt.Sprintf("%s:%s:%s", msg.ChannelConfigID, msg.PlatformChannelRef, msg.PlatformUserID)
	case SessionModeThread:
		if msg.PlatformThreadRef != "" {
			return fmt.Sprintf("%s:%s:%s:%s", msg.ChannelConfigID, msg.PlatformChannelRef, msg.PlatformUserID, msg.PlatformThreadRef)
		}
		return fmt.Sprintf("%s:%s", msg.ChannelConfigID, msg.PlatformChannelRef)
	default:
		return fmt.Sprintf("%s:%s", msg.ChannelConfigID, msg.PlatformChannelRef)
	}
}
