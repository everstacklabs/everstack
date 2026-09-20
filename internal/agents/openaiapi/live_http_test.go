package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/services/encryption"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type liveTestRecoveryAuthorizer struct{}

func (liveTestRecoveryAuthorizer) AuthorizeRecovery(_ context.Context, o Ownership) (Ownership, error) {
	return o, nil
}

// This test exercises the real HTTP service, SQL persistence and upstream API.
// Authentication context is a test fixture, not evidence of deployed auth.
func TestLiveHTTPAgentsJourney(t *testing.T) {
	if os.Getenv("EVS_TEST_OPENAI_AGENTS_LIVE") != "1" {
		t.Skip("explicit live API opt-in required")
	}
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL DSN required")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal("connect disposable database failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := "openai_live_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal("create isolated test schema failed")
	}
	defer db.Exec("DROP SCHEMA " + schema + " CASCADE")
	if _, err := db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	leaseMigration, err := os.ReadFile("../../database/migrations/sql/postgres/openai_agent_session_leases_20260911170000/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(leaseMigration)); err != nil {
		t.Fatal(err)
	}
	fenceMigration, err := os.ReadFile("../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(fenceMigration)); err != nil {
		t.Fatal(err)
	}
	store, _ := NewPostgresStore(db)
	cipher, _ := encryption.NewService("isolated-test-encryption-secret-not-for-production")
	service, err := NewService(store, cipher, UpstreamAdapter{Timeout: 90 * time.Second}, nil, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRecoveryAuthorizer(liveTestRecoveryAuthorizer{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error("supervisor shutdown failed")
		}
	}()
	handler := NewHTTPHandler(service)
	owner := Ownership{OrganizationID: "live-test-org", InstanceID: "live-test-instance", PrincipalID: "live-test-user", Role: "admin"}
	call := func(o Ownership, method, path string, body any, expected int, out any) {
		t.Helper()
		encoded, _ := json.Marshal(body)
		request := httptest.NewRequest(method, "/v1/openai-agents"+path, bytes.NewReader(encoded))
		ctx := contextkeys.WithTenantID(request.Context(), o.InstanceID)
		ctx = contextkeys.WithCloudUserID(ctx, o.PrincipalID)
		ctx = contextkeys.WithUserRole(ctx, o.Role)
		ctx = contextkeys.WithTenantAuthenticated(ctx)
		ctx = contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request.WithContext(ctx))
		if response.Code != expected {
			t.Fatalf("%s %s status=%d want=%d", method, path, response.Code, expected)
		}
		if out != nil && json.Unmarshal(response.Body.Bytes(), out) != nil {
			t.Fatal("invalid response JSON")
		}
	}
	var connection Connection
	call(owner, "POST", "/connections", CreateConnectionRequest{Name: "Live HTTP qualification", APIKey: os.Getenv("OPENAI_API_KEY")}, http.StatusCreated, &connection)
	if connection.Status != "connected" {
		t.Fatalf("connection validation status=%s", connection.Status)
	}
	defer service.DeleteConnection(context.Background(), owner, connection.ID)
	model := os.Getenv("EVS_TEST_OPENAI_AGENTS_MODEL")
	if model == "" {
		model = "gpt-6-astra"
	}
	var session Session
	input := CreateSessionRequest{ConnectionID: connection.ID, IdempotencyKey: uuid.NewString(), Model: model, Environment: EnvironmentSpec{Type: "none"}, Input: json.RawMessage(`"Reply with ready."`)}
	call(owner, "POST", "/sessions", input, http.StatusCreated, &session)
	if session.UpstreamSessionID == "" {
		t.Fatal("upstream session mapping missing")
	}
	defer func() {
		if err := service.DeleteSession(context.Background(), owner, session.ID); err != nil {
			t.Error("live session cleanup failed")
		}
	}()
	var replay Session
	call(owner, "POST", "/sessions", input, http.StatusCreated, &replay)
	if replay.ID != session.ID {
		t.Fatal("idempotent creation duplicated local session")
	}
	foreign := owner
	foreign.InstanceID = "foreign-instance"
	call(foreign, "GET", "/sessions/"+session.ID, nil, http.StatusNotFound, nil)
	viewer := owner
	viewer.Role = "viewer"
	call(viewer, "POST", "/sessions/"+session.ID+"/input", map[string]any{"idempotency_key": uuid.NewString(), "input": "denied"}, http.StatusForbidden, nil)
	deadline := time.Now().Add(90 * time.Second)
	for {
		var current Session
		call(owner, "GET", "/sessions/"+session.ID, nil, http.StatusOK, &current)
		if current.Status == "idle" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("initial session status=%s did not settle", current.Status)
		}
		time.Sleep(time.Second)
	}
	call(owner, "POST", "/sessions/"+session.ID+"/input", map[string]string{"idempotency_key": uuid.NewString(), "input": "Reply exactly EVERSTACK_HTTP_FOLLOWUP_OK."}, http.StatusAccepted, nil)
	deadline = time.Now().Add(90 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		var page struct {
			Data []Event `json:"data"`
		}
		call(owner, "GET", fmt.Sprintf("/sessions/%s/events?limit=500", session.ID), nil, http.StatusOK, &page)
		for _, event := range page.Data {
			var payload map[string]any
			_ = json.Unmarshal(event.Payload, &payload)
			item, _ := payload["item"].(map[string]any)
			assistantOutput := strings.Contains(event.Type, "output_text") || item["role"] == "assistant"
			if assistantOutput && bytes.Contains(event.Payload, []byte("EVERSTACK_HTTP_FOLLOWUP_OK")) {
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(time.Second)
	}
	if !found {
		t.Fatal("follow-up output did not reach durable HTTP event history")
	}
	t.Log("live HTTP connection/create/idempotent replay/foreign denial/follow-up/durable output verified")
}
