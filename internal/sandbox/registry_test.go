package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEntryHashRoundTrip(t *testing.T) {
	now := time.Unix(1700000000, 0)
	in := RegistryEntry{
		SandboxID:       "sbx_abc",
		TenantID:        "tnt_1",
		AgentID:         "agt_1",
		SessionID:       "ses_1",
		LinkedSessionID: "ses_owner",
		BackendType:     "firecracker",
		AgentTarget:     "fcagent-7:8080",
		Status:          "running",
		Image:           "ubuntu:24.04",
		ShortCode:       "abc12",
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Minute),
		LastSeenAt:      now.Add(2 * time.Minute),
	}
	h := entryToHash(in)
	// convert map[string]interface{} to map[string]string the way HGetAll returns it
	flat := make(map[string]string, len(h))
	for k, v := range h {
		flat[k] = v.(string)
	}
	got := entryFromHash(flat)
	if got.SandboxID != in.SandboxID ||
		got.TenantID != in.TenantID ||
		got.AgentID != in.AgentID ||
		got.SessionID != in.SessionID ||
		got.LinkedSessionID != in.LinkedSessionID ||
		got.BackendType != in.BackendType ||
		got.AgentTarget != in.AgentTarget ||
		got.Status != in.Status ||
		got.Image != in.Image ||
		got.ShortCode != in.ShortCode {
		t.Fatalf("string fields mismatch: %+v vs %+v", got, in)
	}
	if !got.CreatedAt.Equal(in.CreatedAt) ||
		!got.UpdatedAt.Equal(in.UpdatedAt) ||
		!got.LastSeenAt.Equal(in.LastSeenAt) {
		t.Fatalf("time fields mismatch: got %v/%v/%v want %v/%v/%v",
			got.CreatedAt, got.UpdatedAt, got.LastSeenAt,
			in.CreatedAt, in.UpdatedAt, in.LastSeenAt)
	}
}

func TestEntryHashOmitsEmptyOptionals(t *testing.T) {
	in := RegistryEntry{
		SandboxID:   "sbx_only",
		TenantID:    "tnt_1",
		BackendType: "docker",
		Status:      "running",
	}
	h := entryToHash(in)
	for _, k := range []string{"agent_id", "session_id", "linked_session_id", "agent_target", "image", "short_code"} {
		if _, present := h[k]; present {
			t.Fatalf("expected %s to be omitted when empty, got %v", k, h[k])
		}
	}
}

func TestLocalRegistryIsAlwaysMiss(t *testing.T) {
	r := NewLocalRegistry()
	ctx := context.Background()

	if _, err := r.GetBySandboxID(ctx, "sbx_x"); !errors.Is(err, ErrRegistryMiss) {
		t.Fatalf("GetBySandboxID: want ErrRegistryMiss, got %v", err)
	}
	if _, err := r.GetBySessionID(ctx, "ses_x"); !errors.Is(err, ErrRegistryMiss) {
		t.Fatalf("GetBySessionID: want ErrRegistryMiss, got %v", err)
	}
	if _, err := r.GetByAgentID(ctx, "agt_x"); !errors.Is(err, ErrRegistryMiss) {
		t.Fatalf("GetByAgentID: want ErrRegistryMiss, got %v", err)
	}
	if err := r.Put(ctx, RegistryEntry{SandboxID: "sbx_x"}); err != nil {
		t.Fatalf("Put: want nil, got %v", err)
	}
	if err := r.LinkSession(ctx, "ses_x", "sbx_x"); err != nil {
		t.Fatalf("LinkSession: want nil, got %v", err)
	}
	if err := r.Delete(ctx, "sbx_x"); err != nil {
		t.Fatalf("Delete: want nil, got %v", err)
	}
}

func TestEntryFromInstanceProjectsRoutingSubset(t *testing.T) {
	inst := &Instance{
		ID:          "sbx_xyz",
		Backend:     "fcagent",
		Status:      StatusRunning,
		AgentTarget: "fcagent-3:8080",
		AgentID:     "agt_7",
		ShortCode:   "xyz9",
		CreatedAt:   time.Unix(1700000000, 0),
		LastUsedAt:  time.Unix(1700001000, 0),
		Config: InstanceConfig{
			TenantID:  "tnt_1",
			SessionID: "ses_99",
			Image:     "ubuntu:24.04",
		},
	}
	e := EntryFromInstance(inst)
	if e.SandboxID != inst.ID || e.BackendType != inst.Backend ||
		e.AgentTarget != inst.AgentTarget || e.AgentID != inst.AgentID ||
		e.SessionID != inst.Config.SessionID || e.TenantID != inst.Config.TenantID ||
		e.ShortCode != inst.ShortCode || e.Image != inst.Config.Image {
		t.Fatalf("projection mismatch: %+v", e)
	}
	if e.Status != string(StatusRunning) {
		t.Fatalf("status projection: got %q want %q", e.Status, StatusRunning)
	}
	if e.LastSeenAt.IsZero() {
		t.Fatalf("LastSeenAt should default to now")
	}
}
