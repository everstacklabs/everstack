package controlplane

import (
	"fmt"
	"strconv"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/previewtoken"
	"github.com/everstacklabs/everstack/internal/sandbox/previewurl"
)

const (
	defaultPreviewTTLSeconds = 3600
	maxPreviewTTLSeconds     = 86400
)

type PreviewService struct {
	signer *previewtoken.Signer
	config PreviewURLConfig
}

type PreviewURLConfig struct {
	BaseDomain string
	TLSEnabled bool
	ListenPort string
}

type IssuePreviewURLRequest struct {
	Scope            sandbox.SandboxScope
	ShortCode        string
	Port             int
	ExpiresInSeconds int32
}

type IssuePreviewURLResponse struct {
	URL       string
	ExpiresAt string
}

func NewPreviewService(signer *previewtoken.Signer, config PreviewURLConfig) *PreviewService {
	return &PreviewService{signer: signer, config: config}
}

func (s *PreviewService) Configured() bool {
	return s != nil && s.signer != nil
}

func (s *PreviewService) IssuePreviewURL(req IssuePreviewURLRequest) (*IssuePreviewURLResponse, error) {
	if !s.Configured() {
		return nil, fmt.Errorf("signed preview URLs are not configured")
	}
	scope := req.Scope.Normalize()
	if !scope.Complete() {
		return nil, fmt.Errorf("sandbox scope incomplete")
	}
	if req.Port < 1 || req.Port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	ttl := previewTTL(req.ExpiresInSeconds)
	subdomain := previewSubdomain(scope.SandboxID, req.ShortCode, req.Port)
	baseURL := previewurl.SignedURLBase(previewurl.Config{
		BaseDomain: s.config.BaseDomain,
		TLSEnabled: s.config.TLSEnabled,
		ListenPort: s.config.ListenPort,
	}, subdomain)
	claims := previewtoken.Claims{
		SandboxID: scope.SandboxID,
		Subdomain: subdomain,
		TenantID:  scope.SandboxTenantID(),
		Port:      req.Port,
	}
	token, err := s.signer.Sign(claims, ttl)
	if err != nil {
		logPreviewAudit("preview_url_issue_failed",
			"organization_id", scope.OrganizationID,
			"tenant_id", scope.TenantID,
			"instance_id", scope.InstanceID,
			"sandbox_id", scope.SandboxID,
			"port", req.Port,
			"error", err.Error(),
		).Warn("preview audit: URL issue failed")
		return nil, fmt.Errorf("failed to generate preview token")
	}
	logPreviewAudit("preview_url_issued",
		"organization_id", scope.OrganizationID,
		"tenant_id", scope.TenantID,
		"instance_id", scope.InstanceID,
		"sandbox_id", scope.SandboxID,
		"port", req.Port,
		"subdomain", subdomain,
		"ttl_seconds", int(ttl.Seconds()),
	).Info("preview audit: URL issued")
	return &IssuePreviewURLResponse{
		URL:       baseURL + "?" + previewtoken.QueryParam + "=" + token,
		ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339),
	}, nil
}

func previewTTL(seconds int32) time.Duration {
	if seconds <= 0 {
		seconds = defaultPreviewTTLSeconds
	}
	if seconds > maxPreviewTTLSeconds {
		seconds = maxPreviewTTLSeconds
	}
	return time.Duration(seconds) * time.Second
}

func previewSubdomain(sandboxID, shortCode string, port int) string {
	if shortCode != "" {
		return shortCode + "-" + strconv.Itoa(port)
	}
	id := sandboxID
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return "sbx-" + id + "-" + strconv.Itoa(port)
}

func logPreviewAudit(event string, fields ...interface{}) *logger.Entry {
	return logger.WithFields(fields...).WithLogEvent(event)
}
