package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// The login token reaches a bounded surface. These are the procedures the
// CLI calls; each is admitted by exact name.
func TestCLIBearerSurfaceAdmitsOnlyTheListedProcedures(t *testing.T) {
	admitted := []string{
		"/everstack.cli.v1.CLIService/WhoAmI",
		"/everstack.agents.v1.AgentsService/ListAgents",
		"/everstack.functions.v1.FunctionsService/GetFunctionByName",
		"/everstack.providers.v1.ProvidersService/ListConfiguredProviders",
		"/everstack.providers.v1.ProvidersService/ListProviderCatalog",
		"/everstack.providers.v1.ProvidersService/ConfigureProvider",
		"/everstack.providers.v1.ProvidersService/ToggleProvider",
		"/everstack.providers.v1.ProvidersService/DeleteProviderConfiguration",
		"/everstack.gateway.v1.GatewayService/ListModels",
		"/everstack.org.v1.OrganizationService/ListOrganizations",
		"/everstack.org.v1.OrganizationService/GetOrganization",
		"/everstack.org.v1.OrganizationService/ListMembers",
		"/everstack.api_key.v1.ApiKeyService/ListApiKeys",
		"/everstack.api_key.v1.ApiKeyService/GetApiKey",
		"/everstack.api_key.v1.ApiKeyService/CreateApiKey",
		"/everstack.api_key.v1.ApiKeyService/DeleteApiKey",
		"/everstack.memory.v1.MemoryService/ListCollections",
		"/everstack.memory.v1.MemoryService/GetCollection",
		"/everstack.memory.v1.MemoryService/CreateCollection",
		"/everstack.memory.v1.MemoryService/DeleteCollection",
		"/everstack.memory.v1.MemoryService/GetMemoryAnalytics",
	}
	for _, p := range admitted {
		if !isCLIBearerProcedure(p) {
			t.Errorf("%s should be admitted for the login token", p)
		}
	}

	refused := []string{
		// Inference: metering and per-key limits are keyed on an API key.
		"/everstack.gateway.v1.GatewayService/ChatCompletions",
		"/everstack.gateway.v1.GatewayService/Embeddings",
		"/everstack.gateway.v1.GatewayService/Speech",
		"/everstack.gateway.v1.GatewayService/Responses",
		// Embedding models are inference too.
		"/everstack.memory.v1.MemoryService/AddDocuments",
		"/everstack.memory.v1.MemoryService/QueryCollection",
		// Shared DDL.
		"/everstack.memory.v1.MemoryService/SetupPgVector",
		// Not tenant-scoped on the server.
		"/everstack.providers.v1.ModelDiscoveryService/SearchModels",
		"/everstack.providers.v1.ModelDiscoveryService/ListCustomModels",
		"/everstack.providers.v1.ModelDiscoveryService/AddCustomModel",
		// Same service as an admitted procedure, not itself listed.
		"/everstack.providers.v1.ProvidersService/UpdateRoutingConfig",
		"/everstack.api_key.v1.ApiKeyService/RegenerateApiKey",
		"/everstack.api_key.v1.ApiKeyService/UpdateApiKey",
		"/everstack.functions.v1.FunctionsService/DeleteFunction",
		// Organization writes are not admitted.
		"/everstack.org.v1.OrganizationService/CreateOrganization",
		"/everstack.org.v1.OrganizationService/UpdateMemberRole",
		"/everstack.org.v1.OrganizationService/RemoveMember",
		"/everstack.org.v1.OrganizationService/UpdateOrganization",
		// Never the billing service.
		"/everstack.billing.v1.BillingService/GetSubscription",
		// Exact match only: no prefix, suffix or case tricks.
		"/everstack.gateway.v1.GatewayService/ListModelsX",
		"/everstack.gateway.v1.GatewayService/ListModels/",
		"everstack.gateway.v1.GatewayService/ListModels",
		"/everstack.gateway.v1.gatewayservice/listmodels",
		"/x/everstack.memory.v1.MemoryService/ListCollections",
		"",
	}
	for _, p := range refused {
		if isCLIBearerProcedure(p) {
			t.Errorf("%q must not be admitted for the login token", p)
		}
	}
}

func TestCLIBearerRoleGate(t *testing.T) {
	adminOnly := []string{
		"/everstack.providers.v1.ProvidersService/ConfigureProvider",
		"/everstack.providers.v1.ProvidersService/ToggleProvider",
		"/everstack.providers.v1.ProvidersService/DeleteProviderConfiguration",
		"/everstack.api_key.v1.ApiKeyService/CreateApiKey",
		"/everstack.api_key.v1.ApiKeyService/DeleteApiKey",
	}
	for _, p := range adminOnly {
		for _, role := range []string{"owner", "admin", " admin "} {
			if !cliBearerRoleAllows(p, role) {
				t.Errorf("%s: role %q should be allowed", p, role)
			}
		}
		// Fails closed: anything that is not exactly owner or admin.
		for _, role := range []string{"", "member", "viewer", "Admin", "OWNER", "administrator", "owner,member"} {
			if cliBearerRoleAllows(p, role) {
				t.Errorf("%s: role %q must be refused", p, role)
			}
		}
	}
	for _, p := range []string{
		"/everstack.providers.v1.ProvidersService/ListConfiguredProviders",
		"/everstack.api_key.v1.ApiKeyService/ListApiKeys",
		"/everstack.memory.v1.MemoryService/CreateCollection",
		"/everstack.agents.v1.AgentsService/CreateAgent",
	} {
		if !cliBearerRoleAllows(p, "member") || !cliBearerRoleAllows(p, "") {
			t.Errorf("%s is not an admin-only procedure and must not be role gated", p)
		}
	}
	// No procedure may sit in both sets, and every admin procedure must be
	// admitted in the first place.
	for p := range cliBearerOrgAdminProcedures {
		if cliBearerProcedures[p] {
			t.Errorf("%s is in both the member and the admin set", p)
		}
		if !isCLIBearerProcedure(p) {
			t.Errorf("%s is admin gated but not admitted", p)
		}
	}
}

// End to end through the bearer path: a real signed token, the membership row
// from the database, and the role gate.
func TestApplyCLIBearerContextEnforcesRoleOnCredentialWrites(t *testing.T) {
	tokens, err := deviceauth.NewTokenManager([]byte(strings.Repeat("k", 32)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(deviceauth.Identity{
		UserID: "user-1", Email: "dev@example.com", OrganizationID: "org-1", ClientID: "evs-cli",
	})
	if err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, role, procedure string, orgHeader string) (context.Context, error) {
		t.Helper()
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer rawDB.Close()
		mock.MatchExpectationsInOrder(false)
		mock.ExpectQuery("FROM everstack.organization_members").
			WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow(role, "user-1"))
		mock.ExpectQuery("user_credential_revocations").WillReturnError(sql.ErrNoRows)

		i := &APIKeyConnectInterceptor{cliTokens: tokens, cliAuthDB: sqlx.NewDb(rawDB, "sqlmock")}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+token)
		if orgHeader != "" {
			h.Set("x-evs-org-id", orgHeader)
		}
		return i.applyCLIBearerContext(context.Background(), h, procedure, "corr-1")
	}

	const createKey = "/everstack.api_key.v1.ApiKeyService/CreateApiKey"
	const listKeys = "/everstack.api_key.v1.ApiKeyService/ListApiKeys"

	t.Run("member cannot mint an API key", func(t *testing.T) {
		ctx, err := run(t, "member", createKey, "")
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("want PermissionDenied, got %v", err)
		}
		if contextkeys.IsTenantAuthenticated(ctx) {
			t.Fatal("a refused request must not come back authenticated")
		}
	})
	t.Run("member with no role recorded cannot mint an API key", func(t *testing.T) {
		if _, err := run(t, "", createKey, ""); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("want PermissionDenied, got %v", err)
		}
	})
	t.Run("admin can", func(t *testing.T) {
		ctx, err := run(t, "admin", createKey, "")
		if err != nil {
			t.Fatalf("admin should pass, got %v", err)
		}
		if org, ok := contextkeys.CLIOrganizationID(ctx); !ok || org != "org-1" {
			t.Fatalf("the token's organization must be recorded for handlers to pin on, got %q %v", org, ok)
		}
		if !contextkeys.HasVerifiedTenantPrincipal(ctx) || contextkeys.GetTenantID(ctx) != "org-1" || contextkeys.GetUserRole(ctx) != "admin" {
			t.Fatalf("admin context is incomplete: tenant=%q role=%q", contextkeys.GetTenantID(ctx), contextkeys.GetUserRole(ctx))
		}
	})
	t.Run("member can list keys", func(t *testing.T) {
		ctx, err := run(t, "member", listKeys, "")
		if err != nil || contextkeys.GetTenantID(ctx) != "org-1" {
			t.Fatalf("member should list keys in its own org, got tenant=%q err=%v", contextkeys.GetTenantID(ctx), err)
		}
	})
	t.Run("a header naming another organization is refused", func(t *testing.T) {
		if _, err := run(t, "owner", listKeys, "org-2"); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
	})
	t.Run("a token that does not verify is refused", func(t *testing.T) {
		i := &APIKeyConnectInterceptor{cliTokens: tokens, cliAuthDB: &sqlx.DB{}}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+token+"x")
		if _, err := i.applyCLIBearerContext(context.Background(), h, listKeys, "corr-1"); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
	})
}

// The same checks through validateAPIKey, the function the unary and stream
// wrappers call with the procedure Connect reports: an unlisted procedure
// never reaches token verification, and a listed one is role gated.
func TestValidateAPIKeyRoutesLoginTokenByProcedure(t *testing.T) {
	tokens, err := deviceauth.NewTokenManager([]byte(strings.Repeat("k", 32)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(deviceauth.Identity{UserID: "user-1", Email: "dev@example.com", OrganizationID: "org-1", ClientID: "evs-cli"})
	if err != nil {
		t.Fatal(err)
	}
	header := func() http.Header {
		h := http.Header{}
		h.Set("Authorization", "Bearer "+token)
		return h
	}

	t.Run("a valid login token is refused on unlisted procedures without a lookup", func(t *testing.T) {
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer rawDB.Close()
		i := &APIKeyConnectInterceptor{cliTokens: tokens, cliAuthDB: sqlx.NewDb(rawDB, "sqlmock")}
		for _, proc := range []string{
			"/everstack.gateway.v1.GatewayService/ChatCompletions",
			"/everstack.memory.v1.MemoryService/AddDocuments",
			"/everstack.memory.v1.MemoryService/SetupPgVector",
			"/everstack.providers.v1.ModelDiscoveryService/SearchModels",
			"/everstack.billing.v1.BillingService/GetSubscription",
		} {
			ctx, err := i.validateAPIKey(context.Background(), header(), proc, "corr-1")
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Errorf("%s: want Unauthenticated, got %v", proc, err)
			}
			if contextkeys.IsTenantAuthenticated(ctx) {
				t.Errorf("%s: came back authenticated", proc)
			}
		}
		// No membership query was expected, so none may have been made.
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	lookup := func(t *testing.T, role string) *APIKeyConnectInterceptor {
		t.Helper()
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rawDB.Close() })
		mock.MatchExpectationsInOrder(false)
		mock.ExpectQuery("FROM everstack.organization_members").
			WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow(role, "user-1"))
		mock.ExpectQuery("user_credential_revocations").WillReturnError(sql.ErrNoRows)
		return &APIKeyConnectInterceptor{cliTokens: tokens, cliAuthDB: sqlx.NewDb(rawDB, "sqlmock")}
	}

	t.Run("member lists providers", func(t *testing.T) {
		ctx, err := lookup(t, "member").validateAPIKey(context.Background(), header(), "/everstack.providers.v1.ProvidersService/ListConfiguredProviders", "corr-1")
		if err != nil || !contextkeys.HasVerifiedTenantPrincipal(ctx) {
			t.Fatalf("member should list providers, got %v", err)
		}
	})
	t.Run("member cannot write a provider secret", func(t *testing.T) {
		_, err := lookup(t, "member").validateAPIKey(context.Background(), header(), "/everstack.providers.v1.ProvidersService/ConfigureProvider", "corr-1")
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("want PermissionDenied, got %v", err)
		}
	})
	t.Run("owner can", func(t *testing.T) {
		if _, err := lookup(t, "owner").validateAPIKey(context.Background(), header(), "/everstack.providers.v1.ProvidersService/ConfigureProvider", "corr-1"); err != nil {
			t.Fatalf("owner should pass, got %v", err)
		}
	})
}

// A role found through the email fallback is not trusted for credential
// writes: email_at_link is not unique, so the row may be another member's.
func TestAdminWritesRefuseARoleFoundByEmailFallback(t *testing.T) {
	tokens, err := deviceauth.NewTokenManager([]byte(strings.Repeat("k", 32)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(deviceauth.Identity{UserID: "identity-sub", Email: "dev@example.com", OrganizationID: "org-1", ClientID: "evs-cli"})
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, proc string) error {
		t.Helper()
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer rawDB.Close()
		// Direct lookup by the token's user id finds nothing; the fallback
		// by verified email finds an owner.
		mock.ExpectQuery("FROM everstack.organization_members om\\s+WHERE").WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery("identity_links").
			WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("owner", "user-9"))
		mock.ExpectQuery("user_credential_revocations").WillReturnError(sql.ErrNoRows)
		i := &APIKeyConnectInterceptor{cliTokens: tokens, cliAuthDB: sqlx.NewDb(rawDB, "sqlmock")}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+token)
		_, err = i.applyCLIBearerContext(context.Background(), h, proc, "corr-1")
		return err
	}
	if err := run(t, "/everstack.api_key.v1.ApiKeyService/CreateApiKey"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("an owner role found by email must not mint a key, got %v", err)
	}
	if err := run(t, "/everstack.api_key.v1.ApiKeyService/ListApiKeys"); err != nil {
		t.Fatalf("membership by verified email still allows reads, got %v", err)
	}
}

// On a managed gateway a request that did not resolve to an instance would
// write under the bare organization id. Credential writes are refused there;
// reads are not.
func TestAdminWritesRefusedOnManagedGatewayWithoutAnInstance(t *testing.T) {
	enterprise.SetManagedGateway(true)
	t.Cleanup(func() { enterprise.SetManagedGateway(false) })

	tokens, err := deviceauth.NewTokenManager([]byte(strings.Repeat("k", 32)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// No instance in the token and no host scope on the request.
	token, err := tokens.Issue(deviceauth.Identity{UserID: "user-1", Email: "dev@example.com", OrganizationID: "org-1", ClientID: "evs-cli"})
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, proc string) error {
		t.Helper()
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer rawDB.Close()
		mock.MatchExpectationsInOrder(false)
		mock.ExpectQuery("FROM everstack.organization_members").
			WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("owner", "user-1"))
		mock.ExpectQuery("user_credential_revocations").WillReturnError(sql.ErrNoRows)
		i := &APIKeyConnectInterceptor{cliTokens: tokens, cliAuthDB: sqlx.NewDb(rawDB, "sqlmock")}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+token)
		_, err = i.applyCLIBearerContext(context.Background(), h, proc, "corr-1")
		return err
	}
	if err := run(t, "/everstack.api_key.v1.ApiKeyService/CreateApiKey"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("owner without an instance must not mint a key on a managed gateway, got %v", err)
	}
	if err := run(t, "/everstack.providers.v1.ProvidersService/ListConfiguredProviders"); err != nil {
		t.Fatalf("reads are unaffected, got %v", err)
	}
}
