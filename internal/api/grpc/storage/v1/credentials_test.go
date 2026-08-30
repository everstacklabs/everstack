package v1

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/commands"
	storagecmd "github.com/everstacklabs/everstack/internal/commands/handlers/storage"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	querypkg "github.com/everstacklabs/everstack/internal/query"
	storagequery "github.com/everstacklabs/everstack/internal/query/handlers/storage"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	"github.com/everstacklabs/everstack/pkg/authz"
	storagepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1"
)

type recordingCredentialStore struct {
	reference          string
	put                []storagecredentials.ProviderCredentials
	revoked            []string
	resolved           map[string]storagecredentials.ProviderCredentials
	migrationReference string
	migratedConfigs    []string
}

type gatedCredentialStore struct {
	*recordingCredentialStore
	enabled bool
	err     error
}

func (s *gatedCredentialStore) CredentialCutoverEnabled(context.Context) (bool, error) {
	return s.enabled, s.err
}

func (s *recordingCredentialStore) Put(_ context.Context, _ string, credentials storagecredentials.ProviderCredentials) (string, error) {
	s.put = append(s.put, credentials)
	return s.reference, nil
}

func (s *recordingCredentialStore) Resolve(_ context.Context, _ string, reference string) (storagecredentials.ProviderCredentials, error) {
	credentials, ok := s.resolved[reference]
	if !ok {
		return storagecredentials.ProviderCredentials{}, storagecredentials.ErrCredentialNotFound
	}
	return credentials, nil
}

func (s *recordingCredentialStore) Revoke(_ context.Context, _ string, reference string) error {
	s.revoked = append(s.revoked, reference)
	return nil
}

func (s *recordingCredentialStore) MigrateLegacyConfig(_ context.Context, _ string, configID string) (string, bool, error) {
	s.migratedConfigs = append(s.migratedConfigs, configID)
	return s.migrationReference, true, nil
}

type recordingConnectionVerifier struct {
	configs     []storagepkg.ConnectionConfig
	credentials []storagecredentials.ProviderCredentials
	err         error
}

func (v *recordingConnectionVerifier) Verify(_ context.Context, cfg storagepkg.ConnectionConfig, credentials storagecredentials.ProviderCredentials) error {
	v.configs = append(v.configs, cfg)
	v.credentials = append(v.credentials, credentials)
	return v.err
}

type recordingEventWriter struct {
	events []database.Event
	err    error
}

func (w *recordingEventWriter) Append(_ context.Context, events ...database.Event) error {
	if w.err != nil {
		return w.err
	}
	w.events = append(w.events, events...)
	return nil
}

func storageCommandContext(t *testing.T) (context.Context, *recordingEventWriter) {
	t.Helper()
	writer := &recordingEventWriter{}
	bus := commands.NewCommandBus(writer, nil)
	bus.RegisterHandler(storagecmd.NewStorageCommandHandler())
	ctx := cqrs.WithSystem(authenticatedStorageContext(string(authz.RoleOwner)), &cqrs.System{CommandBus: bus})
	return ctx, writer
}

type fixedStorageConfigQueryHandler struct {
	config *storagequery.StorageConfigReadModel
}

func (h fixedStorageConfigQueryHandler) QueryType() string { return "GetStorageConfig" }
func (h fixedStorageConfigQueryHandler) Handle(context.Context, querypkg.Query) (interface{}, error) {
	return h.config, nil
}

func storageCommandAndConfigContext(t *testing.T, config *storagequery.StorageConfigReadModel) (context.Context, *recordingEventWriter) {
	t.Helper()
	ctx, writer := storageCommandContext(t)
	queryBus := querypkg.NewQueryBus()
	queryBus.RegisterHandler(fixedStorageConfigQueryHandler{config: config})
	system, err := cqrs.GetSystemFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	system.QueryBus = queryBus
	return ctx, writer
}

func TestConfigureStorageVerifiesThenStoresCredentialsBehindReference(t *testing.T) {
	credentialStore := &recordingCredentialStore{reference: "storagecred_opaque"}
	verifier := &recordingConnectionVerifier{}
	ctx, writer := storageCommandContext(t)
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, verifier)

	request := &storagepb.ConfigureStorageRequest{
		Provider:        storagepb.StorageProvider_STORAGE_PROVIDER_S3,
		Endpoint:        "https://storage.example",
		Region:          "us-east-1",
		Bucket:          "bucket",
		AccessKeyId:     "access-key-value",
		SecretAccessKey: "secret-key-value",
		PathPrefix:      "prefix",
		IsDefault:       true,
	}
	response, err := server.ConfigureStorage(ctx, connect.NewRequest(request))
	if err != nil {
		t.Fatalf("ConfigureStorage() error = %v", err)
	}
	if len(verifier.configs) != 1 || len(credentialStore.put) != 1 {
		t.Fatalf("verification calls = %d, credential writes = %d, want 1 each", len(verifier.configs), len(credentialStore.put))
	}
	if verifier.credentials[0].SecretAccessKey != request.SecretAccessKey {
		t.Fatal("connection verifier did not receive submitted credentials")
	}
	if len(writer.events) != 1 {
		t.Fatalf("persisted events = %d, want 1", len(writer.events))
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(writer.events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["credential_ref"] != "storagecred_opaque" {
		t.Fatalf("event credential_ref = %v", payload["credential_ref"])
	}
	if response.Msg.GetConfig().GetId() != payload["id"] {
		t.Fatalf("response id = %q, event id = %v", response.Msg.GetConfig().GetId(), payload["id"])
	}

	serializedResponse, err := json.Marshal(response.Msg)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(writer.events[0].Payload) + string(serializedResponse)
	for _, forbidden := range []string{request.AccessKeyId, request.SecretAccessKey, "access_key_id", "secret_access_key"} {
		if strings.Contains(combined, forbidden) {
			t.Errorf("event or response exposes %q: %s", forbidden, combined)
		}
	}
}

func TestConfigureStorageRejectsFailedVerificationWithoutPersistingCredentials(t *testing.T) {
	credentialStore := &recordingCredentialStore{reference: "storagecred_unused"}
	verifier := &recordingConnectionVerifier{err: errors.New("Authorization: secret-key-value X-Amz-Signature=signed-value")}
	ctx, writer := storageCommandContext(t)
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, verifier)

	_, err := server.ConfigureStorage(ctx, connect.NewRequest(&storagepb.ConfigureStorageRequest{
		Provider:        storagepb.StorageProvider_STORAGE_PROVIDER_R2,
		Endpoint:        "https://storage.example",
		Bucket:          "bucket",
		AccessKeyId:     "access-key-value",
		SecretAccessKey: "secret-key-value",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("ConfigureStorage() code = %v, want invalid argument (err=%v)", connect.CodeOf(err), err)
	}
	if strings.Contains(err.Error(), "secret-key-value") || strings.Contains(err.Error(), "signed-value") {
		t.Fatalf("verification error exposes provider traffic: %v", err)
	}
	if len(credentialStore.put) != 0 || len(writer.events) != 0 {
		t.Fatalf("failed verification wrote credentials=%d events=%d", len(credentialStore.put), len(writer.events))
	}
}

func TestConfigureStorageFailsWhenCommittedProjectionFailsAndKeepsCredentialForRepair(t *testing.T) {
	credentialStore := &recordingCredentialStore{reference: "storagecred_committed"}
	verifier := &recordingConnectionVerifier{}
	writer := &recordingEventWriter{}
	eventBus := database.NewInMemoryEventBus()
	if err := eventBus.SubscribeCritical("failing-storage-projection", "storage_config.created", "storage_configs", func(context.Context, database.Event) error {
		return errors.New("projection unavailable")
	}); err != nil {
		t.Fatal(err)
	}
	commandBus := commands.NewCommandBus(writer, eventBus)
	commandBus.RegisterHandler(storagecmd.NewStorageCommandHandler())
	ctx := cqrs.WithSystem(authenticatedStorageContext(string(authz.RoleOwner)), &cqrs.System{CommandBus: commandBus})
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, verifier)

	_, err := server.ConfigureStorage(ctx, connect.NewRequest(&storagepb.ConfigureStorageRequest{
		Provider: storagepb.StorageProvider_STORAGE_PROVIDER_S3,
		Region:   "us-east-1", Bucket: "bucket",
		AccessKeyId: "access-key-value", SecretAccessKey: "secret-key-value",
	}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("ConfigureStorage() code = %v, want internal (err=%v)", connect.CodeOf(err), err)
	}
	if len(writer.events) != 1 {
		t.Fatalf("persisted events = %d, want 1", len(writer.events))
	}
	if len(credentialStore.revoked) != 0 {
		t.Fatalf("post-commit projection failure revoked repairable reference = %#v", credentialStore.revoked)
	}
}

func TestConfigureStorageRejectsEncryptedWritesBeforeFleetCutover(t *testing.T) {
	recorder := &recordingCredentialStore{reference: "storagecred_unused"}
	credentialStore := &gatedCredentialStore{recordingCredentialStore: recorder}
	verifier := &recordingConnectionVerifier{}
	ctx, writer := storageCommandContext(t)
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, verifier)

	_, err := server.ConfigureStorage(ctx, connect.NewRequest(&storagepb.ConfigureStorageRequest{
		Provider:        storagepb.StorageProvider_STORAGE_PROVIDER_S3,
		Endpoint:        "https://storage.example",
		Region:          "us-east-1",
		Bucket:          "bucket",
		AccessKeyId:     "access-key-value",
		SecretAccessKey: "secret-key-value",
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("ConfigureStorage() code = %v, want failed precondition (err=%v)", connect.CodeOf(err), err)
	}
	if len(verifier.configs) != 0 || len(recorder.put) != 0 || len(writer.events) != 0 {
		t.Fatalf("blocked cutover verified=%d wrote=%d emitted=%d", len(verifier.configs), len(recorder.put), len(writer.events))
	}
}

func TestUpdateStorageConfigVerifiesRotationAndLeavesRevocationToProjection(t *testing.T) {
	oldCredentials := storagecredentials.ProviderCredentials{AccessKeyID: "old-access", SecretAccessKey: "old-secret"}
	credentialStore := &recordingCredentialStore{
		reference: "storagecred_new",
		resolved:  map[string]storagecredentials.ProviderCredentials{"storagecred_old": oldCredentials},
	}
	verifier := &recordingConnectionVerifier{}
	ctx, writer := storageCommandAndConfigContext(t, &storagequery.StorageConfigReadModel{
		ID: "config-1", TenantID: "tenant-1", Provider: "r2", Endpoint: "https://old.example",
		Region: "auto", Bucket: "bucket", PathPrefix: "prefix", CredentialRef: "storagecred_old",
	})
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, verifier)

	newEndpoint := "https://new.example"
	newAccessKey := "new-access"
	newSecretKey := "new-secret"
	_, err := server.UpdateStorageConfig(ctx, connect.NewRequest(&storagepb.UpdateStorageConfigRequest{
		ConfigId: "config-1", Endpoint: &newEndpoint,
		AccessKeyId: &newAccessKey, SecretAccessKey: &newSecretKey,
	}))
	if err != nil {
		t.Fatalf("UpdateStorageConfig() error = %v", err)
	}
	if len(verifier.configs) != 1 || verifier.configs[0].Endpoint != newEndpoint {
		t.Fatalf("verification config = %#v", verifier.configs)
	}
	if len(credentialStore.put) != 1 || credentialStore.put[0].SecretAccessKey != newSecretKey {
		t.Fatalf("stored credential rotations = %#v", credentialStore.put)
	}
	if len(credentialStore.revoked) != 0 {
		t.Fatalf("API revoked references before projection commit = %#v", credentialStore.revoked)
	}
	if len(writer.events) != 1 {
		t.Fatalf("events = %d, want 1", len(writer.events))
	}
	if strings.Contains(string(writer.events[0].Payload), newAccessKey) || strings.Contains(string(writer.events[0].Payload), newSecretKey) {
		t.Fatalf("rotation event exposes credentials: %s", writer.events[0].Payload)
	}
}

func TestUpdateStorageMetadataVerifiesWithExistingReferenceWithoutWritingASecret(t *testing.T) {
	existing := storagecredentials.ProviderCredentials{AccessKeyID: "existing-access", SecretAccessKey: "existing-secret"}
	credentialStore := &recordingCredentialStore{
		resolved: map[string]storagecredentials.ProviderCredentials{"storagecred_existing": existing},
	}
	verifier := &recordingConnectionVerifier{}
	ctx, _ := storageCommandAndConfigContext(t, &storagequery.StorageConfigReadModel{
		ID: "config-1", TenantID: "tenant-1", Provider: "s3", Endpoint: "https://old.example",
		Region: "us-east-1", Bucket: "bucket", CredentialRef: "storagecred_existing",
	})
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, verifier)

	newBucket := "new-bucket"
	_, err := server.UpdateStorageConfig(ctx, connect.NewRequest(&storagepb.UpdateStorageConfigRequest{
		ConfigId: "config-1", Bucket: &newBucket,
	}))
	if err != nil {
		t.Fatalf("UpdateStorageConfig() error = %v", err)
	}
	if len(verifier.configs) != 1 || verifier.configs[0].Bucket != newBucket {
		t.Fatalf("verification config = %#v", verifier.configs)
	}
	if len(verifier.credentials) != 1 || verifier.credentials[0] != existing {
		t.Fatalf("verification credentials = %#v", verifier.credentials)
	}
	if len(credentialStore.put) != 0 || len(credentialStore.revoked) != 0 {
		t.Fatalf("metadata update wrote=%d revoked=%d secrets", len(credentialStore.put), len(credentialStore.revoked))
	}
}

func TestDeleteStorageConfigLeavesRevocationToProjection(t *testing.T) {
	credentialStore := &recordingCredentialStore{}
	ctx, writer := storageCommandAndConfigContext(t, &storagequery.StorageConfigReadModel{
		ID: "config-1", TenantID: "tenant-1", CredentialRef: "storagecred_delete",
	})
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, &recordingConnectionVerifier{})

	_, err := server.DeleteStorageConfig(ctx, connect.NewRequest(&storagepb.DeleteStorageConfigRequest{ConfigId: "config-1"}))
	if err != nil {
		t.Fatalf("DeleteStorageConfig() error = %v", err)
	}
	if len(writer.events) != 1 {
		t.Fatalf("events = %d, want 1", len(writer.events))
	}
	if len(credentialStore.revoked) != 0 {
		t.Fatalf("API revoked references before projection commit = %#v", credentialStore.revoked)
	}
}

func TestDeleteStorageConfigKeepsReferenceWhenEventCommitFails(t *testing.T) {
	credentialStore := &recordingCredentialStore{}
	ctx, writer := storageCommandAndConfigContext(t, &storagequery.StorageConfigReadModel{
		ID: "config-1", TenantID: "tenant-1", CredentialRef: "storagecred_keep",
	})
	writer.err = errors.New("event write failed")
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, &recordingConnectionVerifier{})

	_, err := server.DeleteStorageConfig(ctx, connect.NewRequest(&storagepb.DeleteStorageConfigRequest{ConfigId: "config-1"}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("DeleteStorageConfig() code = %v, want internal", connect.CodeOf(err))
	}
	if len(credentialStore.revoked) != 0 {
		t.Fatalf("failed delete revoked references = %#v", credentialStore.revoked)
	}
}

func TestGetStoreForConfigLazilyMigratesLegacyCredentials(t *testing.T) {
	credentials := storagecredentials.ProviderCredentials{AccessKeyID: "legacy-access", SecretAccessKey: "legacy-secret"}
	credentialStore := &recordingCredentialStore{
		migrationReference: "storagecred_migrated",
		resolved:           map[string]storagecredentials.ProviderCredentials{"storagecred_migrated": credentials},
	}
	ctx, _ := storageCommandAndConfigContext(t, &storagequery.StorageConfigReadModel{
		ID: "config-1", TenantID: "tenant-1", Provider: "s3", Region: "us-east-1", Bucket: "bucket",
	})
	server := CreateServerWithSecurityDeps(context.Background(), nil, nil, credentialStore, &recordingConnectionVerifier{})

	store, config, err := server.getStoreForConfig(ctx, "config-1", "tenant-1")
	if err != nil {
		t.Fatalf("getStoreForConfig() error = %v", err)
	}
	if store == nil || config.CredentialRef != "storagecred_migrated" {
		t.Fatalf("store = %v, config = %#v", store, config)
	}
	if len(credentialStore.migratedConfigs) != 1 || credentialStore.migratedConfigs[0] != "config-1" {
		t.Fatalf("migrated configs = %#v", credentialStore.migratedConfigs)
	}
}

var _ storagecredentials.Store = (*recordingCredentialStore)(nil)
var _ storagecredentials.LegacyMigrator = (*recordingCredentialStore)(nil)
var _ storagecredentials.CutoverGate = (*gatedCredentialStore)(nil)
var _ storagepkg.ConnectionVerifier = (*recordingConnectionVerifier)(nil)
