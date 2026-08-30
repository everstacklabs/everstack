package controlplane

import (
	"context"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	sshpkg "github.com/everstacklabs/everstack/internal/ssh"
)

// SSHService owns sandbox SSH human-tool operations that belong to the sandbox
// control plane. Agent-facing APIs should delegate here instead of talking to
// token storage directly.
type SSHService struct {
	keyStore *sshpkg.KeyStore
}

type CreateSSHTokenRequest struct {
	Scope            sandbox.SandboxScope
	CreatedBy        string
	SSHUser          string
	SSHHost          string
	SSHPort          int
	ExpiresInMinutes int32
}

type CreateSSHTokenResponse struct {
	Token            *sshpkg.SandboxSSHToken
	RawToken         string
	ConnectionString string
}

type ListSSHTokensRequest struct {
	Scope       sandbox.SandboxScope
	RequestedBy string
}

type RevokeSSHTokenRequest struct {
	Scope       sandbox.SandboxScope
	TokenID     string
	RequestedBy string
}

func NewSSHService(keyStore *sshpkg.KeyStore) *SSHService {
	return &SSHService{keyStore: keyStore}
}

func (s *SSHService) Configured() bool {
	return s != nil && s.keyStore != nil
}

func (s *SSHService) CreateToken(ctx context.Context, req CreateSSHTokenRequest) (*CreateSSHTokenResponse, error) {
	if !s.Configured() {
		return nil, fmt.Errorf("SSH feature is not configured")
	}
	tokenScope, err := sshTokenScope(req.Scope)
	if err != nil {
		return nil, err
	}
	createdBy := req.CreatedBy
	if createdBy == "" {
		createdBy = "unknown"
	}
	token, rawToken, err := s.keyStore.CreateSandboxSSHToken(ctx, tokenScope, createdBy, req.ExpiresInMinutes)
	if err != nil {
		logSSHAudit("ssh_token_create_failed",
			"organization_id", tokenScope.OrganizationID,
			"tenant_id", tokenScope.TenantID,
			"instance_id", tokenScope.InstanceID,
			"sandbox_id", tokenScope.SandboxID,
			"created_by", createdBy,
			"error", err.Error(),
		).Warn("ssh audit: token create failed")
		return nil, err
	}
	logSSHAudit("ssh_token_created",
		"organization_id", token.OrganizationID,
		"tenant_id", token.TenantID,
		"instance_id", token.InstanceID,
		"sandbox_id", token.SandboxID,
		"token_id", token.ID,
		"token_prefix", token.TokenPrefix,
		"created_by", token.CreatedBy,
		"expires_at", token.ExpiresAt.Format(time.RFC3339),
	).Info("ssh audit: token created")

	return &CreateSSHTokenResponse{
		Token:            token,
		RawToken:         rawToken,
		ConnectionString: formatSSHConnectionString(req.SSHUser, req.SSHHost, req.SSHPort),
	}, nil
}

func (s *SSHService) ListTokens(ctx context.Context, req ListSSHTokensRequest) ([]sshpkg.SandboxSSHToken, error) {
	if !s.Configured() {
		return nil, fmt.Errorf("SSH feature is not configured")
	}
	tokenScope, err := sshTokenScope(req.Scope)
	if err != nil {
		return nil, err
	}
	tokens, err := s.keyStore.ListSandboxSSHTokens(ctx, tokenScope)
	if err != nil {
		logSSHAudit("ssh_token_list_failed",
			"organization_id", tokenScope.OrganizationID,
			"tenant_id", tokenScope.TenantID,
			"instance_id", tokenScope.InstanceID,
			"sandbox_id", tokenScope.SandboxID,
			"error", err.Error(),
		).Warn("ssh audit: token list failed")
		return nil, err
	}
	logSSHAudit("ssh_token_listed",
		"organization_id", tokenScope.OrganizationID,
		"tenant_id", tokenScope.TenantID,
		"instance_id", tokenScope.InstanceID,
		"sandbox_id", tokenScope.SandboxID,
		"token_count", len(tokens),
		"requested_by", req.RequestedBy,
	).Info("ssh audit: tokens listed")
	return tokens, nil
}

func (s *SSHService) RevokeToken(ctx context.Context, req RevokeSSHTokenRequest) error {
	if !s.Configured() {
		return fmt.Errorf("SSH feature is not configured")
	}
	tokenScope, err := sshTokenScope(req.Scope)
	if err != nil {
		return err
	}
	if err := s.keyStore.RevokeSandboxSSHToken(ctx, tokenScope, req.TokenID); err != nil {
		logSSHAudit("ssh_token_revoke_failed",
			"organization_id", tokenScope.OrganizationID,
			"tenant_id", tokenScope.TenantID,
			"instance_id", tokenScope.InstanceID,
			"sandbox_id", tokenScope.SandboxID,
			"token_id", req.TokenID,
			"requested_by", req.RequestedBy,
			"error", err.Error(),
		).Warn("ssh audit: token revoke failed")
		return err
	}
	logSSHAudit("ssh_token_revoked",
		"organization_id", tokenScope.OrganizationID,
		"tenant_id", tokenScope.TenantID,
		"instance_id", tokenScope.InstanceID,
		"sandbox_id", tokenScope.SandboxID,
		"token_id", req.TokenID,
		"requested_by", req.RequestedBy,
	).Info("ssh audit: token revoked")
	return nil
}

func sshTokenScope(scope sandbox.SandboxScope) (sshpkg.SandboxSSHTokenScope, error) {
	scope = scope.Normalize()
	if !scope.Complete() || scope.OrganizationID == "" || scope.TenantID == "" || scope.InstanceID == "" {
		return sshpkg.SandboxSSHTokenScope{}, fmt.Errorf("sandbox scope incomplete")
	}
	return sshpkg.SandboxSSHTokenScope{
		OrganizationID: scope.OrganizationID,
		TenantID:       scope.TenantID,
		InstanceID:     scope.InstanceID,
		SandboxID:      scope.SandboxID,
	}, nil
}

func formatSSHConnectionString(user, host string, port int) string {
	if port == 22 {
		return fmt.Sprintf("ssh %s@%s", user, host)
	}
	return fmt.Sprintf("ssh %s@%s -p %d", user, host, port)
}

func logSSHAudit(event string, fields ...interface{}) *logger.Entry {
	return logger.WithFields(fields...).WithLogEvent(event)
}
