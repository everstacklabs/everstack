package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// UploadState is the durable, provider-neutral state of one logical upload.
type UploadState string

const (
	UploadStatePending     UploadState = "pending"
	UploadStateTransferred UploadState = "transferred"
	UploadStateVerifying   UploadState = "verifying"
	UploadStateReady       UploadState = "ready"
	UploadStateFailed      UploadState = "failed"
	UploadStateQuarantined UploadState = "quarantined"
	UploadStateDeleting    UploadState = "deleting"
	UploadStateDeleted     UploadState = "deleted"
)

// ReservationState makes usage accounting an explicit part of the lifecycle.
type ReservationState string

const (
	ReservationStateReserved  ReservationState = "reserved"
	ReservationStateCommitted ReservationState = "committed"
	ReservationStateReleased  ReservationState = "released"
)

var (
	ErrUploadNotFound      = errors.New("storage upload not found")
	ErrIdempotencyConflict = errors.New("storage upload idempotency key was reused with different input")
	ErrUploadBusy          = errors.New("storage upload is already being verified")
	ErrInvalidUploadState  = errors.New("storage upload state does not allow this operation")
)

// QuotaExceededError is returned before any upload capability is exposed.
type QuotaExceededError struct {
	LimitBytes     int64
	CommittedBytes int64
	ReservedBytes  int64
	RequestedBytes int64
}

func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf(
		"storage quota exceeded: %d committed + %d reserved + %d requested exceeds %d bytes",
		e.CommittedBytes,
		e.ReservedBytes,
		e.RequestedBytes,
		e.LimitBytes,
	)
}

// Upload is the durable lifecycle record. object_storage_objects remains the
// registry of ready objects; pending and failed uploads live only here.
type Upload struct {
	ObjectID               string           `db:"id"`
	TenantID               string           `db:"tenant_id"`
	ConfigID               string           `db:"config_id"`
	Key                    string           `db:"key"`
	Filename               string           `db:"filename"`
	ContentType            string           `db:"content_type"`
	ExpectedSizeBytes      int64            `db:"expected_size_bytes"`
	ExpectedChecksumSHA256 string           `db:"expected_checksum_sha256"`
	ActualSizeBytes        int64            `db:"actual_size_bytes"`
	ActualChecksumSHA256   string           `db:"actual_checksum_sha256"`
	Purpose                string           `db:"purpose"`
	ReferenceID            string           `db:"reference_id"`
	ReferenceType          string           `db:"reference_type"`
	Metadata               json.RawMessage  `db:"metadata"`
	IdempotencyKey         string           `db:"idempotency_key"`
	RequestFingerprint     string           `db:"request_fingerprint"`
	State                  UploadState      `db:"state"`
	ReservationState       ReservationState `db:"reservation_state"`
	LastErrorCode          string           `db:"last_error_code"`
	AttemptCount           int64            `db:"attempt_count"`
	LastErrorAt            sql.NullTime     `db:"last_error_at"`
	NextAttemptAt          sql.NullTime     `db:"next_attempt_at"`
	MultipartUploadID      string           `db:"multipart_upload_id"`
	ExpiresAt              time.Time        `db:"expires_at"`
	CreatedAt              time.Time        `db:"created_at"`
	UpdatedAt              time.Time        `db:"updated_at"`
}

// UploadTransition is the append-only, tenant-scoped lifecycle history shown
// by the status API and used for operational reconciliation evidence.
type UploadTransition struct {
	Sequence   int64       `db:"sequence"`
	TenantID   string      `db:"tenant_id"`
	ObjectID   string      `db:"object_id"`
	FromState  UploadState `db:"from_state"`
	ToState    UploadState `db:"to_state"`
	ReasonCode string      `db:"reason_code"`
	CreatedAt  time.Time   `db:"created_at"`
}

// InitiateUploadParams contains the immutable request identity and the quota
// cap resolved for the authenticated tenant.
type InitiateUploadParams struct {
	ObjectID               string
	TenantID               string
	ConfigID               string
	Key                    string
	Filename               string
	ContentType            string
	ExpectedSizeBytes      int64
	ExpectedChecksumSHA256 string
	Purpose                string
	ReferenceID            string
	ReferenceType          string
	Metadata               json.RawMessage
	IdempotencyKey         string
	RequestFingerprint     string
	QuotaBytes             int64
	ExpiresAt              time.Time
	Now                    time.Time
}

// VerifiedUpload contains metadata measured by Everstack while reading the
// provider object. Client-reported size and digest never populate this type.
type VerifiedUpload struct {
	SizeBytes      int64
	ChecksumSHA256 string
	Metadata       json.RawMessage
	Now            time.Time
}

// ImportReadyUploadParams describes an object that already exists at the
// provider boundary. Imports are explicit, idempotent, and immediately
// committed because sync obtains their size from the provider listing.
type ImportReadyUploadParams struct {
	ObjectID       string
	TenantID       string
	ConfigID       string
	Key            string
	Filename       string
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 string
	Purpose        string
	ReferenceID    string
	ReferenceType  string
	Metadata       json.RawMessage
	IdempotencyKey string
	ImportedAt     time.Time
}

// VerificationFailure is a safe, retry-visible provider verification result.
// Reservation accounting remains untouched until a retry succeeds or cleanup
// deletes the provider object.
type VerificationFailure struct {
	State          UploadState
	Code           string
	ActualSize     int64
	ActualChecksum string
	RetryAt        time.Time
	Now            time.Time
}

// PostgresUploadLifecycle owns durable upload transitions and their coupled
// quota accounting transactions.
type PostgresUploadLifecycle struct {
	db *sqlx.DB
}

func NewPostgresUploadLifecycle(db *sqlx.DB) *PostgresUploadLifecycle {
	if db == nil {
		return nil
	}
	return &PostgresUploadLifecycle{db: db}
}

func uploadLifecycleColumns() []string {
	return []string{
		"id",
		"tenant_id",
		"config_id",
		"key",
		"filename",
		"content_type",
		"expected_size_bytes",
		"expected_checksum_sha256",
		"actual_size_bytes",
		"actual_checksum_sha256",
		"purpose",
		"reference_id",
		"reference_type",
		"metadata",
		"idempotency_key",
		"request_fingerprint",
		"state",
		"reservation_state",
		"last_error_code",
		"attempt_count",
		"last_error_at",
		"next_attempt_at",
		"multipart_upload_id",
		"expires_at",
		"created_at",
		"updated_at",
	}
}

func uploadLifecycleSelect() string {
	return strings.Join(uploadLifecycleColumns(), ", ")
}

func uploadTransitionColumns() []string {
	return []string{
		"sequence",
		"tenant_id",
		"object_id",
		"from_state",
		"to_state",
		"reason_code",
		"created_at",
	}
}

func (p *InitiateUploadParams) normalizeAndValidate() error {
	if p.ObjectID == "" || p.TenantID == "" || p.Key == "" || p.Filename == "" || p.IdempotencyKey == "" || p.RequestFingerprint == "" {
		return errors.New("storage upload identity is incomplete")
	}
	if p.ExpectedSizeBytes < 0 {
		return errors.New("storage upload size cannot be negative")
	}
	if p.ContentType == "" {
		p.ContentType = "application/octet-stream"
	}
	if p.Purpose == "" {
		p.Purpose = "upload"
	}
	if len(p.Metadata) == 0 {
		p.Metadata = json.RawMessage(`{}`)
	}
	if p.Now.IsZero() {
		p.Now = time.Now().UTC()
	}
	if p.ExpiresAt.IsZero() || !p.ExpiresAt.After(p.Now) {
		p.ExpiresAt = p.Now.Add(time.Hour)
	}
	return nil
}

// Initiate creates one pending upload and reserves its expected bytes in the
// same serializable transaction. A replay of the same tenant/idempotency key
// returns the original row without touching usage or transition history.
func (r *PostgresUploadLifecycle) Initiate(ctx context.Context, params InitiateUploadParams) (*Upload, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("storage upload lifecycle database is unavailable")
	}
	if err := params.normalizeAndValidate(); err != nil {
		return nil, false, err
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, fmt.Errorf("begin storage upload initiation: %w", err)
	}
	defer tx.Rollback()

	columns := uploadLifecycleSelect()
	insert := `
		INSERT INTO object_storage_uploads (
			id, tenant_id, config_id, key, filename, content_type,
			expected_size_bytes, expected_checksum_sha256, purpose,
			reference_id, reference_type, metadata, idempotency_key,
			request_fingerprint, state, reservation_state, expires_at,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13,
			$14, 'pending', 'reserved', $15, $16, $16
		)
		ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
		RETURNING ` + columns

	var upload Upload
	err = tx.GetContext(
		ctx,
		&upload,
		insert,
		params.ObjectID,
		params.TenantID,
		params.ConfigID,
		params.Key,
		params.Filename,
		params.ContentType,
		params.ExpectedSizeBytes,
		params.ExpectedChecksumSHA256,
		params.Purpose,
		params.ReferenceID,
		params.ReferenceType,
		[]byte(params.Metadata),
		params.IdempotencyKey,
		params.RequestFingerprint,
		params.ExpiresAt,
		params.Now,
	)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.GetContext(
			ctx,
			&upload,
			`SELECT `+columns+` FROM object_storage_uploads WHERE tenant_id = $1 AND idempotency_key = $2`,
			params.TenantID,
			params.IdempotencyKey,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrUploadNotFound
		}
		if err != nil {
			return nil, false, fmt.Errorf("read replayed storage upload: %w", err)
		}
		if upload.RequestFingerprint != params.RequestFingerprint {
			return nil, false, ErrIdempotencyConflict
		}
		if upload.State == UploadStateFailed &&
			upload.ReservationState == ReservationStateReleased &&
			upload.LastErrorCode == "presign_failed" {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO object_storage_usage (
					tenant_id, total_bytes, object_count, reserved_bytes,
					reserved_object_count, updated_at
				) VALUES ($1, 0, 0, 0, 0, $2)
				ON CONFLICT (tenant_id) DO NOTHING
			`, params.TenantID, params.Now); err != nil {
				return nil, false, fmt.Errorf("initialize retried storage usage: %w", err)
			}

			var usage struct {
				TotalBytes          int64 `db:"total_bytes"`
				ObjectCount         int64 `db:"object_count"`
				ReservedBytes       int64 `db:"reserved_bytes"`
				ReservedObjectCount int64 `db:"reserved_object_count"`
			}
			if err := tx.GetContext(ctx, &usage, `
				SELECT total_bytes, object_count, reserved_bytes, reserved_object_count
				FROM object_storage_usage
				WHERE tenant_id = $1
				FOR UPDATE
			`, params.TenantID); err != nil {
				return nil, false, fmt.Errorf("lock retried storage usage: %w", err)
			}
			if quotaExceeded(usage.TotalBytes, usage.ReservedBytes, upload.ExpectedSizeBytes, params.QuotaBytes) {
				return nil, false, &QuotaExceededError{
					LimitBytes:     params.QuotaBytes,
					CommittedBytes: usage.TotalBytes,
					ReservedBytes:  usage.ReservedBytes,
					RequestedBytes: upload.ExpectedSizeBytes,
				}
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE object_storage_usage
				SET reserved_bytes = reserved_bytes + $2,
					reserved_object_count = reserved_object_count + 1,
					updated_at = $3
				WHERE tenant_id = $1
			`, params.TenantID, upload.ExpectedSizeBytes, params.Now); err != nil {
				return nil, false, fmt.Errorf("reserve retried storage usage: %w", err)
			}
			if err := recordUploadTransition(
				ctx,
				tx,
				upload.TenantID,
				upload.ObjectID,
				UploadStateFailed,
				UploadStatePending,
				"initiation_retried",
				params.Now,
			); err != nil {
				return nil, false, err
			}

			var pending Upload
			if err := tx.GetContext(ctx, &pending, `
				UPDATE object_storage_uploads
				SET state = 'pending',
					reservation_state = 'reserved',
					last_error_code = '',
					last_error_at = NULL,
					next_attempt_at = NULL,
					expires_at = $3,
					updated_at = $4
				WHERE tenant_id = $1 AND id = $2
				RETURNING `+uploadLifecycleSelect(), params.TenantID, upload.ObjectID, params.ExpiresAt, params.Now); err != nil {
				return nil, false, fmt.Errorf("reactivate storage upload initiation: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return nil, false, fmt.Errorf("commit retried storage upload: %w", err)
			}
			return &pending, true, nil
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit replayed storage upload: %w", err)
		}
		return &upload, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("insert storage upload: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_storage_usage (
			tenant_id, total_bytes, object_count, reserved_bytes,
			reserved_object_count, updated_at
		) VALUES ($1, 0, 0, 0, 0, $2)
		ON CONFLICT (tenant_id) DO NOTHING
	`, params.TenantID, params.Now); err != nil {
		return nil, false, fmt.Errorf("initialize storage usage: %w", err)
	}

	var usage struct {
		TotalBytes          int64 `db:"total_bytes"`
		ObjectCount         int64 `db:"object_count"`
		ReservedBytes       int64 `db:"reserved_bytes"`
		ReservedObjectCount int64 `db:"reserved_object_count"`
	}
	if err := tx.GetContext(ctx, &usage, `
		SELECT total_bytes, object_count, reserved_bytes, reserved_object_count
		FROM object_storage_usage
		WHERE tenant_id = $1
		FOR UPDATE
	`, params.TenantID); err != nil {
		return nil, false, fmt.Errorf("lock storage usage: %w", err)
	}

	if quotaExceeded(usage.TotalBytes, usage.ReservedBytes, params.ExpectedSizeBytes, params.QuotaBytes) {
		return nil, false, &QuotaExceededError{
			LimitBytes:     params.QuotaBytes,
			CommittedBytes: usage.TotalBytes,
			ReservedBytes:  usage.ReservedBytes,
			RequestedBytes: params.ExpectedSizeBytes,
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE object_storage_usage
		SET reserved_bytes = reserved_bytes + $2,
			reserved_object_count = reserved_object_count + 1,
			updated_at = $3
		WHERE tenant_id = $1
	`, params.TenantID, params.ExpectedSizeBytes, params.Now); err != nil {
		return nil, false, fmt.Errorf("reserve storage usage: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_storage_upload_events (
			tenant_id, object_id, from_state, to_state, reason_code, created_at
		) VALUES ($1, $2, '', 'pending', 'initiated', $3)
	`, params.TenantID, params.ObjectID, params.Now); err != nil {
		return nil, false, fmt.Errorf("record storage upload initiation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit storage upload initiation: %w", err)
	}
	return &upload, true, nil
}

func quotaExceeded(committed, reserved, requested, limit int64) bool {
	if limit < 0 {
		return false
	}
	if committed < 0 || reserved < 0 || requested < 0 {
		return true
	}
	if committed > limit || reserved > limit-committed {
		return true
	}
	return requested > limit-committed-reserved
}

// ImportReady records an existing provider object and its committed usage in
// one transaction. The tenant/idempotency key makes repeated sync scans free
// of duplicate registry rows or accounting.
func (r *PostgresUploadLifecycle) ImportReady(ctx context.Context, params ImportReadyUploadParams) (*Upload, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("storage upload lifecycle database is unavailable")
	}
	if params.ObjectID == "" || params.TenantID == "" || params.Key == "" || params.Filename == "" || params.IdempotencyKey == "" {
		return nil, false, errors.New("storage import identity is incomplete")
	}
	if params.SizeBytes < 0 {
		return nil, false, errors.New("storage import size cannot be negative")
	}
	if params.ChecksumSHA256 != "" {
		if err := (Checksum{Algorithm: ChecksumSHA256, Value: params.ChecksumSHA256}).Validate(); err != nil {
			return nil, false, errors.New("storage import checksum is invalid")
		}
	}
	if params.ContentType == "" {
		params.ContentType = "application/octet-stream"
	}
	if params.Purpose == "" {
		params.Purpose = "upload"
	}
	if len(params.Metadata) == 0 {
		params.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(params.Metadata) {
		return nil, false, errors.New("storage import metadata is invalid")
	}
	if params.ImportedAt.IsZero() {
		params.ImportedAt = time.Now().UTC()
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, fmt.Errorf("begin storage import: %w", err)
	}
	defer tx.Rollback()

	fingerprint := params.ConfigID + "\x00" + params.Key
	providerIdentity := params.TenantID + "\x00" + fingerprint
	if _, err := tx.ExecContext(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
	`, providerIdentity); err != nil {
		return nil, false, fmt.Errorf("lock storage import provider identity: %w", err)
	}

	var upload Upload
	err = tx.GetContext(ctx, &upload, `
		SELECT `+uploadLifecycleSelect()+`
		FROM object_storage_uploads
		WHERE tenant_id = $1 AND config_id = $2 AND key = $3
			AND state <> 'deleted'
		LIMIT 1
	`, params.TenantID, params.ConfigID, params.Key)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit replayed provider storage import: %w", err)
		}
		return &upload, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("read provider storage import: %w", err)
	}

	err = tx.GetContext(ctx, &upload, `
		INSERT INTO object_storage_uploads (
			id, tenant_id, config_id, key, filename, content_type,
			expected_size_bytes, expected_checksum_sha256, actual_size_bytes,
			actual_checksum_sha256, purpose, reference_id, reference_type,
			metadata, idempotency_key, request_fingerprint, state,
			reservation_state, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $7, $8, $9, $10, $11,
			$12, $13, $14, 'ready', 'committed', $15, $15, $15
		)
		ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
		RETURNING `+uploadLifecycleSelect(),
		params.ObjectID,
		params.TenantID,
		params.ConfigID,
		params.Key,
		params.Filename,
		params.ContentType,
		params.SizeBytes,
		params.ChecksumSHA256,
		params.Purpose,
		params.ReferenceID,
		params.ReferenceType,
		[]byte(params.Metadata),
		params.IdempotencyKey,
		fingerprint,
		params.ImportedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.GetContext(ctx, &upload, `
			SELECT `+uploadLifecycleSelect()+`
			FROM object_storage_uploads
			WHERE tenant_id = $1 AND idempotency_key = $2
		`, params.TenantID, params.IdempotencyKey)
		if err != nil {
			return nil, false, fmt.Errorf("read replayed storage import: %w", err)
		}
		if upload.ConfigID != params.ConfigID || upload.Key != params.Key {
			return nil, false, ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit replayed storage import: %w", err)
		}
		return &upload, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("insert storage import lifecycle: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_storage_objects (
			id, tenant_id, config_id, key, filename, content_type,
			size_bytes, checksum_sha256, purpose, reference_id,
			reference_type, metadata, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13
		)
	`, params.ObjectID, params.TenantID, params.ConfigID, params.Key, params.Filename,
		params.ContentType, params.SizeBytes, params.ChecksumSHA256, params.Purpose,
		params.ReferenceID, params.ReferenceType, []byte(params.Metadata), params.ImportedAt); err != nil {
		return nil, false, fmt.Errorf("publish imported storage object: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_storage_usage (
			tenant_id, total_bytes, object_count, reserved_bytes,
			reserved_object_count, updated_at
		) VALUES ($1, $2, 1, 0, 0, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET
			total_bytes = object_storage_usage.total_bytes + $2,
			object_count = object_storage_usage.object_count + 1,
			updated_at = $3
	`, params.TenantID, params.SizeBytes, params.ImportedAt); err != nil {
		return nil, false, fmt.Errorf("account imported storage object: %w", err)
	}
	if err := recordUploadTransition(
		ctx,
		tx,
		params.TenantID,
		params.ObjectID,
		"",
		UploadStateReady,
		"provider_imported",
		params.ImportedAt,
	); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit storage import: %w", err)
	}
	return &upload, true, nil
}

// BeginVerification acquires a bounded verification lease. The transition
// history records the provider transfer before verification even though both
// state changes commit atomically.
func (r *PostgresUploadLifecycle) BeginVerification(
	ctx context.Context,
	tenantID string,
	objectID string,
	now time.Time,
	lease time.Duration,
) (*Upload, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("storage upload lifecycle database is unavailable")
	}
	if tenantID == "" || objectID == "" {
		return nil, false, errors.New("storage upload identity is incomplete")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, fmt.Errorf("begin storage upload verification: %w", err)
	}
	defer tx.Rollback()

	upload, err := getUploadForUpdate(ctx, tx, tenantID, objectID)
	if err != nil {
		return nil, false, err
	}

	switch upload.State {
	case UploadStateReady:
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit ready upload replay: %w", err)
		}
		return upload, true, nil
	case UploadStateVerifying:
		if upload.UpdatedAt.After(now.Add(-lease)) {
			return nil, false, ErrUploadBusy
		}
		if err := recordUploadTransition(
			ctx,
			tx,
			upload.TenantID,
			upload.ObjectID,
			UploadStateVerifying,
			UploadStateTransferred,
			"verification_lease_expired",
			now,
		); err != nil {
			return nil, false, err
		}
	case UploadStatePending:
		if err := recordUploadTransition(
			ctx,
			tx,
			upload.TenantID,
			upload.ObjectID,
			UploadStatePending,
			UploadStateTransferred,
			"provider_transfer_reported",
			now,
		); err != nil {
			return nil, false, err
		}
	case UploadStateFailed, UploadStateQuarantined:
		if err := recordUploadTransition(
			ctx,
			tx,
			upload.TenantID,
			upload.ObjectID,
			upload.State,
			UploadStateTransferred,
			"verification_retried",
			now,
		); err != nil {
			return nil, false, err
		}
	case UploadStateTransferred:
		// The transfer was already recorded by an earlier completion attempt.
	default:
		return nil, false, fmt.Errorf("%w: cannot verify %s upload", ErrInvalidUploadState, upload.State)
	}

	if err := recordUploadTransition(
		ctx,
		tx,
		upload.TenantID,
		upload.ObjectID,
		UploadStateTransferred,
		UploadStateVerifying,
		"verification_started",
		now,
	); err != nil {
		return nil, false, err
	}

	var verifying Upload
	if err := tx.GetContext(ctx, &verifying, `
		UPDATE object_storage_uploads
		SET state = 'verifying',
			attempt_count = attempt_count + 1,
			last_error_code = '',
			last_error_at = NULL,
			next_attempt_at = NULL,
			updated_at = $3
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+uploadLifecycleSelect(), tenantID, objectID, now); err != nil {
		return nil, false, fmt.Errorf("acquire storage verification lease: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit storage upload verification: %w", err)
	}
	return &verifying, false, nil
}

func getUploadForUpdate(ctx context.Context, tx *sqlx.Tx, tenantID, objectID string) (*Upload, error) {
	var upload Upload
	err := tx.GetContext(
		ctx,
		&upload,
		`SELECT `+uploadLifecycleSelect()+` FROM object_storage_uploads WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
		tenantID,
		objectID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUploadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock storage upload: %w", err)
	}
	return &upload, nil
}

func recordUploadTransition(
	ctx context.Context,
	tx *sqlx.Tx,
	tenantID string,
	objectID string,
	from UploadState,
	to UploadState,
	reason string,
	now time.Time,
) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_storage_upload_events (
			tenant_id, object_id, from_state, to_state, reason_code, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, tenantID, objectID, string(from), string(to), reason, now); err != nil {
		return fmt.Errorf("record storage upload transition: %w", err)
	}
	return nil
}

// CommitVerifiedUpload atomically publishes the ready object, converts its
// reservation to committed usage, and records the terminal verification
// transition. A ready replay is a read-only success.
func (r *PostgresUploadLifecycle) CommitVerifiedUpload(
	ctx context.Context,
	tenantID string,
	objectID string,
	attempt int64,
	verified VerifiedUpload,
) (*Upload, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("storage upload lifecycle database is unavailable")
	}
	if tenantID == "" || objectID == "" || attempt < 1 {
		return nil, false, errors.New("storage verification identity is incomplete")
	}
	if verified.SizeBytes < 0 {
		return nil, false, errors.New("verified storage size cannot be negative")
	}
	if err := (Checksum{Algorithm: ChecksumSHA256, Value: verified.ChecksumSHA256}).Validate(); err != nil {
		return nil, false, errors.New("verified storage checksum is invalid")
	}
	if len(verified.Metadata) > 0 && !json.Valid(verified.Metadata) {
		return nil, false, errors.New("verified storage metadata is invalid")
	}
	if verified.Now.IsZero() {
		verified.Now = time.Now().UTC()
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, fmt.Errorf("begin verified storage commit: %w", err)
	}
	defer tx.Rollback()

	upload, err := getUploadForUpdate(ctx, tx, tenantID, objectID)
	if err != nil {
		return nil, false, err
	}
	if upload.State == UploadStateReady {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit ready storage replay: %w", err)
		}
		return upload, true, nil
	}
	if upload.State != UploadStateVerifying || upload.AttemptCount != attempt {
		return nil, false, fmt.Errorf("%w: verification attempt is no longer current", ErrInvalidUploadState)
	}
	if upload.ReservationState != ReservationStateReserved {
		return nil, false, fmt.Errorf("%w: verification has no active reservation", ErrInvalidUploadState)
	}
	if verified.SizeBytes != upload.ExpectedSizeBytes {
		return nil, false, fmt.Errorf("%w: verified size differs from reserved size", ErrInvalidUploadState)
	}

	metadata := verified.Metadata
	if len(metadata) == 0 {
		metadata = upload.Metadata
	}
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_storage_objects (
			id, tenant_id, config_id, key, filename, content_type,
			size_bytes, checksum_sha256, purpose, reference_id,
			reference_type, metadata, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13
		)
	`, upload.ObjectID, upload.TenantID, upload.ConfigID, upload.Key, upload.Filename,
		upload.ContentType, verified.SizeBytes, verified.ChecksumSHA256, upload.Purpose,
		upload.ReferenceID, upload.ReferenceType, []byte(metadata), verified.Now); err != nil {
		return nil, false, fmt.Errorf("publish verified storage object: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE object_storage_usage
		SET reserved_bytes = reserved_bytes - $2,
			reserved_object_count = reserved_object_count - 1,
			total_bytes = total_bytes + $3,
			object_count = object_count + 1,
			updated_at = $4
		WHERE tenant_id = $1
			AND reserved_bytes >= $2
			AND reserved_object_count >= 1
	`, tenantID, upload.ExpectedSizeBytes, verified.SizeBytes, verified.Now)
	if err != nil {
		return nil, false, fmt.Errorf("commit verified storage usage: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected != 1 {
		return nil, false, errors.New("verified storage reservation was not available")
	}

	if err := recordUploadTransition(
		ctx,
		tx,
		tenantID,
		objectID,
		UploadStateVerifying,
		UploadStateReady,
		"verification_succeeded",
		verified.Now,
	); err != nil {
		return nil, false, err
	}

	var ready Upload
	if err := tx.GetContext(ctx, &ready, `
		UPDATE object_storage_uploads
		SET state = 'ready',
			reservation_state = 'committed',
			actual_size_bytes = $4,
			actual_checksum_sha256 = $5,
			metadata = $6,
			last_error_code = '',
			last_error_at = NULL,
			next_attempt_at = NULL,
			updated_at = $7
		WHERE tenant_id = $1 AND id = $2 AND attempt_count = $3
		RETURNING `+uploadLifecycleSelect(), tenantID, objectID, attempt,
		verified.SizeBytes, verified.ChecksumSHA256, []byte(metadata), verified.Now); err != nil {
		return nil, false, fmt.Errorf("mark verified storage upload ready: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit verified storage upload: %w", err)
	}
	return &ready, false, nil
}

// FailVerification records a failed or quarantined outcome without releasing
// the reservation. That keeps unverified provider bytes inside the quota until
// a retry succeeds or reconciliation removes them.
func (r *PostgresUploadLifecycle) FailVerification(
	ctx context.Context,
	tenantID string,
	objectID string,
	attempt int64,
	failure VerificationFailure,
) (*Upload, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("storage upload lifecycle database is unavailable")
	}
	if tenantID == "" || objectID == "" || attempt < 1 {
		return nil, errors.New("storage verification identity is incomplete")
	}
	if failure.State != UploadStateFailed && failure.State != UploadStateQuarantined {
		return nil, errors.New("verification failure must be failed or quarantined")
	}
	failure.Code = safeProviderCode(failure.Code)
	if failure.Code == "" {
		return nil, errors.New("verification failure code is invalid")
	}
	if failure.ActualSize < 0 {
		return nil, errors.New("verification failure size cannot be negative")
	}
	if failure.ActualChecksum != "" {
		if err := (Checksum{Algorithm: ChecksumSHA256, Value: failure.ActualChecksum}).Validate(); err != nil {
			return nil, errors.New("verification failure checksum is invalid")
		}
	}
	if failure.Now.IsZero() {
		failure.Now = time.Now().UTC()
	}
	if failure.RetryAt.IsZero() || failure.RetryAt.Before(failure.Now) {
		failure.RetryAt = failure.Now.Add(time.Minute)
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin storage verification failure: %w", err)
	}
	defer tx.Rollback()

	upload, err := getUploadForUpdate(ctx, tx, tenantID, objectID)
	if err != nil {
		return nil, err
	}
	if upload.State == failure.State && upload.AttemptCount == attempt && upload.LastErrorCode == failure.Code {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit verification failure replay: %w", err)
		}
		return upload, nil
	}
	if upload.State != UploadStateVerifying || upload.AttemptCount != attempt {
		return nil, fmt.Errorf("%w: verification attempt is no longer current", ErrInvalidUploadState)
	}

	if err := recordUploadTransition(
		ctx,
		tx,
		tenantID,
		objectID,
		UploadStateVerifying,
		failure.State,
		failure.Code,
		failure.Now,
	); err != nil {
		return nil, err
	}

	var failed Upload
	if err := tx.GetContext(ctx, &failed, `
		UPDATE object_storage_uploads
		SET state = $4,
			actual_size_bytes = $5,
			actual_checksum_sha256 = $6,
			last_error_code = $7,
			last_error_at = $8,
			next_attempt_at = $9,
			updated_at = $8
		WHERE tenant_id = $1 AND id = $2 AND attempt_count = $3
		RETURNING `+uploadLifecycleSelect(), tenantID, objectID, attempt, string(failure.State),
		failure.ActualSize, failure.ActualChecksum, failure.Code, failure.Now, failure.RetryAt); err != nil {
		return nil, fmt.Errorf("record storage verification failure: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit storage verification failure: %w", err)
	}
	return &failed, nil
}

// BeginDeletion durably records deletion before the provider call. Replays in
// deleting state return the same work item so a failed provider delete can be
// retried without changing usage early.
func (r *PostgresUploadLifecycle) BeginDeletion(
	ctx context.Context,
	tenantID string,
	objectID string,
	now time.Time,
) (*Upload, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("storage upload lifecycle database is unavailable")
	}
	if tenantID == "" || objectID == "" {
		return nil, false, errors.New("storage deletion identity is incomplete")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, fmt.Errorf("begin storage deletion: %w", err)
	}
	defer tx.Rollback()

	upload, err := getUploadForUpdate(ctx, tx, tenantID, objectID)
	if err != nil {
		return nil, false, err
	}
	if upload.State == UploadStateDeleted {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit deleted storage replay: %w", err)
		}
		return upload, true, nil
	}
	if upload.State == UploadStateDeleting {
		if upload.NextAttemptAt.Valid && upload.NextAttemptAt.Time.After(now) {
			return nil, false, fmt.Errorf("%w: upload deletion retry is not due", ErrUploadBusy)
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit deleting storage replay: %w", err)
		}
		return upload, false, nil
	}

	if err := recordUploadTransition(
		ctx,
		tx,
		tenantID,
		objectID,
		upload.State,
		UploadStateDeleting,
		"deletion_started",
		now,
	); err != nil {
		return nil, false, err
	}

	var deleting Upload
	if err := tx.GetContext(ctx, &deleting, `
		UPDATE object_storage_uploads
		SET state = 'deleting',
			next_attempt_at = NULL,
			updated_at = $3
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+uploadLifecycleSelect(), tenantID, objectID, now); err != nil {
		return nil, false, fmt.Errorf("mark storage upload deleting: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit storage deletion start: %w", err)
	}
	return &deleting, false, nil
}

// CompleteDeletion releases either committed usage or a pending reservation
// only after the provider confirms deletion. The lifecycle state is the
// exact-once guard for both accounting paths.
func (r *PostgresUploadLifecycle) CompleteDeletion(
	ctx context.Context,
	tenantID string,
	objectID string,
	now time.Time,
) (*Upload, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("storage upload lifecycle database is unavailable")
	}
	if tenantID == "" || objectID == "" {
		return nil, false, errors.New("storage deletion identity is incomplete")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, fmt.Errorf("begin storage deletion completion: %w", err)
	}
	defer tx.Rollback()

	upload, err := getUploadForUpdate(ctx, tx, tenantID, objectID)
	if err != nil {
		return nil, false, err
	}
	if upload.State == UploadStateDeleted {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit completed deletion replay: %w", err)
		}
		return upload, true, nil
	}
	if upload.State != UploadStateDeleting {
		return nil, false, fmt.Errorf("%w: upload is not deleting", ErrInvalidUploadState)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE object_storage_objects
		SET deleted_at = COALESCE(deleted_at, $3)
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, objectID, now); err != nil {
		return nil, false, fmt.Errorf("mark storage object deleted: %w", err)
	}

	switch upload.ReservationState {
	case ReservationStateCommitted:
		result, err := tx.ExecContext(ctx, `
			UPDATE object_storage_usage
			SET total_bytes = GREATEST(0, total_bytes - $2),
				object_count = GREATEST(0, object_count - 1),
				updated_at = $3
			WHERE tenant_id = $1
		`, tenantID, upload.ActualSizeBytes, now)
		if err != nil {
			return nil, false, fmt.Errorf("release committed storage usage: %w", err)
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return nil, false, errors.New("committed storage usage row was not available")
		}
	case ReservationStateReserved:
		result, err := tx.ExecContext(ctx, `
			UPDATE object_storage_usage
			SET reserved_bytes = GREATEST(0, reserved_bytes - $2),
				reserved_object_count = GREATEST(0, reserved_object_count - 1),
				updated_at = $3
			WHERE tenant_id = $1
		`, tenantID, upload.ExpectedSizeBytes, now)
		if err != nil {
			return nil, false, fmt.Errorf("release storage reservation: %w", err)
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return nil, false, errors.New("reserved storage usage row was not available")
		}
	case ReservationStateReleased:
		// Accounting already converged; only the durable state remains.
	default:
		return nil, false, fmt.Errorf("%w: unknown reservation state %q", ErrInvalidUploadState, upload.ReservationState)
	}

	if err := recordUploadTransition(
		ctx,
		tx,
		tenantID,
		objectID,
		UploadStateDeleting,
		UploadStateDeleted,
		"provider_delete_succeeded",
		now,
	); err != nil {
		return nil, false, err
	}

	var deleted Upload
	if err := tx.GetContext(ctx, &deleted, `
		UPDATE object_storage_uploads
		SET state = 'deleted',
			reservation_state = 'released',
			last_error_code = '',
			last_error_at = NULL,
			next_attempt_at = NULL,
			updated_at = $3
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+uploadLifecycleSelect(), tenantID, objectID, now); err != nil {
		return nil, false, fmt.Errorf("mark storage upload deleted: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit storage deletion: %w", err)
	}
	return &deleted, false, nil
}

// FailDeletion leaves the lifecycle in deleting state and records a bounded
// retry time. Committed usage or a pending reservation remains charged until
// CompleteDeletion observes provider success.
func (r *PostgresUploadLifecycle) FailDeletion(
	ctx context.Context,
	tenantID string,
	objectID string,
	code string,
	retryAt time.Time,
	now time.Time,
) (*Upload, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("storage upload lifecycle database is unavailable")
	}
	if tenantID == "" || objectID == "" {
		return nil, errors.New("storage deletion identity is incomplete")
	}
	code = safeProviderCode(code)
	if code == "" {
		return nil, errors.New("storage deletion failure code is invalid")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if retryAt.IsZero() || retryAt.Before(now) {
		retryAt = now.Add(time.Minute)
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin storage deletion failure: %w", err)
	}
	defer tx.Rollback()

	upload, err := getUploadForUpdate(ctx, tx, tenantID, objectID)
	if err != nil {
		return nil, err
	}
	if upload.State != UploadStateDeleting {
		return nil, fmt.Errorf("%w: upload is not deleting", ErrInvalidUploadState)
	}

	if err := recordUploadTransition(
		ctx,
		tx,
		tenantID,
		objectID,
		UploadStateDeleting,
		UploadStateDeleting,
		code,
		now,
	); err != nil {
		return nil, err
	}

	var failed Upload
	if err := tx.GetContext(ctx, &failed, `
		UPDATE object_storage_uploads
		SET attempt_count = attempt_count + 1,
			last_error_code = $3,
			last_error_at = $4,
			next_attempt_at = $5,
			updated_at = $4
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+uploadLifecycleSelect(), tenantID, objectID, code, now, retryAt); err != nil {
		return nil, fmt.Errorf("record storage deletion failure: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit storage deletion failure: %w", err)
	}
	return &failed, nil
}

// GetStatus returns the current upload plus its ordered transition history.
func (r *PostgresUploadLifecycle) GetStatus(
	ctx context.Context,
	tenantID string,
	objectID string,
) (*Upload, []UploadTransition, error) {
	if r == nil || r.db == nil {
		return nil, nil, errors.New("storage upload lifecycle database is unavailable")
	}
	if tenantID == "" || objectID == "" {
		return nil, nil, errors.New("storage upload identity is incomplete")
	}

	var upload Upload
	err := r.db.GetContext(
		ctx,
		&upload,
		`SELECT `+uploadLifecycleSelect()+` FROM object_storage_uploads WHERE tenant_id = $1 AND id = $2`,
		tenantID,
		objectID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrUploadNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read storage upload status: %w", err)
	}

	var transitions []UploadTransition
	if err := r.db.SelectContext(ctx, &transitions, `
		SELECT sequence, tenant_id, object_id, from_state, to_state, reason_code, created_at
		FROM object_storage_upload_events
		WHERE tenant_id = $1 AND object_id = $2
		ORDER BY sequence ASC
	`, tenantID, objectID); err != nil {
		return nil, nil, fmt.Errorf("read storage upload transitions: %w", err)
	}
	return &upload, transitions, nil
}

// ListReconcileCandidates returns only work that is due now. The provider
// operation remains guarded by the lifecycle transition methods, so this
// bounded read does not hold database locks during network I/O.
func (r *PostgresUploadLifecycle) ListReconcileCandidates(
	ctx context.Context,
	now time.Time,
	verificationLease time.Duration,
	limit int,
) ([]Upload, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("storage upload lifecycle database is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if verificationLease <= 0 {
		verificationLease = 2 * time.Minute
	}
	if limit <= 0 || limit > 1_000 {
		limit = 100
	}

	var uploads []Upload
	if err := r.db.SelectContext(ctx, &uploads, `
		SELECT `+uploadLifecycleSelect()+`
		FROM object_storage_uploads
		WHERE (
			state = 'deleting'
			AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
		) OR (
			state = 'failed'
			AND reservation_state = 'reserved'
			AND (expires_at <= $1 OR next_attempt_at IS NULL OR next_attempt_at <= $1)
		) OR (
			state = 'verifying'
			AND updated_at <= $2
		) OR (
			state IN ('pending', 'transferred', 'quarantined')
			AND expires_at <= $1
		)
		ORDER BY COALESCE(next_attempt_at, expires_at, updated_at) ASC, created_at ASC
		LIMIT $3
	`, now, now.Add(-verificationLease), limit); err != nil {
		return nil, fmt.Errorf("list storage upload reconciliation candidates: %w", err)
	}
	return uploads, nil
}

// FailInitiation releases a reservation when the provider capability could
// not be issued to the client. Callers must use this only for a newly created
// initiation, because an earlier replay may still hold a valid signed URL.
func (r *PostgresUploadLifecycle) FailInitiation(
	ctx context.Context,
	tenantID string,
	objectID string,
	code string,
	now time.Time,
) (*Upload, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("storage upload lifecycle database is unavailable")
	}
	if tenantID == "" || objectID == "" {
		return nil, false, errors.New("storage upload identity is incomplete")
	}
	code = safeProviderCode(code)
	if code == "" {
		return nil, false, errors.New("storage initiation failure code is invalid")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, fmt.Errorf("begin storage initiation failure: %w", err)
	}
	defer tx.Rollback()

	upload, err := getUploadForUpdate(ctx, tx, tenantID, objectID)
	if err != nil {
		return nil, false, err
	}
	if upload.State == UploadStateFailed && upload.ReservationState == ReservationStateReleased {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit initiation failure replay: %w", err)
		}
		return upload, true, nil
	}
	if upload.State != UploadStatePending || upload.ReservationState != ReservationStateReserved {
		return nil, false, fmt.Errorf("%w: upload initiation can no longer fail", ErrInvalidUploadState)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE object_storage_usage
		SET reserved_bytes = reserved_bytes - $2,
			reserved_object_count = reserved_object_count - 1,
			updated_at = $3
		WHERE tenant_id = $1
			AND reserved_bytes >= $2
			AND reserved_object_count >= 1
	`, tenantID, upload.ExpectedSizeBytes, now)
	if err != nil {
		return nil, false, fmt.Errorf("release failed initiation reservation: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return nil, false, errors.New("failed initiation reservation was not available")
	}

	if err := recordUploadTransition(
		ctx,
		tx,
		tenantID,
		objectID,
		UploadStatePending,
		UploadStateFailed,
		code,
		now,
	); err != nil {
		return nil, false, err
	}

	var failed Upload
	if err := tx.GetContext(ctx, &failed, `
		UPDATE object_storage_uploads
		SET state = 'failed',
			reservation_state = 'released',
			last_error_code = $3,
			last_error_at = $4,
			next_attempt_at = NULL,
			updated_at = $4
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+uploadLifecycleSelect(), tenantID, objectID, code, now); err != nil {
		return nil, false, fmt.Errorf("mark storage initiation failed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit storage initiation failure: %w", err)
	}
	return &failed, false, nil
}
