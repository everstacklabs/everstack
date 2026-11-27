package v1

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/jmoiron/sqlx"
)

type liveFunctionBackend struct {
	calls  int
	tenant string
}

func (b *liveFunctionBackend) Name() string                           { return "test" }
func (b *liveFunctionBackend) Start(context.Context) error            { return nil }
func (b *liveFunctionBackend) Stop(context.Context) error             { return nil }
func (b *liveFunctionBackend) SupportsRuntime(isolation.Runtime) bool { return true }
func (b *liveFunctionBackend) Stats() isolation.BackendStats          { return isolation.BackendStats{} }
func (b *liveFunctionBackend) Execute(_ context.Context, r isolation.ExecutionRequest) (*isolation.ExecutionResult, error) {
	b.calls++
	b.tenant = r.TenantID
	return &isolation.ExecutionResult{Success: true, Result: map[string]any{"ok": true}}, nil
}

type liveLoopProvider struct {
	*scriptedModel
	replies     []gw.ChatCompletionResponse
	requests    []gw.ChatCompletionRequest
	streamCalls int
}

func (p *liveLoopProvider) Chat(_ context.Context, r gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.requests = append(p.requests, r)
	reply := p.replies[0]
	p.replies = p.replies[1:]
	return reply, nil
}
func (p *liveLoopProvider) ChatStream(_ context.Context, r gw.ChatCompletionRequest, on func(gw.ChatResponseChunk) error) error {
	p.streamCalls++
	reply := p.replies[0]
	p.replies = p.replies[1:]
	for _, choice := range reply.Choices {
		if err := on(gw.ChatResponseChunk{ID: reply.ID, Model: r.Model, Choices: []gw.ChoiceDelta{{Index: choice.Index, Delta: choice.Message, FinishReason: choice.FinishReason}}}); err != nil {
			return err
		}
	}
	return on(gw.ChatResponseChunk{Usage: &reply.Usage})
}
func liveReply(names ...string) gw.ChatCompletionResponse {
	msg := gw.NewMessage(gw.RoleAssistant)
	for _, name := range names {
		msg.ToolCalls = append(msg.ToolCalls, gw.ToolCall{ID: name, Type: "function", Function: gw.ToolCallFunction{Name: name, Arguments: "{}"}})
	}
	finish := "tool_calls"
	if len(names) == 0 {
		msg = gw.NewMessage(gw.RoleAssistant, gw.Text("done"))
		finish = "stop"
	}
	return gw.ChatCompletionResponse{ID: "c", Model: "m-primary", Choices: []gw.Choice{{Message: msg, FinishReason: finish}}, Usage: gw.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}}
}
func TestLiveProcessorToolOwnership(t *testing.T) {
	const tenant = "a1111111-1111-4111-8111-111111111111"
	const query = `SELECT \* FROM functions\s+WHERE name = \$1 AND tenant_id = \$2 AND enabled = true`
	for _, mode := range []string{"client", "registered", "mixed", "missing tenant", "other tenant"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			names := []string{"weather"}
			if mode == "registered" {
				names = []string{"owned"}
			}
			if mode == "mixed" {
				names = []string{"owned", "weather"}
			}
			first := liveReply(names...)
			p := &liveLoopProvider{scriptedModel: &scriptedModel{name: "pp", model: "m-primary"}, replies: []gw.ChatCompletionResponse{first, liveReply()}}
			s := attemptServer(t, newAttemptSink(), nil, p.scriptedModel)
			s.defaultBundle.Load().reg.Register(p)
			backend := &liveFunctionBackend{}
			s.toolLoop = toolloop.NewLoopManagerWithBackend(sqlx.NewDb(db, "sqlmock"), true, backend)
			rows := func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{"id", "tenant_id", "name", "enabled", "mode", "runtime", "code"}).AddRow("fn", tenant, "owned", true, "isolated", "nodejs20", "return 1")
			}
			ctx := contextkeys.WithTenantID(context.Background(), tenant)
			if mode == "missing tenant" {
				ctx = context.Background()
			} else if mode == "other tenant" {
				ctx = contextkeys.WithTenantID(context.Background(), "b1111111-1111-4111-8111-111111111111")
				mock.ExpectQuery(query).WithArgs("weather", "b1111111-1111-4111-8111-111111111111").WillReturnError(sql.ErrNoRows)
			} else {
				for _, name := range names {
					expect := mock.ExpectQuery(query).WithArgs(name, tenant)
					if name == "owned" {
						expect.WillReturnRows(rows())
					} else {
						expect.WillReturnError(sql.ErrNoRows)
					}
				}
				if mode == "registered" {
					mock.ExpectQuery(query).WithArgs("owned", tenant).WillReturnRows(rows())
					mock.ExpectExec("INSERT INTO function_executions").WillReturnResult(sqlmock.NewResult(1, 1))
				}
			}
			var response *pb.ChatCompletionResponse
			if err := processChatCompletion(ctx, s, chatReq("m-primary", false), func(r *pb.ChatCompletionResponse) error { response = r; return nil }); err != nil {
				t.Fatal(err)
			}
			if mode == "registered" {
				if len(p.requests) != 2 || backend.calls != 1 || backend.tenant != tenant || response.Choices[0].FinishReason != "stop" {
					t.Fatal(len(p.requests), backend, response)
				}
				messages := p.requests[1].Messages
				if messages[len(messages)-1].Role != gw.RoleTool {
					t.Fatal(messages)
				}
			} else {
				if len(p.requests) != 1 || backend.calls != 0 || len(response.Choices[0].Message.ToolCalls) != len(names) || response.Choices[0].FinishReason != "tool_calls" {
					t.Fatal(len(p.requests), backend, response)
				}
			}
			if p.streamCalls != 0 {
				t.Fatal("explicit unary streamed upstream")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestLiveProcessorUnaryModesPreserveUsageAndCalls(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		p := &liveLoopProvider{scriptedModel: &scriptedModel{name: "pp", model: "m-primary"}, replies: []gw.ChatCompletionResponse{liveReply("weather")}}
		s := attemptServer(t, newAttemptSink(), nil, p.scriptedModel)
		s.defaultBundle.Load().reg.Register(p)
		req := chatReq("m-primary", false)
		if !explicit {
			req.Stream = nil
		}
		var calls int
		var usage *pb.Usage
		if err := processChatCompletion(contextkeys.WithTenantID(context.Background(), "tenant-1"), s, req, func(r *pb.ChatCompletionResponse) error {
			for _, c := range r.Choices {
				calls += len(c.Message.ToolCalls)
			}
			if r.Usage != nil {
				usage = r.Usage
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if calls != 1 || usage == nil || usage.PromptTokens != 11 || usage.CompletionTokens != 7 {
			t.Fatal(calls, usage)
		}
		if explicit && (len(p.requests) != 1 || p.streamCalls != 0) {
			t.Fatal("explicit false ignored")
		}
		if !explicit && (p.streamCalls != 1 || len(p.requests) != 0) {
			t.Fatal("omitted stream did not use feature default")
		}
	}
}
