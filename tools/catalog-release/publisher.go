package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/everstacklabs/everstack/internal/catalogdistribution"
)

const (
	r2EndpointEnvironmentVariable        = "EVS_CATALOG_R2_ENDPOINT"
	r2BucketEnvironmentVariable          = "EVS_CATALOG_R2_BUCKET"
	r2AccessKeyIDEnvironmentVariable     = "EVS_CATALOG_R2_ACCESS_KEY_ID"
	r2SecretAccessKeyEnvironmentVariable = "EVS_CATALOG_R2_SECRET_ACCESS_KEY"

	distributionObjectPrefix = "v1"
	bundleCacheControl       = "public, max-age=31536000, immutable, stale-if-error=604800"
	channelCacheControl      = "public, max-age=30, must-revalidate, stale-if-error=86400"
	channelDocumentLimit     = 64 * 1024
)

type releaseArtifacts struct {
	Version     string
	ChannelName string
	BundlePath  string
	Bundle      []byte
	Channel     []byte
}

type r2Config struct {
	endpoint        string
	bucket          string
	accessKeyID     string
	secretAccessKey string
}

type existingChannel struct {
	document *catalogdistribution.Channel
	data     []byte
	etag     string
	exists   bool
}

type catalogObjectStore interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

func publishReleaseToR2(ctx context.Context, getenv func(string) string, artifacts releaseArtifacts) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	config, err := loadR2Config(getenv)
	if err != nil {
		return err
	}
	store, err := newR2ObjectStore(ctx, config)
	if err != nil {
		return err
	}
	return publishRelease(ctx, store, config.bucket, artifacts)
}

func loadR2Config(getenv func(string) string) (r2Config, error) {
	config := r2Config{
		endpoint:        strings.TrimSpace(getenv(r2EndpointEnvironmentVariable)),
		bucket:          strings.TrimSpace(getenv(r2BucketEnvironmentVariable)),
		accessKeyID:     strings.TrimSpace(getenv(r2AccessKeyIDEnvironmentVariable)),
		secretAccessKey: strings.TrimSpace(getenv(r2SecretAccessKeyEnvironmentVariable)),
	}
	requiredValues := []struct {
		name  string
		value string
	}{
		{name: r2EndpointEnvironmentVariable, value: config.endpoint},
		{name: r2BucketEnvironmentVariable, value: config.bucket},
		{name: r2AccessKeyIDEnvironmentVariable, value: config.accessKeyID},
		{name: r2SecretAccessKeyEnvironmentVariable, value: config.secretAccessKey},
	}
	for _, required := range requiredValues {
		if required.value == "" {
			return r2Config{}, fmt.Errorf("%s is required for catalog publication", required.name)
		}
	}

	endpoint, err := url.Parse(config.endpoint)
	if err != nil {
		return r2Config{}, fmt.Errorf("parse %s: %w", r2EndpointEnvironmentVariable, err)
	}
	if endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return r2Config{}, fmt.Errorf("%s must be an HTTPS origin", r2EndpointEnvironmentVariable)
	}
	config.endpoint = strings.TrimRight(config.endpoint, "/")
	return config, nil
}

func newR2ObjectStore(ctx context.Context, config r2Config) (catalogObjectStore, error) {
	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			config.accessKeyID,
			config.secretAccessKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("configure R2 client: %w", err)
	}
	return s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(config.endpoint)
		options.UsePathStyle = true
	}), nil
}

func publishRelease(ctx context.Context, store catalogObjectStore, bucket string, artifacts releaseArtifacts) error {
	if err := validateReleaseArtifacts(artifacts); err != nil {
		return err
	}

	channelKey := path.Join(distributionObjectPrefix, "channels", artifacts.ChannelName+".json")
	currentChannel, err := readExistingChannel(ctx, store, bucket, channelKey)
	if err != nil {
		return fmt.Errorf("inspect current catalog channel: %w", err)
	}
	promoteChannel := true
	if currentChannel.document != nil && currentChannel.document.Channel == artifacts.ChannelName {
		comparison, err := catalogdistribution.CompareVersions(currentChannel.document.Version, artifacts.Version)
		// A malformed current version is repaired through the ETag-guarded write
		// below. The candidate has already passed strict semantic validation.
		if err == nil && comparison < 0 {
			return fmt.Errorf(
				"refusing to promote catalog version %q over newer channel version %q",
				artifacts.Version,
				currentChannel.document.Version,
			)
		}
		if err == nil && comparison == 0 {
			var candidate catalogdistribution.Channel
			if err := json.Unmarshal(artifacts.Channel, &candidate); err != nil {
				return fmt.Errorf("decode candidate catalog channel: %w", err)
			}
			if currentChannel.document.BundlePath != candidate.BundlePath ||
				!strings.EqualFold(currentChannel.document.BundleSHA256, candidate.BundleSHA256) ||
				currentChannel.document.BundleSize != candidate.BundleSize {
				return fmt.Errorf("catalog channel version %q already describes different content", artifacts.Version)
			}
			// Exact retries need no channel write. A different signed document for
			// the same immutable content is safe to repair with compare-and-swap.
			promoteChannel = !bytes.Equal(currentChannel.data, artifacts.Channel)
		}
	}

	bundleKey := path.Join(distributionObjectPrefix, artifacts.BundlePath)
	if err := putImmutableObject(ctx, store, bucket, bundleKey, artifacts.Bundle, bundleCacheControl); err != nil {
		return fmt.Errorf("publish immutable catalog bundle: %w", err)
	}
	if !promoteChannel {
		return nil
	}

	// The signed channel is the only mutable object and the release commit
	// point. The conditional write prevents concurrent publishers from replacing
	// a channel state they did not inspect.
	putInput := &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(channelKey),
		Body:          bytes.NewReader(artifacts.Channel),
		ContentLength: aws.Int64(int64(len(artifacts.Channel))),
		ContentType:   aws.String("application/json"),
		CacheControl:  aws.String(channelCacheControl),
	}
	if currentChannel.exists {
		putInput.IfMatch = aws.String(currentChannel.etag)
	} else {
		putInput.IfNoneMatch = aws.String("*")
	}
	if _, err := store.PutObject(ctx, putInput); err != nil {
		return fmt.Errorf("promote catalog channel: %w", err)
	}
	if err := verifyObject(ctx, store, bucket, channelKey, artifacts.Channel); err != nil {
		return fmt.Errorf("verify promoted catalog channel: %w", err)
	}
	return nil
}

func readExistingChannel(ctx context.Context, store catalogObjectStore, bucket, key string) (*existingChannel, error) {
	result, err := store.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isObjectNotFound(err) {
			return &existingChannel{}, nil
		}
		return nil, err
	}
	defer result.Body.Close()
	state := &existingChannel{etag: aws.ToString(result.ETag), exists: true}
	if state.etag == "" {
		return nil, fmt.Errorf("current catalog channel has no ETag")
	}

	data, err := io.ReadAll(io.LimitReader(result.Body, channelDocumentLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > channelDocumentLimit {
		// Treat an oversized pointer as malformed and repair it through the ETag
		// guarded promotion below.
		return state, nil
	}
	state.data = data
	var channel catalogdistribution.Channel
	if err := json.Unmarshal(data, &channel); err == nil && channel.Channel != "" && channel.Version != "" {
		state.document = &channel
	}
	return state, nil
}

type objectStoreAPIError interface {
	ErrorCode() string
}

func isObjectNotFound(err error) bool {
	var apiError objectStoreAPIError
	if !errors.As(err, &apiError) {
		return false
	}
	switch apiError.ErrorCode() {
	case "NoSuchKey", "NotFound", "404":
		return true
	default:
		return false
	}
}

func validateReleaseArtifacts(artifacts releaseArtifacts) error {
	if len(artifacts.Bundle) == 0 || len(artifacts.Channel) == 0 {
		return fmt.Errorf("catalog release artifacts are incomplete")
	}
	var channel catalogdistribution.Channel
	if err := json.Unmarshal(artifacts.Channel, &channel); err != nil {
		return fmt.Errorf("decode catalog channel before publish: %w", err)
	}
	wantBundlePath := path.Join("releases", artifacts.Version, "catalog.bundle.json")
	if artifacts.Version == "" || artifacts.ChannelName == "" || artifacts.BundlePath != wantBundlePath {
		return fmt.Errorf("catalog release artifact paths are inconsistent")
	}
	if channel.Version != artifacts.Version || channel.Channel != artifacts.ChannelName || channel.BundlePath != artifacts.BundlePath || channel.BundleSize != int64(len(artifacts.Bundle)) {
		return fmt.Errorf("catalog channel does not describe the release artifacts")
	}
	digest := sha256.Sum256(artifacts.Bundle)
	if !strings.EqualFold(channel.BundleSHA256, hex.EncodeToString(digest[:])) {
		return fmt.Errorf("catalog channel bundle digest does not match release artifact")
	}
	return nil
}

func putImmutableObject(ctx context.Context, store catalogObjectStore, bucket, key string, data []byte, cacheControl string) error {
	_, putErr := store.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
		ContentType:   aws.String("application/json"),
		CacheControl:  aws.String(cacheControl),
		IfNoneMatch:   aws.String("*"),
	})
	if putErr != nil {
		// Retrying a completed release is safe only when the existing immutable
		// object is byte-for-byte identical. A different object at the same
		// version is a release collision and must never be overwritten.
		if verifyErr := verifyObject(ctx, store, bucket, key, data); verifyErr != nil {
			return errors.Join(putErr, verifyErr)
		}
		return nil
	}
	return verifyObject(ctx, store, bucket, key, data)
}

func verifyObject(ctx context.Context, store catalogObjectStore, bucket, key string, want []byte) error {
	result, err := store.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("read back %q: %w", key, err)
	}
	defer result.Body.Close()

	got, err := io.ReadAll(io.LimitReader(result.Body, int64(len(want))+1))
	if err != nil {
		return fmt.Errorf("read back %q body: %w", key, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("read back %q differs from uploaded artifact", key)
	}
	return nil
}
