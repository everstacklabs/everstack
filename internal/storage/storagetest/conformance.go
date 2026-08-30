// Package storagetest contains the behavior-oriented Blob Plane V2 provider
// contract. Every provider profile must run this exact suite.
package storagetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/storage"
)

type Config struct {
	Profile           string
	Bucket            string
	Store             storage.BlobPlane
	FallbackCopyStore storage.BlobPlane
	HTTPClient        *http.Client
	RetryProbe        func(context.Context) error
	ThrottleProbe     func(context.Context) error
	AuthProbe         func(context.Context) error
	CorruptionProbe   func(context.Context) error
	ExpiryWait        time.Duration
}

// Run executes the complete provider contract through public storage methods.
func Run(t *testing.T, config Config) {
	t.Helper()
	if strings.TrimSpace(config.Profile) == "" || strings.TrimSpace(config.Bucket) == "" || config.Store == nil {
		t.Fatal("storagetest.Config requires profile, bucket, and store")
	}
	if config.FallbackCopyStore == nil {
		t.Fatal("storagetest.Config requires a safe-copy fallback store")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if config.ExpiryWait <= 0 {
		config.ExpiryWait = 2500 * time.Millisecond
	}
	prefix := "everstack-conformance/" + randomSuffix(t) + "/"

	t.Run("original interface compatibility", func(t *testing.T) {
		key := prefix + "compatibility.txt"
		deferDelete(t, config.Store, config.Bucket, key)
		etag, err := config.Store.Put(context.Background(), config.Bucket, key, "text/plain", strings.NewReader("legacy contract"))
		if err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		if etag == "" {
			t.Fatal("Put() returned an empty ETag")
		}
		size, contentType, err := config.Store.Head(context.Background(), config.Bucket, key)
		if err != nil {
			t.Fatalf("Head() error = %v", err)
		}
		if size != int64(len("legacy contract")) || contentType != "text/plain" {
			t.Fatalf("Head() = (%d, %q), want (%d, text/plain)", size, contentType, len("legacy contract"))
		}
		objects, err := config.Store.List(context.Background(), config.Bucket, prefix)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if !containsKey(objects, key) {
			t.Fatalf("List() omitted %q", key)
		}
	})

	t.Run("direct read checksum and conditional create", func(t *testing.T) {
		key := prefix + "unicode/naïve-東京.txt"
		deferDelete(t, config.Store, config.Bucket, key)
		body := []byte("immutable bytes")
		size := int64(len(body))
		checksum := storage.NewSHA256Checksum(body)
		created, err := config.Store.PutIfAbsent(context.Background(), config.Bucket, key, bytes.NewReader(body), storage.PutOptions{
			ContentType:  "text/plain; charset=utf-8",
			ExpectedSize: &size,
			Checksum:     checksum,
		})
		if err != nil {
			t.Fatalf("PutIfAbsent() error = %v", err)
		}
		if created.Checksum != checksum || created.SizeBytes != size {
			t.Fatalf("PutIfAbsent() metadata = %+v", created)
		}

		_, err = config.Store.PutIfAbsent(context.Background(), config.Bucket, key, strings.NewReader("replacement"), storage.PutOptions{})
		if !storage.IsCode(err, storage.ErrorAlreadyExists) {
			t.Fatalf("second PutIfAbsent() error = %v, want already_exists", err)
		}

		object, err := config.Store.Get(context.Background(), config.Bucket, key)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		got, readErr := io.ReadAll(object.Body)
		closeErr := object.Body.Close()
		if readErr != nil {
			t.Fatalf("ReadAll() error = %v", readErr)
		}
		if closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
		if !bytes.Equal(got, body) || object.Checksum != checksum {
			t.Fatalf("Get() = (%q, %+v), want (%q, %+v)", got, object.Checksum, body, checksum)
		}
	})

	t.Run("checksum mismatch never creates an object", func(t *testing.T) {
		key := prefix + "checksum-mismatch"
		body := []byte("actual")
		_, err := config.Store.PutIfAbsent(context.Background(), config.Bucket, key, bytes.NewReader(body), storage.PutOptions{
			Checksum: storage.NewSHA256Checksum([]byte("declared")),
		})
		if !storage.IsCode(err, storage.ErrorChecksumMismatch) {
			t.Fatalf("PutIfAbsent() error = %v, want checksum_mismatch", err)
		}
		_, _, err = config.Store.Head(context.Background(), config.Bucket, key)
		if !storage.IsCode(err, storage.ErrorNotFound) {
			t.Fatalf("Head() error = %v, want not_found", err)
		}
	})

	t.Run("opaque cursor pagination", func(t *testing.T) {
		want := make([]string, 0, 5)
		for index := 0; index < 5; index++ {
			key := prefix + "pages/" + string(rune('a'+index))
			want = append(want, key)
			deferDelete(t, config.Store, config.Bucket, key)
			if _, err := config.Store.PutIfAbsent(context.Background(), config.Bucket, key, strings.NewReader(key), storage.PutOptions{}); err != nil {
				t.Fatalf("PutIfAbsent(%q) error = %v", key, err)
			}
		}

		var got []string
		cursor := ""
		for pageNumber := 0; pageNumber < 10; pageNumber++ {
			page, err := config.Store.ListPage(context.Background(), config.Bucket, storage.ListOptions{
				Prefix: prefix + "pages/", Cursor: cursor, PageSize: 2,
			})
			if err != nil {
				t.Fatalf("ListPage() error = %v", err)
			}
			for _, object := range page.Objects {
				got = append(got, object.Key)
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor == cursor {
				t.Fatalf("ListPage() repeated cursor %q", cursor)
			}
			cursor = page.NextCursor
		}
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("paginated keys = %q, want %q", got, want)
		}
	})

	t.Run("native copy preserves bytes and no-overwrite semantics", func(t *testing.T) {
		sourceKey := prefix + "copy/source"
		destinationKey := prefix + "copy/native"
		deferDelete(t, config.Store, config.Bucket, sourceKey)
		deferDelete(t, config.Store, config.Bucket, destinationKey)
		body := []byte("copy source")
		if _, err := config.Store.PutIfAbsent(context.Background(), config.Bucket, sourceKey, bytes.NewReader(body), storage.PutOptions{}); err != nil {
			t.Fatalf("PutIfAbsent() error = %v", err)
		}
		result, err := config.Store.CopyIfAbsent(context.Background(), config.Bucket, sourceKey, config.Bucket, destinationKey)
		if err != nil {
			t.Fatalf("CopyIfAbsent() error = %v", err)
		}
		if config.Store.BlobCapabilities().NativeCopy && result.UsedFallback {
			t.Fatal("native-copy profile silently used fallback")
		}
		_, err = config.Store.CopyIfAbsent(context.Background(), config.Bucket, sourceKey, config.Bucket, destinationKey)
		if !storage.IsCode(err, storage.ErrorAlreadyExists) {
			t.Fatalf("second CopyIfAbsent() error = %v, want already_exists", err)
		}
		assertObjectBody(t, config.Store, config.Bucket, destinationKey, body)
	})

	t.Run("safe copy fallback preserves bytes and no-overwrite semantics", func(t *testing.T) {
		sourceKey := prefix + "copy/fallback-source"
		destinationKey := prefix + "copy/fallback-destination"
		deferDelete(t, config.Store, config.Bucket, sourceKey)
		deferDelete(t, config.Store, config.Bucket, destinationKey)
		body := []byte("fallback copy source")
		if _, err := config.Store.PutIfAbsent(context.Background(), config.Bucket, sourceKey, bytes.NewReader(body), storage.PutOptions{}); err != nil {
			t.Fatalf("PutIfAbsent() error = %v", err)
		}
		result, err := config.FallbackCopyStore.CopyIfAbsent(context.Background(), config.Bucket, sourceKey, config.Bucket, destinationKey)
		if err != nil {
			t.Fatalf("CopyIfAbsent() error = %v", err)
		}
		if !result.UsedFallback || config.FallbackCopyStore.BlobCapabilities().NativeCopy {
			t.Fatalf("fallback copy result/capabilities = %+v / %+v", result, config.FallbackCopyStore.BlobCapabilities())
		}
		_, err = config.FallbackCopyStore.CopyIfAbsent(context.Background(), config.Bucket, sourceKey, config.Bucket, destinationKey)
		if !storage.IsCode(err, storage.ErrorAlreadyExists) {
			t.Fatalf("second fallback CopyIfAbsent() error = %v, want already_exists", err)
		}
		assertObjectBody(t, config.Store, config.Bucket, destinationKey, body)
	})

	t.Run("multipart interruption aborts without publishing an object", func(t *testing.T) {
		key := prefix + "multipart/aborted"
		upload, err := config.Store.BeginMultipart(context.Background(), config.Bucket, key, storage.MultipartOptions{ContentType: "application/octet-stream"})
		if err != nil {
			t.Fatalf("BeginMultipart() error = %v", err)
		}
		if _, err := config.Store.UploadPart(context.Background(), upload, 1, strings.NewReader("partial"), storage.PutOptions{}); err != nil {
			t.Fatalf("UploadPart() error = %v", err)
		}
		interrupted, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := config.Store.UploadPart(interrupted, upload, 2, strings.NewReader("must not publish"), storage.PutOptions{}); !storage.IsCode(err, storage.ErrorUnavailable) {
			t.Fatalf("interrupted UploadPart() error = %v, want unavailable", err)
		}
		if err := config.Store.AbortMultipart(context.Background(), upload); err != nil {
			t.Fatalf("AbortMultipart() error = %v", err)
		}
		_, _, err = config.Store.Head(context.Background(), config.Bucket, key)
		if !storage.IsCode(err, storage.ErrorNotFound) {
			t.Fatalf("Head() error = %v, want not_found", err)
		}
	})

	t.Run("multipart completion verifies full size and checksum", func(t *testing.T) {
		key := prefix + "multipart/completed"
		deferDelete(t, config.Store, config.Bucket, key)
		first := bytes.Repeat([]byte("a"), 5*1024*1024)
		second := []byte("final multipart bytes")
		whole := append(append([]byte(nil), first...), second...)
		size := int64(len(whole))
		checksum := storage.NewSHA256Checksum(whole)
		upload, err := config.Store.BeginMultipart(context.Background(), config.Bucket, key, storage.MultipartOptions{
			ContentType: "application/octet-stream", ExpectedSize: &size, Checksum: checksum,
		})
		if err != nil {
			t.Fatalf("BeginMultipart() error = %v", err)
		}
		aborted := false
		defer func() {
			if !aborted {
				_ = config.Store.AbortMultipart(context.Background(), upload)
			}
		}()
		partOne, err := config.Store.UploadPart(context.Background(), upload, 1, bytes.NewReader(first), storage.PutOptions{Checksum: storage.NewSHA256Checksum(first)})
		if err != nil {
			t.Fatalf("UploadPart(1) error = %v", err)
		}
		partTwo, err := config.Store.UploadPart(context.Background(), upload, 2, bytes.NewReader(second), storage.PutOptions{Checksum: storage.NewSHA256Checksum(second)})
		if err != nil {
			t.Fatalf("UploadPart(2) error = %v", err)
		}
		completed, err := config.Store.CompleteMultipart(context.Background(), upload, []storage.UploadedPart{partTwo, partOne})
		if err != nil {
			t.Fatalf("CompleteMultipart() error = %v", err)
		}
		aborted = true
		if completed.SizeBytes != size || completed.Checksum != checksum {
			t.Fatalf("CompleteMultipart() = %+v", completed)
		}
		assertObjectBody(t, config.Store, config.Bucket, key, whole)
	})

	t.Run("presigned upload download and expiry", func(t *testing.T) {
		key := prefix + "presigned/object"
		deferDelete(t, config.Store, config.Bucket, key)
		body := []byte("presigned body")
		uploadURL, headers, err := config.Store.PutPresignedURL(context.Background(), config.Bucket, key, "text/plain", int64(len(body)), time.Minute)
		if err != nil {
			t.Fatalf("PutPresignedURL() error = %v", err)
		}
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPut, uploadURL, bytes.NewReader(body))
		if err != nil {
			t.Fatal("failed to construct presigned upload request")
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, err := config.HTTPClient.Do(request)
		if err != nil {
			t.Fatal("presigned upload request failed")
		}
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("presigned upload status = %d", response.StatusCode)
		}

		downloadURL, err := config.Store.GetPresignedURL(context.Background(), config.Bucket, key, time.Minute)
		if err != nil {
			t.Fatalf("GetPresignedURL() error = %v", err)
		}
		response, err = config.HTTPClient.Get(downloadURL)
		if err != nil {
			t.Fatal("presigned download request failed")
		}
		got, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
			t.Fatalf("presigned download = status %d, bytes %q", response.StatusCode, got)
		}

		expiredURL, err := config.Store.GetPresignedURL(context.Background(), config.Bucket, key, time.Second)
		if err != nil {
			t.Fatalf("GetPresignedURL(expiring) error = %v", err)
		}
		time.Sleep(config.ExpiryWait)
		response, err = config.HTTPClient.Get(expiredURL)
		if err != nil {
			t.Fatal("expired presigned download request failed")
		}
		_ = response.Body.Close()
		if response.StatusCode < 400 {
			t.Fatalf("expired presigned download status = %d, want failure", response.StatusCode)
		}
	})

	t.Run("retry and throttling classification", func(t *testing.T) {
		if config.RetryProbe == nil || config.ThrottleProbe == nil || config.AuthProbe == nil {
			t.Fatal("provider profile omitted retry, throttling, or authentication probes")
		}
		if err := config.RetryProbe(context.Background()); err != nil {
			t.Fatalf("retry probe error = %v", err)
		}
		err := config.ThrottleProbe(context.Background())
		if !storage.IsCode(err, storage.ErrorThrottled) || !storage.IsRetryable(err) {
			t.Fatalf("throttle probe error = %v, want retryable throttled", err)
		}
		err = config.AuthProbe(context.Background())
		if !storage.IsCode(err, storage.ErrorUnauthorized) || storage.IsRetryable(err) {
			t.Fatalf("authentication probe error = %v, want non-retryable unauthorized", err)
		}
		if strings.Contains(err.Error(), "everstack-invalid") {
			t.Fatalf("authentication error exposed credential material: %v", err)
		}
	})

	t.Run("read-side corruption is detected", func(t *testing.T) {
		if config.CorruptionProbe == nil {
			t.Fatal("provider profile omitted read-side corruption probe")
		}
		err := config.CorruptionProbe(context.Background())
		if !storage.IsCode(err, storage.ErrorChecksumMismatch) {
			t.Fatalf("corruption probe error = %v, want checksum_mismatch", err)
		}
	})

	t.Run("not found is classified", func(t *testing.T) {
		_, err := config.Store.Get(context.Background(), config.Bucket, prefix+"missing")
		if !storage.IsCode(err, storage.ErrorNotFound) {
			t.Fatalf("Get(missing) error = %v, want not_found", err)
		}
	})
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate conformance prefix: %v", err)
	}
	return hex.EncodeToString(value)
}

func deferDelete(t *testing.T, store storage.ObjectStore, bucket, key string) {
	t.Helper()
	t.Cleanup(func() {
		if err := store.Delete(context.Background(), bucket, key); err != nil && !storage.IsCode(err, storage.ErrorNotFound) {
			t.Errorf("cleanup Delete(%q) error = %v", key, err)
		}
	})
}

func assertObjectBody(t *testing.T, store storage.BlobPlane, bucket, key string, want []byte) {
	t.Helper()
	object, err := store.Get(context.Background(), bucket, key)
	if err != nil {
		t.Fatalf("Get(%q) error = %v", key, err)
	}
	got, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil {
		t.Fatalf("ReadAll(%q) error = %v", key, readErr)
	}
	if closeErr != nil {
		t.Fatalf("Close(%q) error = %v", key, closeErr)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Get(%q) bytes differ: got %d bytes, want %d", key, len(got), len(want))
	}
}

func containsKey(objects []storage.BucketObject, key string) bool {
	for _, object := range objects {
		if object.Key == key {
			return true
		}
	}
	return false
}
