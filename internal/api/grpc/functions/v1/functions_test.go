package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	querypkg "github.com/everstacklabs/everstack/internal/query"
	functionspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1"
)

type missingFunctionQueryHandler struct{}

func (missingFunctionQueryHandler) QueryType() string { return "GetFunctionByName" }

func (missingFunctionQueryHandler) Handle(context.Context, querypkg.Query) (interface{}, error) {
	return nil, nil
}

type missingFunctionByIDQueryHandler struct{}

func (missingFunctionByIDQueryHandler) QueryType() string { return "GetFunctionByID" }

func (missingFunctionByIDQueryHandler) Handle(context.Context, querypkg.Query) (interface{}, error) {
	return nil, nil
}

func functionQueryContext(handlers ...querypkg.QueryHandler) context.Context {
	queryBus := querypkg.NewQueryBus()
	for _, handler := range handlers {
		queryBus.RegisterHandler(handler)
	}
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	return cqrs.WithSystem(ctx, &cqrs.System{QueryBus: queryBus})
}

func TestGetFunctionByNameReturnsNotFoundForMissingFunction(t *testing.T) {
	ctx := functionQueryContext(missingFunctionQueryHandler{})

	_, err := CreateServer().GetFunctionByName(ctx, connect.NewRequest(&functionspb.GetFunctionByNameRequest{
		Name: "web_search",
	}))
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("GetFunctionByName() code = %v, want %v (err = %v)", got, connect.CodeNotFound, err)
	}
}

func TestGetFunctionReturnsNotFoundForMissingFunction(t *testing.T) {
	ctx := functionQueryContext(missingFunctionByIDQueryHandler{})

	_, err := CreateServer().GetFunction(ctx, connect.NewRequest(&functionspb.GetFunctionRequest{
		Id: "function-1",
	}))
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("GetFunction() code = %v, want %v (err = %v)", got, connect.CodeNotFound, err)
	}
}
