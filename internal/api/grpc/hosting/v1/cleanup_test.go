package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/hosting"
	"github.com/everstacklabs/everstack/internal/hosting/moderation"
	"github.com/everstacklabs/everstack/internal/storage"
)

type cleanupObjectStore struct {
	deleted     []string
	failKey     string
	headSizes   map[string]int64
	listObjects []storage.BucketObject
	listErr     error
	putKeys     []string
	putBody     [][]byte
	putErrors   []error
	putHook     func(int)
}

type recordingPurger struct {
	slugs []string
	err   error
}

func (p *recordingPurger) PurgeSlug(_ context.Context, slug string) error {
	p.slugs = append(p.slugs, slug)
	return p.err
}

func expectInitialDeletingTransition(mock sqlmock.Sqlmock, siteID, slug string, generation int64, currentVersion any) {
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, slug, tenant_id[\\s\\S]+FOR UPDATE").
		WithArgs(siteID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			siteID, slug, "tenant-1", nil, "active", false, "public",
			currentVersion, hosting.ManifestKey(slug), 10, 1, nil,
			nil, false, generation, nil, now, now,
		))
	mock.ExpectQuery("UPDATE sites[\\s\\S]+moderation_generation = moderation_generation \\+ 1[\\s\\S]+RETURNING moderation_generation").
		WithArgs(siteID).
		WillReturnRows(sqlmock.NewRows([]string{"moderation_generation"}).AddRow(generation + 1))
	mock.ExpectExec("UPDATE site_moderation_actions[\\s\\S]+site deletion started").
		WithArgs(siteID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func (s *cleanupObjectStore) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, errors.New("not implemented")
}

func (s *cleanupObjectStore) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", errors.New("not implemented")
}

func (s *cleanupObjectStore) Put(_ context.Context, _, key, _ string, body io.Reader) (string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	s.putKeys = append(s.putKeys, key)
	s.putBody = append(s.putBody, data)
	index := len(s.putBody) - 1
	if s.putHook != nil {
		s.putHook(index)
	}
	if index < len(s.putErrors) && s.putErrors[index] != nil {
		return "", s.putErrors[index]
	}
	return "etag", nil
}

func (s *cleanupObjectStore) Delete(_ context.Context, _, key string) error {
	s.deleted = append(s.deleted, key)
	if key == s.failKey {
		return errors.New("delete failed")
	}
	return nil
}

func (s *cleanupObjectStore) Head(_ context.Context, _, key string) (int64, string, error) {
	if size, ok := s.headSizes[key]; ok {
		return size, "application/octet-stream", nil
	}
	return 0, "", errors.New("not implemented")
}

func (s *cleanupObjectStore) List(context.Context, string, string) ([]storage.BucketObject, error) {
	return s.listObjects, s.listErr
}

func TestAbandonPendingVersionDeletesObjectsAndReleasesReservation(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &cleanupObjectStore{}
	s := &Server{
		db:     db,
		store:  store,
		cfg:    Config{Bucket: "sites"},
		purger: hosting.NoopPurger{},
	}

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, slug, tenant_id").
		WithArgs("demo").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", "tenant-1", nil, "active", false, "public",
			nil, nil, 0, 0, nil, nil, false, 0, nil, now, nil,
		))
	mock.ExpectQuery("SELECT id, status").
		WithArgs("site-1", int32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow("version-1", "pending"))
	mock.ExpectQuery("SELECT r2_key FROM site_files").
		WithArgs("version-1").
		WillReturnRows(sqlmock.NewRows([]string{"r2_key"}).
			AddRow("sites/demo/v1/index.html").
			AddRow("sites/demo/v1/app.js"))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM site_versions").
		WithArgs("version-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM sites s").
		WithArgs("site-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := s.abandonPendingVersionLocked(context.Background(), "demo", 1); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	wantDeleted := []string{
		"sites/demo/manifest.json",
		"sites/demo/v1/index.html",
		"sites/demo/v1/app.js",
	}
	if len(store.deleted) != len(wantDeleted) {
		t.Fatalf("deleted = %v, want %v", store.deleted, wantDeleted)
	}
	for i := range wantDeleted {
		if store.deleted[i] != wantDeleted[i] {
			t.Fatalf("deleted = %v, want %v", store.deleted, wantDeleted)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonPendingVersionRetainsReservationWhenObjectDeleteFails(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	objectKey := "sites/demo/v1/index.html"
	purger := &recordingPurger{}
	s := &Server{
		db:     db,
		store:  &cleanupObjectStore{failKey: objectKey},
		cfg:    Config{Bucket: "sites"},
		purger: purger,
	}

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, slug, tenant_id").
		WithArgs("demo").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", "tenant-1", nil, "active", false, "public",
			nil, nil, 0, 0, nil, nil, false, 0, nil, now, nil,
		))
	mock.ExpectQuery("SELECT id, status").
		WithArgs("site-1", int32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow("version-1", "pending"))
	mock.ExpectQuery("SELECT r2_key FROM site_files").
		WithArgs("version-1").
		WillReturnRows(sqlmock.NewRows([]string{"r2_key"}).AddRow(objectKey))

	if err := s.abandonPendingVersionLocked(context.Background(), "demo", 1); err == nil {
		t.Fatal("object delete failure should retain the DB reservation")
	}
	if len(purger.slugs) != 1 || purger.slugs[0] != "demo" {
		t.Fatalf("purged slugs = %v, want manifest repair purged before object cleanup", purger.slugs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonPendingVersionPreservesDeletionTombstone(t *testing.T) {
	for _, siteStatus := range []string{"deleting", "deleted"} {
		t.Run(siteStatus, func(t *testing.T) {
			rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
			if err != nil {
				t.Fatal(err)
			}
			defer rawDB.Close()
			db := sqlx.NewDb(rawDB, "sqlmock")
			store := &cleanupObjectStore{}
			s := &Server{db: db, store: store, cfg: Config{Bucket: "sites"}, purger: hosting.NoopPurger{}}

			now := time.Now().UTC()
			mock.ExpectQuery("SELECT id, slug, tenant_id").
				WithArgs("demo").
				WillReturnRows(sqlmock.NewRows([]string{
					"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
					"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
					"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
				}).AddRow(
					"site-1", "demo", "tenant-1", nil, siteStatus, false, "public",
					nil, nil, 0, 0, nil, nil, false, 0, nil, now, nil,
				))
			mock.ExpectQuery("SELECT id, status").
				WithArgs("site-1", int32(1)).
				WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow("version-1", "pending"))
			mock.ExpectQuery("SELECT r2_key FROM site_files").
				WithArgs("version-1").
				WillReturnRows(sqlmock.NewRows([]string{"r2_key"}).AddRow("sites/demo/v1/index.html"))
			mock.ExpectBegin()
			mock.ExpectExec("DELETE FROM site_versions").
				WithArgs("version-1").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("(?s)DELETE FROM sites s.*s.status NOT IN \\('deleted', 'deleting'\\)").
				WithArgs("site-1").
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("(?s)UPDATE sites.*status NOT IN \\('deleted', 'deleting'\\)").
				WithArgs("site-1").
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()

			if err := s.abandonPendingVersionLocked(context.Background(), "demo", 1); err != nil {
				t.Fatalf("cleanup %s site's pending version: %v", siteStatus, err)
			}
			wantDeleted := []string{hosting.ManifestKey("demo"), "sites/demo/v1/index.html"}
			if len(store.deleted) != len(wantDeleted) {
				t.Fatalf("deleted objects = %v, want %v", store.deleted, wantDeleted)
			}
			for i := range wantDeleted {
				if store.deleted[i] != wantDeleted[i] {
					t.Fatalf("deleted objects = %v, want %v", store.deleted, wantDeleted)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRepairFailedActivationRestoresProjectionWithoutReleasingReservation(t *testing.T) {
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
			"site-1", "demo", "tenant-1", nil, "active", false, "public",
			int32(1), "sites/demo/manifest.json", 10, 1, nil, nil, false, 0, nil, now, now,
		))
	mock.ExpectQuery("SELECT f.path, f.r2_key").
		WithArgs("site-1", int32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"path", "r2_key", "content_type", "size_bytes", "sha256"}).
			AddRow("index.html", "sites/demo/v1/index.html", "text/html", 10, nil))
	mock.ExpectExec("pg_advisory_unlock").
		WithArgs("demo").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.repairFailedActivation(context.Background(), "demo"); err != nil {
		t.Fatalf("repair failed activation: %v", err)
	}
	if len(store.putBody) != 1 || len(store.deleted) != 0 {
		t.Fatalf("manifest writes=%d object deletes=%v, want one repair and no reservation cleanup", len(store.putBody), store.deleted)
	}
	if len(purger.slugs) != 1 || purger.slugs[0] != "demo" {
		t.Fatalf("purged slugs = %v", purger.slugs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReapStalePendingUsesBoundedExpiryQuery(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	s := &Server{db: db, store: &cleanupObjectStore{}}

	mock.ExpectQuery("(?s)s.status = 'deleted' AND v.status = 'finalized'.*v.created_at < \\$1.*cleanup_attempted_at ASC NULLS FIRST").
		WithArgs(sqlmock.AnyArg(), 12).
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "version"}))

	cleaned, err := s.ReapStalePending(context.Background(), 12)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if cleaned != 0 {
		t.Fatalf("cleaned = %d, want 0", cleaned)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileDeletingSitesUsesBoundedOldestFirstQuery(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	s := &Server{db: db, store: &cleanupObjectStore{}}

	mock.ExpectQuery("(?s)FROM sites.*WHERE status = 'deleting'.*ORDER BY updated_at ASC.*LIMIT \\$1").
		WithArgs(12).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}))

	completed, err := s.ReconcileDeletingSites(context.Background(), 12)
	if err != nil {
		t.Fatalf("reconcile deleting sites: %v", err)
	}
	if completed != 0 {
		t.Fatalf("completed = %d, want 0", completed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMarkCleanupAttemptMovesFailedCandidateBehindUnattemptedWork(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	s := &Server{db: db}

	mock.ExpectExec("UPDATE site_versions SET cleanup_attempted_at = NOW\\(\\) WHERE id = \\$1").
		WithArgs("version-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.markCleanupAttempt(context.Background(), "version-1"); err != nil {
		t.Fatalf("mark cleanup attempt: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonDeletedFinalizedVersionReleasesQuotaAfterURLExpiry(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &cleanupObjectStore{}
	s := &Server{db: db, store: store, cfg: Config{Bucket: "sites"}, purger: hosting.NoopPurger{}}

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, slug, tenant_id").
		WithArgs("demo").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", "tenant-1", nil, "deleted", false, "public",
			int32(1), nil, 10, 1, nil, nil, false, 0, nil, now, now,
		))
	mock.ExpectQuery("SELECT id, status").
		WithArgs("site-1", int32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow("version-1", "finalized"))
	mock.ExpectQuery("SELECT r2_key FROM site_files").
		WithArgs("version-1").
		WillReturnRows(sqlmock.NewRows([]string{"r2_key"}).AddRow("sites/demo/v1/index.html"))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE site_versions SET status = 'deleted'").
		WithArgs("version-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := s.abandonPendingVersionLocked(context.Background(), "demo", 1); err != nil {
		t.Fatalf("release deleted finalized version: %v", err)
	}
	wantDeleted := []string{hosting.ManifestKey("demo"), "sites/demo/v1/index.html"}
	if len(store.deleted) != len(wantDeleted) {
		t.Fatalf("deleted = %v, want %v", store.deleted, wantDeleted)
	}
	for i := range wantDeleted {
		if store.deleted[i] != wantDeleted[i] {
			t.Fatalf("deleted = %v, want %v", store.deleted, wantDeleted)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteOwnedSiteRetainsQuotaWhenObjectCleanupFails(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	objectKey := "sites/demo/v1/index.html"
	store := &cleanupObjectStore{
		failKey: objectKey,
		listObjects: []storage.BucketObject{
			{Key: hosting.ManifestKey("demo")},
			{Key: objectKey},
		},
	}
	s := &Server{
		db:     db,
		store:  store,
		cfg:    Config{Bucket: "sites"},
		purger: hosting.NoopPurger{},
	}
	expectInitialDeletingTransition(mock, "site-1", "demo", 0, nil)

	err = s.deleteOwnedSite(context.Background(), &siteRow{ID: "site-1", Slug: "demo"})
	if err == nil {
		t.Fatal("delete should fail while a retained object still consumes quota")
	}
	if len(store.deleted) != 2 || store.deleted[0] != hosting.ManifestKey("demo") || store.deleted[1] != objectKey {
		t.Fatalf("delete attempts = %v", store.deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteOwnedSiteReleasesOnlyFinalizedQuotaAfterObjectCleanup(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	objectKey := "sites/demo/v1/index.html"
	store := &cleanupObjectStore{
		listObjects: []storage.BucketObject{
			{Key: hosting.ManifestKey("demo")},
			{Key: objectKey},
		},
	}
	putPrecededManifestDelete := false
	store.putHook = func(_ int) {
		putPrecededManifestDelete = len(store.deleted) == 0
	}
	s := &Server{
		db:     db,
		store:  store,
		cfg:    Config{Bucket: "sites"},
		purger: hosting.NoopPurger{},
	}

	expectInitialDeletingTransition(mock, "site-1", "demo", 0, int32(1))
	mock.ExpectQuery("SELECT f.path, f.r2_key, f.content_type, f.size_bytes, f.sha256").
		WithArgs("site-1", int32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"path", "r2_key", "content_type", "size_bytes", "sha256"}).
			AddRow("index.html", objectKey, "text/html", int64(10), nil))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE sites").
		WithArgs("site-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE site_versions").
		WithArgs("site-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE site_moderation_actions").
		WithArgs("site-1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	version := int32(1)
	if err := s.deleteOwnedSite(context.Background(), &siteRow{
		ID: "site-1", Slug: "demo", Status: "active", Access: "public", CurrentVersion: &version,
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !putPrecededManifestDelete {
		t.Fatal("deletion must project a disabled manifest before removing it")
	}
	if len(store.putBody) != 1 {
		t.Fatalf("manifest writes = %d, want 1", len(store.putBody))
	}
	var manifest hosting.Manifest
	if err := json.Unmarshal(store.putBody[0], &manifest); err != nil {
		t.Fatalf("decode deleting manifest: %v", err)
	}
	if manifest.Status != "deleting" {
		t.Fatalf("manifest status = %q, want deleting (non-active)", manifest.Status)
	}
	if len(store.deleted) != 2 || store.deleted[0] != hosting.ManifestKey("demo") || store.deleted[1] != objectKey {
		t.Fatalf("deleted objects = %v", store.deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteOwnedSiteLeavesDisabledManifestWhenManifestRemovalFails(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	manifestKey := hosting.ManifestKey("demo")
	store := &cleanupObjectStore{failKey: manifestKey}
	s := &Server{
		db: db, store: store, cfg: Config{Bucket: "sites"}, purger: hosting.NoopPurger{},
	}

	expectInitialDeletingTransition(mock, "site-1", "demo", 7, int32(1))
	mock.ExpectQuery("SELECT f.path, f.r2_key, f.content_type, f.size_bytes, f.sha256").
		WithArgs("site-1", int32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"path", "r2_key", "content_type", "size_bytes", "sha256"}).
			AddRow("index.html", "sites/demo/v1/index.html", "text/html", int64(10), nil))

	err = s.deleteOwnedSite(context.Background(), &siteRow{
		ID: "site-1", Slug: "demo", Status: "active", Access: "public", CurrentVersion: nil,
	})
	if err == nil {
		t.Fatal("manifest removal failure should queue deletion for retry")
	}
	if len(store.putBody) != 1 {
		t.Fatalf("manifest writes = %d, want one disabled projection", len(store.putBody))
	}
	var manifest hosting.Manifest
	if err := json.Unmarshal(store.putBody[0], &manifest); err != nil {
		t.Fatalf("decode deleting manifest: %v", err)
	}
	if manifest.Status == "active" {
		t.Fatalf("failed manifest removal left an active projection: %+v", manifest)
	}
	if manifest.ModerationGeneration != 8 {
		t.Fatalf("deleting manifest generation = %d, want 8 after superseding generation 7", manifest.ModerationGeneration)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeletingRetryPreservesGenerationAndPendingTakedown(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	s := &Server{db: sqlx.NewDb(rawDB, "sqlmock")}

	mock.ExpectBegin()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, slug, tenant_id[\\s\\S]+FOR UPDATE").
		WithArgs("site-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", "tenant-1", nil, "deleting", false, "public",
			int32(1), hosting.ManifestKey("demo"), 10, 1, nil,
			nil, false, int64(8), nil, now, now,
		))
	mock.ExpectExec("SET claim_token_hash = NULL, updated_at = NOW\\(\\)").
		WithArgs("site-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	site := &siteRow{ID: "site-1", Status: "deleting", ModerationGeneration: 8}
	continuing, err := s.transitionSiteToDeleting(context.Background(), site)
	if err != nil {
		t.Fatalf("retry deleting transition: %v", err)
	}
	if !continuing || site.ModerationGeneration != 8 {
		t.Fatalf("continuing=%v generation=%d, want true/8", continuing, site.ModerationGeneration)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestModerationTakedownCanProjectADeletingSite(t *testing.T) {
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
			"site-1", "demo", "tenant-1", nil, "deleting", false, "public",
			int32(1), hosting.ManifestKey("demo"), 10, 1, nil,
			nil, true, int64(8), nil, now, now,
		))
	mock.ExpectQuery("SELECT f.path, f.r2_key, f.content_type, f.size_bytes, f.sha256").
		WithArgs("site-1", int32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"path", "r2_key", "content_type", "size_bytes", "sha256"}).
			AddRow("index.html", "sites/demo/v1/index.html", "text/html", int64(10), nil))

	if err := s.applyModerationManifest(context.Background(), moderation.Action{
		SiteID: "site-1", Slug: "demo", Generation: 8, Kind: moderation.ActionKindTakedown,
	}); err != nil {
		t.Fatalf("project deleting-site takedown: %v", err)
	}
	if len(store.putBody) != 1 {
		t.Fatalf("manifest writes = %d, want 1", len(store.putBody))
	}
	var manifest hosting.Manifest
	if err := json.Unmarshal(store.putBody[0], &manifest); err != nil {
		t.Fatalf("decode moderation manifest: %v", err)
	}
	if manifest.Status != "deleting" || manifest.ModerationGeneration != 8 {
		t.Fatalf("moderation manifest = %+v", manifest)
	}
	if len(purger.slugs) != 1 || purger.slugs[0] != "demo" {
		t.Fatalf("purged slugs = %v", purger.slugs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivateVersionRejectsDeletedOrDeletingSiteUnderProjectionLock(t *testing.T) {
	for _, status := range []string{"deleting", "deleted"} {
		t.Run(status, func(t *testing.T) {
			rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
			if err != nil {
				t.Fatal(err)
			}
			defer rawDB.Close()
			db := sqlx.NewDb(rawDB, "sqlmock")
			store := &cleanupObjectStore{}
			s := &Server{db: db, store: store, cfg: Config{Bucket: "sites"}, purger: hosting.NoopPurger{}}

			mock.ExpectBegin()
			mock.ExpectExec("pg_advisory_xact_lock").
				WithArgs("demo").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT s.current_version").
				WithArgs("version-2").
				WillReturnRows(sqlmock.NewRows([]string{
					"current_version", "spa_fallback", "access", "expires_at", "claim_token_hash", "status", "moderation_generation",
				}).AddRow(int32(1), false, "public", nil, nil, status, 0))
			mock.ExpectRollback()

			_, _, err = s.activateVersion(context.Background(), &siteRow{ID: "site-1", Slug: "demo"}, "version-2", 2, nil)
			if !errors.Is(err, errSiteDeletedDuringActivation) {
				t.Fatalf("error = %v, want deletion-state activation rejection", err)
			}
			if len(store.putBody) != 0 {
				t.Fatalf("manifest writes = %d, want none", len(store.putBody))
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRepairDeletingSiteRemovesManifestInsteadOfRewritingIt(t *testing.T) {
	store := &cleanupObjectStore{}
	purger := &recordingPurger{}
	s := &Server{store: store, cfg: Config{Bucket: "sites"}, purger: purger}
	version := int32(2)

	if err := s.repairSiteProjection(context.Background(), &siteRow{
		Slug:           "demo",
		Status:         "deleting",
		CurrentVersion: &version,
	}); err != nil {
		t.Fatalf("repair deleting projection: %v", err)
	}
	if len(store.putBody) != 0 {
		t.Fatalf("manifest rewrites = %d, want none", len(store.putBody))
	}
	if len(store.deleted) != 1 || store.deleted[0] != hosting.ManifestKey("demo") {
		t.Fatalf("manifest deletes = %v", store.deleted)
	}
	if len(purger.slugs) != 1 || purger.slugs[0] != "demo" {
		t.Fatalf("purged slugs = %v", purger.slugs)
	}
}
