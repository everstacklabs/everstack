package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/enterprise"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
)

// StorageToolContext holds shared dependencies for the storage artifact tools.
type StorageToolContext struct {
	Store     storage.ObjectStore
	Uploader  ArtifactUploader
	DB        *sqlx.DB
	TenantID  string
	SessionID string
	Bucket    string
	ConfigID  string
}

type ArtifactUploader interface {
	Upload(context.Context, storage.InitiateUploadParams, io.Reader) (*storage.Upload, string, error)
}

// NewStorageHandlers returns synthetic tool handlers for artifact storage.
func NewStorageHandlers(ctx *StorageToolContext) []SyntheticToolHandler {
	return []SyntheticToolHandler{
		&uploadArtifactHandler{ctx: ctx},
		&downloadArtifactHandler{ctx: ctx},
		&listArtifactsHandler{ctx: ctx},
	}
}

// ---------------------------------------------------------------------------
// upload_artifact
// ---------------------------------------------------------------------------

type uploadArtifactHandler struct{ ctx *StorageToolContext }

func (h *uploadArtifactHandler) Name() string { return "upload_artifact" }

func (h *uploadArtifactHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "upload_artifact",
			Description: "Upload a file artifact to the tenant's object storage. The file is persisted and associated with the current agent session. Use this to save generated files, reports, images, or any output that should be downloadable later.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filename": map[string]interface{}{
						"type":        "string",
						"description": "Name of the file (e.g. 'report.csv', 'output.json').",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "File content. For binary files, provide base64-encoded data. For text files, provide the raw text content.",
					},
					"purpose": map[string]interface{}{
						"type":        "string",
						"description": "Classification of the artifact. Defaults to 'artifact'.",
						"enum":        []string{"artifact", "dataset", "upload", "eval_result"},
					},
					"content_type": map[string]interface{}{
						"type":        "string",
						"description": "MIME type of the file (e.g. 'text/csv', 'application/json'). Auto-detected from filename if omitted.",
					},
				},
				"required": []string{"filename", "content"},
			},
		},
	}
}

func (h *uploadArtifactHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionArtifactPromote, h.ctx.TenantID); err != nil {
		return "", err
	}
	filename, _ := args["filename"].(string)
	content, _ := args["content"].(string)
	if filename == "" || content == "" {
		return "", fmt.Errorf("filename and content are required")
	}

	purpose, _ := args["purpose"].(string)
	if purpose == "" {
		purpose = "artifact"
	}
	contentType, _ := args["content_type"].(string)
	if contentType == "" {
		contentType = inferContentType(filename)
	}

	// Decode content: try base64 first, fall back to plain text
	var body []byte
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err == nil && len(decoded) > 0 {
		body = decoded
	} else {
		body = []byte(content)
	}

	if h.ctx.Uploader == nil {
		return "", fmt.Errorf("verified artifact uploader is not configured")
	}

	objectID := uuid.New().String()
	key := fmt.Sprintf("tenants/%s/%s/%s/%s", h.ctx.TenantID, purpose, objectID, filename)
	checksum := storage.NewSHA256Checksum(body)
	fingerprintDigest := sha256.Sum256([]byte(strings.Join([]string{
		"agent-artifact:v1",
		h.ctx.TenantID,
		h.ctx.ConfigID,
		h.ctx.SessionID,
		filename,
		contentType,
		purpose,
		checksum.Value,
	}, "\x00")))
	quotaBytes := int64(-1)
	if limit, capped := enterprise.ResolveEntitlements(
		ctx,
		enterprise.LicenseMonitorFromContext(ctx),
	).Limit(enterprise.UsageTypeStorageBytes); capped {
		quotaBytes = limit
	}
	metadata, _ := json.Marshal(map[string]string{"agent_session_id": h.ctx.SessionID})
	now := time.Now().UTC()
	upload, _, err := h.ctx.Uploader.Upload(ctx, storage.InitiateUploadParams{
		ObjectID:               objectID,
		TenantID:               h.ctx.TenantID,
		ConfigID:               h.ctx.ConfigID,
		Key:                    key,
		Filename:               filename,
		ContentType:            contentType,
		ExpectedSizeBytes:      int64(len(body)),
		ExpectedChecksumSHA256: checksum.Value,
		Purpose:                purpose,
		ReferenceID:            h.ctx.SessionID,
		ReferenceType:          "agent_session",
		Metadata:               metadata,
		IdempotencyKey:         "agent-artifact:" + objectID,
		RequestFingerprint:     hex.EncodeToString(fingerprintDigest[:]),
		QuotaBytes:             quotaBytes,
		ExpiresAt:              now.Add(15 * time.Minute),
		Now:                    now,
	}, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to upload artifact: %w", err)
	}

	result, _ := json.Marshal(map[string]interface{}{
		"object_id":  upload.ObjectID,
		"key":        upload.Key,
		"size_bytes": upload.ActualSizeBytes,
	})
	return string(result), nil
}

// ---------------------------------------------------------------------------
// download_artifact
// ---------------------------------------------------------------------------

type downloadArtifactHandler struct{ ctx *StorageToolContext }

func (h *downloadArtifactHandler) Name() string { return "download_artifact" }

func (h *downloadArtifactHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "download_artifact",
			Description: "Get a temporary download URL for a previously uploaded artifact. The URL is valid for 15 minutes.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"object_id": map[string]interface{}{
						"type":        "string",
						"description": "ID of the object to download (returned by upload_artifact or list_artifacts).",
					},
				},
				"required": []string{"object_id"},
			},
		},
	}
}

func (h *downloadArtifactHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionObjectDownload, h.ctx.TenantID); err != nil {
		return "", err
	}
	objectID, _ := args["object_id"].(string)
	if objectID == "" {
		return "", fmt.Errorf("object_id is required")
	}

	if h.ctx.DB == nil {
		return "", fmt.Errorf("database not available")
	}

	var obj struct {
		Key         string `db:"key"`
		Filename    string `db:"filename"`
		ContentType string `db:"content_type"`
		SizeBytes   int64  `db:"size_bytes"`
	}
	err := h.ctx.DB.GetContext(ctx, &obj,
		`SELECT key, filename, content_type, size_bytes FROM object_storage_objects WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		objectID, h.ctx.TenantID)
	if err != nil {
		return "", fmt.Errorf("artifact not found: %w", err)
	}

	url, err := h.ctx.Store.GetPresignedURL(ctx, h.ctx.Bucket, obj.Key, 15*time.Minute)
	if err != nil {
		return "", fmt.Errorf("failed to generate download URL: %w", err)
	}

	result, _ := json.Marshal(map[string]interface{}{
		"url":          url,
		"filename":     obj.Filename,
		"content_type": obj.ContentType,
		"size_bytes":   obj.SizeBytes,
	})
	return string(result), nil
}

// ---------------------------------------------------------------------------
// list_artifacts
// ---------------------------------------------------------------------------

type listArtifactsHandler struct{ ctx *StorageToolContext }

func (h *listArtifactsHandler) Name() string { return "list_artifacts" }

func (h *listArtifactsHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "list_artifacts",
			Description: "List artifacts stored in the tenant's object storage. By default lists artifacts for the current agent session.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"purpose": map[string]interface{}{
						"type":        "string",
						"description": "Filter by purpose (e.g. 'artifact', 'dataset'). Omit to list all.",
					},
					"reference_id": map[string]interface{}{
						"type":        "string",
						"description": "Filter by reference ID. Defaults to the current session ID.",
					},
				},
			},
		},
	}
}

func (h *listArtifactsHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionObjectList, h.ctx.TenantID); err != nil {
		return "", err
	}
	if h.ctx.DB == nil {
		return "", fmt.Errorf("database not available")
	}

	referenceID, _ := args["reference_id"].(string)
	if referenceID == "" {
		referenceID = h.ctx.SessionID
	}
	purpose, _ := args["purpose"].(string)

	query := `SELECT id, filename, content_type, size_bytes, purpose, created_at
		FROM object_storage_objects
		WHERE tenant_id = $1 AND reference_id = $2 AND deleted_at IS NULL`
	queryArgs := []interface{}{h.ctx.TenantID, referenceID}

	if purpose != "" {
		query += " AND purpose = $3"
		queryArgs = append(queryArgs, purpose)
	}
	query += " ORDER BY created_at DESC"

	var objects []struct {
		ID          string    `db:"id" json:"object_id"`
		Filename    string    `db:"filename" json:"filename"`
		ContentType string    `db:"content_type" json:"content_type"`
		SizeBytes   int64     `db:"size_bytes" json:"size_bytes"`
		Purpose     string    `db:"purpose" json:"purpose"`
		CreatedAt   time.Time `db:"created_at" json:"created_at"`
	}
	if err := h.ctx.DB.SelectContext(ctx, &objects, query, queryArgs...); err != nil {
		return "", fmt.Errorf("failed to list artifacts: %w", err)
	}

	if len(objects) == 0 {
		return "[]", nil
	}

	result, _ := json.Marshal(objects)
	return string(result), nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// inferContentType guesses MIME type from filename extension.
func inferContentType(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".csv"):
		return "text/csv"
	case strings.HasSuffix(lower, ".txt"):
		return "text/plain"
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".htm"):
		return "text/html"
	case strings.HasSuffix(lower, ".xml"):
		return "application/xml"
	case strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"):
		return "application/x-yaml"
	case strings.HasSuffix(lower, ".md"):
		return "text/markdown"
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".zip"):
		return "application/zip"
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "application/gzip"
	case strings.HasSuffix(lower, ".js"):
		return "application/javascript"
	case strings.HasSuffix(lower, ".ts"):
		return "application/typescript"
	case strings.HasSuffix(lower, ".py"):
		return "text/x-python"
	case strings.HasSuffix(lower, ".go"):
		return "text/x-go"
	default:
		return "application/octet-stream"
	}
}
