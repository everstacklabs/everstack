package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/google/uuid"
)

// StorageCommandHandler handles storage config and object commands.
type StorageCommandHandler struct{}

func NewStorageCommandHandler() *StorageCommandHandler { return &StorageCommandHandler{} }

func (h *StorageCommandHandler) CommandType() string {
	return "ConfigureStorage|UpdateStorageConfig|DeleteStorageConfig|CompleteUpload|DeleteObject"
}

func (h *StorageCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *ConfigureStorageCommand:
		return h.handleConfigure(ctx, c)
	case *UpdateStorageConfigCommand:
		return h.handleUpdateConfig(ctx, c)
	case *DeleteStorageConfigCommand:
		return h.handleDeleteConfig(ctx, c)
	case *CompleteUploadCommand:
		return h.handleCompleteUpload(ctx, c)
	case *DeleteObjectCommand:
		return h.handleDeleteObject(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func (h *StorageCommandHandler) handleConfigure(ctx context.Context, cmd *ConfigureStorageCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionConnectionConfigure, cmd.TenantID); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	tenantID := cmd.TenantID

	logger.WithFields(
		"command_id", cmd.ID,
		"provider", cmd.Provider,
		"bucket", cmd.Bucket,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing storage configure command")

	payload := map[string]interface{}{
		"id":             cmd.ID,
		"tenant_id":      tenantID,
		"provider":       cmd.Provider,
		"endpoint":       cmd.Endpoint,
		"region":         cmd.Region,
		"bucket":         cmd.Bucket,
		"credential_ref": cmd.CredentialRef,
		"path_prefix":    cmd.PathPrefix,
		"is_default":     cmd.IsDefault,
		"enabled":        true,
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "storage_config.created",
		Stream:    "storage_configs",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *StorageCommandHandler) handleUpdateConfig(ctx context.Context, cmd *UpdateStorageConfigCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionConnectionUpdate, cmd.TenantID); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	tenantID := cmd.TenantID

	logger.WithFields(
		"command_id", cmd.ID,
		"config_id", cmd.ConfigID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing storage config update command")

	payload := map[string]interface{}{
		"id":             cmd.ConfigID,
		"tenant_id":      tenantID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	if cmd.Endpoint != nil {
		payload["endpoint"] = *cmd.Endpoint
	}
	if cmd.Region != nil {
		payload["region"] = *cmd.Region
	}
	if cmd.Bucket != nil {
		payload["bucket"] = *cmd.Bucket
	}
	if cmd.CredentialRef != nil {
		payload["credential_ref"] = *cmd.CredentialRef
		if cmd.PreviousCredentialRef != "" {
			payload["previous_credential_ref"] = cmd.PreviousCredentialRef
		}
	}
	if cmd.PathPrefix != nil {
		payload["path_prefix"] = *cmd.PathPrefix
	}
	if cmd.Enabled != nil {
		payload["enabled"] = *cmd.Enabled
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "storage_config.updated",
		Stream:    "storage_configs",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *StorageCommandHandler) handleDeleteConfig(ctx context.Context, cmd *DeleteStorageConfigCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionConnectionDelete, cmd.TenantID); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	tenantID := cmd.TenantID

	payload := map[string]interface{}{
		"id":             cmd.ConfigID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	if cmd.CredentialRef != "" {
		payload["credential_ref"] = cmd.CredentialRef
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "storage_config.deleted",
		Stream:    "storage_configs",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *StorageCommandHandler) handleCompleteUpload(ctx context.Context, cmd *CompleteUploadCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionUploadComplete, cmd.TenantID); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	tenantID := cmd.TenantID

	payload := map[string]interface{}{
		"id":              cmd.ObjectID,
		"tenant_id":       tenantID,
		"checksum_sha256": cmd.ChecksumSHA256,
		"size_bytes":      cmd.SizeBytes,
		"metadata":        cmd.Metadata,
		"completed_at":    now.Format(time.RFC3339),
		"correlation_id":  correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "storage_object.upload_completed",
		Stream:    "storage_objects",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *StorageCommandHandler) handleDeleteObject(ctx context.Context, cmd *DeleteObjectCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionObjectDelete, cmd.TenantID); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	tenantID := cmd.TenantID

	payload := map[string]interface{}{
		"id":             cmd.ObjectID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "storage_object.deleted",
		Stream:    "storage_objects",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
