package controlplane

import (
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/previewtoken"
)

func TestPreviewTTLDefaultsAndClamps(t *testing.T) {
	t.Parallel()

	if got := previewTTL(0); got != time.Hour {
		t.Fatalf("expected default 1h TTL, got %s", got)
	}
	if got := previewTTL(90); got != 90*time.Second {
		t.Fatalf("expected requested TTL, got %s", got)
	}
	if got := previewTTL(999999); got != 24*time.Hour {
		t.Fatalf("expected max 24h TTL, got %s", got)
	}
}

func TestPreviewSubdomain(t *testing.T) {
	t.Parallel()

	if got := previewSubdomain("sbx-1234567890", "abc123", 3000); got != "abc123-3000" {
		t.Fatalf("unexpected short-code subdomain: %q", got)
	}
	if got := previewSubdomain("sbx-1234567890", "", 3000); got != "sbx-34567890-3000" {
		t.Fatalf("unexpected fallback subdomain: %q", got)
	}
}

func TestIssuePreviewURLSignsScopedClaims(t *testing.T) {
	t.Parallel()

	signer, err := previewtoken.NewSigner([]byte("test-preview-secret-32-bytes-long"))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	svc := NewPreviewService(signer, PreviewURLConfig{
		BaseDomain: "preview.evs.run",
		TLSEnabled: true,
	})
	issued, err := svc.IssuePreviewURL(IssuePreviewURLRequest{
		Scope: sandbox.SandboxScope{
			OrganizationID: "org-a",
			TenantID:       "workspace-a",
			InstanceID:     "inst-a",
			SandboxID:      "sbx-a",
		},
		ShortCode:        "abc123",
		Port:             3000,
		ExpiresInSeconds: 60,
	})
	if err != nil {
		t.Fatalf("issue preview URL: %v", err)
	}
	const prefix = "https://abc123-3000.preview.evs.run?" + previewtoken.QueryParam + "="
	if !strings.HasPrefix(issued.URL, prefix) {
		t.Fatalf("unexpected URL: %q", issued.URL)
	}
	claims, err := signer.Verify(strings.TrimPrefix(issued.URL, prefix))
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if claims.SandboxID != "sbx-a" || claims.Subdomain != "abc123-3000" || claims.TenantID != "inst-a" || claims.Port != 3000 {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestIssuePreviewURLRejectsIncompleteScope(t *testing.T) {
	t.Parallel()

	signer, err := previewtoken.NewSigner([]byte("test-preview-secret-32-bytes-long"))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	_, err = NewPreviewService(signer, PreviewURLConfig{}).IssuePreviewURL(IssuePreviewURLRequest{Port: 3000})
	if err == nil {
		t.Fatal("expected incomplete scope error")
	}
}
