package storage

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRequireBlobPlaneRejectsLegacyStoreExplicitly(t *testing.T) {
	_, err := RequireBlobPlane(legacyObjectStore{})
	if !IsCode(err, ErrorUnsupported) {
		t.Fatalf("RequireBlobPlane() error = %v, want unsupported", err)
	}
}

func TestRequireBlobPlaneRejectsTypedNilStore(t *testing.T) {
	var typedNil *legacyObjectStore
	_, err := RequireBlobPlane(typedNil)
	if !IsCode(err, ErrorUnsupported) {
		t.Fatalf("RequireBlobPlane(typed nil) error = %v, want unsupported", err)
	}
}

func TestOperationErrorDoesNotExposeProviderDetail(t *testing.T) {
	err := NewOperationError("get", ErrorUnauthorized, false, "SignatureDoesNotMatch", 403)
	if got := err.Error(); strings.Contains(got, "provider-secret") || strings.Contains(got, "SignatureDoesNotMatch") {
		t.Fatalf("OperationError.Error() exposed provider detail: %q", got)
	}
	if !IsCode(err, ErrorUnauthorized) {
		t.Fatalf("IsCode() = false for unauthorized error")
	}
	if IsRetryable(err) {
		t.Fatal("IsRetryable() = true for non-retryable error")
	}
}

func TestOperationErrorDropsUntrustedProviderCode(t *testing.T) {
	err := NewOperationError("get", ErrorProvider, false, "AccessDenied\nprovider-secret", 500)
	if err.ProviderCode != "" {
		t.Fatalf("ProviderCode = %q, want redacted", err.ProviderCode)
	}
	if strings.Contains(err.Error(), "provider-secret") {
		t.Fatalf("OperationError.Error() exposed untrusted provider code: %q", err.Error())
	}

	safe := NewOperationError("get", ErrorUnauthorized, false, "SignatureDoesNotMatch", 403)
	if safe.ProviderCode != "SignatureDoesNotMatch" {
		t.Fatalf("safe ProviderCode = %q", safe.ProviderCode)
	}
}

func TestSHA256ChecksumUsesStableLowercaseHex(t *testing.T) {
	checksum := NewSHA256Checksum([]byte("everstack"))
	if checksum.Algorithm != ChecksumSHA256 {
		t.Fatalf("Algorithm = %q, want %q", checksum.Algorithm, ChecksumSHA256)
	}
	const want = "f45ad39c4699d83cac1d85030aaab7d4b8730c9a7c263a0427178a571cc720bc"
	if checksum.Value != want {
		t.Fatalf("Value = %q, want %q", checksum.Value, want)
	}
	if err := checksum.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := (Checksum{Algorithm: ChecksumSHA256, Value: "not-a-digest"}).Validate(); !IsCode(err, ErrorInvalidArgument) {
		t.Fatalf("invalid checksum error = %v, want invalid_argument", err)
	}
}

type legacyObjectStore struct{}

func (legacyObjectStore) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, nil
}
func (legacyObjectStore) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}
func (legacyObjectStore) Put(context.Context, string, string, string, io.Reader) (string, error) {
	return "", nil
}
func (legacyObjectStore) Delete(context.Context, string, string) error { return nil }
func (legacyObjectStore) Head(context.Context, string, string) (int64, string, error) {
	return 0, "", nil
}
func (legacyObjectStore) List(context.Context, string, string) ([]BucketObject, error) {
	return nil, nil
}
