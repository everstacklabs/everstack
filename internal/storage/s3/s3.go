package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/everstacklabs/everstack/internal/storage"
	storageegress "github.com/everstacklabs/everstack/internal/storage/egress"
)

// WireChecksumMode selects the provider-supported transport checksum. The
// Blob Plane contract remains SHA-256 regardless of this wire encoding.
type WireChecksumMode string

const (
	WireChecksumSHA256     WireChecksumMode = "sha256"
	WireChecksumContentMD5 WireChecksumMode = "content-md5"

	defaultMaxStagingBytes int64 = 512 << 20
	maximumStagingBytes    int64 = 5 << 30
)

// Config holds the configuration for an S3-compatible storage backend.
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	PathPrefix      string
	ForcePathStyle  bool // true for MinIO
	HTTPClient      *http.Client
	DebugHTTP       bool
	// EnforceManagedEgress applies the managed-cloud endpoint and dial policy.
	// It rejects caller-injected clients so the policy cannot be bypassed.
	EnforceManagedEgress bool
	// RetryMaxAttempts and RetryMaxBackoff tune the SDK's standard retryer.
	// Zero retains the SDK default. They exist primarily for bounded service
	// policy and deterministic failure-injection tests.
	RetryMaxAttempts int
	RetryMaxBackoff  time.Duration
	// DisableNativeCopy uses the direct-read plus conditional-create fallback.
	// It is useful for S3-compatible providers that reject CopyObject.
	DisableNativeCopy bool
	// WireChecksum defaults to SHA-256. R2's S3 API currently supports
	// Content-MD5 for PutObject and UploadPart, while Everstack retains and
	// verifies SHA-256 in object metadata.
	WireChecksum WireChecksumMode
	// MaxStagingBytes bounds local temporary disk use for each replayable V2
	// write. Zero defaults to 512 MiB. Values above the S3 5 GiB single-request
	// limit are rejected; larger objects must use multipart uploads.
	MaxStagingBytes int64
}

// Store implements storage.ObjectStore using AWS SDK v2.
// Compatible with S3, R2, MinIO, and any S3-compatible backend.
type Store struct {
	client            *s3.Client
	presigner         *s3.PresignClient
	bucket            string
	pathPrefix        string
	redactor          *secretRedactor
	disableNativeCopy bool
	wireChecksum      WireChecksumMode
	maxStagingBytes   int64
	retriesEnabled    bool
}

// debugTransport logs only redacted request metadata. It is installed solely
// when Config.DebugHTTP is explicitly enabled.
type debugTransport struct {
	wrapped  http.RoundTripper
	redactor *secretRedactor
}

func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	endpoint := d.redactor.Redact(safeURL(req.URL))
	slog.Info("s3 HTTP request", "method", req.Method, "endpoint", endpoint)
	resp, err := d.wrapped.RoundTrip(req)
	if resp != nil {
		slog.Info("s3 HTTP response", "status", resp.StatusCode, "endpoint", endpoint)
	}
	if err != nil {
		slog.Error("s3 HTTP error", "endpoint", endpoint, "error", d.redactor.Redact(err.Error()))
	}
	return resp, err
}

type secretRedactor struct {
	secrets []string
}

var (
	urlPattern            = regexp.MustCompile(`https?://[^\s"']+`)
	authorizationPattern  = regexp.MustCompile(`(?i)\s*(authorization|proxy-authorization|x-amz-security-token)\s*:\s*[^\r\n]+`)
	sensitiveQueryPattern = regexp.MustCompile(`(?i)\s*(x-amz-signature|x-amz-credential|x-amz-security-token|signature|token)\s*=\s*[^&\s]+`)
)

func newSecretRedactor(values ...string) *secretRedactor {
	redactor := &secretRedactor{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			redactor.secrets = append(redactor.secrets, value)
		}
	}
	return redactor
}

func (r *secretRedactor) Redact(value string) string {
	value = urlPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[REDACTED_URL]"
		}
		return safeURL(parsed)
	})
	value = authorizationPattern.ReplaceAllString(value, " [REDACTED]")
	value = sensitiveQueryPattern.ReplaceAllString(value, " [REDACTED]")
	if r != nil {
		for _, secret := range r.secrets {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

func safeURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	clone := *value
	clone.User = nil
	clone.RawQuery = ""
	clone.ForceQuery = false
	clone.Fragment = ""
	return clone.String()
}

// normalizeBaseEndpoint strips a duplicated trailing bucket segment from the endpoint
// (e.g. https://host/bucket + bucket=bucket), regardless of addressing style.
func normalizeBaseEndpoint(endpoint, bucket string) string {
	if endpoint == "" || bucket == "" {
		return endpoint
	}

	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return endpoint
	}

	trimmedPath := strings.Trim(u.Path, "/")
	if trimmedPath == "" {
		return endpoint
	}

	parts := strings.Split(trimmedPath, "/")
	if parts[len(parts)-1] != bucket {
		return endpoint
	}

	parts = parts[:len(parts)-1]
	if len(parts) == 0 {
		u.Path = ""
	} else {
		u.Path = "/" + strings.Join(parts, "/")
	}
	u.RawPath = ""

	return u.String()
}

// New creates a new S3-compatible object store.
func New(ctx context.Context, cfg Config) (*Store, error) {
	redactor := newSecretRedactor(cfg.AccessKeyID, cfg.SecretAccessKey)
	region := cfg.Region
	if region == "" {
		region = "auto" // R2 uses "auto"
	}
	endpoint := normalizeBaseEndpoint(cfg.Endpoint, cfg.Bucket)
	wireChecksum := cfg.WireChecksum
	if wireChecksum == "" {
		wireChecksum = WireChecksumSHA256
	}
	if wireChecksum != WireChecksumSHA256 && wireChecksum != WireChecksumContentMD5 {
		return nil, errors.New("unsupported S3 wire checksum mode")
	}
	maxStagingBytes := cfg.MaxStagingBytes
	if maxStagingBytes == 0 {
		maxStagingBytes = defaultMaxStagingBytes
	}
	if maxStagingBytes < 0 || maxStagingBytes > maximumStagingBytes {
		return nil, errors.New("S3 staging limit must be between 1 byte and 5 GiB")
	}
	if cfg.RetryMaxAttempts < 0 {
		return nil, errors.New("S3 retry attempts cannot be negative")
	}
	if endpoint != cfg.Endpoint {
		slog.Warn("s3.New: normalized base endpoint that included the bucket for path-style requests",
			"bucket", cfg.Bucket,
			"endpoint", redactor.Redact(safeURLString(cfg.Endpoint)),
			"normalizedEndpoint", redactor.Redact(safeURLString(endpoint)),
		)
	}

	var httpClient *http.Client
	if cfg.EnforceManagedEgress {
		if cfg.HTTPClient != nil {
			return nil, errors.New("managed S3 egress does not accept a custom HTTP client")
		}
		managedClient, managedErr := storageegress.NewManagedClient(endpoint, storageegress.ConfigFromEnvironment())
		if managedErr != nil {
			return nil, fmt.Errorf("configure managed S3 egress: %w", managedErr)
		}
		httpClient = managedClient
	} else {
		httpClient = cfg.HTTPClient
		if httpClient == nil {
			httpClient = &http.Client{Transport: http.DefaultTransport}
		} else {
			copyClient := *httpClient
			httpClient = &copyClient
			if httpClient.Transport == nil {
				httpClient.Transport = http.DefaultTransport
			}
		}
	}
	if cfg.DebugHTTP {
		httpClient.Transport = &debugTransport{wrapped: httpClient.Transport, redactor: redactor}
	}

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "",
		)),
		config.WithHTTPClient(httpClient),
		config.WithRetryer(func() aws.Retryer {
			return awsretry.NewStandard(func(options *awsretry.StandardOptions) {
				if cfg.RetryMaxAttempts > 0 {
					options.MaxAttempts = cfg.RetryMaxAttempts
				}
				if cfg.RetryMaxBackoff > 0 {
					options.MaxBackoff = cfg.RetryMaxBackoff
				}
			})
		}),
	)
	if err != nil {
		return nil, errors.New("failed to load AWS config: " + redactor.Redact(err.Error()))
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		if cfg.ForcePathStyle {
			o.UsePathStyle = true
		}
	})

	return &Store{
		client:            client,
		presigner:         s3.NewPresignClient(client),
		bucket:            cfg.Bucket,
		pathPrefix:        cfg.PathPrefix,
		redactor:          redactor,
		disableNativeCopy: cfg.DisableNativeCopy,
		wireChecksum:      wireChecksum,
		maxStagingBytes:   maxStagingBytes,
		retriesEnabled:    cfg.RetryMaxAttempts != 1,
	}, nil
}

func safeURLString(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[INVALID_URL]"
	}
	return safeURL(parsed)
}

func (s *Store) providerError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return classifyProviderError(operation, err, s.redactor)
}

// Verify proves that the configured credentials can access the target bucket
// without writing an object.
func (s *Store) Verify(ctx context.Context, bucket string) error {
	b := bucket
	if b == "" {
		b = s.bucket
	}
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(b)})
	return s.providerError("storage connection verification failed", err)
}

func (s *Store) fullKey(key string) string {
	if s.pathPrefix != "" {
		return s.pathPrefix + "/" + key
	}
	return key
}

func (s *Store) PutPresignedURL(ctx context.Context, bucket, key, contentType string, sizeBytes int64, expiry time.Duration) (string, map[string]string, error) {
	b := bucket
	if b == "" {
		b = s.bucket
	}

	input := &s3.PutObjectInput{
		Bucket:      aws.String(b),
		Key:         aws.String(s.fullKey(key)),
		ContentType: aws.String(contentType),
	}
	if sizeBytes > 0 {
		input.ContentLength = aws.Int64(sizeBytes)
	}

	result, err := s.presigner.PresignPutObject(ctx, input, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", nil, s.providerError("failed to create presigned upload URL", err)
	}

	headers := make(map[string]string)
	for k, v := range result.SignedHeader {
		if len(v) > 0 && !isSensitiveResponseHeader(k) {
			headers[k] = s.redactor.Redact(v[0])
		}
	}

	return result.URL, headers, nil
}

func isSensitiveResponseHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "x-amz-security-token":
		return true
	default:
		return false
	}
}

func (s *Store) GetPresignedURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	b := bucket
	if b == "" {
		b = s.bucket
	}

	result, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b),
		Key:    aws.String(s.fullKey(key)),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", s.providerError("failed to create presigned download URL", err)
	}

	return result.URL, nil
}

func (s *Store) Put(ctx context.Context, bucket, key, contentType string, body io.Reader) (string, error) {
	b := bucket
	if b == "" {
		b = s.bucket
	}

	result, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b),
		Key:         aws.String(s.fullKey(key)),
		ContentType: aws.String(contentType),
		Body:        body,
	})
	if err != nil {
		return "", s.providerError("failed to put object", err)
	}

	etag := ""
	if result.ETag != nil {
		etag = strings.Trim(*result.ETag, "\"")
	}
	return etag, nil
}

func (s *Store) Delete(ctx context.Context, bucket, key string) error {
	b := bucket
	if b == "" {
		b = s.bucket
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil {
		return s.providerError("failed to delete object", err)
	}
	return nil
}

func (s *Store) Head(ctx context.Context, bucket, key string) (int64, string, error) {
	b := bucket
	if b == "" {
		b = s.bucket
	}

	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil {
		return 0, "", s.providerError("failed to head object", err)
	}

	ct := ""
	if result.ContentType != nil {
		ct = *result.ContentType
	}

	var size int64
	if result.ContentLength != nil {
		size = *result.ContentLength
	}

	return size, ct, nil
}

func (s *Store) List(ctx context.Context, bucket, prefix string) ([]storage.BucketObject, error) {
	b := bucket
	if b == "" {
		b = s.bucket
	}

	fullPrefix := s.fullKey(prefix)

	slog.Info("s3.List: calling ListObjectsV2",
		"bucket", s.redactor.Redact(b),
		"prefix", s.redactor.Redact(fullPrefix),
		"storeBucket", s.redactor.Redact(s.bucket),
		"pathPrefix", s.redactor.Redact(s.pathPrefix),
	)

	// First try HeadBucket to verify access
	_, headErr := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(b),
	})
	if headErr != nil {
		slog.Error("s3.List: HeadBucket failed", "bucket", s.redactor.Redact(b), "error", s.redactor.Redact(headErr.Error()))
	} else {
		slog.Info("s3.List: HeadBucket succeeded", "bucket", s.redactor.Redact(b))
	}

	var objects []storage.BucketObject
	var continuationToken *string

	for {
		input := &s3.ListObjectsV2Input{
			Bucket: aws.String(b),
		}
		if fullPrefix != "" {
			input.Prefix = aws.String(fullPrefix)
		}
		if continuationToken != nil {
			input.ContinuationToken = continuationToken
		}

		result, err := s.client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, s.providerError("failed to list objects", err)
		}

		for _, obj := range result.Contents {
			key := ""
			if obj.Key != nil {
				key = *obj.Key
				// Strip the pathPrefix back out so keys are consistent
				if s.pathPrefix != "" && strings.HasPrefix(key, s.pathPrefix+"/") {
					key = key[len(s.pathPrefix)+1:]
				}
			}
			var size int64
			if obj.Size != nil {
				size = *obj.Size
			}
			var lastMod time.Time
			if obj.LastModified != nil {
				lastMod = *obj.LastModified
			}
			objects = append(objects, storage.BucketObject{
				Key:          key,
				SizeBytes:    size,
				LastModified: lastMod,
			})
		}

		if result.IsTruncated == nil || !*result.IsTruncated {
			break
		}
		continuationToken = result.NextContinuationToken
	}

	return objects, nil
}

// Verify interface compliance at compile time.
var _ storage.ObjectStore = (*Store)(nil)
