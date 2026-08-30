package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	querypkg "github.com/everstacklabs/everstack/internal/query"
	storagequery "github.com/everstacklabs/everstack/internal/query/handlers/storage"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	storagepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1"
)

type recordingManagedResolver struct {
	tenantID   string
	cellID     string
	pathPrefix string
	store      storagepkg.ObjectStore
	err        error
}

type recordingManagedEnsurer struct {
	tenantIDs  []string
	connection *storagepkg.ManagedConnection
	err        error
}

func (e *recordingManagedEnsurer) EnsureDefault(_ context.Context, tenantID string) (*storagepkg.ManagedConnection, error) {
	e.tenantIDs = append(e.tenantIDs, tenantID)
	return e.connection, e.err
}

type fixedStorageConfigListHandler struct {
	configs []storagequery.StorageConfigReadModel
}

func (h fixedStorageConfigListHandler) QueryType() string { return "ListStorageConfigs" }
func (h fixedStorageConfigListHandler) Handle(context.Context, querypkg.Query) (interface{}, error) {
	return h.configs, nil
}

func (r *recordingManagedResolver) ResolveManagedStore(_ context.Context, connection storagepkg.ManagedConnection) (storagepkg.ObjectStore, error) {
	r.tenantID = connection.TenantID
	r.cellID = connection.CellID
	r.pathPrefix = connection.PathPrefix
	return r.store, r.err
}

func TestManagedStorageConfigResponseRedactsPhysicalPlacement(t *testing.T) {
	rm := &storagequery.StorageConfigReadModel{
		ID: "managed-config", TenantID: "tenant-a", Provider: storagepkg.ProviderEverstack,
		Endpoint: "https://physical.example", Region: "auto", Bucket: "physical-bucket",
		PathPrefix: "should-not-leak", CredentialRef: "storagecred_should_not_leak",
		ManagementMode: storagepkg.ManagementSystem, ManagedCellID: "r2-eu-production",
		ManagedPathPrefix: "tenants/opaque", IsDefault: true, Enabled: true,
	}

	got := configReadModelToProto(rm)
	if got.GetProvider() != storagepb.StorageProvider_STORAGE_PROVIDER_EVERSTACK || !got.GetSystemManaged() {
		t.Fatalf("managed response provider=%v system_managed=%t", got.GetProvider(), got.GetSystemManaged())
	}
	if got.GetEndpoint() != "" || got.GetRegion() != "" || got.GetBucket() != "" || got.GetPathPrefix() != "" {
		t.Fatalf("managed response exposes physical placement: %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"physical.example", "physical-bucket", "should-not-leak", "r2-eu-production", "tenants/opaque", "storagecred_should_not_leak"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("managed response exposes %q: %s", forbidden, encoded)
		}
	}
}

func TestGetStoreForManagedConfigUsesInternalResolverWithoutTenantCredentials(t *testing.T) {
	ctx, _ := storageCommandAndConfigContext(t, &storagequery.StorageConfigReadModel{
		ID: "managed-config", TenantID: "tenant-1", Provider: storagepkg.ProviderEverstack,
		ManagementMode: storagepkg.ManagementSystem, ManagedCellID: "r2-eu-production",
		ManagedPathPrefix: "tenants/opaque", IsDefault: true, Enabled: true,
	})
	resolver := &recordingManagedResolver{store: inertObjectStore{}}
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, nil, nil)
	server.SetManagedStorage(nil, resolver)

	store, config, err := server.getStoreForConfig(ctx, "managed-config", "tenant-1")
	if err != nil {
		t.Fatalf("getStoreForConfig() error = %v", err)
	}
	if store == nil || config == nil {
		t.Fatalf("getStoreForConfig() store=%v config=%#v", store, config)
	}
	if resolver.tenantID != "tenant-1" || resolver.cellID != "r2-eu-production" || resolver.pathPrefix != "tenants/opaque" {
		t.Fatalf("resolver placement = tenant %q cell %q prefix %q", resolver.tenantID, resolver.cellID, resolver.pathPrefix)
	}
}

func TestManagedStoreResolutionFailsClosedWithoutLeakingPlacement(t *testing.T) {
	tests := []struct {
		name     string
		config   storagequery.StorageConfigReadModel
		resolver *recordingManagedResolver
		wantCode connect.Code
	}{
		{
			name: "cross-tenant placement",
			config: storagequery.StorageConfigReadModel{
				ID: "managed-config", TenantID: "tenant-2", Provider: storagepkg.ProviderEverstack,
				ManagementMode: storagepkg.ManagementSystem, ManagedCellID: "r2-eu-production",
				ManagedPathPrefix: "tenants/opaque", IsDefault: true, Enabled: true,
			},
			resolver: &recordingManagedResolver{store: inertObjectStore{}},
			wantCode: connect.CodeInternal,
		},
		{
			name: "cell unavailable",
			config: storagequery.StorageConfigReadModel{
				ID: "managed-config", TenantID: "tenant-1", Provider: storagepkg.ProviderEverstack,
				ManagementMode: storagepkg.ManagementSystem, ManagedCellID: "r2-eu-production",
				ManagedPathPrefix: "tenants/opaque", IsDefault: true, Enabled: true,
			},
			resolver: &recordingManagedResolver{err: errors.New("physical endpoint secret")},
			wantCode: connect.CodeUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := storageCommandAndConfigContext(t, &test.config)
			server := CreateServerWithSecurityDeps(context.Background(), nil, nil, nil, nil)
			server.SetManagedStorage(nil, test.resolver)

			_, _, err := server.getStoreForConfig(ctx, test.config.ID, "tenant-1")
			if connect.CodeOf(err) != test.wantCode {
				t.Fatalf("getStoreForConfig() code = %v, want %v (err=%v)", connect.CodeOf(err), test.wantCode, err)
			}
			if strings.Contains(err.Error(), "physical endpoint secret") || strings.Contains(err.Error(), "r2-eu-production") {
				t.Fatalf("getStoreForConfig() leaked placement details: %v", err)
			}
			if test.name == "cross-tenant placement" && test.resolver.tenantID != "" {
				t.Fatalf("resolver called for cross-tenant placement: tenant=%q", test.resolver.tenantID)
			}
		})
	}
}

func TestManagedDefaultTakesPrecedenceOverLegacyCloudStore(t *testing.T) {
	managedConfig := storagequery.StorageConfigReadModel{
		ID: "managed-config", TenantID: "tenant-1", Provider: storagepkg.ProviderEverstack,
		ManagementMode: storagepkg.ManagementSystem, ManagedCellID: "r2-eu-production",
		ManagedPathPrefix: "tenants/opaque", IsDefault: true, Enabled: true,
	}
	ctx, _ := storageCommandContext(t)
	queryBus := querypkg.NewQueryBus()
	queryBus.RegisterHandler(fixedStorageConfigListHandler{configs: []storagequery.StorageConfigReadModel{managedConfig}})
	system, err := cqrs.GetSystemFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	system.QueryBus = queryBus

	ensurer := &recordingManagedEnsurer{}
	managedStore := inertObjectStore{}
	resolver := &recordingManagedResolver{store: managedStore}
	server := CreateServerWithSecurityDeps(context.Background(), leakingObjectStore{}, nil, nil, nil)
	server.SetManagedStorage(ensurer, resolver)

	store, config, err := server.getStoreForConfig(ctx, "", "tenant-1")
	if err != nil {
		t.Fatalf("getStoreForConfig() error = %v", err)
	}
	if _, ok := store.(inertObjectStore); !ok {
		t.Fatalf("getStoreForConfig() store type = %T, want managed store", store)
	}
	if config == nil || config.ID != managedConfig.ID {
		t.Fatalf("getStoreForConfig() config = %#v", config)
	}
	if len(ensurer.tenantIDs) != 1 || ensurer.tenantIDs[0] != "tenant-1" {
		t.Fatalf("EnsureDefault() tenant calls = %#v", ensurer.tenantIDs)
	}
}

func TestListStorageConfigsEnsuresAndRedactsManagedDefault(t *testing.T) {
	managedConfig := storagequery.StorageConfigReadModel{
		ID: "managed-config", TenantID: "tenant-1", Provider: storagepkg.ProviderEverstack,
		Endpoint: "https://physical.example", Region: "auto", Bucket: "physical-bucket",
		PathPrefix: "should-not-leak", CredentialRef: "storagecred_should_not_leak",
		ManagementMode: storagepkg.ManagementSystem, ManagedCellID: "r2-eu-production",
		ManagedPathPrefix: "tenants/opaque", IsDefault: true, Enabled: true,
	}
	ctx, _ := storageCommandContext(t)
	queryBus := querypkg.NewQueryBus()
	queryBus.RegisterHandler(fixedStorageConfigListHandler{configs: []storagequery.StorageConfigReadModel{managedConfig}})
	system, err := cqrs.GetSystemFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	system.QueryBus = queryBus

	ensurer := &recordingManagedEnsurer{connection: &storagepkg.ManagedConnection{
		ConfigID: managedConfig.ID, TenantID: managedConfig.TenantID,
		CellID: managedConfig.ManagedCellID, PathPrefix: managedConfig.ManagedPathPrefix,
	}}
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, nil, nil)
	server.SetManagedStorage(ensurer, &recordingManagedResolver{})

	response, err := server.ListStorageConfigs(ctx, connect.NewRequest(&storagepb.ListStorageConfigsRequest{}))
	if err != nil {
		t.Fatalf("ListStorageConfigs() error = %v", err)
	}
	if len(ensurer.tenantIDs) != 1 || ensurer.tenantIDs[0] != "tenant-1" {
		t.Fatalf("EnsureDefault() tenant calls = %#v", ensurer.tenantIDs)
	}
	if len(response.Msg.GetConfigs()) != 1 {
		t.Fatalf("ListStorageConfigs() configs = %d, want 1", len(response.Msg.GetConfigs()))
	}
	config := response.Msg.GetConfigs()[0]
	if config.GetProvider() != storagepb.StorageProvider_STORAGE_PROVIDER_EVERSTACK || !config.GetSystemManaged() {
		t.Fatalf("managed response provider=%v system_managed=%t", config.GetProvider(), config.GetSystemManaged())
	}
	if config.GetEndpoint() != "" || config.GetRegion() != "" || config.GetBucket() != "" || config.GetPathPrefix() != "" {
		t.Fatalf("managed list response exposes physical placement: %#v", config)
	}
}

func TestCustomersCannotCreateOrMutateManagedStorageConnections(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		ctx, writer := storageCommandContext(t)
		server := CreateServerWithSecurityDeps(context.Background(), nil, nil, nil, nil)
		_, err := server.ConfigureStorage(ctx, connect.NewRequest(&storagepb.ConfigureStorageRequest{
			Provider: storagepb.StorageProvider_STORAGE_PROVIDER_EVERSTACK,
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("ConfigureStorage() code = %v, want invalid argument (err=%v)", connect.CodeOf(err), err)
		}
		if len(writer.events) != 0 {
			t.Fatalf("ConfigureStorage() emitted %d events", len(writer.events))
		}
	})

	t.Run("replace default", func(t *testing.T) {
		ctx, writer := storageCommandContext(t)
		server := CreateServerWithSecurityDeps(context.Background(), nil, nil, nil, nil)
		server.SetManagedStorage(&recordingManagedEnsurer{}, &recordingManagedResolver{})
		_, err := server.ConfigureStorage(ctx, connect.NewRequest(&storagepb.ConfigureStorageRequest{
			Provider:  storagepb.StorageProvider_STORAGE_PROVIDER_S3,
			IsDefault: true,
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("ConfigureStorage() code = %v, want failed precondition (err=%v)", connect.CodeOf(err), err)
		}
		if len(writer.events) != 0 {
			t.Fatalf("ConfigureStorage() emitted %d events", len(writer.events))
		}
	})

	for _, test := range []struct {
		name string
		call func(*Server, context.Context) error
	}{
		{name: "update", call: func(server *Server, ctx context.Context) error {
			enabled := false
			_, err := server.UpdateStorageConfig(ctx, connect.NewRequest(&storagepb.UpdateStorageConfigRequest{ConfigId: "managed-config", Enabled: &enabled}))
			return err
		}},
		{name: "delete", call: func(server *Server, ctx context.Context) error {
			_, err := server.DeleteStorageConfig(ctx, connect.NewRequest(&storagepb.DeleteStorageConfigRequest{ConfigId: "managed-config"}))
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, writer := storageCommandAndConfigContext(t, &storagequery.StorageConfigReadModel{
				ID: "managed-config", TenantID: "tenant-1", Provider: storagepkg.ProviderEverstack,
				ManagementMode: storagepkg.ManagementSystem, ManagedCellID: "r2-eu-production",
			})
			server := CreateServerWithSecurityDeps(context.Background(), nil, nil, nil, nil)
			if err := test.call(server, ctx); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("%s code = %v, want failed precondition (err=%v)", test.name, connect.CodeOf(err), err)
			}
			if len(writer.events) != 0 {
				t.Fatalf("%s emitted %d events", test.name, len(writer.events))
			}
		})
	}
}

func TestStorageObjectKeyKeepsManagedPhysicalMetadataOpaque(t *testing.T) {
	t.Parallel()

	managed := &storagequery.StorageConfigReadModel{
		ID:             "managed-connection-id",
		Provider:       storagepkg.ProviderEverstack,
		ManagementMode: storagepkg.ManagementSystem,
	}
	key := storageObjectKey(managed, "tenant-secret", "artifact", "object-id", "customer-report.pdf")
	if key != "v1/connections/managed-connection-id/pending/object-id" {
		t.Fatalf("managed storage key = %q", key)
	}
	for _, forbidden := range []string{"tenant-secret", "artifact", "customer-report.pdf"} {
		if strings.Contains(key, forbidden) {
			t.Fatalf("managed storage key exposes %q: %q", forbidden, key)
		}
	}

	external := &storagequery.StorageConfigReadModel{
		ID:             "external-connection-id",
		Provider:       "s3",
		ManagementMode: storagepkg.ManagementCustomer,
	}
	legacyKey := storageObjectKey(external, "tenant-1", "artifact", "object-id", "report.pdf")
	if legacyKey != "tenants/tenant-1/artifact/object-id/report.pdf" {
		t.Fatalf("external storage key = %q", legacyKey)
	}
}
