package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/everstacklabs/everstack/internal/storage"
)

func TestPutIfAbsentRejectsChecksumMismatchBeforeProviderWrite(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint:        server.URL,
		Region:          "us-east-1",
		Bucket:          "bucket",
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		ForcePathStyle:  true,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	wantSize := int64(len("actual body"))
	_, err = store.PutIfAbsent(context.Background(), "", "key", strings.NewReader("actual body"), storage.PutOptions{
		ContentType:  "text/plain",
		ExpectedSize: &wantSize,
		Checksum:     storage.NewSHA256Checksum([]byte("different body")),
	})
	if !storage.IsCode(err, storage.ErrorChecksumMismatch) {
		t.Fatalf("PutIfAbsent() error = %v, want checksum_mismatch", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("provider received %d requests after local checksum mismatch", got)
	}
}

func TestPutIfAbsentSupportsContentMD5WireIntegrity(t *testing.T) {
	var contentMD5 string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		contentMD5 = request.Header.Get("Content-MD5")
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint:        server.URL,
		Region:          "auto",
		Bucket:          "bucket",
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		ForcePathStyle:  true,
		HTTPClient:      server.Client(),
		WireChecksum:    WireChecksumContentMD5,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := store.PutIfAbsent(context.Background(), "", "key", strings.NewReader("actual body"), storage.PutOptions{}); err != nil {
		t.Fatalf("PutIfAbsent() error = %v", err)
	}
	if contentMD5 != "JNjvKK7wEb3wtd1auk4mzQ==" {
		t.Fatalf("Content-MD5 = %q, want body digest", contentMD5)
	}
}

func TestConditionalRequestConflictIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `<Error><Code>ConditionalRequestConflict</Code><Message>retry</Message></Error>`)
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint:         server.URL,
		Region:           "us-east-1",
		Bucket:           "bucket",
		AccessKeyID:      "access",
		SecretAccessKey:  "secret",
		ForcePathStyle:   true,
		HTTPClient:       server.Client(),
		RetryMaxAttempts: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = store.PutIfAbsent(context.Background(), "", "key", strings.NewReader("body"), storage.PutOptions{})
	if !storage.IsCode(err, storage.ErrorConflict) || !storage.IsRetryable(err) {
		t.Fatalf("PutIfAbsent() error = %v, want retryable conflict", err)
	}
}

func TestChecksumReadCloserRejectsCorruptProviderBody(t *testing.T) {
	expected := storage.NewSHA256Checksum([]byte("expected"))
	reader := newChecksumReadCloser(io.NopCloser(strings.NewReader("corrupt")), expected)
	_, err := io.ReadAll(reader)
	if !storage.IsCode(err, storage.ErrorChecksumMismatch) {
		t.Fatalf("ReadAll() error = %v, want checksum_mismatch", err)
	}
}

func TestMalformedProviderChecksumMetadataFailsExplicitly(t *testing.T) {
	_, err := checksumFromProvider(nil, map[string]string{checksumMetadataKey: "malformed"}, "")
	if !storage.IsCode(err, storage.ErrorChecksumMismatch) {
		t.Fatalf("checksumFromProvider() error = %v, want checksum_mismatch", err)
	}
}

func TestMalformedProviderChecksumHeaderFailsExplicitly(t *testing.T) {
	value := "not-base64"
	_, err := checksumFromProvider(&value, nil, "")
	if !storage.IsCode(err, storage.ErrorChecksumMismatch) {
		t.Fatalf("checksumFromProvider() error = %v, want checksum_mismatch", err)
	}
}

func TestCompositeProviderChecksumDoesNotMasqueradeAsFullObjectSHA256(t *testing.T) {
	value := "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=-3"
	checksum, err := checksumFromProvider(&value, nil, types.ChecksumTypeComposite)
	if err != nil {
		t.Fatalf("checksumFromProvider() error = %v", err)
	}
	if !checksum.IsZero() {
		t.Fatalf("checksumFromProvider() = %+v, want no full-object checksum", checksum)
	}
}

func TestGetAcceptsMultipartCompositeChecksumWithoutEverstackMetadata(t *testing.T) {
	body := []byte("multipart object bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Amz-Checksum-Type", string(types.ChecksumTypeComposite))
		w.Header().Set("X-Amz-Checksum-Sha256", "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=-3")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	store := newTestStore(t, server, 1)
	object, err := store.Get(context.Background(), "", "multipart")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !object.Checksum.IsZero() {
		t.Fatalf("Get() checksum = %+v, want no full-object checksum", object.Checksum)
	}
	got, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, body) {
		t.Fatalf("Get() = (%q, %v, %v), want multipart body", got, readErr, closeErr)
	}
}

func TestProviderReadCloserClassifiesAndRedactsStreamingFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		expected storage.Checksum
	}{
		{name: "without checksum"},
		{name: "with checksum", expected: storage.NewSHA256Checksum([]byte("body"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := newProviderReadCloser(&faultReadCloser{readErr: errors.New("signed-provider-secret")}, test.expected, nil)
			_, err := io.ReadAll(reader)
			if !storage.IsCode(err, storage.ErrorUnavailable) || !storage.IsRetryable(err) {
				t.Fatalf("ReadAll() error = %v, want retryable unavailable", err)
			}
			if strings.Contains(err.Error(), "signed-provider-secret") {
				t.Fatalf("ReadAll() exposed provider stream error: %v", err)
			}
		})
	}
}

func TestProviderReadCloserClassifiesAndRedactsCloseFailure(t *testing.T) {
	reader := newProviderReadCloser(&faultReadCloser{closeErr: errors.New("signed-provider-secret")}, storage.Checksum{}, nil)
	err := reader.Close()
	if !storage.IsCode(err, storage.ErrorUnavailable) || !storage.IsRetryable(err) {
		t.Fatalf("Close() error = %v, want retryable unavailable", err)
	}
	if strings.Contains(err.Error(), "signed-provider-secret") {
		t.Fatalf("Close() exposed provider stream error: %v", err)
	}
}

func TestClassifyStreamErrorNormalizesTypedNilOperationError(t *testing.T) {
	var typedNil *storage.OperationError
	var err error = typedNil
	classified := classifyStreamError("read object", err, "BodyRead", nil)
	if !storage.IsCode(classified, storage.ErrorUnavailable) || !storage.IsRetryable(classified) {
		t.Fatalf("classifyStreamError() = %v, want retryable unavailable", classified)
	}
}

func TestClassifyProviderErrorDropsMaliciousProviderCode(t *testing.T) {
	err := classifyProviderError("get object", &smithy.GenericAPIError{
		Code:    "AccessDenied\nprovider-secret",
		Message: "provider-secret",
	}, nil)
	var operationErr *storage.OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("classifyProviderError() = %T, want OperationError", err)
	}
	if operationErr.ProviderCode != "" || strings.Contains(err.Error(), "provider-secret") {
		t.Fatalf("classifyProviderError() exposed provider data: %+v", operationErr)
	}
}

func TestProviderErrorRedactsTokenShapedCredentialCode(t *testing.T) {
	const credential = "token-shaped-secret"
	store := &Store{redactor: newSecretRedactor(credential)}
	err := store.providerError("get object", &smithy.GenericAPIError{
		Code:    credential,
		Message: credential,
	})
	var operationErr *storage.OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("providerError() = %T, want OperationError", err)
	}
	if operationErr.ProviderCode != "" || strings.Contains(err.Error(), credential) {
		t.Fatalf("providerError() exposed credential-shaped code: %+v", operationErr)
	}
}

func TestVerifyRetriesProviderThrottling(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `<Error><Code>SlowDown</Code><Message>retry</Message></Error>`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint:         server.URL,
		Region:           "us-east-1",
		Bucket:           "bucket",
		AccessKeyID:      "access",
		SecretAccessKey:  "secret",
		ForcePathStyle:   true,
		HTTPClient:       server.Client(),
		RetryMaxAttempts: 2,
		RetryMaxBackoff:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := store.Verify(context.Background(), ""); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("provider requests = %d, want 2", got)
	}
}

func TestGetClassifiesNotFoundWithoutProviderBody(t *testing.T) {
	const secretBody = "provider-secret-body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>`+secretBody+`</Message></Error>`)
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint:        server.URL,
		Region:          "us-east-1",
		Bucket:          "bucket",
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		ForcePathStyle:  true,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = store.Get(context.Background(), "", "missing")
	if !storage.IsCode(err, storage.ErrorNotFound) {
		t.Fatalf("Get() error = %v, want not_found", err)
	}
	if strings.Contains(err.Error(), secretBody) {
		t.Fatalf("Get() exposed provider response body: %v", err)
	}
}

func TestBlobPlaneCapabilityRecordReflectsSafeCopyFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint:          server.URL,
		Region:            "us-east-1",
		Bucket:            "bucket",
		AccessKeyID:       "access",
		SecretAccessKey:   "secret",
		ForcePathStyle:    true,
		HTTPClient:        server.Client(),
		DisableNativeCopy: true,
		MaxStagingBytes:   1024,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := store.BlobCapabilities()
	if !got.Copy || !got.SafeCopyFallback || got.NativeCopy || got.SafeCopyMaxBytes != 1024 {
		t.Fatalf("BlobCapabilities() = %+v", got)
	}
	if got.ContractVersion != storage.BlobPlaneV2 {
		t.Fatalf("ContractVersion = %q, want %q", got.ContractVersion, storage.BlobPlaneV2)
	}
}

func TestBlobPlaneCapabilityRecordReflectsDisabledRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint: server.URL, Region: "us-east-1", Bucket: "bucket",
		AccessKeyID: "access", SecretAccessKey: "secret", ForcePathStyle: true,
		HTTPClient: server.Client(), RetryMaxAttempts: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if store.BlobCapabilities().Retries {
		t.Fatal("BlobCapabilities().Retries = true with one SDK attempt")
	}
}

func TestCopyFallbackEnforcesDiscoverableStagingLimitBeforeDestinationWrite(t *testing.T) {
	var destinationWrites atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			w.Header().Set("Content-Length", "5")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "12345")
		case http.MethodPut:
			destinationWrites.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint: server.URL, Region: "us-east-1", Bucket: "bucket",
		AccessKeyID: "access", SecretAccessKey: "secret", ForcePathStyle: true,
		HTTPClient: server.Client(), DisableNativeCopy: true, MaxStagingBytes: 4,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = store.CopyIfAbsent(context.Background(), "", "source", "", "destination")
	if !storage.IsCode(err, storage.ErrorUnsupported) {
		t.Fatalf("CopyIfAbsent() error = %v, want unsupported", err)
	}
	if got := store.BlobCapabilities().SafeCopyMaxBytes; got != 4 {
		t.Fatalf("SafeCopyMaxBytes = %d, want 4", got)
	}
	if got := destinationWrites.Load(); got != 0 {
		t.Fatalf("destination received %d writes beyond safe-copy limit", got)
	}
}

func TestStagedBodyCanBeReplayedForProviderRetries(t *testing.T) {
	staged, err := stageBody(context.Background(), bytes.NewBufferString("retryable body"), storage.PutOptions{}, defaultMaxStagingBytes)
	if err != nil {
		t.Fatalf("stageBody() error = %v", err)
	}
	defer staged.Close()

	first, err := io.ReadAll(staged.Reader)
	if err != nil {
		t.Fatalf("first ReadAll() error = %v", err)
	}
	if _, err := staged.Reader.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}
	second, err := io.ReadAll(staged.Reader)
	if err != nil {
		t.Fatalf("second ReadAll() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("replayed body = %q, want %q", second, first)
	}
}

func TestStageBodyBoundsDeclaredAndUnknownBodies(t *testing.T) {
	t.Run("declared size", func(t *testing.T) {
		body := strings.NewReader("0123456789")
		expected := int64(4)
		_, err := stageBody(context.Background(), body, storage.PutOptions{ExpectedSize: &expected}, 8)
		if !storage.IsCode(err, storage.ErrorInvalidArgument) {
			t.Fatalf("stageBody() error = %v, want invalid_argument", err)
		}
		if consumed := int64(10 - body.Len()); consumed != expected+1 {
			t.Fatalf("stageBody() consumed %d bytes, want %d", consumed, expected+1)
		}
	})

	t.Run("unknown size quota", func(t *testing.T) {
		body := strings.NewReader("0123456789")
		_, err := stageBody(context.Background(), body, storage.PutOptions{}, 4)
		if !storage.IsCode(err, storage.ErrorInvalidArgument) {
			t.Fatalf("stageBody() error = %v, want invalid_argument", err)
		}
		if consumed := int64(10 - body.Len()); consumed != 5 {
			t.Fatalf("stageBody() consumed %d bytes, want 5", consumed)
		}
	})

	t.Run("negative size before read", func(t *testing.T) {
		expected := int64(-1)
		_, err := stageBody(context.Background(), panicReader{}, storage.PutOptions{ExpectedSize: &expected}, 4)
		if !storage.IsCode(err, storage.ErrorInvalidArgument) {
			t.Fatalf("stageBody() error = %v, want invalid_argument", err)
		}
	})

	t.Run("canceled context before read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := stageBody(ctx, panicReader{}, storage.PutOptions{}, 4)
		if !storage.IsCode(err, storage.ErrorUnavailable) || storage.IsRetryable(err) {
			t.Fatalf("stageBody() error = %v, want non-retryable unavailable", err)
		}
	})
}

func TestStageBodyPreservesClassifiedSourceFailures(t *testing.T) {
	t.Run("checksum mismatch", func(t *testing.T) {
		reader := newChecksumReadCloser(
			io.NopCloser(strings.NewReader("corrupt")),
			storage.NewSHA256Checksum([]byte("expected")),
		)
		defer reader.Close()
		_, err := stageBody(context.Background(), reader, storage.PutOptions{}, defaultMaxStagingBytes)
		if !storage.IsCode(err, storage.ErrorChecksumMismatch) || storage.IsRetryable(err) {
			t.Fatalf("stageBody() error = %v, want non-retryable checksum_mismatch", err)
		}
	})

	t.Run("retryable stream failure", func(t *testing.T) {
		reader := newProviderReadCloser(&faultReadCloser{readErr: errors.New("provider stream")}, storage.Checksum{}, nil)
		defer reader.Close()
		_, err := stageBody(context.Background(), reader, storage.PutOptions{}, defaultMaxStagingBytes)
		if !storage.IsCode(err, storage.ErrorUnavailable) || !storage.IsRetryable(err) {
			t.Fatalf("stageBody() error = %v, want retryable unavailable", err)
		}
	})
}

func TestCompleteMultipartDoesNotDeleteAfterTransientVerificationFailure(t *testing.T) {
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			writeCompleteMultipartResponse(w, `"completed-etag"`)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `<Error><Code>InternalError</Code><Message>transient</Message></Error>`)
		case http.MethodDelete:
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store := newTestStore(t, server, 1)
	body := []byte("expected")
	size := int64(len(body))
	upload := storage.MultipartUpload{
		Bucket: "bucket", Key: "key", ID: "upload-id",
		Options: storage.MultipartOptions{ExpectedSize: &size, Checksum: storage.NewSHA256Checksum(body)},
	}
	_, err := store.CompleteMultipart(context.Background(), upload, []storage.UploadedPart{{
		Number: 1, ETag: "part-etag", Checksum: storage.NewSHA256Checksum(body),
	}})
	if !storage.IsCode(err, storage.ErrorUnavailable) || !storage.IsRetryable(err) {
		t.Fatalf("CompleteMultipart() error = %v, want retryable unavailable", err)
	}
	if got := deletes.Load(); got != 0 {
		t.Fatalf("verification failure issued %d deletes, want 0", got)
	}
}

func TestCompleteMultipartConditionsMismatchCleanupOnCompletedETag(t *testing.T) {
	var deleteIfMatch string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			writeCompleteMultipartResponse(w, `"completed-etag"`)
		case http.MethodGet:
			body := []byte("corrupt!")
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Header().Set("ETag", `"completed-etag"`)
			w.Header().Set("X-Amz-Meta-"+checksumMetadataKey, storage.NewSHA256Checksum([]byte("expected")).Value)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		case http.MethodDelete:
			deleteIfMatch = request.Header.Get("If-Match")
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code><Message>replacement exists</Message></Error>`)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store := newTestStore(t, server, 1)
	body := []byte("expected")
	size := int64(len(body))
	upload := storage.MultipartUpload{
		Bucket: "bucket", Key: "key", ID: "upload-id",
		Options: storage.MultipartOptions{ExpectedSize: &size, Checksum: storage.NewSHA256Checksum(body)},
	}
	_, err := store.CompleteMultipart(context.Background(), upload, []storage.UploadedPart{{
		Number: 1, ETag: "part-etag", Checksum: storage.NewSHA256Checksum(body),
	}})
	if !storage.IsCode(err, storage.ErrorChecksumMismatch) {
		t.Fatalf("CompleteMultipart() error = %v, want checksum_mismatch", err)
	}
	if strings.Trim(deleteIfMatch, `"`) != "completed-etag" {
		t.Fatalf("cleanup If-Match = %q, want completed ETag", deleteIfMatch)
	}
}

func newTestStore(t *testing.T, server *httptest.Server, retryAttempts int) *Store {
	t.Helper()
	store, err := New(context.Background(), Config{
		Endpoint: server.URL, Region: "us-east-1", Bucket: "bucket",
		AccessKeyID: "access", SecretAccessKey: "secret", ForcePathStyle: true,
		HTTPClient: server.Client(), RetryMaxAttempts: retryAttempts,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return store
}

func writeCompleteMultipartResponse(w http.ResponseWriter, etag string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><Location>http://example.invalid/bucket/key</Location><Bucket>bucket</Bucket><Key>key</Key><ETag>`+etag+`</ETag></CompleteMultipartUploadResult>`)
}

type faultReadCloser struct {
	readErr  error
	closeErr error
}

func (r *faultReadCloser) Read([]byte) (int, error) { return 0, r.readErr }
func (r *faultReadCloser) Close() error             { return r.closeErr }

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("reader must not be consumed") }
