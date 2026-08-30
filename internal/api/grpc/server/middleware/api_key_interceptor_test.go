package middleware

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/database"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1/functionsconnect"
	"github.com/everstacklabs/everstack/pkg/tenant"
	"github.com/jmoiron/sqlx"
)

func TestValidateAPIKey_BypassesForTenantContext(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, nil)

	ctx := tenant.WithConfig(context.Background(), &tenant.Config{
		InstanceID:     "inst-test",
		OrganizationID: "org-test",
		SchemaName:     "inst_test_schema",
	})

	if _, err := interceptor.validateAPIKey(ctx, http.Header{}, "/everstack.gateway.v1.GatewayService/ListModels", "corr-test"); err != nil {
		t.Fatalf("expected tenant-context request to bypass API key validation, got error: %v", err)
	}
}

func TestValidateAPIKey_BypassesForTenantAuthenticatedFlag(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, nil)

	ctx := contextkeys.WithTenantAuthenticated(context.Background())
	if _, err := interceptor.validateAPIKey(ctx, http.Header{}, "/everstack.agents.v1.AgentsService/ListAgents", "corr-test"); err != nil {
		t.Fatalf("expected tenant-authenticated request to bypass API key validation, got error: %v", err)
	}
}

func TestValidateAPIKeyInstallsVerifiedTenantPrincipal(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, nil)
	const key = "storage-api-key"
	hash, ok := apikeylib.HashWithSecret(key, "test-hash-secret")
	if !ok {
		t.Fatal("failed to hash test API key")
	}
	interceptor.cache.Store(hash, cacheEntry{
		valid:     true,
		orgID:     "tenant-1",
		expiresAt: time.Now().Add(time.Minute),
	})

	ctx := contextkeys.WithAPIKeyHashSecret(context.Background(), "test-hash-secret")
	header := http.Header{}
	header.Set(common.EverstackApiKey, key)
	ctx, err := interceptor.validateAPIKey(ctx, header, "/everstack.storage.v1.ObjectStorageService/ListObjects", "corr-test")
	if err != nil {
		t.Fatalf("validateAPIKey() error = %v", err)
	}
	if !contextkeys.IsTenantAuthenticated(ctx) {
		t.Fatal("validated API key context is not marked authenticated")
	}
	if got := contextkeys.GetTenantID(ctx); got != "tenant-1" {
		t.Fatalf("tenant = %q, want tenant-1", got)
	}
	if got := contextkeys.GetAPIKeyHash(ctx); got != hash {
		t.Fatalf("API key hash = %q, want %q", got, hash)
	}
}

func TestValidateAPIKeyAcceptsVerifiedCLIBearerForEnabledService(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID: "user-1", OrganizationID: "org-1", InstanceID: "instance-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT COALESCE\(om\.role, ''\)[\s\S]*om\.user_id::text[\s\S]*FROM everstack\.organization_members om[\s\S]*JOIN everstack\.tenant_config tc`).
		WithArgs("user-1", "org-1", "instance-1").
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("admin", "user-1"))

	interceptor := NewAPIKeyInterceptorWithSessionDB(false, nil, db)
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)
	header.Set(common.EverstackOrgId, "org-1")
	header.Set("x-mf-tenant-id", "org-1") // Current CLI sends its org; the signed instance claim remains authoritative.

	ctx, err := interceptor.validateAPIKey(context.Background(), header, "/everstack.agents.v1.AgentsService/ListAgents", "corr-test")
	if err != nil {
		t.Fatalf("validateAPIKey() error = %v", err)
	}
	if !contextkeys.IsTenantAuthenticated(ctx) {
		t.Fatal("CLI bearer context is not marked authenticated")
	}
	if got := contextkeys.GetUserID(ctx); got != "user-1" {
		t.Fatalf("user ID = %q, want user-1", got)
	}
	if got := contextkeys.GetTenantID(ctx); got != "instance-1" {
		t.Fatalf("tenant ID = %q, want signed instance-1", got)
	}
	if got := database.TenantSchemaFromContext(ctx); got != "instance-1" {
		t.Fatalf("database tenant = %q, want signed instance-1", got)
	}
	if got := contextkeys.GetUserRole(ctx); got != "admin" {
		t.Fatalf("role = %q, want admin", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKeyAcceptsVerifiedCLIBearerForFunctionLookup(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID: "user-1", OrganizationID: "org-1", InstanceID: "instance-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT COALESCE\(om\.role, ''\)[\s\S]*om\.user_id::text[\s\S]*FROM everstack\.organization_members om[\s\S]*JOIN everstack\.tenant_config tc`).
		WithArgs("user-1", "org-1", "instance-1").
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("admin", "user-1"))

	interceptor := NewAPIKeyInterceptorWithSessionDB(false, nil, sqlx.NewDb(rawDB, "sqlmock"))
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)

	ctx, err := interceptor.validateAPIKey(
		context.Background(),
		header,
		functionsconnect.FunctionsServiceGetFunctionByNameProcedure,
		"corr-test",
	)
	if err != nil {
		t.Fatalf("validateAPIKey() error = %v", err)
	}
	if got := contextkeys.GetTenantID(ctx); got != "instance-1" {
		t.Fatalf("tenant ID = %q, want instance-1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKeyRejectsCLIBearerForFunctionMutation(t *testing.T) {
	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID: "user-1", OrganizationID: "org-1", InstanceID: "instance-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	interceptor := NewAPIKeyInterceptorWithPolicy(false, nil)
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)

	_, err = interceptor.validateAPIKey(
		context.Background(),
		header,
		functionsconnect.FunctionsServiceCreateFunctionProcedure,
		"corr-test",
	)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("error code = %v, want unauthenticated (err=%v)", connect.CodeOf(err), err)
	}
}

func TestValidateAPIKeyUsesSeparateCLIAuthorizationDB(t *testing.T) {
	sessionRawDB, sessionMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sessionRawDB.Close()
	authRawDB, authMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer authRawDB.Close()

	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID: "user-1", OrganizationID: "org-1", InstanceID: "instance-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	authMock.ExpectQuery(`SELECT COALESCE\(om\.role, ''\)[\s\S]*om\.user_id::text[\s\S]*FROM everstack\.organization_members om[\s\S]*JOIN everstack\.tenant_config tc`).
		WithArgs("user-1", "org-1", "instance-1").
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("admin", "user-1"))

	interceptor := NewAPIKeyInterceptorWithSessionDB(false, nil, sqlx.NewDb(sessionRawDB, "sqlmock"))
	interceptor.SetCLIAuthorizationDB(sqlx.NewDb(authRawDB, "sqlmock"))
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)

	if _, err := interceptor.validateAPIKey(
		context.Background(),
		header,
		"/everstack.agents.v1.AgentsService/ListAgents",
		"corr-test",
	); err != nil {
		t.Fatalf("validateAPIKey() error = %v", err)
	}
	if err := authMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := sessionMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKeyBindsLegacyCLIBearerToVerifiedRequestInstance(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Tokens issued by the pre-PKCE device flow did not carry instance_id.
	token, err := manager.Issue(deviceauth.Identity{
		UserID: "user-1", OrganizationID: "org-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT COALESCE\(om\.role, ''\)[\s\S]*om\.user_id::text[\s\S]*FROM everstack\.organization_members om[\s\S]*JOIN everstack\.tenant_config tc`).
		WithArgs("user-1", "org-1", "instance-1").
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("admin", "user-1"))

	interceptor := NewAPIKeyInterceptorWithSessionDB(false, nil, db)
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)
	header.Set(common.EverstackOrgId, "org-1")
	header.Set(common.EverstackTenantID, "org-1")
	ctx := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-1", OrganizationID: "org-1",
	})

	ctx, err = interceptor.validateAPIKey(ctx, header, "/everstack.agents.v1.AgentsService/ListAgents", "corr-test")
	if err != nil {
		t.Fatalf("validateAPIKey() error = %v", err)
	}
	if got := contextkeys.GetTenantID(ctx); got != "instance-1" {
		t.Fatalf("tenant ID = %q, want verified request instance-1", got)
	}
	if got := database.TenantSchemaFromContext(ctx); got != "instance-1" {
		t.Fatalf("database tenant = %q, want verified request instance-1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKeyResolvesPlatformUserByVerifiedCLIEmail(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID:         "identity-user-1",
		Email:          "verified@example.com",
		OrganizationID: "org-1",
		InstanceID:     "instance-1",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT COALESCE\(om\.role, ''\)[\s\S]*om\.user_id::text[\s\S]*FROM everstack\.organization_members om[\s\S]*JOIN everstack\.tenant_config tc`).
		WithArgs("identity-user-1", "org-1", "instance-1").
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}))
	mock.ExpectQuery(`SELECT COALESCE\(om\.role, ''\)[\s\S]*om\.user_id::text[\s\S]*FROM everstack\.organization_members om[\s\S]*JOIN everstack\.identity_links il[\s\S]*JOIN everstack\.tenant_config tc[\s\S]*il\.email_verified = TRUE`).
		WithArgs("org-1", "instance-1", "verified@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("owner", "platform-user-1"))

	interceptor := NewAPIKeyInterceptorWithSessionDB(false, nil, db)
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)

	ctx, err := interceptor.validateAPIKey(
		context.Background(),
		header,
		"/everstack.agents.v1.AgentsService/ListAgents",
		"corr-test",
	)
	if err != nil {
		t.Fatalf("validateAPIKey() error = %v", err)
	}
	if got := contextkeys.GetUserID(ctx); got != "platform-user-1" {
		t.Fatalf("user ID = %q, want canonical platform-user-1", got)
	}
	if got := contextkeys.GetTenantID(ctx); got != "instance-1" {
		t.Fatalf("tenant ID = %q, want instance-1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKeyRejectsBoundCLIBearerOnDifferentVerifiedRequestInstance(t *testing.T) {
	rawDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID: "user-1", OrganizationID: "org-1", InstanceID: "instance-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	interceptor := NewAPIKeyInterceptorWithSessionDB(false, nil, sqlx.NewDb(rawDB, "sqlmock"))
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)
	ctx := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-2", OrganizationID: "org-1",
	})

	_, err = interceptor.validateAPIKey(ctx, header, "/everstack.agents.v1.AgentsService/ListAgents", "corr-test")
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("error code = %v, want unauthenticated (err=%v)", connect.CodeOf(err), err)
	}
}

func TestValidateAPIKeyRejectsCLIBearerOutsideEnabledService(t *testing.T) {
	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID: "user-1", OrganizationID: "org-1", InstanceID: "instance-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	interceptor := NewAPIKeyInterceptorWithPolicy(false, nil)
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)

	_, err = interceptor.validateAPIKey(context.Background(), header, "/everstack.gateway.v1.GatewayService/ListModels", "corr-test")
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("error code = %v, want unauthenticated (err=%v)", connect.CodeOf(err), err)
	}
}

func TestValidateAPIKeyRejectsCLIBearerHeaderOutsideSignedIdentity(t *testing.T) {
	rawDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	manager, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID: "user-1", OrganizationID: "org-1", InstanceID: "instance-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	interceptor := NewAPIKeyInterceptorWithSessionDB(false, nil, sqlx.NewDb(rawDB, "sqlmock"))
	interceptor.SetCLIDeviceTokenManager(manager)
	header := http.Header{}
	header.Set(common.Authorization, "Bearer "+token)
	header.Set("x-mf-tenant-id", "instance-2")

	_, err = interceptor.validateAPIKey(context.Background(), header, "/everstack.agents.v1.AgentsService/ListAgents", "corr-test")
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("error code = %v, want unauthenticated (err=%v)", connect.CodeOf(err), err)
	}
}
