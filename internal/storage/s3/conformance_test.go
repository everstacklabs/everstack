package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storage/storagetest"
)

const (
	conformanceEnabledEnv      = "EVERSTACK_STORAGE_CONFORMANCE_ENABLED"
	conformanceProfileEnv      = "EVERSTACK_STORAGE_CONFORMANCE_PROFILE"
	conformanceEndpointEnv     = "EVERSTACK_STORAGE_CONFORMANCE_ENDPOINT"
	conformanceRegionEnv       = "EVERSTACK_STORAGE_CONFORMANCE_REGION"
	conformanceBucketEnv       = "EVERSTACK_STORAGE_CONFORMANCE_BUCKET"
	conformanceAccessKeyEnv    = "EVERSTACK_STORAGE_CONFORMANCE_ACCESS_KEY_ID"
	conformanceSecretKeyEnv    = "EVERSTACK_STORAGE_CONFORMANCE_SECRET_ACCESS_KEY"
	conformancePathStyleEnv    = "EVERSTACK_STORAGE_CONFORMANCE_FORCE_PATH_STYLE"
	conformanceCreateBucketEnv = "EVERSTACK_STORAGE_CONFORMANCE_CREATE_BUCKET"
)

func TestBlobPlaneV2ProviderConformance(t *testing.T) {
	if os.Getenv(conformanceEnabledEnv) != "1" {
		t.Skip("set " + conformanceEnabledEnv + "=1 to run provider conformance")
	}
	profile := strings.ToLower(strings.TrimSpace(requiredConformanceEnv(t, conformanceProfileEnv)))
	if profile != "minio" && profile != "r2" && profile != "s3" {
		t.Fatalf("%s must be minio, r2, or s3", conformanceProfileEnv)
	}

	base := conformanceConfig(t, nil, false, 3)
	store, err := New(context.Background(), base)
	if err != nil {
		t.Fatalf("create %s conformance store: %v", profile, err)
	}
	if envBool(t, conformanceCreateBucketEnv, false) {
		if _, err := store.client.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String(base.Bucket)}); err != nil {
			t.Fatalf("create conformance bucket: %s", safeOperationErrorDetails(store.providerError("create conformance bucket", err)))
		}
		t.Cleanup(func() {
			if _, err := store.client.DeleteBucket(context.Background(), &awss3.DeleteBucketInput{Bucket: aws.String(base.Bucket)}); err != nil {
				t.Errorf("delete conformance bucket: %v", store.providerError("delete conformance bucket", err))
			}
		})
	}
	if err := store.Verify(context.Background(), base.Bucket); err != nil {
		t.Fatalf("verify %s conformance store: %v", profile, err)
	}

	fallbackConfig := conformanceConfig(t, nil, true, 3)
	fallbackStore, err := New(context.Background(), fallbackConfig)
	if err != nil {
		t.Fatalf("create %s fallback-copy store: %v", profile, err)
	}

	storagetest.Run(t, storagetest.Config{
		Profile:           profile,
		Bucket:            base.Bucket,
		Store:             store,
		FallbackCopyStore: fallbackStore,
		HTTPClient:        &http.Client{Timeout: 30 * time.Second},
		RetryProbe: func(ctx context.Context) error {
			transport := &replayWriteTransport{wrapped: http.DefaultTransport}
			client := &http.Client{Transport: transport}
			retrying, err := New(ctx, conformanceConfig(t, client, false, 2))
			if err != nil {
				return err
			}
			body := []byte("replayable conditional write")
			size := int64(len(body))
			checksum := storage.NewSHA256Checksum(body)
			key := "everstack-conformance/retry-write-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			if _, err := retrying.PutIfAbsent(ctx, base.Bucket, key, bytes.NewReader(body), storage.PutOptions{
				ExpectedSize: &size,
				Checksum:     checksum,
			}); err != nil {
				return err
			}
			defer func() { _ = retrying.Delete(context.Background(), base.Bucket, key) }()
			attempts := transport.snapshot()
			if len(attempts) != 2 {
				return fmt.Errorf("provider write attempts = %d, want 2", len(attempts))
			}
			if attempts[0].body != string(body) || attempts[1].body != string(body) {
				return errors.New("provider retry did not replay the complete body")
			}
			if attempts[0] != attempts[1] {
				return errors.New("provider retry changed conditional or checksum headers")
			}
			if attempts[0].ifNoneMatch != "*" || attempts[0].metadataSHA256 != checksum.Value {
				return errors.New("provider retry omitted conditional or Everstack checksum headers")
			}
			if profile == "r2" {
				if attempts[0].contentMD5 == "" {
					return errors.New("R2 retry omitted Content-MD5")
				}
			} else if attempts[0].checksumSHA256 == "" {
				return errors.New("S3 retry omitted SHA-256 wire checksum")
			}
			return nil
		},
		ThrottleProbe: func(ctx context.Context) error {
			client := &http.Client{Transport: &throttleTransport{
				wrapped: http.DefaultTransport,
				respond: func(*http.Request) bool { return true },
			}}
			throttled, err := New(ctx, conformanceConfig(t, client, false, 1))
			if err != nil {
				return err
			}
			return throttled.Verify(ctx, base.Bucket)
		},
		AuthProbe: func(ctx context.Context) error {
			invalidConfig := conformanceConfig(t, nil, false, 1)
			// Preserve the provider's credential shape and known access-key ID so
			// the request reaches signature authentication. R2 rejects malformed
			// placeholder credentials as a generic request error instead.
			invalidConfig.SecretAccessKey = invalidCredential(invalidConfig.SecretAccessKey)
			unauthorized, err := New(ctx, invalidConfig)
			if err != nil {
				return err
			}
			return unauthorized.Verify(ctx, base.Bucket)
		},
		CorruptionProbe: func(ctx context.Context) error {
			transport := &corruptingGetTransport{wrapped: http.DefaultTransport}
			client := &http.Client{Transport: transport}
			corrupting, err := New(ctx, conformanceConfig(t, client, false, 1))
			if err != nil {
				return err
			}
			body := []byte("uncorrupted provider object")
			key := "everstack-conformance/corruption-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			if _, err := corrupting.PutIfAbsent(ctx, base.Bucket, key, bytes.NewReader(body), storage.PutOptions{
				Checksum: storage.NewSHA256Checksum(body),
			}); err != nil {
				return err
			}
			defer func() { _ = store.Delete(context.Background(), base.Bucket, key) }()
			object, err := corrupting.Get(ctx, base.Bucket, key)
			if err != nil {
				return err
			}
			_, readErr := io.ReadAll(object.Body)
			closeErr := object.Body.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			return errors.New("corrupted provider response passed checksum verification")
		},
	})
}

func invalidCredential(value string) string {
	if value == "" {
		return "x"
	}
	replacement := byte('A')
	if value[0] == replacement {
		replacement = 'B'
	}
	return string(replacement) + value[1:]
}

func TestInvalidCredentialPreservesShape(t *testing.T) {
	for _, original := range []string{"secret", "AnotherSecret", "0123456789abcdef"} {
		invalid := invalidCredential(original)
		if invalid == original {
			t.Fatalf("invalidCredential(%q) did not change the credential", original)
		}
		if len(invalid) != len(original) {
			t.Fatalf("invalidCredential(%q) length = %d, want %d", original, len(invalid), len(original))
		}
	}
}

func TestAuthenticationProbeUsesKnownAccessKeyAndClassifiesInvalidSignature(t *testing.T) {
	const accessKey = "known-access-key"
	var attempts atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts.Add(1)
		if authorization := request.Header.Get("Authorization"); !strings.Contains(authorization, "Credential="+accessKey+"/") {
			t.Fatalf("Authorization did not preserve the known access key")
		}
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": []string{"application/xml"}},
			Body: io.NopCloser(strings.NewReader(
				`<Error><Code>SignatureDoesNotMatch</Code><Message>signature mismatch</Message></Error>`,
			)),
			Request: request,
		}, nil
	})
	store, err := New(context.Background(), Config{
		Endpoint: "https://s3.invalid", Region: "auto", Bucket: "bucket",
		AccessKeyID: accessKey, SecretAccessKey: invalidCredential("valid-shaped-secret"), ForcePathStyle: true,
		HTTPClient: &http.Client{Transport: transport}, RetryMaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Verify(context.Background(), "bucket")
	if !storage.IsCode(err, storage.ErrorUnauthorized) || storage.IsRetryable(err) {
		t.Fatalf("Verify() error = %v, want non-retryable unauthorized", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("authentication attempts = %d, want 1", got)
	}
}

func safeOperationErrorDetails(err error) string {
	var operationErr *storage.OperationError
	if errors.As(err, &operationErr) {
		return fmt.Sprintf("code=%s retryable=%t provider_code=%s status=%d", operationErr.Code, operationErr.Retryable, operationErr.ProviderCode, operationErr.HTTPStatus)
	}
	return "unclassified storage error"
}

func conformanceConfig(t *testing.T, client *http.Client, disableNativeCopy bool, retryAttempts int) Config {
	t.Helper()
	profile := strings.ToLower(strings.TrimSpace(requiredConformanceEnv(t, conformanceProfileEnv)))
	forcePathStyleDefault := profile == "minio"
	wireChecksum := WireChecksumSHA256
	if profile == "r2" {
		wireChecksum = WireChecksumContentMD5
	}
	return Config{
		Endpoint:          strings.TrimSpace(os.Getenv(conformanceEndpointEnv)),
		Region:            defaultString(os.Getenv(conformanceRegionEnv), "us-east-1"),
		Bucket:            requiredConformanceEnv(t, conformanceBucketEnv),
		AccessKeyID:       requiredConformanceEnv(t, conformanceAccessKeyEnv),
		SecretAccessKey:   requiredConformanceEnv(t, conformanceSecretKeyEnv),
		ForcePathStyle:    envBool(t, conformancePathStyleEnv, forcePathStyleDefault),
		HTTPClient:        client,
		RetryMaxAttempts:  retryAttempts,
		RetryMaxBackoff:   25 * time.Millisecond,
		DisableNativeCopy: disableNativeCopy || profile == "minio" || profile == "r2",
		WireChecksum:      wireChecksum,
	}
}

func requiredConformanceEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required when provider conformance is enabled", name)
	}
	return value
}

func envBool(t *testing.T, name string, fallback bool) bool {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		t.Fatalf("%s must be a boolean", name)
	}
	return parsed
}

func defaultString(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

type throttleTransport struct {
	wrapped http.RoundTripper
	respond func(*http.Request) bool
}

type writeAttempt struct {
	body            string
	wireBody        string
	contentEncoding string
	ifNoneMatch     string
	metadataSHA256  string
	checksumSHA256  string
	contentMD5      string
	contentLength   int64
}

type replayWriteTransport struct {
	wrapped  http.RoundTripper
	attempts atomic.Int32
	mu       sync.Mutex
	records  []writeAttempt
}

func (t *replayWriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodPut || request.URL.Query().Has("uploadId") {
		return t.wrapped.RoundTrip(request)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	logicalBody, err := logicalRequestBody(request, body)
	if err != nil {
		return nil, err
	}
	record := writeAttempt{
		body:            string(logicalBody),
		wireBody:        string(body),
		contentEncoding: request.Header.Get("Content-Encoding"),
		ifNoneMatch:     request.Header.Get("If-None-Match"),
		metadataSHA256:  request.Header.Get("X-Amz-Meta-" + checksumMetadataKey),
		checksumSHA256:  request.Header.Get("X-Amz-Checksum-Sha256"),
		contentMD5:      request.Header.Get("Content-MD5"),
		contentLength:   request.ContentLength,
	}
	t.mu.Lock()
	t.records = append(t.records, record)
	t.mu.Unlock()

	if t.attempts.Add(1) == 1 {
		return throttledResponse(request), nil
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	return t.wrapped.RoundTrip(request)
}

func logicalRequestBody(request *http.Request, wireBody []byte) ([]byte, error) {
	if !strings.Contains(strings.ToLower(request.Header.Get("Content-Encoding")), "aws-chunked") {
		return wireBody, nil
	}
	decoded, err := io.ReadAll(httputil.NewChunkedReader(bytes.NewReader(wireBody)))
	if err != nil {
		return nil, fmt.Errorf("decode aws-chunked retry body: %w", err)
	}
	return decoded, nil
}

func TestReplayWriteTransportRecordsCompleteLogicalBody(t *testing.T) {
	upstream := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, request.Body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"ETag": []string{`"etag"`}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})

	transport := &replayWriteTransport{wrapped: upstream}
	store, err := New(context.Background(), Config{
		Endpoint: "https://s3.invalid", Region: "auto", Bucket: "bucket",
		AccessKeyID: "access", SecretAccessKey: "secret", ForcePathStyle: true,
		HTTPClient: &http.Client{Transport: transport}, RetryMaxAttempts: 2,
		RetryMaxBackoff: time.Millisecond, WireChecksum: WireChecksumContentMD5,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("replayable conditional write")
	size := int64(len(body))
	if _, err = store.PutIfAbsent(context.Background(), "", "key", bytes.NewReader(body), storage.PutOptions{
		ExpectedSize: &size, Checksum: storage.NewSHA256Checksum(body),
	}); err != nil {
		t.Fatal(err)
	}
	attempts := transport.snapshot()
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	for i, attempt := range attempts {
		if attempt.body != string(body) {
			t.Fatalf("attempt %d body = %q, want %q", i+1, attempt.body, body)
		}
		if !strings.Contains(strings.ToLower(attempt.contentEncoding), "aws-chunked") {
			t.Fatalf("attempt %d Content-Encoding = %q, want aws-chunked", i+1, attempt.contentEncoding)
		}
		if attempt.wireBody == attempt.body {
			t.Fatalf("attempt %d did not exercise encoded wire body", i+1)
		}
	}
}

func (t *replayWriteTransport) snapshot() []writeAttempt {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]writeAttempt(nil), t.records...)
}

type corruptingGetTransport struct {
	wrapped   http.RoundTripper
	corrupted atomic.Bool
}

func (t *corruptingGetTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.wrapped.RoundTrip(request)
	if err != nil || response == nil || request.Method != http.MethodGet || response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, err
	}
	if !t.corrupted.CompareAndSwap(false, true) {
		return response, nil
	}
	_ = response.Body.Close()
	body := []byte("corrupted provider response")
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	for _, name := range []string{
		"X-Amz-Checksum-Crc32", "X-Amz-Checksum-Crc32c", "X-Amz-Checksum-Crc64nvme",
		"X-Amz-Checksum-Sha1", "X-Amz-Checksum-Sha256", "X-Amz-Checksum-Type",
	} {
		response.Header.Del(name)
	}
	return response, nil
}

func throttledResponse(request *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Status:     http.StatusText(http.StatusServiceUnavailable),
		Header: http.Header{
			"Content-Type": []string{"application/xml"},
			"Retry-After":  []string{"0"},
		},
		Body:    io.NopCloser(strings.NewReader(`<Error><Code>SlowDown</Code><Message>injected</Message></Error>`)),
		Request: request,
	}
}

func (t *throttleTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.respond != nil && t.respond(request) {
		return throttledResponse(request), nil
	}
	return t.wrapped.RoundTrip(request)
}
