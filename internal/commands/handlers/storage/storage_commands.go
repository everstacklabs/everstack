package storage

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/google/uuid"
)

// ConfigureStorageCommand creates or updates a storage backend config.
type ConfigureStorageCommand struct {
	commands.BaseCommand
	TenantID      string `json:"tenant_id"`
	Provider      string `json:"provider"` // s3, r2, minio, gcs
	Endpoint      string `json:"endpoint"`
	Region        string `json:"region"`
	Bucket        string `json:"bucket"`
	CredentialRef string `json:"credential_ref"`
	PathPrefix    string `json:"path_prefix"`
	IsDefault     bool   `json:"is_default"`
}

func NewConfigureStorageCommand(tenantID, provider, endpoint, region, bucket, credentialRef, pathPrefix string, isDefault bool, userID, traceID string) *ConfigureStorageCommand {
	return &ConfigureStorageCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:      tenantID,
		Provider:      provider,
		Endpoint:      endpoint,
		Region:        region,
		Bucket:        bucket,
		CredentialRef: credentialRef,
		PathPrefix:    pathPrefix,
		IsDefault:     isDefault,
	}
}

func (c ConfigureStorageCommand) AggregateID() string { return c.ID }
func (c ConfigureStorageCommand) CommandType() string { return "ConfigureStorage" }
func (c ConfigureStorageCommand) Validate() error {
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id cannot be empty")
	}
	if c.Provider == "" {
		return fmt.Errorf("provider cannot be empty")
	}
	valid := map[string]bool{"s3": true, "r2": true, "minio": true, "gcs": true}
	if !valid[c.Provider] {
		return fmt.Errorf("invalid provider: %s (must be s3, r2, minio, or gcs)", c.Provider)
	}
	if c.Bucket == "" {
		return fmt.Errorf("bucket cannot be empty")
	}
	if c.CredentialRef == "" {
		return fmt.Errorf("credential_ref cannot be empty")
	}
	return nil
}

// UpdateStorageConfigCommand updates an existing storage config.
type UpdateStorageConfigCommand struct {
	commands.BaseCommand
	ConfigID              string  `json:"config_id"`
	TenantID              string  `json:"tenant_id"`
	Endpoint              *string `json:"endpoint,omitempty"`
	Region                *string `json:"region,omitempty"`
	Bucket                *string `json:"bucket,omitempty"`
	CredentialRef         *string `json:"credential_ref,omitempty"`
	PreviousCredentialRef string  `json:"previous_credential_ref,omitempty"`
	PathPrefix            *string `json:"path_prefix,omitempty"`
	Enabled               *bool   `json:"enabled,omitempty"`
}

func NewUpdateStorageConfigCommand(configID, tenantID, userID, traceID string) *UpdateStorageConfigCommand {
	return &UpdateStorageConfigCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ConfigID: configID,
		TenantID: tenantID,
	}
}

func (c UpdateStorageConfigCommand) AggregateID() string { return c.ConfigID }
func (c UpdateStorageConfigCommand) CommandType() string { return "UpdateStorageConfig" }
func (c UpdateStorageConfigCommand) Validate() error {
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id cannot be empty")
	}
	if c.ConfigID == "" {
		return fmt.Errorf("config_id cannot be empty")
	}
	return nil
}

// DeleteStorageConfigCommand deletes a storage config.
type DeleteStorageConfigCommand struct {
	commands.BaseCommand
	ConfigID      string `json:"config_id"`
	TenantID      string `json:"tenant_id"`
	CredentialRef string `json:"credential_ref,omitempty"`
}

func NewDeleteStorageConfigCommand(configID, tenantID, userID, traceID string) *DeleteStorageConfigCommand {
	return &DeleteStorageConfigCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ConfigID: configID,
		TenantID: tenantID,
	}
}

func (c DeleteStorageConfigCommand) AggregateID() string { return c.ConfigID }
func (c DeleteStorageConfigCommand) CommandType() string { return "DeleteStorageConfig" }
func (c DeleteStorageConfigCommand) Validate() error {
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id cannot be empty")
	}
	if c.ConfigID == "" {
		return fmt.Errorf("config_id cannot be empty")
	}
	return nil
}

// CompleteUploadCommand confirms an upload and registers the object.
type CompleteUploadCommand struct {
	commands.BaseCommand
	TenantID       string                 `json:"tenant_id"`
	ObjectID       string                 `json:"object_id"`
	ChecksumSHA256 string                 `json:"checksum_sha256"`
	SizeBytes      int64                  `json:"size_bytes"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

func NewCompleteUploadCommand(tenantID, objectID, checksumSHA256 string, sizeBytes int64, metadata map[string]interface{}, userID, traceID string) *CompleteUploadCommand {
	return &CompleteUploadCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:       tenantID,
		ObjectID:       objectID,
		ChecksumSHA256: checksumSHA256,
		SizeBytes:      sizeBytes,
		Metadata:       metadata,
	}
}

func (c CompleteUploadCommand) AggregateID() string { return c.ObjectID }
func (c CompleteUploadCommand) CommandType() string { return "CompleteUpload" }
func (c CompleteUploadCommand) Validate() error {
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id cannot be empty")
	}
	if c.ObjectID == "" {
		return fmt.Errorf("object_id cannot be empty")
	}
	return nil
}

// DeleteObjectCommand deletes an object from storage.
type DeleteObjectCommand struct {
	commands.BaseCommand
	TenantID string `json:"tenant_id"`
	ObjectID string `json:"object_id"`
}

func NewDeleteObjectCommand(tenantID, objectID, userID, traceID string) *DeleteObjectCommand {
	return &DeleteObjectCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID: tenantID,
		ObjectID: objectID,
	}
}

func (c DeleteObjectCommand) AggregateID() string { return c.ObjectID }
func (c DeleteObjectCommand) CommandType() string { return "DeleteObject" }
func (c DeleteObjectCommand) Validate() error {
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id cannot be empty")
	}
	if c.ObjectID == "" {
		return fmt.Errorf("object_id cannot be empty")
	}
	return nil
}
