package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/jmoiron/sqlx"
)

func expectMemorySetupQueries(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM pg_available_extensions").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER TABLE memory_embeddings ADD COLUMN IF NOT EXISTS embedding vector").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT EXISTS\\(").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	return sqlx.NewDb(rawDB, "sqlmock"), mock
}

func setupMemoryTestServer(db *sqlx.DB) *Server {
	registry := gw.NewRegistry()
	router := gw.NewRouter(registry, nil)
	srv := CreateServer()
	srv.db = db
	srv.engine = agentrt.NewEngine(registry, router, nil)
	return srv
}

func TestSetupMemoryDoesNotInstallProcessWideEmbedder(t *testing.T) {
	t.Run("connect", func(t *testing.T) {
		db, mock := expectMemorySetupQueries(t)
		srv := setupMemoryTestServer(db)

		_, err := srv.SetupMemory(context.Background(), connect.NewRequest(&agentspb.SetupMemoryRequest{}))
		if err != nil {
			t.Fatalf("SetupMemory: %v", err)
		}
		if srv.memoryStore == nil {
			t.Fatal("memory store was not installed")
		}
		if srv.memoryEmbedder != nil {
			t.Fatal("SetupMemory installed a process-wide embedder")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations: %v", err)
		}
	})

	t.Run("rest", func(t *testing.T) {
		db, mock := expectMemorySetupQueries(t)
		srv := setupMemoryTestServer(db)
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/agents/setup-memory", nil)

		srv.handleSetupMemoryConnect(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
		}
		if srv.memoryStore == nil {
			t.Fatal("memory store was not installed")
		}
		if srv.memoryEmbedder != nil {
			t.Fatal("SetupMemory installed a process-wide embedder")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations: %v", err)
		}
	})
}
