package model_discovery

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/model_discovery/v1"
	"github.com/jmoiron/sqlx"
)

func TestHandlersRequireTenantContext(t *testing.T) {
	server := &Server{}
	tests := map[string]func() error{
		"SearchModels": func() error {
			_, err := server.SearchModels(context.Background(), connect.NewRequest(&providerspb.SearchModelsRequest{}))
			return err
		},
		"AddCustomModel": func() error {
			_, err := server.AddCustomModel(context.Background(), connect.NewRequest(&providerspb.AddCustomModelRequest{}))
			return err
		},
		"ListCustomModels": func() error {
			_, err := server.ListCustomModels(context.Background(), connect.NewRequest(&providerspb.ListCustomModelsRequest{}))
			return err
		},
		"DeleteCustomModel": func() error {
			_, err := server.DeleteCustomModel(context.Background(), connect.NewRequest(&providerspb.DeleteCustomModelRequest{}))
			return err
		},
		"UpdateCustomModel": func() error {
			_, err := server.UpdateCustomModel(context.Background(), connect.NewRequest(&providerspb.UpdateCustomModelRequest{}))
			return err
		},
		"GetCustomModel": func() error {
			_, err := server.GetCustomModel(context.Background(), connect.NewRequest(&providerspb.GetCustomModelRequest{}))
			return err
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			if code := connect.CodeOf(call()); code != connect.CodePermissionDenied {
				t.Fatalf("code = %v, want %v", code, connect.CodePermissionDenied)
			}
		})
	}
}

func TestSearchModelsScopesProviderConfigToTenant(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	server := &Server{providerRepo: provider_config.NewRepository(sqlx.NewDb(raw, "sqlmock"))}
	mock.ExpectQuery(`(?s)FROM provider_configurations\s+WHERE organization_id = \$1 AND provider_name = \$2`).
		WithArgs("tenant-a", "unsupported").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")
	_, err = server.SearchModels(ctx, connect.NewRequest(&providerspb.SearchModelsRequest{
		ProviderName:      "unsupported",
		UseProviderApiKey: true,
	}))
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want %v", code, connect.CodeInvalidArgument)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
