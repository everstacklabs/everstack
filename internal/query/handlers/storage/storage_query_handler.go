package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/jmoiron/sqlx"
)

// --- Read Models ---

type StorageConfigReadModel struct {
	ID                string    `db:"id" json:"id"`
	TenantID          string    `db:"tenant_id" json:"tenant_id"`
	Provider          string    `db:"provider" json:"provider"`
	Endpoint          string    `db:"endpoint" json:"endpoint"`
	Region            string    `db:"region" json:"region"`
	Bucket            string    `db:"bucket" json:"bucket"`
	CredentialRef     string    `db:"credential_ref" json:"-"`
	PathPrefix        string    `db:"path_prefix" json:"path_prefix"`
	IsDefault         bool      `db:"is_default" json:"is_default"`
	Enabled           bool      `db:"enabled" json:"enabled"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time `db:"updated_at" json:"updated_at"`
	ManagementMode    string    `db:"management_mode" json:"-"`
	ManagedCellID     string    `db:"managed_cell_id" json:"-"`
	ManagedPathPrefix string    `db:"managed_path_prefix" json:"-"`
}

type StorageObjectReadModel struct {
	ID             string         `db:"id" json:"id"`
	TenantID       string         `db:"tenant_id" json:"tenant_id"`
	ConfigID       string         `db:"config_id" json:"config_id"`
	Key            string         `db:"key" json:"key"`
	Filename       string         `db:"filename" json:"filename"`
	ContentType    string         `db:"content_type" json:"content_type"`
	SizeBytes      int64          `db:"size_bytes" json:"size_bytes"`
	ChecksumSHA256 string         `db:"checksum_sha256" json:"checksum_sha256"`
	Purpose        string         `db:"purpose" json:"purpose"`
	ReferenceID    string         `db:"reference_id" json:"reference_id"`
	ReferenceType  string         `db:"reference_type" json:"reference_type"`
	Metadata       sql.NullString `db:"metadata" json:"metadata"`
	CreatedAt      time.Time      `db:"created_at" json:"created_at"`
	DeletedAt      sql.NullTime   `db:"deleted_at" json:"deleted_at"`
}

type StorageUsageReadModel struct {
	TenantID            string    `db:"tenant_id" json:"tenant_id"`
	TotalBytes          int64     `db:"total_bytes" json:"total_bytes"`
	ObjectCount         int64     `db:"object_count" json:"object_count"`
	ReservedBytes       int64     `db:"reserved_bytes" json:"reserved_bytes"`
	ReservedObjectCount int64     `db:"reserved_object_count" json:"reserved_object_count"`
	UpdatedAt           time.Time `db:"updated_at" json:"updated_at"`
}

// --- Queries ---

// GetStorageConfigQuery retrieves a storage config by ID.
type GetStorageConfigQuery struct {
	query.BaseQuery
	ConfigID string `json:"config_id"`
	TenantID string `json:"tenant_id"`
}

func NewGetStorageConfigQuery(configID, tenantID string) *GetStorageConfigQuery {
	return &GetStorageConfigQuery{ConfigID: configID, TenantID: tenantID}
}

func (q GetStorageConfigQuery) QueryType() string { return "GetStorageConfig" }
func (q GetStorageConfigQuery) Validate() error {
	if q.TenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	if q.ConfigID == "" {
		return fmt.Errorf("config_id is required")
	}
	return nil
}

// ListStorageConfigsQuery lists storage configs for a tenant.
type ListStorageConfigsQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
}

func NewListStorageConfigsQuery(tenantID string) *ListStorageConfigsQuery {
	return &ListStorageConfigsQuery{TenantID: tenantID}
}

func (q ListStorageConfigsQuery) QueryType() string { return "ListStorageConfigs" }
func (q ListStorageConfigsQuery) Validate() error {
	if q.TenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	return nil
}

// ListObjectsQuery lists objects filtered by purpose and reference.
type ListObjectsQuery struct {
	query.BaseQuery
	TenantID      string `json:"tenant_id"`
	Purpose       string `json:"purpose,omitempty"`
	ReferenceID   string `json:"reference_id,omitempty"`
	ReferenceType string `json:"reference_type,omitempty"`
	PageSize      int    `json:"page_size,omitempty"`
	Offset        int    `json:"offset,omitempty"`
}

func NewListObjectsQuery(tenantID, purpose, referenceID, referenceType string, pageSize, offset int) *ListObjectsQuery {
	return &ListObjectsQuery{
		TenantID:      tenantID,
		Purpose:       purpose,
		ReferenceID:   referenceID,
		ReferenceType: referenceType,
		PageSize:      pageSize,
		Offset:        offset,
	}
}

func (q ListObjectsQuery) QueryType() string { return "ListStorageObjects" }
func (q ListObjectsQuery) Validate() error {
	if q.TenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	return nil
}

// GetStorageUsageQuery retrieves usage for a tenant.
type GetStorageUsageQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
}

func NewGetStorageUsageQuery(tenantID string) *GetStorageUsageQuery {
	return &GetStorageUsageQuery{TenantID: tenantID}
}

func (q GetStorageUsageQuery) QueryType() string { return "GetStorageUsage" }
func (q GetStorageUsageQuery) Validate() error {
	if q.TenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	return nil
}

// --- Handlers ---

// GetStorageConfigHandler handles GetStorageConfig queries.
type GetStorageConfigHandler struct {
	db *sqlx.DB
}

func NewGetStorageConfigHandler(db *sqlx.DB) *GetStorageConfigHandler {
	return &GetStorageConfigHandler{db: db}
}

func (h *GetStorageConfigHandler) QueryType() string { return "GetStorageConfig" }

func (h *GetStorageConfigHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetStorageConfigQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type")
	}
	if err := qry.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionConnectionRead, qry.TenantID); err != nil {
		return nil, err
	}

	var out StorageConfigReadModel
	err := h.db.GetContext(ctx, &out,
		`SELECT id, tenant_id, provider, endpoint, region, bucket, COALESCE(credential_ref, '') AS credential_ref, path_prefix, is_default, enabled, created_at, updated_at,
		        management_mode, COALESCE(managed_cell_id, '') AS managed_cell_id, managed_path_prefix
		 FROM object_storage_configs WHERE id = $1 AND tenant_id = $2`, qry.ConfigID, qry.TenantID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get storage config: %w", err)
	}
	return &out, nil
}

// ListStorageConfigsHandler handles ListStorageConfigs queries.
type ListStorageConfigsHandler struct {
	db *sqlx.DB
}

func NewListStorageConfigsHandler(db *sqlx.DB) *ListStorageConfigsHandler {
	return &ListStorageConfigsHandler{db: db}
}

func (h *ListStorageConfigsHandler) QueryType() string { return "ListStorageConfigs" }

func (h *ListStorageConfigsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListStorageConfigsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type")
	}
	if err := qry.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionConnectionRead, qry.TenantID); err != nil {
		return nil, err
	}

	var out []StorageConfigReadModel
	err := h.db.SelectContext(ctx, &out,
		`SELECT id, tenant_id, provider, endpoint, region, bucket, COALESCE(credential_ref, '') AS credential_ref, path_prefix, is_default, enabled, created_at, updated_at,
		        management_mode, COALESCE(managed_cell_id, '') AS managed_cell_id, managed_path_prefix
		 FROM object_storage_configs WHERE tenant_id = $1 ORDER BY created_at DESC`, qry.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list storage configs: %w", err)
	}
	return out, nil
}

// ListStorageObjectsHandler handles ListStorageObjects queries.
type ListStorageObjectsHandler struct {
	db *sqlx.DB
}

func NewListStorageObjectsHandler(db *sqlx.DB) *ListStorageObjectsHandler {
	return &ListStorageObjectsHandler{db: db}
}

func (h *ListStorageObjectsHandler) QueryType() string { return "ListStorageObjects" }

func (h *ListStorageObjectsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListObjectsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type")
	}
	if err := qry.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionObjectList, qry.TenantID); err != nil {
		return nil, err
	}

	baseQuery := `SELECT id, tenant_id, config_id, key, filename, content_type, size_bytes, checksum_sha256,
		purpose, reference_id, reference_type, metadata, created_at, deleted_at
		FROM object_storage_objects WHERE tenant_id = $1 AND deleted_at IS NULL`
	args := []interface{}{qry.TenantID}
	argIdx := 2

	if qry.Purpose != "" {
		baseQuery += fmt.Sprintf(" AND purpose = $%d", argIdx)
		args = append(args, qry.Purpose)
		argIdx++
	}
	if qry.ReferenceID != "" {
		baseQuery += fmt.Sprintf(" AND reference_id = $%d", argIdx)
		args = append(args, qry.ReferenceID)
		argIdx++
	}
	if qry.ReferenceType != "" {
		baseQuery += fmt.Sprintf(" AND reference_type = $%d", argIdx)
		args = append(args, qry.ReferenceType)
		argIdx++
	}

	baseQuery += " ORDER BY created_at DESC"

	pageSize := qry.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", pageSize, qry.Offset)

	var out []StorageObjectReadModel
	err := h.db.SelectContext(ctx, &out, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list storage objects: %w", err)
	}
	return out, nil
}

// GetStorageUsageHandler handles GetStorageUsage queries.
type GetStorageUsageHandler struct {
	db *sqlx.DB
}

func NewGetStorageUsageHandler(db *sqlx.DB) *GetStorageUsageHandler {
	return &GetStorageUsageHandler{db: db}
}

func (h *GetStorageUsageHandler) QueryType() string { return "GetStorageUsage" }

func (h *GetStorageUsageHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetStorageUsageQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type")
	}
	if err := qry.Validate(); err != nil {
		return nil, err
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionUsageRead, qry.TenantID); err != nil {
		return nil, err
	}

	var out StorageUsageReadModel
	err := h.db.GetContext(ctx, &out,
		`SELECT tenant_id, total_bytes, object_count, reserved_bytes, reserved_object_count, updated_at FROM object_storage_usage WHERE tenant_id = $1`,
		qry.TenantID)
	if err == sql.ErrNoRows {
		return &StorageUsageReadModel{TenantID: qry.TenantID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get storage usage: %w", err)
	}
	return &out, nil
}
