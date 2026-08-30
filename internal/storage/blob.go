package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

// BlobPlaneV2 identifies the additive object-storage contract. ObjectStore is
// intentionally unchanged so existing consumers and third-party adapters keep
// compiling while they migrate deliberately.
const BlobPlaneV2 = "blob-plane.v2"

// ErrorCode is a stable, provider-neutral storage failure category.
type ErrorCode string

const (
	ErrorUnknown          ErrorCode = "unknown"
	ErrorInvalidArgument  ErrorCode = "invalid_argument"
	ErrorUnsupported      ErrorCode = "unsupported"
	ErrorNotFound         ErrorCode = "not_found"
	ErrorAlreadyExists    ErrorCode = "already_exists"
	ErrorConflict         ErrorCode = "conflict"
	ErrorChecksumMismatch ErrorCode = "checksum_mismatch"
	ErrorUnauthorized     ErrorCode = "unauthorized"
	ErrorExpired          ErrorCode = "expired"
	ErrorThrottled        ErrorCode = "throttled"
	ErrorTimeout          ErrorCode = "timeout"
	ErrorUnavailable      ErrorCode = "unavailable"
	ErrorProvider         ErrorCode = "provider_error"
)

// OperationError deliberately excludes a provider response message. Provider
// bodies can echo authorization material or signed URLs, so callers get only a
// stable code plus safe protocol metadata.
type OperationError struct {
	Operation    string
	Code         ErrorCode
	Retryable    bool
	ProviderCode string
	HTTPStatus   int
}

// NewOperationError constructs a safe provider-neutral error.
func NewOperationError(operation string, code ErrorCode, retryable bool, providerCode string, httpStatus int) *OperationError {
	if code == "" {
		code = ErrorUnknown
	}
	return &OperationError{
		Operation:    strings.TrimSpace(operation),
		Code:         code,
		Retryable:    retryable,
		ProviderCode: safeProviderCode(providerCode),
		HTTPStatus:   httpStatus,
	}
}

// safeProviderCode accepts only a short ASCII token. S3-compatible endpoints
// control error-code response fields, so arbitrary provider text must not cross
// Everstack's error boundary or reach structured logs.
func safeProviderCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return ""
	}
	return value
}

func (e *OperationError) Error() string {
	if e == nil {
		return "storage operation failed"
	}
	if e.Operation == "" {
		return fmt.Sprintf("storage operation failed: %s", e.Code)
	}
	return fmt.Sprintf("storage %s failed: %s", e.Operation, e.Code)
}

// IsCode reports whether err has the requested stable storage code.
func IsCode(err error, code ErrorCode) bool {
	var operationErr *OperationError
	return errors.As(err, &operationErr) && operationErr != nil && operationErr.Code == code
}

// IsRetryable reports whether retrying the failed provider operation is safe.
func IsRetryable(err error) bool {
	var operationErr *OperationError
	return errors.As(err, &operationErr) && operationErr != nil && operationErr.Retryable
}

// ChecksumAlgorithm is a provider-neutral content digest algorithm.
type ChecksumAlgorithm string

const ChecksumSHA256 ChecksumAlgorithm = "sha256"

// Checksum uses lowercase hexadecimal values at the Everstack boundary.
// Adapters translate to provider-specific wire formats such as base64.
type Checksum struct {
	Algorithm ChecksumAlgorithm `json:"algorithm"`
	Value     string            `json:"value"`
}

func NewSHA256Checksum(data []byte) Checksum {
	digest := sha256.Sum256(data)
	return Checksum{Algorithm: ChecksumSHA256, Value: hex.EncodeToString(digest[:])}
}

func (c Checksum) IsZero() bool {
	return c.Algorithm == "" && c.Value == ""
}

// Validate rejects ambiguous or provider-specific checksum representations.
func (c Checksum) Validate() error {
	if c.IsZero() {
		return nil
	}
	if c.Algorithm != ChecksumSHA256 || len(c.Value) != sha256.Size*2 || c.Value != strings.ToLower(c.Value) {
		return NewOperationError("validate checksum", ErrorInvalidArgument, false, "", 0)
	}
	decoded, err := hex.DecodeString(c.Value)
	if err != nil || len(decoded) != sha256.Size {
		return NewOperationError("validate checksum", ErrorInvalidArgument, false, "", 0)
	}
	return nil
}

// ObjectInfo is stable metadata returned by Blob Plane V2 operations.
type ObjectInfo struct {
	Key          string
	SizeBytes    int64
	ContentType  string
	ETag         string
	Checksum     Checksum
	LastModified time.Time
}

// ObjectReader owns Body. Callers must close it.
type ObjectReader struct {
	ObjectInfo
	Body io.ReadCloser
}

type ListOptions struct {
	Prefix   string
	Cursor   string
	PageSize int32
}

type ObjectPage struct {
	Objects    []ObjectInfo
	NextCursor string
}

type PutOptions struct {
	ContentType  string
	ExpectedSize *int64
	Checksum     Checksum
}

type CopyResult struct {
	ObjectInfo
	UsedFallback bool
}

type MultipartOptions struct {
	ContentType  string
	ExpectedSize *int64
	Checksum     Checksum
}

type MultipartUpload struct {
	Bucket  string
	Key     string
	ID      string
	Options MultipartOptions
}

type UploadedPart struct {
	Number   int32
	ETag     string
	Checksum Checksum
}

// BlobCapabilities is the evidence-facing capability record. It describes the
// configured adapter, not a public support claim. Support is published only
// after the provider conformance profile records a passing run.
type BlobCapabilities struct {
	ContractVersion  string `json:"contract_version"`
	DirectRead       bool   `json:"direct_read"`
	PaginatedList    bool   `json:"paginated_list"`
	ConditionalWrite bool   `json:"conditional_write"`
	Copy             bool   `json:"copy"`
	NativeCopy       bool   `json:"native_copy"`
	SafeCopyFallback bool   `json:"safe_copy_fallback"`
	SafeCopyMaxBytes int64  `json:"safe_copy_max_bytes"`
	Multipart        bool   `json:"multipart"`
	SHA256           bool   `json:"sha256"`
	PresignedURLs    bool   `json:"presigned_urls"`
	ClassifiedErrors bool   `json:"classified_errors"`
	Retries          bool   `json:"retries"`
}

// BlobPlane is the additive V2 contract. It embeds ObjectStore only to retain
// the compatibility surface; no method was added to ObjectStore itself.
type BlobPlane interface {
	ObjectStore
	BlobCapabilities() BlobCapabilities
	Get(ctx context.Context, bucket, key string) (*ObjectReader, error)
	ListPage(ctx context.Context, bucket string, options ListOptions) (ObjectPage, error)
	PutIfAbsent(ctx context.Context, bucket, key string, body io.Reader, options PutOptions) (ObjectInfo, error)
	CopyIfAbsent(ctx context.Context, sourceBucket, sourceKey, destinationBucket, destinationKey string) (CopyResult, error)
	BeginMultipart(ctx context.Context, bucket, key string, options MultipartOptions) (MultipartUpload, error)
	UploadPart(ctx context.Context, upload MultipartUpload, partNumber int32, body io.Reader, options PutOptions) (UploadedPart, error)
	CompleteMultipart(ctx context.Context, upload MultipartUpload, parts []UploadedPart) (ObjectInfo, error)
	AbortMultipart(ctx context.Context, upload MultipartUpload) error
}

// RequireBlobPlane converts an existing store without silently weakening the
// requested operation. Legacy adapters receive an explicit unsupported error.
func RequireBlobPlane(store ObjectStore) (BlobPlane, error) {
	if store == nil || isNilInterface(store) {
		return nil, NewOperationError("require blob plane v2", ErrorUnsupported, false, "", 0)
	}
	blobStore, ok := store.(BlobPlane)
	if !ok || isNilInterface(blobStore) {
		return nil, NewOperationError("require blob plane v2", ErrorUnsupported, false, "", 0)
	}
	return blobStore, nil
}

func isNilInterface(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
