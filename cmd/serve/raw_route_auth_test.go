package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"

	agentsv1 "github.com/everstacklabs/everstack/internal/api/grpc/agents/v1"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// Handlers registered directly on the router sit outside the /v1 auth chain,
// so each has to bring its own caller check. This test reads the wiring in
// start_api.go and fails when an agents-server handler is assigned bare
// without being on the reviewed list below.
//
// Adding a name to selfAuthenticating is a security decision: the handler
// must verify the caller and the tenant itself. Say how in the comment.
func TestRawAgentsHandlersAreWiredThroughAnAuthWrapper(t *testing.T) {
	selfAuthenticating := map[string]string{
		// SSH signature or a verified principal that owns the sandbox
		// (authenticateShellRequest).
		"sandboxShellHandler":            "authenticateShellRequest",
		"sandboxShellByIDHandler":        "authenticateShellRequest",
		"sandboxShellSessionsHandler":    "authenticateShellRequest",
		"sandboxKillShellSessionHandler": "authenticateShellRequest",
		"browserStreamHandler":           "authenticateShellRequest",
		"browserStreamByIDHandler":       "authenticateShellRequest",
		// Static output, no tenant data.
		"mcpConfigHandler":         "static",
		"agentCapabilitiesHandler": "static",
	}

	src, err := os.ReadFile("start_api.go")
	if err != nil {
		t.Fatal(err)
	}
	bare := regexp.MustCompile(`(?m)^\s*(\w+Handler) = http\.HandlerFunc\(agentsServer\.\w+\)`)
	for _, m := range bare.FindAllStringSubmatch(string(src), -1) {
		if _, ok := selfAuthenticating[m[1]]; !ok {
			t.Errorf("%s is assigned a bare agents handler: wire it through rawSandboxOwned or rawTenantVerified, or add it to selfAuthenticating with the reason it is safe", m[1])
		}
	}

	// The routes that were open until 2026-11-11 must stay wrapped.
	for _, name := range []string{
		"sandboxCommandHandler", "sandboxExecuteCodeHandler", "sandboxUploadFileHandler",
		"sandboxDownloadFileHandler", "sandboxLogsHandler", "sandboxStatsHandler",
		"sandboxEventsHandler", "sandboxFilesHandler", "sandboxFileSearchHandler", "sandboxSSHInfoHandler",
		"sandboxRestoreHandler", "sandboxRecoverHandler", "sandboxStartVerbHandler",
		"sandboxStopVerbHandler", "sandboxArchiveVerbHandler", "sandboxDeleteVerbHandler",
		"sandboxFSListHandler", "sandboxFSUploadHandler", "sandboxFSDownloadHandler",
		"sandboxFSMkdirHandler", "sandboxFSDeleteHandler", "sandboxFSMoveHandler",
		"sandboxFSPermissionsHandler", "sandboxProcessSessionsHandler", "sandboxProcessSessionHandler",
		"sandboxProcessExecHandler", "sandboxProcessCommandHandler", "sandboxProcessLogsHandler",
	} {
		if !regexp.MustCompile(`(?m)^\s*` + name + ` = rawSandboxOwned\(`).Match(src) {
			t.Errorf("%s must be wired through rawSandboxOwned", name)
		}
	}
	for _, name := range []string{"snapshotCreateHandler", "lcWebhookCreateHandler", "otlpUpsertHandler", "imageBuildHandler", "sandboxLifecycleEventsHandler"} {
		if !regexp.MustCompile(`(?m)^\s*` + name + ` = rawTenantVerified\(`).Match(src) {
			t.Errorf("%s must be wired through rawTenantVerified", name)
		}
	}
	for _, name := range []string{"userInputHandler", "stopTurnHandler"} {
		if !regexp.MustCompile(`(?m)^\s*` + name + ` = withRawDeviceTokenAuth\(`).Match(src) {
			t.Errorf("%s must accept only verified callers (handler checks) plus the device token wrapper", name)
		}
	}
}

func TestWithRawDeviceTokenAuth(t *testing.T) {
	tokens, err := deviceauth.NewTokenManager([]byte(strings.Repeat("k", 32)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// The wrapper only needs a non-nil handle; no case below reaches a query.
	db := &sqlx.DB{}

	run := func(ctx context.Context, authorization string) (code int, reached, verified bool) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = true
			verified = contextkeys.HasVerifiedTenantPrincipal(r.Context())
		})
		req := httptest.NewRequest("GET", "/v1/sandbox/sbx/logs/stream", nil).WithContext(ctx)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		withRawDeviceTokenAuth(next, tokens, db).ServeHTTP(rec, req)
		return rec.Code, reached, verified
	}

	t.Run("no credential reaches next unverified, for next to refuse", func(t *testing.T) {
		_, reached, verified := run(context.Background(), "")
		if !reached || verified {
			t.Fatalf("reached=%v verified=%v", reached, verified)
		}
	})
	t.Run("a bearer that is not a device token is refused here and grants nothing", func(t *testing.T) {
		code, reached, _ := run(context.Background(), "Bearer not-a-real-token")
		if reached || code != http.StatusUnauthorized {
			t.Fatalf("want 401 without reaching next, got %d reached=%v", code, reached)
		}
	})
	t.Run("an already verified request is passed through untouched", func(t *testing.T) {
		ctx := contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant-a", "hash", "key-1")
		// Whatever the Authorization header holds, it is not re-read.
		code, reached, verified := run(ctx, "Bearer whatever")
		if !reached || !verified || code != http.StatusOK {
			t.Fatalf("code=%d reached=%v verified=%v", code, reached, verified)
		}
	})
	t.Run("without a token manager the wrapper is a no-op", func(t *testing.T) {
		reached := false
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer anything")
		withRawDeviceTokenAuth(next, nil, nil).ServeHTTP(httptest.NewRecorder(), req)
		if !reached {
			t.Fatal("next must run so it can refuse the request itself")
		}
	})
}

func TestRawSandboxOwnedAcceptsDeviceTokenForOwner(t *testing.T) {
	const (
		orgID      = "11111111-1111-1111-1111-111111111111"
		instanceID = "tenant-a"
	)
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db := sqlx.NewDb(raw, "sqlmock")
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT COALESCE\\(role,''\\) AS role, user_id::text AS user_id").
		WithArgs("user-a", orgID).
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("admin", "user-a"))
	mock.ExpectQuery(`SELECT revoked_at FROM everstack\.user_credential_revocations`).
		WithArgs("user-a").
		WillReturnRows(sqlmock.NewRows([]string{"revoked_at"}).AddRow(nil))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM everstack.tenant_config").
		WithArgs(instanceID, orgID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	tokens, err := deviceauth.NewTokenManager([]byte("01234567890123456789012345678901"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(deviceauth.Identity{UserID: "user-a", OrganizationID: orgID, InstanceID: instanceID, ClientID: "evs-cli"})
	if err != nil {
		t.Fatal(err)
	}
	mgr := &sandbox.SandboxManager{}
	mgr.SeedLiveInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {ID: "sbx-a", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: instanceID, SessionID: "sess-a"}},
	})
	agents := agentsv1.CreateServer()
	agents.SetSandboxManager(mgr)
	reached := false
	owned := withRawDeviceTokenAuth(agents.RequireSandboxOwnership(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = contextkeys.HasVerifiedTenantPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})), tokens, db)
	router := mux.NewRouter()
	router.Handle("/v1/sandbox/{sandbox_id}/command", owned).Methods(http.MethodPost)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandbox/sbx-a/command", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || !reached {
		t.Fatalf("status = %d, reached = %v, body = %q", rec.Code, reached, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
