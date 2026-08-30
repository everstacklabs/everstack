package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func TestCreateDatasetVersion_PGSnapshotsActiveItems(t *testing.T) {
	db := newDatasetVersionTestDB(t)
	defer db.Close()

	ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")
	ctx = contextkeys.WithUserID(ctx, "user-a")

	if _, err := db.ExecContext(ctx, `
		INSERT INTO datasets (id, tenant_id, name)
		VALUES ('dataset-a', 'tenant-a', 'Golden dataset')
	`); err != nil {
		t.Fatalf("insert dataset: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO dataset_items (
			id, dataset_id, tenant_id, input, expected_output, metadata,
			source_trace_id, source_observation_id, status
		) VALUES
			('item-1', 'dataset-a', 'tenant-a', '{"prompt":"one"}', '{"answer":"one"}', '{"slot":1}', 'trace-1', 'obs-1', 'active'),
			('item-2', 'dataset-a', 'tenant-a', '{"prompt":"two"}', '{"answer":"two"}', '{"slot":2}', 'trace-2', 'obs-2', 'active'),
			('item-archived', 'dataset-a', 'tenant-a', '{"prompt":"archived"}', '{"answer":"archived"}', '{}', '', '', 'archived')
	`); err != nil {
		t.Fatalf("insert dataset items: %v", err)
	}

	server := &DatasetServer{db: db}
	resp, err := server.CreateDatasetVersion(ctx, connect.NewRequest(&datasetspb.CreateDatasetVersionRequest{
		TenantId:  "ignored-request-tenant",
		DatasetId: "dataset-a",
		Label:     "golden-v1",
		Note:      "before edits",
	}))
	if err != nil {
		t.Fatalf("CreateDatasetVersion: %v", err)
	}
	version := resp.Msg.GetVersion()
	if version.GetTenantId() != "tenant-a" || version.GetVersionNumber() != 1 ||
		version.GetItemCount() != 2 || version.GetCreatedBy() != "user-a" {
		t.Fatalf("unexpected version: %+v", version)
	}

	type snapshotItem struct {
		SourceDatasetItemID sql.NullString `db:"source_dataset_item_id"`
		Input               []byte         `db:"input"`
	}
	var items []snapshotItem
	if err := db.SelectContext(ctx, &items, `
		SELECT source_dataset_item_id, input
		FROM dataset_version_items
		WHERE dataset_version_id = $1 AND tenant_id = 'tenant-a'
		ORDER BY source_dataset_item_id
	`, version.GetId()); err != nil {
		t.Fatalf("select version items: %v", err)
	}
	if len(items) != 2 || !items[0].SourceDatasetItemID.Valid || items[0].SourceDatasetItemID.String != "item-1" ||
		!items[1].SourceDatasetItemID.Valid || items[1].SourceDatasetItemID.String != "item-2" {
		t.Fatalf("snapshot sources = %+v, want active item-1/item-2", items)
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE dataset_items
		SET input = '{"prompt":"mutated"}'
		WHERE id = 'item-1' AND tenant_id = 'tenant-a'
	`); err != nil {
		t.Fatalf("mutate live item: %v", err)
	}
	var frozenInput []byte
	if err := db.GetContext(ctx, &frozenInput, `
		SELECT input
		FROM dataset_version_items
		WHERE dataset_version_id = $1 AND source_dataset_item_id = 'item-1'
	`, version.GetId()); err != nil {
		t.Fatalf("select frozen input: %v", err)
	}
	var frozen map[string]string
	if err := json.Unmarshal(frozenInput, &frozen); err != nil {
		t.Fatalf("unmarshal frozen input: %v", err)
	}
	if frozen["prompt"] != "one" {
		t.Fatalf("snapshot mutated with live item: input=%s", string(frozenInput))
	}

	listResp, err := server.ListDatasetVersions(ctx, connect.NewRequest(&datasetspb.ListDatasetVersionsRequest{
		DatasetId: "dataset-a",
	}))
	if err != nil {
		t.Fatalf("ListDatasetVersions: %v", err)
	}
	if listResp.Msg.GetTotal() != 1 || len(listResp.Msg.GetVersions()) != 1 ||
		listResp.Msg.GetVersions()[0].GetId() != version.GetId() {
		t.Fatalf("unexpected list response: %+v", listResp.Msg)
	}

	getResp, err := server.GetDatasetVersion(ctx, connect.NewRequest(&datasetspb.GetDatasetVersionRequest{
		Id: version.GetId(),
	}))
	if err != nil {
		t.Fatalf("GetDatasetVersion: %v", err)
	}
	if getResp.Msg.GetVersion().GetId() != version.GetId() {
		t.Fatalf("unexpected get response: %+v", getResp.Msg)
	}

	foreignCtx := contextkeys.WithTenantID(context.Background(), "tenant-b")
	_, err = server.GetDatasetVersion(foreignCtx, connect.NewRequest(&datasetspb.GetDatasetVersionRequest{
		Id: version.GetId(),
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("foreign tenant err=%v, want not found", err)
	}
}

func newDatasetVersionTestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	dsn := os.Getenv("EVAL_RUNNER_PG_DSN")
	if dsn == "" {
		t.Skip("set EVAL_RUNNER_PG_DSN to run dataset version Postgres tests")
	}

	admin, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatalf("connect admin db: %v", err)
	}
	schema := "dataset_version_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		_ = admin.Close()
	})

	db, err := sqlx.Connect("pgx", datasetVersionTestDSNWithSearchPath(dsn, schema))
	if err != nil {
		t.Fatalf("connect schema db: %v", err)
	}
	setupDatasetVersionTestSchema(context.Background(), t, db)
	return db
}

func datasetVersionTestDSNWithSearchPath(dsn, schema string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		q := u.Query()
		q.Set("options", "-c search_path="+schema+",public")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return strings.TrimSpace(dsn) + " options='-c search_path=" + schema + ",public'"
}

func setupDatasetVersionTestSchema(ctx context.Context, t *testing.T, db *sqlx.DB) {
	t.Helper()

	_, err := db.ExecContext(ctx, `
		CREATE TABLE datasets (
			id VARCHAR(255) PRIMARY KEY,
			tenant_id VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			description TEXT DEFAULT '',
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			archived_at TIMESTAMPTZ
		);

		CREATE TABLE dataset_items (
			id VARCHAR(255) PRIMARY KEY,
			dataset_id VARCHAR(255) NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
			tenant_id VARCHAR(255) NOT NULL,
			input JSONB NOT NULL,
			expected_output JSONB,
			metadata JSONB DEFAULT '{}',
			source_trace_id VARCHAR(255) DEFAULT '',
			source_observation_id VARCHAR(255) DEFAULT '',
			status VARCHAR(50) NOT NULL DEFAULT 'active',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE dataset_versions (
			id TEXT PRIMARY KEY,
			dataset_id TEXT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
			tenant_id TEXT NOT NULL,
			version_number INT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			item_count INT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_by TEXT NOT NULL DEFAULT '',
			UNIQUE(dataset_id, version_number)
		);

		CREATE TABLE dataset_version_items (
			id TEXT PRIMARY KEY,
			dataset_version_id TEXT NOT NULL REFERENCES dataset_versions(id) ON DELETE CASCADE,
			tenant_id TEXT NOT NULL,
			source_dataset_item_id TEXT,
			input JSONB NOT NULL,
			expected_output JSONB,
			metadata JSONB DEFAULT '{}',
			source_trace_id TEXT DEFAULT '',
			source_observation_id TEXT DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	if err != nil {
		db.Close()
		t.Fatalf("setup schema: %v", err)
	}
}
