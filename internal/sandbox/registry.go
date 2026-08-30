package sandbox

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Registry is a shared, durable lookup layer for sandbox metadata.
//
// The in-memory map on SandboxManager is a per-process cache of live
// handles (backend clients, mutexes, channels). The Registry is the
// "where does this sandbox live" routing layer: enough metadata to
// reconstitute an Instance handle after a process restart, and shared
// across control-plane replicas.
//
// Postgres `sandbox_instances` remains the durable source of truth.
// Registry is a read-through / write-through cache on top of it with
// a TTL safety net for stragglers.
type Registry interface {
	// GetBySandboxID returns the registry entry for sandboxID, or
	// ErrRegistryMiss if it isn't cached. Callers should fall back
	// to Postgres on miss and Put() the result.
	GetBySandboxID(ctx context.Context, sandboxID string) (*RegistryEntry, error)
	// GetBySessionID resolves a session ID to its sandbox ID, then loads
	// the entry. Multiple sessions can point at the same sandbox
	// (linked agents share a sandbox).
	GetBySessionID(ctx context.Context, sessionID string) (*RegistryEntry, error)
	// GetByAgentID resolves a persistent agent ID to its sandbox entry.
	GetByAgentID(ctx context.Context, agentID string) (*RegistryEntry, error)
	// Put writes the entry and refreshes its TTL. Also updates the
	// by_session and by_agent indices when those fields are non-empty.
	Put(ctx context.Context, entry RegistryEntry) error
	// LinkSession registers an additional session_id → sandbox_id
	// mapping without touching the meta hash. Used when a linked
	// agent acquires a shared sandbox.
	LinkSession(ctx context.Context, sessionID, sandboxID string) error
	// Delete removes the entry and its indices. Idempotent.
	Delete(ctx context.Context, sandboxID string) error
}

// ErrRegistryMiss indicates the requested entry is not in the registry.
// Callers should fall back to Postgres.
var ErrRegistryMiss = errors.New("sandbox registry: miss")

// RegistryEntry is the durable projection of an Instance that survives
// in the shared registry. It is the minimum needed to route a request to
// the right backend host and reconstitute the in-process handle.
type RegistryEntry struct {
	SandboxID       string    `json:"sandbox_id"`
	TenantID        string    `json:"tenant_id"`
	AgentID         string    `json:"agent_id,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	LinkedSessionID string    `json:"linked_session_id,omitempty"`
	BackendType     string    `json:"backend_type"`
	AgentTarget     string    `json:"agent_target,omitempty"`
	Status          string    `json:"status"`
	Image           string    `json:"image,omitempty"`
	ShortCode       string    `json:"short_code,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
}

// ---------------------------------------------------------------------------
// LocalRegistry — no-op used when Redis isn't configured.
// ---------------------------------------------------------------------------

// LocalRegistry returns ErrRegistryMiss for every lookup and accepts writes
// silently. It exists so callers can be unconditional about registry use;
// the manager's in-memory map handles the single-instance case.
type LocalRegistry struct{}

// NewLocalRegistry returns a no-op Registry.
func NewLocalRegistry() *LocalRegistry { return &LocalRegistry{} }

func (LocalRegistry) GetBySandboxID(context.Context, string) (*RegistryEntry, error) {
	return nil, ErrRegistryMiss
}
func (LocalRegistry) GetBySessionID(context.Context, string) (*RegistryEntry, error) {
	return nil, ErrRegistryMiss
}
func (LocalRegistry) GetByAgentID(context.Context, string) (*RegistryEntry, error) {
	return nil, ErrRegistryMiss
}
func (LocalRegistry) Put(context.Context, RegistryEntry) error            { return nil }
func (LocalRegistry) LinkSession(context.Context, string, string) error   { return nil }
func (LocalRegistry) Delete(context.Context, string) error                { return nil }

// ---------------------------------------------------------------------------
// RedisRegistry — shared metadata in Redis.
// ---------------------------------------------------------------------------

const (
	registryKeyMeta      = "sbx:meta:"
	registryKeyBySession = "sbx:by_session:"
	registryKeyByAgent   = "sbx:by_agent:"
	registryTTL          = 24 * time.Hour
)

// RedisRegistry implements Registry on top of go-redis. Hashes store
// entry fields so concurrent updaters can HSET individual fields
// without lost-update races on a JSON blob.
type RedisRegistry struct {
	client *redis.Client
}

// NewRedisRegistry returns a Registry backed by the given Redis client.
func NewRedisRegistry(client *redis.Client) *RedisRegistry {
	return &RedisRegistry{client: client}
}

func (r *RedisRegistry) GetBySandboxID(ctx context.Context, sandboxID string) (*RegistryEntry, error) {
	fields, err := r.client.HGetAll(ctx, registryKeyMeta+sandboxID).Result()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, ErrRegistryMiss
	}
	return entryFromHash(fields), nil
}

func (r *RedisRegistry) GetBySessionID(ctx context.Context, sessionID string) (*RegistryEntry, error) {
	sandboxID, err := r.client.Get(ctx, registryKeyBySession+sessionID).Result()
	if err == redis.Nil {
		return nil, ErrRegistryMiss
	}
	if err != nil {
		return nil, err
	}
	return r.GetBySandboxID(ctx, sandboxID)
}

func (r *RedisRegistry) GetByAgentID(ctx context.Context, agentID string) (*RegistryEntry, error) {
	sandboxID, err := r.client.Get(ctx, registryKeyByAgent+agentID).Result()
	if err == redis.Nil {
		return nil, ErrRegistryMiss
	}
	if err != nil {
		return nil, err
	}
	return r.GetBySandboxID(ctx, sandboxID)
}

func (r *RedisRegistry) Put(ctx context.Context, entry RegistryEntry) error {
	if entry.SandboxID == "" {
		return errors.New("sandbox registry: SandboxID required")
	}
	if entry.LastSeenAt.IsZero() {
		entry.LastSeenAt = time.Now()
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = entry.LastSeenAt
	}
	metaKey := registryKeyMeta + entry.SandboxID
	pipe := r.client.TxPipeline()
	pipe.HSet(ctx, metaKey, entryToHash(entry))
	pipe.Expire(ctx, metaKey, registryTTL)
	if entry.SessionID != "" {
		sk := registryKeyBySession + entry.SessionID
		pipe.Set(ctx, sk, entry.SandboxID, registryTTL)
	}
	if entry.AgentID != "" {
		ak := registryKeyByAgent + entry.AgentID
		pipe.Set(ctx, ak, entry.SandboxID, registryTTL)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisRegistry) LinkSession(ctx context.Context, sessionID, sandboxID string) error {
	if sessionID == "" || sandboxID == "" {
		return errors.New("sandbox registry: sessionID and sandboxID required")
	}
	return r.client.Set(ctx, registryKeyBySession+sessionID, sandboxID, registryTTL).Err()
}

func (r *RedisRegistry) Delete(ctx context.Context, sandboxID string) error {
	entry, err := r.GetBySandboxID(ctx, sandboxID)
	if err != nil && !errors.Is(err, ErrRegistryMiss) {
		return err
	}
	pipe := r.client.TxPipeline()
	pipe.Del(ctx, registryKeyMeta+sandboxID)
	if entry != nil {
		if entry.SessionID != "" {
			pipe.Del(ctx, registryKeyBySession+entry.SessionID)
		}
		if entry.AgentID != "" {
			pipe.Del(ctx, registryKeyByAgent+entry.AgentID)
		}
	}
	_, err = pipe.Exec(ctx)
	return err
}

// ---------------------------------------------------------------------------
// hash <-> entry helpers
// ---------------------------------------------------------------------------

func entryToHash(e RegistryEntry) map[string]interface{} {
	h := map[string]interface{}{
		"sandbox_id":    e.SandboxID,
		"tenant_id":     e.TenantID,
		"backend_type":  e.BackendType,
		"status":        e.Status,
		"created_at":    strconv.FormatInt(e.CreatedAt.Unix(), 10),
		"updated_at":    strconv.FormatInt(e.UpdatedAt.Unix(), 10),
		"last_seen_at":  strconv.FormatInt(e.LastSeenAt.Unix(), 10),
	}
	if e.AgentID != "" {
		h["agent_id"] = e.AgentID
	}
	if e.SessionID != "" {
		h["session_id"] = e.SessionID
	}
	if e.LinkedSessionID != "" {
		h["linked_session_id"] = e.LinkedSessionID
	}
	if e.AgentTarget != "" {
		h["agent_target"] = e.AgentTarget
	}
	if e.Image != "" {
		h["image"] = e.Image
	}
	if e.ShortCode != "" {
		h["short_code"] = e.ShortCode
	}
	return h
}

func entryFromHash(h map[string]string) *RegistryEntry {
	e := &RegistryEntry{
		SandboxID:       h["sandbox_id"],
		TenantID:        h["tenant_id"],
		AgentID:         h["agent_id"],
		SessionID:       h["session_id"],
		LinkedSessionID: h["linked_session_id"],
		BackendType:     h["backend_type"],
		AgentTarget:     h["agent_target"],
		Status:          h["status"],
		Image:           h["image"],
		ShortCode:       h["short_code"],
	}
	if v, err := strconv.ParseInt(h["created_at"], 10, 64); err == nil {
		e.CreatedAt = time.Unix(v, 0)
	}
	if v, err := strconv.ParseInt(h["updated_at"], 10, 64); err == nil {
		e.UpdatedAt = time.Unix(v, 0)
	}
	if v, err := strconv.ParseInt(h["last_seen_at"], 10, 64); err == nil {
		e.LastSeenAt = time.Unix(v, 0)
	}
	return e
}

// EntryFromInstance projects a live Instance into a RegistryEntry. The
// projection is lossy on purpose — only the routing/recovery subset
// goes into the shared registry.
func EntryFromInstance(inst *Instance) RegistryEntry {
	now := time.Now()
	updated := inst.LastUsedAt
	if updated.IsZero() {
		updated = now
	}
	return RegistryEntry{
		SandboxID:    inst.ID,
		TenantID:     inst.Config.TenantID,
		AgentID:      inst.AgentID,
		SessionID:    inst.Config.SessionID,
		BackendType:  inst.Backend,
		AgentTarget:  inst.AgentTarget,
		Status:       string(inst.Status),
		Image:        inst.Config.Image,
		ShortCode:    inst.ShortCode,
		CreatedAt:    inst.CreatedAt,
		UpdatedAt:    updated,
		LastSeenAt:   now,
	}
}
