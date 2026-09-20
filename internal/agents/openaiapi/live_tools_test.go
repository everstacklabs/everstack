package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	toolpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/tools"
	functionexecutor "github.com/everstacklabs/everstack/internal/functions/executor"
	"github.com/everstacklabs/everstack/internal/services/encryption"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// approvalRequiredFunctions uses the production function adapter and executor,
// adding the policy decision that this live mutating fixture requires review.
type approvalRequiredFunctions struct{ *toolpkg.FunctionAdapter }

func (f approvalRequiredFunctions) ResolveFunction(ctx context.Context, owner toolpkg.Owner, id, name string) (toolpkg.Grant, error) {
	grant, err := f.FunctionAdapter.ResolveFunction(ctx, owner, id, name)
	grant.RequiresApproval = true
	return grant, err
}

// TestLiveOpenAIAgentFunctionRequiredAction is paid and opt-in. It exercises a
// real OpenAI session and the production webhook executor. Authentication is an
// explicit service fixture; this test is not evidence of deployed HTTP auth.
func TestLiveOpenAIAgentFunctionRequiredAction(t *testing.T) {
	if os.Getenv("EVS_TEST_OPENAI_AGENTS_LIVE") != "1" {
		t.Skip("explicit live API opt-in required")
	}
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	apiKey := os.Getenv("OPENAI_API_KEY")
	if dsn == "" || apiKey == "" {
		t.Skip("disposable PostgreSQL DSN and application key required")
	}

	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal("connect disposable database failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := "openai_live_tools_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = db.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal("create isolated schema failed")
	}
	defer db.Exec("DROP SCHEMA " + schema + " CASCADE")
	if _, err = db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatal("select isolated schema failed")
	}
	for _, migrationPath := range []string{
		"../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql",
		"../../database/migrations/sql/postgres/openai_agent_session_leases_20260911170000/up.sql",
		"../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql",
		"../../database/migrations/sql/postgres/openai_agent_tools_20260911010000/up.sql",
		"../../database/migrations/sql/postgres/openai_agent_tool_result_delivery_20260912000000/up.sql",
		"../../database/migrations/sql/postgres/openai_agents_usage_20260911160000/up.sql",
	} {
		migration, readErr := os.ReadFile(migrationPath)
		if readErr != nil {
			t.Fatal("read isolated schema migration failed")
		}
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatal("apply isolated schema migration failed")
		}
	}
	if _, err = db.Exec(`CREATE TABLE functions (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, name TEXT NOT NULL, description TEXT,
 mode TEXT NOT NULL, parameters JSONB NOT NULL, webhook_url TEXT,
 webhook_method TEXT, webhook_headers JSONB, webhook_timeout_ms INTEGER,
 timeout_ms INTEGER NOT NULL, max_retries INTEGER NOT NULL, enabled BOOLEAN NOT NULL
)`); err != nil {
		t.Fatal("create minimal native functions schema failed")
	}

	const marker = "EVERSTACK_LIVE_TOOL_RESULT_74C2"
	var calls atomic.Int32
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"marker": marker})
	}))
	defer webhook.Close()

	owner := Ownership{OrganizationID: uuid.NewString(), InstanceID: uuid.NewString(), PrincipalID: "live-tool-user", Role: "admin"}
	functionID := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO functions
 (id,tenant_id,name,description,mode,parameters,webhook_url,webhook_method,webhook_headers,webhook_timeout_ms,timeout_ms,max_retries,enabled)
 VALUES ($1,$2,'everstack_live_marker','Return the exact marker from the tool','webhook',$3,$4,'POST','{}',30000,30000,0,true)`,
		functionID, owner.InstanceID, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), webhook.URL); err != nil {
		t.Fatal("seed native function failed")
	}

	store, _ := NewPostgresStore(db)
	cipher, _ := encryption.NewService("isolated-live-tools-encryption-key")
	toolStore, _ := toolpkg.NewPostgresStore(db, 2*time.Minute)
	approvalGate, _ := toolpkg.NewPostgresApprovalGate(db, 5*time.Minute)
	registry := functionexecutor.NewRegistry()
	registry.RegisterExecutor(functionexecutor.NewWebhookExecutor())
	functionAdapter, _ := toolpkg.NewFunctionAdapter(db, registry)
	manager, _ := toolpkg.NewManager(toolStore, approvalRequiredFunctions{functionAdapter}, nil, approvalGate, 30*time.Second)
	tools := ToolsAdapter{Manager: manager, Approvals: approvalGate}
	service, err := NewService(store, cipher, UpstreamAdapter{Timeout: 2 * time.Minute}, nil, tools, Config{})
	if err != nil {
		t.Fatal("construct live tool service failed")
	}
	service.SetRecoveryAuthorizer(liveTestRecoveryAuthorizer{})
	service.SetApprovals(tools)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if service.Close(ctx) != nil {
			t.Error("supervisor shutdown failed")
		}
	}()

	connection, err := service.CreateConnection(context.Background(), owner, CreateConnectionRequest{Name: "Live tool qualification", APIKey: apiKey})
	if err != nil {
		t.Fatal("live connection validation failed")
	}
	defer service.DeleteConnection(context.Background(), owner, connection.ID)
	model := os.Getenv("EVS_TEST_OPENAI_AGENTS_MODEL")
	if model == "" {
		model = "gpt-6-astra"
	}
	session, err := service.CreateSession(context.Background(), owner, CreateSessionRequest{
		ConnectionID: connection.ID, IdempotencyKey: uuid.NewString(), Model: model,
		Instructions: "Call the only provided function exactly once. After its result, reply with only the marker it returns.",
		Environment:  EnvironmentSpec{Type: "none"}, Input: json.RawMessage(`"Call the required tool now."`),
		Tools: []ToolSpec{{Type: "function", ResourceID: functionID, Name: "everstack_live_marker"}},
	})
	if err != nil {
		t.Fatal("live tool session creation failed")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if service.DeleteSession(ctx, owner, session.ID) != nil {
			t.Error("live tool session cleanup failed")
		}
	}()

	deadline := time.Now().Add(2 * time.Minute)
	var approval Approval
	for time.Now().Before(deadline) {
		pending, listErr := service.ListApprovals(context.Background(), owner)
		if listErr != nil {
			t.Fatal("list live tool approvals failed")
		}
		if len(pending) > 0 {
			approval = pending[0]
			break
		}
		time.Sleep(time.Second)
	}
	if approval.ID == "" {
		t.Fatal("real required action did not create an approval")
	}
	if calls.Load() != 0 {
		t.Fatal("mutating webhook executed before approval")
	}
	if err = service.ResolveApproval(context.Background(), owner, approval.ID, "approved"); err != nil {
		t.Fatal("approve and submit live tool result failed")
	}
	if calls.Load() != 1 {
		t.Fatalf("webhook executions=%d want=1", calls.Load())
	}

	found := false
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		events, listErr := service.ListEvents(context.Background(), owner, session.ID, 0, 500)
		if listErr != nil {
			t.Fatal("list durable live tool events failed")
		}
		for _, event := range events {
			var payload struct {
				Role string `json:"role"`
				Item struct {
					Role string `json:"role"`
				} `json:"item"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			assistantOutput := strings.Contains(event.Type, "output_text") || payload.Role == "assistant" || payload.Item.Role == "assistant"
			if bytesContain(event.Payload, marker) && assistantOutput {
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(time.Second)
	}
	if !found {
		t.Fatal("assistant marker was not retained in durable event history")
	}
	var status string
	if err = db.Get(&status, `SELECT status FROM openai_agent_tool_calls WHERE session_id=$1 AND call_id=$2`, session.ID, approval.CallID); err != nil || status != "completed" {
		t.Fatal("completed tool result was not retained")
	}
	var approvalStatus string
	if err = db.Get(&approvalStatus, `SELECT status FROM openai_agent_tool_approvals WHERE id=$1`, approval.ID); err != nil || approvalStatus != "approved" {
		t.Fatal("approval decision was not retained")
	}
	t.Log("real required action, approval, webhook execution, result submission, assistant marker, and cleanup verified")
}

func bytesContain(payload json.RawMessage, marker string) bool {
	return strings.Contains(string(payload), marker)
}
