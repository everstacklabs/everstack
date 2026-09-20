package openaiapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	toolpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/tools"
	upstreampkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/upstream"
	"github.com/everstacklabs/everstack/internal/mcp"
	"github.com/everstacklabs/everstack/internal/services/encryption"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// TestLiveOpenAIAgentMCPRequiredAction is paid and opt-in. It proves that a
// real OpenAI required action crosses Everstack's tenant-scoped MCP registry
// and Streamable HTTP transport only after durable approval.
func TestLiveOpenAIAgentMCPRequiredAction(t *testing.T) {
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
	schema := "openai_live_mcp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

	const (
		marker   = "EVERSTACK_LIVE_MCP_RESULT_91D7"
		toolName = "everstack_live_mcp_marker"
	)
	var toolCalls atomic.Int32
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request mcp.JsonRpcRequest
		if decodeErr := json.NewDecoder(r.Body).Decode(&request); decodeErr != nil {
			http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			response["result"] = map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "live-mcp-fixture", "version": "1.0.0"},
			}
		case "notifications/initialized":
			response["result"] = map[string]any{}
		case "tools/list":
			response["result"] = map[string]any{"tools": []map[string]any{{
				"name": toolName, "description": "Return the exact qualification marker",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			}}}
		case "tools/call":
			var params mcp.ToolCallParams
			encoded, _ := json.Marshal(request.Params)
			if json.Unmarshal(encoded, &params) != nil || params.Name != toolName {
				http.Error(w, "unexpected MCP tool", http.StatusBadRequest)
				return
			}
			toolCalls.Add(1)
			response["result"] = map[string]any{"content": []map[string]string{{"type": "text", "text": marker}}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mcpServer.Close()

	owner := Ownership{OrganizationID: uuid.NewString(), InstanceID: uuid.NewString(), PrincipalID: "live-mcp-user", Role: "admin"}
	serverID := uuid.NewString()
	mcpRegistry := mcp.NewRegistry(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	registerCtx, registerCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer registerCancel()
	if _, err = mcpRegistry.Register(registerCtx, mcp.ServerConfig{
		ID: serverID, TenantID: owner.InstanceID, Name: "live-mcp-fixture",
		URL: mcpServer.URL, TransportType: mcp.TransportStreamableHTTP,
	}); err != nil {
		t.Fatal("register local MCP fixture failed")
	}
	defer mcpRegistry.Unregister(serverID)

	store, _ := NewPostgresStore(db)
	cipher, _ := encryption.NewService("isolated-live-mcp-encryption-key")
	toolStore, _ := toolpkg.NewPostgresStore(db, 2*time.Minute)
	approvalGate, _ := toolpkg.NewPostgresApprovalGate(db, 5*time.Minute)
	manager, _ := toolpkg.NewManager(toolStore, nil, mcpRegistry, approvalGate, 30*time.Second)
	tools := ToolsAdapter{Manager: manager, Approvals: approvalGate}
	service, err := NewService(store, cipher, UpstreamAdapter{Timeout: 2 * time.Minute}, nil, tools, Config{})
	if err != nil {
		t.Fatal("construct live MCP service failed")
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

	connection, err := service.CreateConnection(context.Background(), owner, CreateConnectionRequest{Name: "Live MCP qualification", APIKey: apiKey})
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
		Instructions: "You must call the provided everstack_live_mcp_marker tool exactly once. Do not answer from your own knowledge. After its result, reply with only the marker it returns.",
		Environment:  EnvironmentSpec{Type: "none"}, Input: json.RawMessage(`"Use the everstack_live_mcp_marker tool now. Do not reply until you have called it."`),
		Tools: []ToolSpec{{Type: "mcp", ResourceID: serverID, Name: toolName}},
	})
	if err != nil {
		t.Fatal("live MCP session creation failed")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if service.DeleteSession(ctx, owner, session.ID) != nil {
			t.Error("live MCP session cleanup failed")
		}
	}()

	deadline := time.Now().Add(2 * time.Minute)
	var approval Approval
	for time.Now().Before(deadline) {
		pending, listErr := service.ListApprovals(context.Background(), owner)
		if listErr != nil {
			t.Fatal("list live MCP approvals failed")
		}
		if len(pending) > 0 {
			approval = pending[0]
			break
		}
		time.Sleep(time.Second)
	}
	if approval.ID == "" {
		current, _ := service.GetSession(context.Background(), owner, session.ID)
		events, _ := service.ListEvents(context.Background(), owner, session.ID, 0, 500)
		counts := map[string]int{}
		assistant := ""
		for _, event := range events {
			counts[event.Type]++
			if strings.Contains(event.Type, "output_text") && len(assistant) < 500 {
				assistant += string(event.Payload)
			}
		}
		if len(assistant) > 500 {
			assistant = assistant[:500]
		}
		t.Fatalf("real MCP required action did not create an approval: status=%q readiness=%q last_error=%q event_types=%v assistant_output=%q", current.Status, current.Readiness, current.LastError, counts, assistant)
	}
	if toolCalls.Load() != 0 {
		t.Fatal("MCP tool executed before approval")
	}
	if err = service.ResolveApproval(context.Background(), owner, approval.ID, "approved"); err != nil {
		t.Fatal("approve and submit live MCP result failed")
	}
	if toolCalls.Load() != 1 {
		t.Fatalf("MCP tool executions=%d want=1", toolCalls.Load())
	}

	found := false
	// The provider may persist the final assistant item shortly after the
	// session first reports idle. Keep the qualification tied to Everstack's
	// durable history while allowing the reconciliation poll to observe it.
	deadline = time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		events, listErr := service.ListEvents(context.Background(), owner, session.ID, 0, 500)
		if listErr != nil {
			t.Fatal("list durable live MCP events failed")
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
		storedBeforeRefresh, storedErr := store.GetSession(context.Background(), owner, session.ID)
		current, currentErr := service.GetSession(context.Background(), owner, session.ID)
		remote, remoteErr := (UpstreamAdapter{Timeout: 30 * time.Second}).GetSession(context.Background(), ConnectionSecret{APIKey: apiKey}, session.UpstreamSessionID)
		counts := map[string]int{}
		assistant := ""
		var itemSummaries []map[string]any
		var localEvents []map[string]any
		events, _ := service.ListEvents(context.Background(), owner, session.ID, 0, 500)
		for _, event := range events {
			counts[event.Type]++
			localEvents = append(localEvents, map[string]any{"type": event.Type, "payload": redactEvidenceJSON(event.Payload)})
			var item struct {
				Type   string `json:"type"`
				Role   string `json:"role"`
				Status string `json:"status"`
			}
			_ = json.Unmarshal(event.Payload, &item)
			if strings.Contains(event.Type, "item") {
				itemSummaries = append(itemSummaries, map[string]any{"event_type": event.Type, "item_type": item.Type, "role": item.Role, "status": item.Status, "contains_marker": bytesContain(event.Payload, marker)})
			}
			if (strings.Contains(event.Type, "output_text") || item.Role == "assistant") && len(assistant) < 500 {
				assistant += string(event.Payload)
			}
		}
		if len(assistant) > 500 {
			assistant = assistant[:500]
		}
		var pendingActions []map[string]string
		remoteStatus := ""
		if remote != nil {
			remoteStatus = remote.Status
			var raw struct {
				RequiredActions []struct {
					TurnID string `json:"turn_id"`
					CallID string `json:"call_id"`
					Type   string `json:"type"`
					Name   string `json:"name"`
					State  string `json:"state"`
					Status string `json:"status"`
				} `json:"required_actions"`
			}
			if json.Unmarshal(remote.Raw, &raw) == nil {
				for _, action := range raw.RequiredActions {
					pendingActions = append(pendingActions, map[string]string{"turn_id": action.TurnID, "call_id": action.CallID, "type": action.Type, "name": action.Name, "state": defaultString(action.State, action.Status)})
				}
			}
		}
		localStatus, readiness, lastError := "", "", ""
		if current != nil {
			localStatus, readiness, lastError = current.Status, current.Readiness, current.LastError
		}
		var storedResult json.RawMessage
		_ = db.Get(&storedResult, `SELECT COALESCE(result_json,'{}'::jsonb) FROM openai_agent_tool_calls WHERE session_id=$1 AND call_id=$2`, session.ID, approval.CallID)
		var completed toolpkg.Result
		_ = json.Unmarshal(storedResult, &completed)
		submitted := map[string]any{"type": "agent.session.input.tool_result", "turn_id": completed.TurnID, "call_id": completed.CallID, "success": completed.Success}
		if completed.Success {
			submitted["output"] = completed.Output
		} else {
			submitted["error"] = completed.Error
		}
		var savedItems []any
		upstreamClient, upstreamClientErr := upstreampkg.NewClient(upstreampkg.Config{APIKey: apiKey, Timeout: 30 * time.Second})
		if upstreamClientErr == nil {
			after := ""
			for pageNumber := 0; pageNumber < 10; pageNumber++ {
				pageCtx, pageCancel := context.WithTimeout(context.Background(), 30*time.Second)
				page, pageErr := upstreamClient.ListItems(pageCtx, session.UpstreamSessionID, upstreampkg.ListOptions{After: after, Order: "asc", Limit: 100})
				pageCancel()
				if pageErr != nil {
					upstreamClientErr = pageErr
					break
				}
				for _, item := range page.Data {
					savedItems = append(savedItems, redactEvidenceJSON(item.Raw))
				}
				if !page.HasMore {
					break
				}
				after = page.LastID
				if after == "" {
					break
				}
			}
		}
		turnID := ""
		if remote != nil {
			turnID = currentTurnID(&upstreampkg.Session{Raw: remote.Raw})
		}
		var turnDetail any
		turnDetailErr := ""
		if turnID != "" {
			turnCtx, turnCancel := context.WithTimeout(context.Background(), 30*time.Second)
			req, reqErr := http.NewRequestWithContext(turnCtx, http.MethodGet, "https://api.openai.com/v1/agents/sessions/"+session.UpstreamSessionID+"/turns/"+turnID, nil)
			if reqErr == nil {
				req.Header.Set("Authorization", "Bearer "+apiKey)
				req.Header.Set("OpenAI-Beta", "agents=v1")
				resp, getErr := http.DefaultClient.Do(req)
				if getErr != nil {
					turnDetailErr = getErr.Error()
				} else {
					body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
					_ = resp.Body.Close()
					if readErr != nil {
						turnDetailErr = readErr.Error()
					} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
						turnDetailErr = "HTTP status " + resp.Status
					} else {
						turnDetail = redactEvidenceJSON(body)
					}
				}
			} else {
				turnDetailErr = reqErr.Error()
			}
			turnCancel()
		}
		evidence := map[string]any{
			"local_status": localStatus, "readiness": readiness, "last_error": lastError, "local_get_error": errorString(currentErr),
			"stored_session_before_refresh": storedBeforeRefresh, "stored_session_error": errorString(storedErr),
			"remote_status": remoteStatus, "remote_get_error": errorString(remoteErr), "pending_actions": pendingActions,
			"event_type_counts": counts, "item_summaries": itemSummaries, "local_events": localEvents, "assistant_output": assistant, "submitted_tool_result": submitted,
			"mcp_tool_calls": toolCalls.Load(),
			"saved_items":    savedItems, "saved_items_error": errorString(upstreamClientErr), "current_turn_id": turnID,
			"current_turn": turnDetail, "current_turn_error": turnDetailErr,
		}
		if encoded, marshalErr := json.MarshalIndent(evidence, "", "  "); marshalErr == nil {
			_ = os.WriteFile("/private/tmp/everstack-live-mcp-evidence.json", append(encoded, '\n'), 0o600)
		}
		t.Fatalf("assistant MCP marker was not retained: local_status=%q readiness=%q last_error=%q local_error=%v remote_status=%q remote_error=%v pending_actions=%v event_types=%v assistant_output=%q submitted=%v", localStatus, readiness, lastError, currentErr, remoteStatus, remoteErr, pendingActions, counts, assistant, submitted)
	}
	passedEvents, _ := service.ListEvents(context.Background(), owner, session.ID, 0, 500)
	localEvents := make([]map[string]any, 0, len(passedEvents))
	for _, event := range passedEvents {
		localEvents = append(localEvents, map[string]any{"type": event.Type, "payload": redactEvidenceJSON(event.Payload)})
	}
	passEvidence := map[string]any{"result": "passed", "mcp_tool_calls": toolCalls.Load(), "local_events": localEvents}
	if encoded, marshalErr := json.MarshalIndent(passEvidence, "", "  "); marshalErr == nil {
		_ = os.WriteFile("/private/tmp/everstack-live-mcp-evidence.json", append(encoded, '\n'), 0o600)
	}
	var status string
	if err = db.Get(&status, `SELECT status FROM openai_agent_tool_calls WHERE session_id=$1 AND call_id=$2`, session.ID, approval.CallID); err != nil || status != "completed" {
		t.Fatal("completed MCP result was not retained")
	}
	var approvalStatus string
	if err = db.Get(&approvalStatus, `SELECT status FROM openai_agent_tool_approvals WHERE id=$1`, approval.ID); err != nil || approvalStatus != "approved" {
		t.Fatal("MCP approval decision was not retained")
	}
	t.Log("real required action, approval, MCP transport, result submission, assistant marker, and cleanup verified")
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func redactEvidenceJSON(raw json.RawMessage) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "unparseable fixture payload"
	}
	var redact func(any) any
	redact = func(input any) any {
		switch typed := input.(type) {
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, child := range typed {
				lower := strings.ToLower(key)
				if strings.Contains(lower, "credential") || strings.Contains(lower, "token") || strings.Contains(lower, "api_key") || strings.Contains(lower, "authorization") || strings.Contains(lower, "url") {
					out[key] = "[redacted]"
				} else {
					out[key] = redact(child)
				}
			}
			return out
		case []any:
			out := make([]any, len(typed))
			for i, child := range typed {
				out[i] = redact(child)
			}
			return out
		default:
			return input
		}
	}
	return redact(value)
}
