package transport

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/auth/oauthserver"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/google/uuid"
)

func TestSessionOrganizationUsesVerifiedHostOwnerInsteadOfInstanceTenant(t *testing.T) {
	ctx := contextkeys.WithTenantID(context.Background(), "instance-b")
	ctx = contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{
		InstanceID: "instance-b", OrganizationID: "org-b",
	})
	if got := sessionOrganizationID(ctx); got != "org-b" {
		t.Fatalf("sessionOrganizationID() = %q, want org-b", got)
	}
}

func TestOAuthIdentityUsesVerifiedInstanceOrganization(t *testing.T) {
	userID := uuid.New()
	orgA := uuid.New()
	orgB := uuid.New()
	user := &domain.UserWithOrganizations{
		User: domain.User{ID: userID, Email: "person@example.com"},
		Organizations: []domain.OrganizationMembership{
			{ID: orgA, Slug: "org-a"},
			{ID: orgB, Slug: "org-b"},
		},
	}
	scope := contextkeys.RequestInstanceScope{InstanceID: "instance-b", OrganizationID: orgB.String()}

	identity, err := oauthIdentityForRequest(user, scope, true)
	if err != nil {
		t.Fatalf("oauthIdentityForRequest() error = %v", err)
	}
	if identity.UserID != userID.String() || identity.OrganizationID != orgB.String() ||
		identity.OrganizationSlug != "org-b" || identity.InstanceID != "instance-b" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestOAuthIdentityRejectsInstanceOutsideUserOrganizations(t *testing.T) {
	user := &domain.UserWithOrganizations{
		User:          domain.User{ID: uuid.New()},
		Organizations: []domain.OrganizationMembership{{ID: uuid.New(), Slug: "org-a"}},
	}
	_, err := oauthIdentityForRequest(user, contextkeys.RequestInstanceScope{
		InstanceID: "instance-b", OrganizationID: uuid.NewString(),
	}, true)
	if !errors.Is(err, oauthserver.ErrAccessDenied) {
		t.Fatalf("error = %v, want access denied", err)
	}
}
