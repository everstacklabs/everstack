package v1

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/jmoiron/sqlx"
)

type delayedAgentQueryBus struct {
	calls   int
	readyAt int
}

func (b *delayedAgentQueryBus) Execute(_ context.Context, q query.Query) (interface{}, error) {
	lookup, ok := q.(*agentsquery.GetAgentByIDQuery)
	if !ok || lookup.TenantID != "tenant-a" {
		return &query.Response{}, nil
	}
	b.calls++
	if b.readyAt > 0 && b.calls >= b.readyAt {
		return &query.Response{Data: &agentsquery.AgentDefinitionReadModel{ID: lookup.ID, TenantID: lookup.TenantID}}, nil
	}
	return &query.Response{}, nil
}

func (*delayedAgentQueryBus) RegisterHandler(query.QueryHandler) {}

func TestCreateAgentWaitsForReadableProjection(t *testing.T) {
	queries := &delayedAgentQueryBus{readyAt: 6}
	commands := &recordingCommandBus{}
	ctx := cqrs.WithSystem(verifiedTenantCtx("tenant-a"), &cqrs.System{CommandBus: commands, QueryBus: queries})
	response, err := (&Server{}).CreateAgent(ctx, connect.NewRequest(&agentspb.CreateAgentRequest{
		Name:  "waited-agent",
		Model: "test-model",
	}))
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if response.Msg.GetAgent() == nil || commands.dispatched != 1 || queries.calls < queries.readyAt {
		t.Fatalf("agent = %+v, dispatches = %d, query calls = %d", response.Msg.GetAgent(), commands.dispatched, queries.calls)
	}
}

func TestWaitForCreatedAgentStopsOnContextCancellation(t *testing.T) {
	queries := &delayedAgentQueryBus{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := (&Server{}).waitForCreatedAgent(ctx, &cqrs.System{QueryBus: queries}, "agent-1", "tenant-a")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

type cancelingCommandBus struct {
	cancel context.CancelFunc
}

func (b *cancelingCommandBus) Dispatch(context.Context, commands.Command) error {
	b.cancel()
	return nil
}

func (*cancelingCommandBus) RegisterHandler(commands.CommandHandler) {}

func TestCreateAgentConfirmsReservedSlotAfterRequestCancellation(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	mock.ExpectExec(`SELECT pg_advisory_lock`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	countQuery := `SELECT COUNT\(\*\) FROM agent_definitions\s+WHERE tenant_id = ANY\(\$1::uuid\[\]\) AND deleted_at IS NULL`
	mock.ExpectQuery(countQuery).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT EXISTS\s*\(\s*SELECT 1 FROM agent_definitions`).
		WithArgs("tenant-a", "cancelled-agent").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(countQuery).
		WithArgs(sqlmock.AnyArg()).
		WillDelayFor(100 * time.Millisecond).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`SELECT pg_advisory_unlock`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, cancel := context.WithCancel(verifiedTenantCtx("tenant-a"))
	queries := &delayedAgentQueryBus{readyAt: 1}
	commands := &cancelingCommandBus{cancel: cancel}
	ctx = cqrs.WithSystem(ctx, &cqrs.System{CommandBus: commands, QueryBus: queries})
	started := time.Now()
	response, err := (&Server{db: sqlx.NewDb(rawDB, "sqlmock")}).CreateAgent(ctx, connect.NewRequest(&agentspb.CreateAgentRequest{
		Name:  "cancelled-agent",
		Model: "test-model",
	}))
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if response.Msg.GetAgent() == nil {
		t.Fatal("CreateAgent returned no agent")
	}
	if elapsed := time.Since(started); elapsed < 75*time.Millisecond {
		t.Fatalf("slot confirmation stopped after %s when the request was canceled", elapsed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
