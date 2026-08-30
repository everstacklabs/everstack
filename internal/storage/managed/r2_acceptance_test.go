package managed

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	s3store "github.com/everstacklabs/everstack/internal/storage/s3"
)

const managedR2AcceptanceEnabledEnv = "EVERSTACK_MANAGED_STORAGE_ACCEPTANCE_ENABLED"

var (
	managedR2AcceptanceEUHost = regexp.MustCompile(`^[0-9a-f]{32}\.eu\.r2\.cloudflarestorage\.com$`)
	managedR2AcceptanceCellID = regexp.MustCompile(`^r2-eu-(dev|staging|prod)-001$`)
)

// TestManagedR2LifecycleAcceptance is deliberately credential-gated. It runs
// the same hardened R2 client and tenant resolver used by managed gateways,
// while the logical caller supplies neither physical placement nor credentials.
// A passing local mock or MinIO run is not a substitute for this test.
func TestManagedR2LifecycleAcceptance(t *testing.T) {
	if os.Getenv(managedR2AcceptanceEnabledEnv) != "1" {
		t.Skip("set " + managedR2AcceptanceEnabledEnv + "=1 to run managed R2 acceptance")
	}

	cellID := requiredManagedR2AcceptanceEnv(t, "EVS_MANAGED_STORAGE_CELL_ID")
	endpoint := requiredManagedR2AcceptanceEnv(t, "EVS_MANAGED_STORAGE_R2_ENDPOINT")
	region := requiredManagedR2AcceptanceEnv(t, "EVS_MANAGED_STORAGE_R2_REGION")
	bucket := requiredManagedR2AcceptanceEnv(t, "EVS_MANAGED_STORAGE_R2_BUCKET")
	accessKeyID := requiredManagedR2AcceptanceEnv(t, "EVS_MANAGED_STORAGE_R2_ACCESS_KEY")
	secretAccessKey := requiredManagedR2AcceptanceEnv(t, "EVS_MANAGED_STORAGE_R2_SECRET_KEY")

	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme != "https" || parsedEndpoint.User != nil || parsedEndpoint.Port() != "" || parsedEndpoint.Path != "" || parsedEndpoint.RawQuery != "" || parsedEndpoint.Fragment != "" || !managedR2AcceptanceEUHost.MatchString(parsedEndpoint.Hostname()) {
		t.Fatal("EVS_MANAGED_STORAGE_R2_ENDPOINT must be an account EU jurisdiction endpoint")
	}
	if region != "auto" {
		t.Fatal("EVS_MANAGED_STORAGE_R2_REGION must be auto")
	}
	if !managedR2AcceptanceCellID.MatchString(cellID) {
		t.Fatal("EVS_MANAGED_STORAGE_CELL_ID must identify an EU R2 cell")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	baseStore, err := s3store.New(ctx, s3store.Config{
		Endpoint:             endpoint,
		Region:               region,
		Bucket:               bucket,
		AccessKeyID:          accessKeyID,
		SecretAccessKey:      secretAccessKey,
		ForcePathStyle:       true,
		EnforceManagedEgress: true,
		DisableNativeCopy:    true,
		WireChecksum:         s3store.WireChecksumContentMD5,
	})
	if err != nil {
		t.Fatalf("construct managed R2 client: %v", err)
	}
	forbiddenBuckets, err := parseManagedR2ForbiddenBuckets(
		requiredManagedR2AcceptanceEnv(t, "EVERSTACK_MANAGED_STORAGE_FORBIDDEN_BUCKETS"),
		bucket,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbiddenBucket := range forbiddenBuckets {
		verifyErr := baseStore.Verify(ctx, forbiddenBucket)
		if verifyErr == nil {
			t.Fatal("managed R2 credential accessed a different environment bucket")
		}
		if !storagepkg.IsCode(verifyErr, storagepkg.ErrorUnauthorized) && !storagepkg.IsCode(verifyErr, storagepkg.ErrorNotFound) {
			t.Fatalf("managed R2 cross-environment denial returned an unexpected error: %v", verifyErr)
		}
	}

	resolver, err := NewResolver(Cell{ID: cellID, Bucket: bucket, Store: baseStore})
	if err != nil {
		t.Fatalf("construct managed R2 resolver: %v", err)
	}
	tenantID := "managed-r2-acceptance-" + managedR2AcceptanceSuffix(t)
	resolved, err := resolver.ResolveManagedStore(ctx, storagepkg.ManagedConnection{
		TenantID:   tenantID,
		CellID:     cellID,
		PathPrefix: storagepkg.ManagedTenantPrefix(tenantID),
	})
	if err != nil {
		t.Fatalf("resolve managed R2 tenant store: %v", err)
	}
	store, err := storagepkg.RequireBlobPlane(resolved)
	if err != nil {
		t.Fatalf("require managed Blob Plane V2: %v", err)
	}

	logicalBucket := "caller-has-no-physical-bucket"
	logicalKey := "v1/acceptance/" + managedR2AcceptanceSuffix(t) + ".txt"
	body := []byte("everstack managed R2 acceptance " + managedR2AcceptanceSuffix(t))
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			_ = store.Delete(cleanupContext, logicalBucket, logicalKey)
		}
	})

	uploadURL, uploadHeaders, err := store.PutPresignedURL(ctx, logicalBucket, logicalKey, "text/plain", int64(len(body)), time.Minute)
	if err != nil {
		t.Fatalf("create managed presigned upload: %v", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal("construct managed presigned upload request")
	}
	for name, value := range uploadHeaders {
		request.Header.Set(name, value)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatal("managed presigned upload request failed")
	}
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("managed presigned upload status = %d", response.StatusCode)
	}

	sizeBytes, contentType, err := store.Head(ctx, logicalBucket, logicalKey)
	if err != nil {
		t.Fatalf("verify managed upload metadata: %v", err)
	}
	if sizeBytes != int64(len(body)) || !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("managed upload metadata = (%d, %q)", sizeBytes, contentType)
	}

	page, err := store.ListPage(ctx, logicalBucket, storagepkg.ListOptions{
		Prefix:   "v1/acceptance/",
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("list managed objects: %v", err)
	}
	found := false
	for _, object := range page.Objects {
		if strings.Contains(object.Key, tenantID) || strings.HasPrefix(object.Key, "tenants/") {
			t.Fatal("managed listing exposed a physical tenant prefix")
		}
		if object.Key == logicalKey {
			found = true
		}
	}
	if !found {
		t.Fatal("managed listing omitted the accepted object")
	}
	if page.NextCursor != "" {
		t.Fatal("managed acceptance prefix unexpectedly required pagination")
	}

	directObject, err := store.Get(ctx, logicalBucket, logicalKey)
	if err != nil {
		t.Fatalf("verify managed upload bytes: %v", err)
	}
	directBody, readErr := io.ReadAll(directObject.Body)
	closeErr := directObject.Body.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(directBody, body) {
		t.Fatal("managed direct verification returned different bytes")
	}

	downloadURL, err := store.GetPresignedURL(ctx, logicalBucket, logicalKey, time.Minute)
	if err != nil {
		t.Fatalf("create managed presigned download: %v", err)
	}
	downloadResponse, err := httpClient.Get(downloadURL)
	if err != nil {
		t.Fatal("managed presigned download request failed")
	}
	downloaded, downloadReadErr := io.ReadAll(downloadResponse.Body)
	_ = downloadResponse.Body.Close()
	if downloadReadErr != nil || downloadResponse.StatusCode != http.StatusOK || !bytes.Equal(downloaded, body) {
		t.Fatalf("managed presigned download status = %d with unexpected bytes", downloadResponse.StatusCode)
	}

	if err := store.Delete(ctx, logicalBucket, logicalKey); err != nil {
		t.Fatalf("delete managed object: %v", err)
	}
	deleted = true
	if _, _, err := store.Head(ctx, logicalBucket, logicalKey); !storagepkg.IsCode(err, storagepkg.ErrorNotFound) {
		t.Fatalf("deleted managed object lookup = %v, want not_found", err)
	}
}

func requiredManagedR2AcceptanceEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required when managed R2 acceptance is enabled", name)
	}
	return value
}

func managedR2AcceptanceSuffix(t *testing.T) string {
	t.Helper()
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		t.Fatal("generate managed R2 acceptance suffix")
	}
	return hex.EncodeToString(value)
}

func parseManagedR2ForbiddenBuckets(raw, ownBucket string) ([]string, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return nil, errors.New("EVERSTACK_MANAGED_STORAGE_FORBIDDEN_BUCKETS must contain the other two environment buckets")
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" || parts[index] == ownBucket {
			return nil, errors.New("managed R2 forbidden buckets must be non-empty and exclude the active bucket")
		}
	}
	if parts[0] == parts[1] {
		return nil, errors.New("managed R2 forbidden buckets must be distinct")
	}
	return parts, nil
}

func TestParseManagedR2ForbiddenBuckets(t *testing.T) {
	t.Parallel()

	got, err := parseManagedR2ForbiddenBuckets("staging-bucket,prod-bucket", "dev-bucket")
	if err != nil || strings.Join(got, ",") != "staging-bucket,prod-bucket" {
		t.Fatalf("parseManagedR2ForbiddenBuckets() = %#v, %v", got, err)
	}
	for _, raw := range []string{
		"",
		"one-bucket",
		"dev-bucket,prod-bucket",
		"same-bucket,same-bucket",
		"staging-bucket,",
	} {
		if _, err := parseManagedR2ForbiddenBuckets(raw, "dev-bucket"); err == nil {
			t.Fatalf("parseManagedR2ForbiddenBuckets(%q) succeeded", raw)
		}
	}
}
