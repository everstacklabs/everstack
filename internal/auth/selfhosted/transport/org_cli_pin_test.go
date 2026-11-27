package transport

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	orgv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/org/v1"
)

const (
	cliPinOrgA = "11111111-1111-1111-1111-111111111111"
	cliPinOrgB = "22222222-2222-2222-2222-222222222222"
	cliPinUser = "33333333-3333-3333-3333-333333333333"
)

func cliCtx(org string) context.Context {
	ctx := contextkeys.WithUserID(context.Background(), cliPinUser)
	return contextkeys.WithCLIOrganizationID(ctx, org)
}

func TestCLIOrganizationPin(t *testing.T) {
	if _, pinned := cliOrganizationPin(context.Background()); pinned {
		t.Fatal("a browser session must not be pinned")
	}
	got, pinned := cliOrganizationPin(cliCtx(cliPinOrgA))
	if !pinned || got != uuid.MustParse(cliPinOrgA) {
		t.Fatalf("pin = %v %v", got, pinned)
	}
	// A token whose organization is not a UUID is pinned to nothing.
	got, pinned = cliOrganizationPin(cliCtx("not-a-uuid"))
	if !pinned || got != uuid.Nil {
		t.Fatalf("unparseable organization must pin to nil, got %v %v", got, pinned)
	}
}

func TestGetUserIDAcceptsTheCLILoginIdentityOnly(t *testing.T) {
	h := &OrgHandler{}
	uid, err := h.getUserID(cliCtx(cliPinOrgA), http.Header{})
	if err != nil || uid != uuid.MustParse(cliPinUser) {
		t.Fatalf("CLI login identity should be accepted, got %v %v", uid, err)
	}
	// A user id in context without the CLI marker is what an API key or an
	// internal caller could carry; it is not an organization-service login.
	plain := contextkeys.WithUserID(context.Background(), cliPinUser)
	if _, err := h.getUserID(plain, http.Header{}); err == nil {
		t.Fatal("a bare user id must not authenticate")
	}
	keyCtx := contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant", "hash", "key-1")
	if _, err := h.getUserID(keyCtx, http.Header{}); err == nil {
		t.Fatal("an API key must not authenticate to the organization service")
	}
	// The marker with no usable user id authenticates nobody.
	if _, err := h.getUserID(contextkeys.WithCLIOrganizationID(context.Background(), cliPinOrgA), http.Header{}); err == nil {
		t.Fatal("CLI marker without a user id must not authenticate")
	}
}

// A login token for organization A must not list members of organization B,
// even when the same user belongs to both. The pin is checked before the
// membership lookup, so no store is needed to prove the refusal.
func TestListMembersRefusesAnotherOrganizationForACLILogin(t *testing.T) {
	h := &OrgHandler{}
	_, err := h.ListMembers(cliCtx(cliPinOrgA), connect.NewRequest(&orgv1.ListMembersRequest{OrganizationId: cliPinOrgB}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}
