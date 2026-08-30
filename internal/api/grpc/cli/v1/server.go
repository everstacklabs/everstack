package v1

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	clipb "github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1/cliconnect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// CLIServer implements the CLIService ConnectRPC interface.
type CLIServer struct {
	db           *sqlx.DB
	sandboxMgr   *sandbox.SandboxManager
	deviceTokens *deviceauth.TokenManager
}

// NewCLIServer creates a new CLI service handler.
func NewCLIServer(db *sqlx.DB, deviceTokens ...*deviceauth.TokenManager) *CLIServer {
	server := &CLIServer{db: db}
	if len(deviceTokens) > 0 {
		server.deviceTokens = deviceTokens[0]
	}
	return server
}

// SetSandboxManager injects the sandbox manager after initialization.
func (s *CLIServer) SetSandboxManager(mgr *sandbox.SandboxManager) {
	s.sandboxMgr = mgr
}

// apiKeyContext holds the user/org resolved from an API key.
type apiKeyContext struct {
	UserID string
	OrgID  string
}

// resolveAPIKeyContext extracts user_id and org_id from the API key used to authenticate.
// Falls back to context keys if available.
func (s *CLIServer) resolveAPIKeyContext(ctx context.Context, header http.Header) (*apiKeyContext, error) {
	// A verified bearer carries both identities explicitly: organization is
	// the account/membership boundary, while context TenantID may be the
	// hostname-selected instance. Decode the token first so Whoami never
	// reports an instance UUID as the organization ID.
	if auth := header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		return s.resolveJWTContext(ctx, token)
	}

	// API-key authentication can use the principal installed by middleware.
	userID := contextkeys.GetUserID(ctx)
	orgID := contextkeys.GetTenantID(ctx)
	if userID != "" && orgID != "" {
		return &apiKeyContext{UserID: userID, OrgID: orgID}, nil
	}

	// Check for API key (canonical x-evs-api-key, falling back to legacy
	// x-mf-api-key / x-everstack-api-key). The eyJ device-token branch below
	// therefore also fires when the token arrives under the new header name.
	apiKey := common.GetHeader(header.Get, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey)
	if apiKey == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	// If it looks like a JWT (device flow token stored as api key), try JWT first
	if strings.HasPrefix(apiKey, "eyJ") {
		return s.resolveJWTContext(ctx, apiKey)
	}

	keyHash, ok := apikeylib.HashFromContext(ctx, apiKey)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("API key hashing not configured"))
	}

	var result apiKeyContext
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(user_id, ''), COALESCE(org_id, '') FROM api_keys WHERE hash = $1 AND NOT revoked`,
		keyHash,
	).Scan(&result.UserID, &result.OrgID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid API key"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to resolve API key: %w", err))
	}

	if result.OrgID == "" {
		_ = s.db.QueryRowContext(ctx,
			`SELECT organization_id FROM organization_members WHERE user_id = $1 LIMIT 1`,
			result.UserID,
		).Scan(&result.OrgID)
	}

	if result.OrgID == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no organization associated with this API key"))
	}

	return &result, nil
}

// ConnectServer interface methods

func (s *CLIServer) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return cliconnect.NewCLIServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *CLIServer) FileDescriptor() protoreflect.FileDescriptor {
	return clipb.File_everstack_cli_v1_cli_service_proto
}

func (s *CLIServer) AppName() string {
	return cliconnect.CLIServiceName
}

func (s *CLIServer) MethodPrefix() string {
	return cliconnect.CLIServiceName
}

// resolveJWTContext verifies a CLI device-flow token and extracts its identity.
func (s *CLIServer) resolveJWTContext(ctx context.Context, tokenStr string) (*apiKeyContext, error) {
	if s.deviceTokens == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("device token validation is not configured"))
	}
	identity, err := s.deviceTokens.Verify(tokenStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired device token"))
	}
	if identity.InstanceID != "" {
		currentInstanceID := contextkeys.GetTenantID(ctx)
		if currentInstanceID == "" || currentInstanceID != identity.InstanceID {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("CLI token is not valid for this instance"))
		}
	}
	return &apiKeyContext{UserID: identity.UserID, OrgID: identity.OrganizationID}, nil
}

// ============================================
// Whoami
// ============================================

func (s *CLIServer) Whoami(ctx context.Context, req *connect.Request[clipb.WhoamiRequest]) (*connect.Response[clipb.WhoamiResponse], error) {
	akCtx, err := s.resolveAPIKeyContext(ctx, req.Header())
	if err != nil {
		return nil, err
	}

	var email, orgSlug string
	_ = s.db.GetContext(ctx, &email, `SELECT email FROM users WHERE id = $1`, akCtx.UserID)
	_ = s.db.GetContext(ctx, &orgSlug, `SELECT slug FROM organizations WHERE id = $1`, akCtx.OrgID)

	return connect.NewResponse(&clipb.WhoamiResponse{
		UserId:  akCtx.UserID,
		Email:   email,
		OrgId:   akCtx.OrgID,
		OrgSlug: orgSlug,
	}), nil
}

// ============================================
// Config management
// ============================================

type worktreeConfigRow struct {
	ID          uuid.UUID `db:"id"`
	OrgID       uuid.UUID `db:"org_id"`
	UserID      uuid.UUID `db:"user_id"`
	ProjectName string    `db:"project_name"`
	ConfigYAML  string    `db:"config_yaml"`
	ConfigHash  string    `db:"config_hash"`
	Branch      *string   `db:"branch"`
	GitRemote   *string   `db:"git_remote"`
	PushedAt    string    `db:"pushed_at"`
	CreatedAt   string    `db:"created_at"`
}

func (s *CLIServer) PushConfig(ctx context.Context, req *connect.Request[clipb.PushConfigRequest]) (*connect.Response[clipb.PushConfigResponse], error) {
	akCtx, err := s.resolveAPIKeyContext(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	orgID := akCtx.OrgID
	userID := akCtx.UserID

	configYAML := req.Msg.ConfigYaml
	configHash := req.Msg.ConfigHash
	if configHash == "" {
		h := sha256.Sum256([]byte(configYAML))
		configHash = hex.EncodeToString(h[:])
	}

	// Dedup check
	var existingID string
	err = s.db.GetContext(ctx, &existingID, `
		SELECT id FROM worktree_configs WHERE org_id = $1 AND config_hash = $2
	`, orgID, configHash)
	if err == nil {
		return connect.NewResponse(&clipb.PushConfigResponse{
			ConfigId: existingID,
			Created:  false,
		}), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to check existing config: %w", err))
	}

	id := uuid.New()
	var branch, gitRemote *string
	if req.Msg.Branch != nil {
		b := *req.Msg.Branch
		branch = &b
	}
	if req.Msg.GitRemote != nil {
		g := *req.Msg.GitRemote
		gitRemote = &g
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO worktree_configs (id, org_id, user_id, project_name, config_yaml, config_hash, branch, git_remote)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, id, orgID, userID, req.Msg.ProjectName, configYAML, configHash, branch, gitRemote)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to insert config: %w", err))
	}

	logger.Infof("cli: pushed config %s for project %s (org: %s)", id, req.Msg.ProjectName, orgID)

	return connect.NewResponse(&clipb.PushConfigResponse{
		ConfigId: id.String(),
		Created:  true,
	}), nil
}

func (s *CLIServer) ListConfigs(ctx context.Context, req *connect.Request[clipb.ListConfigsRequest]) (*connect.Response[clipb.ListConfigsResponse], error) {
	akCtx, err := s.resolveAPIKeyContext(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	orgID := akCtx.OrgID

	var rows []worktreeConfigRow

	if req.Msg.ProjectName != nil && *req.Msg.ProjectName != "" {
		err = s.db.SelectContext(ctx, &rows, `
			SELECT id, org_id, user_id, project_name, config_yaml, config_hash, branch, git_remote, pushed_at, created_at
			FROM worktree_configs WHERE org_id = $1 AND project_name = $2 ORDER BY pushed_at DESC
		`, orgID, *req.Msg.ProjectName)
	} else {
		err = s.db.SelectContext(ctx, &rows, `
			SELECT id, org_id, user_id, project_name, config_yaml, config_hash, branch, git_remote, pushed_at, created_at
			FROM worktree_configs WHERE org_id = $1 ORDER BY pushed_at DESC
		`, orgID)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list configs: %w", err))
	}

	configs := make([]*clipb.WorktreeConfig, len(rows))
	for i, r := range rows {
		configs[i] = rowToProto(&r)
	}

	return connect.NewResponse(&clipb.ListConfigsResponse{
		Configs: configs,
	}), nil
}

func (s *CLIServer) GetConfig(ctx context.Context, req *connect.Request[clipb.GetConfigRequest]) (*connect.Response[clipb.GetConfigResponse], error) {
	akCtx, err := s.resolveAPIKeyContext(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	orgID := akCtx.OrgID

	var row worktreeConfigRow
	err = s.db.GetContext(ctx, &row, `
		SELECT id, org_id, user_id, project_name, config_yaml, config_hash, branch, git_remote, pushed_at, created_at
		FROM worktree_configs WHERE id = $1 AND org_id = $2
	`, req.Msg.ConfigId, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("config not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get config: %w", err))
	}

	return connect.NewResponse(&clipb.GetConfigResponse{
		Config: rowToProto(&row),
	}), nil
}

func (s *CLIServer) DeleteConfig(ctx context.Context, req *connect.Request[clipb.DeleteConfigRequest]) (*connect.Response[clipb.DeleteConfigResponse], error) {
	akCtx, err := s.resolveAPIKeyContext(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	orgID := akCtx.OrgID

	result, err := s.db.ExecContext(ctx, `DELETE FROM worktree_configs WHERE id = $1 AND org_id = $2`, req.Msg.ConfigId, orgID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete config: %w", err))
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("config not found"))
	}

	return connect.NewResponse(&clipb.DeleteConfigResponse{Success: true}), nil
}

// ============================================
// Sandbox management
// ============================================

func (s *CLIServer) CreateSandbox(ctx context.Context, req *connect.Request[clipb.CreateSandboxRequest]) (*connect.Response[clipb.CreateSandboxResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox provisioning is not configured"))
	}

	akCtx, err := s.resolveAPIKeyContext(ctx, req.Header())
	if err != nil {
		return nil, err
	}

	// Load the pushed config from DB
	var row worktreeConfigRow
	err = s.db.GetContext(ctx, &row, `
		SELECT id, org_id, user_id, project_name, config_yaml, config_hash, branch, git_remote, pushed_at, created_at
		FROM worktree_configs WHERE id = $1 AND org_id = $2
	`, req.Msg.ConfigId, akCtx.OrgID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("config not found — run ewt push first"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load config: %w", err))
	}

	// Build SandboxConfig from the pushed worktree config
	cfg := sandbox.DefaultSandboxConfig()
	cfg.Enabled = true
	cfg.Name = row.ProjectName
	cfg.SSHEnabled = true

	// Note: we don't set GitRepoURL because that requires a GitHub App installation ID.
	// Instead, the ewt.yaml config is passed as an env var for the sandbox to bootstrap from.

	// Inject ewt.yaml content as env var so the sandbox can bootstrap
	if cfg.EnvVars == nil {
		cfg.EnvVars = make(map[string]string)
	}
	cfg.EnvVars["EWT_CONFIG_YAML"] = row.ConfigYAML
	cfg.EnvVars["EWT_PROJECT_NAME"] = row.ProjectName

	// Create the sandbox
	sessionID := uuid.New().String()
	inst, err := s.sandboxMgr.GetOrCreate(ctx, sessionID, akCtx.OrgID, cfg)
	if err != nil {
		logger.WithError(err).Error("cli: failed to create sandbox")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create sandbox: %w", err))
	}

	logger.Infof("cli: created sandbox %s for project %s (org: %s)", inst.ID, row.ProjectName, akCtx.OrgID)

	return connect.NewResponse(&clipb.CreateSandboxResponse{
		SandboxId: inst.ID,
		Status:    string(inst.Status),
	}), nil
}

func (s *CLIServer) ListSandboxes(ctx context.Context, req *connect.Request[clipb.ListSandboxesRequest]) (*connect.Response[clipb.ListSandboxesResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox provisioning is not configured"))
	}

	akCtx, err := s.resolveAPIKeyContext(ctx, req.Header())
	if err != nil {
		return nil, err
	}

	allInstances := s.sandboxMgr.ListInstances()
	sandboxes := make([]*clipb.SandboxSummary, 0)
	for _, inst := range allInstances {
		if inst.Config.TenantID == akCtx.OrgID {
			sandboxes = append(sandboxes, &clipb.SandboxSummary{
				Id:          inst.ID,
				ProjectName: inst.Name,
				Status:      string(inst.Status),
				CreatedAt:   timestamppb.New(inst.CreatedAt),
			})
		}
	}

	return connect.NewResponse(&clipb.ListSandboxesResponse{
		Sandboxes: sandboxes,
	}), nil
}

func (s *CLIServer) StopSandbox(ctx context.Context, req *connect.Request[clipb.StopSandboxRequest]) (*connect.Response[clipb.StopSandboxResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox provisioning is not configured"))
	}

	akCtx, err := s.resolveAPIKeyContext(ctx, req.Header())
	if err != nil {
		return nil, err
	}

	// Verify the sandbox belongs to this tenant
	inst, ok := s.sandboxMgr.GetBySandboxID(req.Msg.SandboxId)
	if !ok || inst == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("sandbox not found"))
	}
	if inst.Config.TenantID != akCtx.OrgID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("sandbox does not belong to your organization"))
	}

	if err := s.sandboxMgr.StopSandbox(ctx, req.Msg.SandboxId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to stop sandbox: %w", err))
	}

	return connect.NewResponse(&clipb.StopSandboxResponse{Success: true}), nil
}

// ============================================
// Helpers
// ============================================

func rowToProto(r *worktreeConfigRow) *clipb.WorktreeConfig {
	cfg := &clipb.WorktreeConfig{
		Id:          r.ID.String(),
		ProjectName: r.ProjectName,
		ConfigHash:  r.ConfigHash,
		ConfigYaml:  r.ConfigYAML,
		Branch:      r.Branch,
		GitRemote:   r.GitRemote,
		PushedAt:    timestamppb.Now(), // TODO: parse r.PushedAt
	}
	return cfg
}
