package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/commands"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/storageauth"
)

func authenticatedCommandContext(tenantID string) context.Context {
	return contextkeys.WithAuthenticatedAPIKey(context.Background(), tenantID, "verified-key-hash")
}

func TestStorageCommandsRequireTenant(t *testing.T) {
	tests := []struct {
		name string
		cmd  commands.Command
	}{
		{
			name: "configure storage",
			cmd:  NewConfigureStorageCommand("", "s3", "", "us-east-1", "bucket", "storagecred_ref", "", true, "user-1", ""),
		},
		{
			name: "update storage config",
			cmd:  NewUpdateStorageConfigCommand("config-1", "", "user-1", ""),
		},
		{
			name: "delete storage config",
			cmd:  NewDeleteStorageConfigCommand("config-1", "", "user-1", ""),
		},
		{
			name: "complete upload",
			cmd:  NewCompleteUploadCommand("", "object-1", "", 1, nil, "user-1", ""),
		},
		{
			name: "delete object",
			cmd:  NewDeleteObjectCommand("", "object-1", "user-1", ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cmd.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want missing tenant error")
			}
			if _, err := NewStorageCommandHandler().Handle(context.Background(), tt.cmd); err == nil {
				t.Fatal("Handle() error = nil, want missing tenant error")
			}
		})
	}
}

func TestStorageCommandEventsPreserveExplicitTenant(t *testing.T) {
	tests := []struct {
		name string
		cmd  commands.Command
	}{
		{
			name: "configure storage",
			cmd:  NewConfigureStorageCommand("tenant-1", "s3", "", "us-east-1", "bucket", "storagecred_ref", "", true, "user-1", ""),
		},
		{
			name: "update storage config",
			cmd:  NewUpdateStorageConfigCommand("config-1", "tenant-1", "user-1", ""),
		},
		{
			name: "delete storage config",
			cmd:  NewDeleteStorageConfigCommand("config-1", "tenant-1", "user-1", ""),
		},
		{
			name: "complete upload",
			cmd:  NewCompleteUploadCommand("tenant-1", "object-1", "", 1, nil, "user-1", ""),
		},
		{
			name: "delete object",
			cmd:  NewDeleteObjectCommand("tenant-1", "object-1", "user-1", ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := NewStorageCommandHandler().Handle(authenticatedCommandContext("tenant-1"), tt.cmd)
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("Handle() returned %d events, want 1", len(events))
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
				t.Fatalf("unmarshal event payload: %v", err)
			}
			if got := payload["tenant_id"]; got != "tenant-1" {
				t.Fatalf("event tenant_id = %v, want tenant-1", got)
			}
		})
	}
}

func TestStorageCommandHandlersRejectUnverifiedAndCrossTenantCalls(t *testing.T) {
	commandsForTenant := []struct {
		name string
		cmd  commands.Command
	}{
		{name: "configure", cmd: NewConfigureStorageCommand("tenant-1", "s3", "", "us-east-1", "bucket", "storagecred_ref", "", true, "user-1", "")},
		{name: "update config", cmd: NewUpdateStorageConfigCommand("config-1", "tenant-1", "user-1", "")},
		{name: "delete config", cmd: NewDeleteStorageConfigCommand("config-1", "tenant-1", "user-1", "")},
		{name: "complete upload", cmd: NewCompleteUploadCommand("tenant-1", "object-1", "", 1, nil, "user-1", "")},
		{name: "delete object", cmd: NewDeleteObjectCommand("tenant-1", "object-1", "user-1", "")},
	}

	for _, tt := range commandsForTenant {
		t.Run(tt.name+" unauthenticated", func(t *testing.T) {
			if _, err := NewStorageCommandHandler().Handle(context.Background(), tt.cmd); !errors.Is(err, storageauth.ErrUnauthenticated) {
				t.Fatalf("Handle() error = %v, want unauthenticated", err)
			}
		})
		t.Run(tt.name+" cross tenant", func(t *testing.T) {
			if _, err := NewStorageCommandHandler().Handle(authenticatedCommandContext("tenant-2"), tt.cmd); !errors.Is(err, storageauth.ErrPermissionDenied) {
				t.Fatalf("Handle() error = %v, want permission denied", err)
			}
		})
	}
}

func TestStorageConfigEventsContainOnlyOpaqueCredentialReferences(t *testing.T) {
	cmd := NewConfigureStorageCommand(
		"tenant-1", "s3", "https://storage.example", "us-east-1", "bucket",
		"storagecred_opaque", "prefix", true, "user-1", "trace-1",
	)
	events, err := NewStorageCommandHandler().Handle(authenticatedCommandContext("tenant-1"), cmd)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("Handle() returned %d events, want 1", len(events))
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if got := payload["credential_ref"]; got != "storagecred_opaque" {
		t.Fatalf("credential_ref = %v, want storagecred_opaque", got)
	}
	for _, forbidden := range []string{"access_key_id", "secret_access_key", "authorization", "signed_url"} {
		if _, exists := payload[forbidden]; exists {
			t.Errorf("event contains forbidden credential field %q", forbidden)
		}
	}
}
