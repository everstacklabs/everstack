package sandbox

import (
	"context"
	"fmt"
	"strings"
)

// BindOpenAIExecutorOwnership binds a newly-created OpenAI executor sandbox to
// its authenticated instance, or verifies an existing binding. The first bind
// is atomic in Postgres and the in-memory check is performed under the manager
// lock so concurrent callers cannot move a sandbox between instances.
func (m *SandboxManager) BindOpenAIExecutorOwnership(ctx context.Context, sandboxID, tenantID, instanceID string) (*Instance, error) {
	tenantID = strings.TrimSpace(tenantID)
	instanceID = strings.TrimSpace(instanceID)
	if tenantID == "" || instanceID == "" {
		return nil, fmt.Errorf("sandbox ownership requires tenant and instance")
	}

	inst, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	if inst.Config.TenantID != tenantID {
		return nil, fmt.Errorf("sandbox is not owned by authenticated tenant")
	}
	if !strings.HasPrefix(inst.Config.SessionID, "openai_") || inst.ID != "sbx_"+inst.Config.SessionID {
		return nil, fmt.Errorf("sandbox was not provisioned for an OpenAI executor session")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.instancesBySandbox[sandboxID]
	if current == nil {
		current = inst
	}
	if current.InstanceID != "" && current.InstanceID != instanceID {
		return nil, fmt.Errorf("sandbox is not owned by authenticated instance")
	}

	if m.db != nil {
		result, updateErr := m.db.ExecContext(ctx, `
			UPDATE sandbox_instances
			SET instance_id = $1, updated_at = NOW()
			WHERE id = $2 AND tenant_id = $3
			  AND (instance_id IS NULL OR instance_id = '' OR instance_id = $1)`,
			instanceID, sandboxID, tenantID,
		)
		if updateErr != nil {
			return nil, fmt.Errorf("bind sandbox instance ownership: %w", updateErr)
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return nil, fmt.Errorf("verify sandbox instance ownership: %w", rowsErr)
		}
		if rows != 1 {
			return nil, fmt.Errorf("sandbox is not owned by authenticated instance")
		}
	}

	current.InstanceID = instanceID
	inst.InstanceID = instanceID
	return current, nil
}

// LookupOpenAIExecutorSandbox resolves a sandbox only within its authenticated
// tenant and instance scope. It deliberately does not accept ownership fields
// from an upstream event or tool payload.
func (m *SandboxManager) LookupOpenAIExecutorSandbox(ctx context.Context, sandboxID, tenantID, instanceID string) (*Instance, error) {
	tenantID = strings.TrimSpace(tenantID)
	instanceID = strings.TrimSpace(instanceID)
	if tenantID == "" || instanceID == "" {
		return nil, fmt.Errorf("sandbox ownership requires tenant and instance")
	}
	inst, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	if inst.Config.TenantID != tenantID || inst.InstanceID != instanceID {
		return nil, fmt.Errorf("sandbox is not owned by authenticated instance")
	}
	// The DB fallback deliberately returns a detached handle. OpenAI executor
	// operations need the authenticated handle registered so session-keyed
	// lifecycle cleanup cannot silently succeed against an empty local map.
	m.mu.Lock()
	if current := m.instancesBySandbox[inst.ID]; current != nil {
		inst = current
	} else {
		m.instancesBySandbox[inst.ID] = inst
		m.instances[inst.Config.SessionID] = inst
	}
	m.mu.Unlock()
	return inst, nil
}
