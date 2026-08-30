package oidc

import (
	"context"
	"testing"
)

func TestMemClientStorePreservesPlatformClientKind(t *testing.T) {
	t.Parallel()

	store := NewMemClientStore()
	store.Register(Client{
		ID:           "everstack-operations",
		Kind:         ClientKindPlatform,
		RedirectURIs: []string{"https://operations.ops.example/auth/oidc/callback"},
	})

	client, ok, err := store.Get(context.Background(), "everstack-operations")
	if err != nil || !ok {
		t.Fatalf("Get() = ok %v, err %v", ok, err)
	}
	if client.Kind != ClientKindPlatform {
		t.Fatalf("Kind = %q, want %q", client.Kind, ClientKindPlatform)
	}
}
