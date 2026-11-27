package v1

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/api/common"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/jmoiron/sqlx"
)

func TestCLIRefusesBotKeysInSQLAndContext(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	server := &CLIServer{db: sqlx.NewDb(raw, "sqlmock")}
	header := http.Header{}
	header.Set(common.EverstackApiKey, "sk-test")
	ctx := contextkeys.WithAPIKeyHashSecret(context.Background(), "test-secret")
	// SQL must filter before resolving identity; a revoked or restricted key
	// produces the same no-row answer, never a partially populated principal.
	mock.ExpectQuery(`FROM api_keys WHERE hash = \$1 AND NOT revoked AND surfaces IS NULL`).WillReturnError(sql.ErrNoRows)
	if _, err = server.resolveAPIKeyContext(ctx, header); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("SQL path: %v", err)
	}
	ctx = contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(ctx, "tenant", "digest", "bot-key"), []string{"inference", "mcp", "telemetry", "a2a"})
	ctx = contextkeys.WithUserID(ctx, "user")
	if _, err = server.resolveAPIKeyContext(ctx, header); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("context path: %v", err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
