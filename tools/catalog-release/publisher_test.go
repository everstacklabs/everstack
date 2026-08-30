package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/everstacklabs/everstack/internal/catalogdistribution"
)

type objectStoreOperation struct {
	method       string
	key          string
	ifMatch      string
	ifNoneMatch  string
	cacheControl string
}

type fakeObjectStore struct {
	objects    map[string][]byte
	operations []objectStoreOperation
}

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: make(map[string][]byte)}
}

func (s *fakeObjectStore) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	key := aws.ToString(input.Key)
	s.operations = append(s.operations, objectStoreOperation{
		method:       "put",
		key:          key,
		ifMatch:      aws.ToString(input.IfMatch),
		ifNoneMatch:  aws.ToString(input.IfNoneMatch),
		cacheControl: aws.ToString(input.CacheControl),
	})
	if aws.ToString(input.IfNoneMatch) == "*" {
		if _, exists := s.objects[key]; exists {
			return nil, errors.New("precondition failed")
		}
	}
	if ifMatch := aws.ToString(input.IfMatch); ifMatch != "" {
		existing, exists := s.objects[key]
		if !exists || objectETag(existing) != ifMatch {
			return nil, errors.New("precondition failed")
		}
	}
	data, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	s.objects[key] = bytes.Clone(data)
	return &s3.PutObjectOutput{}, nil
}

func (s *fakeObjectStore) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := aws.ToString(input.Key)
	s.operations = append(s.operations, objectStoreOperation{method: "get", key: key})
	data, exists := s.objects[key]
	if !exists {
		return nil, fakeObjectStoreError{code: "NoSuchKey"}
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
		ETag: aws.String(objectETag(data)),
	}, nil
}

type fakeObjectStoreError struct {
	code string
}

func (e fakeObjectStoreError) Error() string     { return e.code }
func (e fakeObjectStoreError) ErrorCode() string { return e.code }

func objectETag(data []byte) string {
	digest := sha256.Sum256(data)
	return `"` + hex.EncodeToString(digest[:]) + `"`
}

func TestPublishReleaseCommitsChannelAfterVerifiedBundle(t *testing.T) {
	artifacts := testReleaseArtifacts(t)
	store := newFakeObjectStore()

	if err := publishRelease(context.Background(), store, "catalog", artifacts); err != nil {
		t.Fatalf("publishRelease() error = %v", err)
	}

	bundleKey := "v1/" + artifacts.BundlePath
	channelKey := "v1/channels/stable.json"
	want := []objectStoreOperation{
		{method: "get", key: channelKey},
		{method: "put", key: bundleKey, ifNoneMatch: "*", cacheControl: bundleCacheControl},
		{method: "get", key: bundleKey},
		{method: "put", key: channelKey, ifNoneMatch: "*", cacheControl: channelCacheControl},
		{method: "get", key: channelKey},
	}
	if len(store.operations) != len(want) {
		t.Fatalf("operations = %#v, want %#v", store.operations, want)
	}
	for index := range want {
		if store.operations[index] != want[index] {
			t.Fatalf("operation %d = %#v, want %#v", index, store.operations[index], want[index])
		}
	}
}

func TestPublishReleaseRejectsImmutableVersionCollision(t *testing.T) {
	artifacts := testReleaseArtifacts(t)
	store := newFakeObjectStore()
	store.objects["v1/"+artifacts.BundlePath] = []byte("different release")

	err := publishRelease(context.Background(), store, "catalog", artifacts)
	if err == nil || !strings.Contains(err.Error(), "differs from uploaded artifact") {
		t.Fatalf("publishRelease() error = %v, want immutable collision", err)
	}
	if _, exists := store.objects["v1/channels/stable.json"]; exists {
		t.Fatal("channel was promoted after immutable bundle collision")
	}
}

func TestPublishReleaseIsIdempotentForIdenticalBundle(t *testing.T) {
	artifacts := testReleaseArtifacts(t)
	store := newFakeObjectStore()
	store.objects["v1/"+artifacts.BundlePath] = bytes.Clone(artifacts.Bundle)

	if err := publishRelease(context.Background(), store, "catalog", artifacts); err != nil {
		t.Fatalf("publishRelease() error = %v", err)
	}
	if !bytes.Equal(store.objects["v1/channels/stable.json"], artifacts.Channel) {
		t.Fatal("channel was not promoted for idempotent release retry")
	}
}

func TestPublishReleaseDoesNotRewriteIdenticalChannel(t *testing.T) {
	artifacts := testReleaseArtifacts(t)
	store := newFakeObjectStore()
	store.objects["v1/"+artifacts.BundlePath] = bytes.Clone(artifacts.Bundle)
	store.objects["v1/channels/stable.json"] = bytes.Clone(artifacts.Channel)

	if err := publishRelease(context.Background(), store, "catalog", artifacts); err != nil {
		t.Fatalf("publishRelease() error = %v", err)
	}
	for _, operation := range store.operations {
		if operation.method == "put" && operation.key == "v1/channels/stable.json" {
			t.Fatal("identical channel was rewritten")
		}
	}
}

func TestPublishReleaseUsesInspectedChannelETag(t *testing.T) {
	current := testReleaseArtifactsForVersion(t, "2.4.0")
	candidate := testReleaseArtifacts(t)
	store := newFakeObjectStore()
	channelKey := "v1/channels/stable.json"
	store.objects[channelKey] = bytes.Clone(current.Channel)

	if err := publishRelease(context.Background(), store, "catalog", candidate); err != nil {
		t.Fatalf("publishRelease() error = %v", err)
	}
	wantETag := objectETag(current.Channel)
	for _, operation := range store.operations {
		if operation.method == "put" && operation.key == channelKey {
			if operation.ifMatch != wantETag || operation.ifNoneMatch != "" {
				t.Fatalf("channel promotion condition = (%q, %q), want If-Match %q", operation.ifMatch, operation.ifNoneMatch, wantETag)
			}
			return
		}
	}
	t.Fatal("channel promotion was not attempted")
}

func TestPublishReleaseRejectsOlderChannelPromotion(t *testing.T) {
	candidate := testReleaseArtifacts(t)
	newer := testReleaseArtifactsForVersion(t, "2.5.0")
	store := newFakeObjectStore()
	store.objects["v1/channels/stable.json"] = bytes.Clone(newer.Channel)

	err := publishRelease(context.Background(), store, "catalog", candidate)
	if err == nil || !strings.Contains(err.Error(), "newer channel version") {
		t.Fatalf("publishRelease() error = %v, want newer channel rejection", err)
	}
	if _, exists := store.objects["v1/"+candidate.BundlePath]; exists {
		t.Fatal("older immutable bundle was uploaded before channel rejection")
	}
}

func TestLoadR2ConfigRequiresHTTPSOrigin(t *testing.T) {
	values := map[string]string{
		r2EndpointEnvironmentVariable:        "http://example.invalid/path",
		r2BucketEnvironmentVariable:          "catalog",
		r2AccessKeyIDEnvironmentVariable:     "access",
		r2SecretAccessKeyEnvironmentVariable: "secret",
	}
	_, err := loadR2Config(func(key string) string { return values[key] })
	if err == nil || !strings.Contains(err.Error(), "HTTPS origin") {
		t.Fatalf("loadR2Config() error = %v", err)
	}
}

func testReleaseArtifacts(t *testing.T) releaseArtifacts {
	return testReleaseArtifactsForVersion(t, "2.4.1")
}

func testReleaseArtifactsForVersion(t *testing.T, version string) releaseArtifacts {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle, digest, err := catalogdistribution.BuildBundle(
		version,
		[]byte("providers:\n  example:\n    models:\n      - name: example-model\n"),
		[]byte("providers:\n  example:\n    display_name: Example\n"),
		[]byte("changes: []\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := "releases/" + version + "/catalog.bundle.json"
	channel, err := catalogdistribution.SignChannel(privateKey, catalogdistribution.Channel{
		Channel:      "stable",
		Version:      version,
		BundlePath:   bundlePath,
		BundleSHA256: digest,
		BundleSize:   int64(len(bundle)),
		PublishedAt:  time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	return releaseArtifacts{
		Version:     version,
		ChannelName: "stable",
		BundlePath:  bundlePath,
		Bundle:      bundle,
		Channel:     channel,
	}
}
