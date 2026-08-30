package revision

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestPostgresStoreGetForSessionPinsAndReturnsExactRevision(t *testing.T) {
	store, mock, closeDB := newRevisionStoreMock(t)
	defer closeDB()
	manifest := testStoredManifest(t, []byte("export default () => 'ok'\n"))

	mock.ExpectExec(`(?s)UPDATE agent_sessions AS session.*session\.revision_id IS NULL`).
		WithArgs("session-1", "tenant-1", "agent-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT revision_id FROM agent_sessions`).
		WithArgs("session-1", "tenant-1", "agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"revision_id"}).AddRow("revision-1"))
	expectRevisionRead(t, mock, manifest, manifest.Files[0].Content)

	revision, err := store.GetForSession(context.Background(), "tenant-1", "agent-1", "session-1")
	if err != nil {
		t.Fatalf("GetForSession() error = %v", err)
	}
	if revision.ID != "revision-1" || revision.Manifest.Digest != manifest.Digest {
		t.Fatalf("revision = %+v", revision)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresStoreRejectsRevisionContentThatDoesNotMatchDigest(t *testing.T) {
	store, mock, closeDB := newRevisionStoreMock(t)
	defer closeDB()
	manifest := testStoredManifest(t, []byte("export default () => 'ok'\n"))
	expectRevisionRead(t, mock, manifest, []byte("tampered\n"))

	_, err := store.Get(context.Background(), "tenant-1", "revision-1")
	if err == nil || !strings.Contains(err.Error(), "stored digest does not match") {
		t.Fatalf("Get() error = %v, want integrity failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func newRevisionStoreMock(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	return NewPostgresStore(sqlx.NewDb(raw, "sqlmock")), mock, func() { _ = raw.Close() }
}

func testStoredManifest(t *testing.T, content []byte) *Manifest {
	t.Helper()
	manifest, err := NewManifest([]File{{Path: "tool.ts", Content: content}}, []Function{{
		Name: "lookup", Path: "tool.ts", Export: "default", Runtime: "deno",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func expectRevisionRead(t *testing.T, mock sqlmock.Sqlmock, manifest *Manifest, content []byte) {
	t.Helper()
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`(?s)SELECT id, tenant_id, agent_id, revision_number, digest, manifest,.*FROM agent_revisions`).
		WithArgs("revision-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "agent_id", "revision_number", "digest", "manifest", "created_by", "created_at",
		}).AddRow("revision-1", "tenant-1", "agent-1", 1, manifest.Digest, manifestJSON, "user-1", time.Now()))
	file := manifest.Files[0]
	mock.ExpectQuery(`(?s)SELECT path, sha256, mode, size_bytes, content.*FROM agent_revision_files`).
		WithArgs("revision-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"path", "sha256", "mode", "size_bytes", "content"}).
			AddRow(file.Path, file.SHA256, file.Mode, file.Size, content))
}
