package v1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/hosting"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	pkgdb "github.com/everstacklabs/everstack/pkg/database"
	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
)

// A bare tenant id in context (as the standalone LocalTenantInterceptor
// injects downstream of the anonymous auth bypass) must NOT be treated as an
// authenticated owner: only the explicit authenticated marker counts.
// Otherwise an unauthenticated caller on a standalone gateway could act as
// the local tenant.
func TestTenantIDRequiresAuthentication(t *testing.T) {
	s := &Server{}

	injected := pkgdb.WithTenantSchema(
		contextkeys.WithTenantID(context.Background(), "local-tenant"),
		"local-tenant",
	)
	if got := s.tenantID(injected); got != "" {
		t.Errorf("injected-but-unauthenticated tenant should be anonymous, got %q", got)
	}

	authed := contextkeys.WithTenantAuthenticated(
		contextkeys.WithTenantID(context.Background(), "real-tenant"),
	)
	if got := s.tenantID(authed); got != "real-tenant" {
		t.Errorf("authenticated tenant = %q, want real-tenant", got)
	}

	if got := s.tenantID(context.Background()); got != "" {
		t.Errorf("empty context should be anonymous, got %q", got)
	}
}

func TestNormalizeEmail(t *testing.T) {
	good := []string{"a@b.co", "User@Example.COM", "x.y+z@sub.domain.io"}
	for _, e := range good {
		if _, err := normalizeEmail(e); err != nil {
			t.Errorf("normalizeEmail(%q) unexpected error: %v", e, err)
		}
	}
	bad := []string{"", "no-at", "@nolocal.com", "trailing@", "a b@c.com", "x@"}
	for _, e := range bad {
		if _, err := normalizeEmail(e); err == nil {
			t.Errorf("normalizeEmail(%q) should have failed", e)
		}
	}
	if got, _ := normalizeEmail("  Foo@Bar.COM "); got != "foo@bar.com" {
		t.Errorf("normalizeEmail lowercase/trim = %q", got)
	}
}

func TestVerifyCodeDoesNotIssueKeyWhenQuotaCheckedClaimFails(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	s := CreateServerWithDeps(context.Background(), db, &cleanupObjectStore{}, Config{Bucket: "sites"})
	s.SetQuotaResolver(hosting.QuotaResolverFunc(func(context.Context, string) (hosting.TenantQuota, error) {
		return hosting.TenantQuota{Tier: "free", MaxSites: 0, MaxStorageBytes: 500}, nil
	}))
	s.SetOwnerProvisioner(func(context.Context, string) (string, string, error) {
		return "user-1", "tenant-1", nil
	})
	issued := 0
	s.SetKeyIssuer(func(context.Context, string, string) (string, error) {
		issued++
		return "secret-key", nil
	})

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	mock.ExpectQuery("SELECT id, code_hash, attempts FROM site_email_codes").
		WithArgs("owner@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "code_hash", "attempts"}).
			AddRow("code-1", hashToken("123456"), 0))
	mock.ExpectExec("UPDATE site_email_codes SET consumed_at").
		WithArgs("code-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("pg_advisory_lock").
		WithArgs("demo").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id, slug, tenant_id").
		WithArgs("demo").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", nil, nil, "active", false, "noindex",
			int32(1), "sites/demo/manifest.json", 10, 1, hashToken("claim-token"), nil, false, 0, expiresAt, now, now,
		))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(total_bytes\\), 0\\)::bigint").
		WithArgs("site-1").
		WillReturnRows(sqlmock.NewRows([]string{"storage_bytes"}).AddRow(10))
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("hosting-quota:tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("COUNT\\(DISTINCT s.id\\)").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"sites", "storage_bytes"}).AddRow(0, 0))
	mock.ExpectRollback()
	mock.ExpectExec("pg_advisory_unlock").
		WithArgs("demo").
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err = s.VerifyCode(context.Background(), connect.NewRequest(&hostingpb.VerifyCodeRequest{
		Email:      "owner@example.com",
		Code:       "123456",
		Slug:       "demo",
		ClaimToken: "claim-token",
	}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, error = %v; want ResourceExhausted", connect.CodeOf(err), err)
	}
	if issued != 0 {
		t.Fatalf("issued keys = %d, want 0", issued)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimRetryForSameOwnerIsIdempotentUntilKeyIssued(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &cleanupObjectStore{}
	purger := &recordingPurger{}
	s := &Server{db: db, store: store, cfg: Config{Bucket: "sites"}, purger: purger}

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, slug, tenant_id").
		WithArgs("demo").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", "tenant-1", "user-1", "active", false, "public",
			int32(1), "sites/demo/manifest.json", 10, 1, hashToken("claim-token"), now, false, 0, nil, now, now,
		))
	mock.ExpectQuery("SELECT f.path, f.r2_key").
		WithArgs("site-1", int32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"path", "r2_key", "content_type", "size_bytes", "sha256"}).
			AddRow("index.html", "sites/demo/v1/index.html", "text/html", 10, nil))

	if err := s.bindClaimedSiteLocked(context.Background(), "demo", "claim-token", "tenant-1", "user-1"); err != nil {
		t.Fatalf("idempotent claim retry: %v", err)
	}
	if len(store.putBody) != 1 {
		t.Fatalf("manifest writes = %d, want owned projection repair", len(store.putBody))
	}
	var manifest hosting.Manifest
	if err := json.Unmarshal(store.putBody[0], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.NoIndex || manifest.ExpiresAt != nil {
		t.Fatalf("repaired manifest = %+v, want permanent owned projection", manifest)
	}
	if len(purger.slugs) != 1 || purger.slugs[0] != "demo" {
		t.Fatalf("purged slugs = %v, want demo", purger.slugs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimRetryCannotReprojectDeletingSite(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &cleanupObjectStore{}
	s := &Server{db: db, store: store, cfg: Config{Bucket: "sites"}}

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, slug, tenant_id").
		WithArgs("demo").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", "tenant-1", "user-1", "deleting", false, "public",
			int32(1), nil, 10, 1, hashToken("claim-token"), now, false, 0, nil, now, now,
		))

	err = s.bindClaimedSiteLocked(context.Background(), "demo", "claim-token", "tenant-1", "user-1")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, error = %v; want FailedPrecondition", connect.CodeOf(err), err)
	}
	if len(store.putBody) != 0 {
		t.Fatalf("manifest writes = %d, want none", len(store.putBody))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRetireClaimTokenIgnoresCancelledVerificationRequest(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	s := &Server{db: db}
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()

	mock.ExpectExec("SET claim_token_hash = NULL").
		WithArgs("demo", "tenant-1", "user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.retireClaimToken(requestCtx, "demo", "tenant-1", "user-1"); err != nil {
		t.Fatalf("retire claim token: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedClaimRestoresAnonymousManifest(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &cleanupObjectStore{}
	purger := &recordingPurger{}
	s := &Server{
		db:     db,
		store:  store,
		cfg:    Config{Bucket: "sites"},
		purger: purger,
		quotaResolver: hosting.QuotaResolverFunc(func(context.Context, string) (hosting.TenantQuota, error) {
			return hosting.TenantQuota{Tier: "enterprise", MaxSites: -1, MaxStorageBytes: -1}, nil
		}),
	}

	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)
	claimHash := hashToken("claim-token")
	siteRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", nil, nil, "active", false, "public",
			int32(1), "sites/demo/manifest.json", 10, 1, claimHash, nil, false, 0, expiresAt, now, now,
		)
	}
	fileRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"path", "r2_key", "content_type", "size_bytes", "sha256"}).
			AddRow("index.html", "sites/demo/v1/index.html", "text/html", 10, nil)
	}

	mock.ExpectQuery("SELECT id, slug, tenant_id").WithArgs("demo").WillReturnRows(siteRows())
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(total_bytes\\), 0\\)::bigint").
		WithArgs("site-1").
		WillReturnRows(sqlmock.NewRows([]string{"storage_bytes"}).AddRow(10))
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("hosting-quota:tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("COUNT\\(DISTINCT s.id\\)").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"sites", "storage_bytes"}).AddRow(0, 0))
	mock.ExpectQuery("SELECT f.path, f.r2_key").
		WithArgs("site-1", int32(1)).
		WillReturnRows(fileRows())
	mock.ExpectExec("UPDATE sites SET").
		WithArgs("site-1", "tenant-1", "user-1").
		WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()
	mock.ExpectQuery("SELECT id, slug, tenant_id").WithArgs("demo").WillReturnRows(siteRows())
	mock.ExpectQuery("SELECT f.path, f.r2_key").
		WithArgs("site-1", int32(1)).
		WillReturnRows(fileRows())

	err = s.bindClaimedSiteLocked(context.Background(), "demo", "claim-token", "tenant-1", "user-1")
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, error = %v; want Internal", connect.CodeOf(err), err)
	}
	if len(store.putBody) != 2 {
		t.Fatalf("manifest writes = %d, want permanent then restored anonymous", len(store.putBody))
	}
	var permanent, restored hosting.Manifest
	if err := json.Unmarshal(store.putBody[0], &permanent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(store.putBody[1], &restored); err != nil {
		t.Fatal(err)
	}
	if permanent.NoIndex || permanent.ExpiresAt != nil {
		t.Fatalf("permanent manifest = %+v", permanent)
	}
	if !restored.NoIndex || restored.ExpiresAt == nil || !restored.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("restored manifest = %+v, want anonymous expiry %s", restored, expiresAt)
	}
	if len(purger.slugs) != 1 || purger.slugs[0] != "demo" {
		t.Fatalf("purged slugs = %v, want restored claim projection purged", purger.slugs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAmbiguousClaimManifestWriteRestoresAfterRequestCancellation(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	store := &cleanupObjectStore{
		putErrors: []error{errors.New("outcome unknown")},
		putHook: func(index int) {
			if index == 0 {
				cancelRequest()
			}
		},
	}
	s := &Server{
		db:    db,
		store: store,
		cfg:   Config{Bucket: "sites"},
		quotaResolver: hosting.QuotaResolverFunc(func(context.Context, string) (hosting.TenantQuota, error) {
			return hosting.TenantQuota{Tier: "enterprise", MaxSites: -1, MaxStorageBytes: -1}, nil
		}),
	}

	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)
	claimHash := hashToken("claim-token")
	siteRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", nil, nil, "active", false, "public",
			int32(1), "sites/demo/manifest.json", 10, 1, claimHash, nil, false, 0, expiresAt, now, now,
		)
	}
	fileRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"path", "r2_key", "content_type", "size_bytes", "sha256"}).
			AddRow("index.html", "sites/demo/v1/index.html", "text/html", 10, nil)
	}

	mock.ExpectQuery("SELECT id, slug, tenant_id").WithArgs("demo").WillReturnRows(siteRows())
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(total_bytes\\), 0\\)::bigint").
		WithArgs("site-1").
		WillReturnRows(sqlmock.NewRows([]string{"storage_bytes"}).AddRow(10))
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("hosting-quota:tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("COUNT\\(DISTINCT s.id\\)").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"sites", "storage_bytes"}).AddRow(0, 0))
	mock.ExpectQuery("SELECT f.path, f.r2_key").
		WithArgs("site-1", int32(1)).
		WillReturnRows(fileRows())
	mock.ExpectRollback()
	mock.ExpectQuery("SELECT id, slug, tenant_id").WithArgs("demo").WillReturnRows(siteRows())
	mock.ExpectQuery("SELECT f.path, f.r2_key").
		WithArgs("site-1", int32(1)).
		WillReturnRows(fileRows())

	err = s.bindClaimedSiteLocked(requestCtx, "demo", "claim-token", "tenant-1", "user-1")
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, error = %v; want Internal", connect.CodeOf(err), err)
	}
	if len(store.putBody) != 2 {
		t.Fatalf("manifest writes = %d, want ambiguous permanent write then anonymous restore: %v", len(store.putBody), err)
	}
	var restored hosting.Manifest
	if err := json.Unmarshal(store.putBody[1], &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.NoIndex || restored.ExpiresAt == nil || !restored.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("restored manifest = %+v, want anonymous expiry %s", restored, expiresAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
