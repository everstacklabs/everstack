package v1

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

const testCallbackSecret = "0f1e2d3c-4b5a-4968-8776-655443322110"

func withFreshCallbackSecretStore(t *testing.T) {
	t.Helper()
	prev := callbackSecretStore
	callbackSecretStore = &CallbackSecretStore{secrets: make(map[string]*callbackSecretEntry)}
	t.Cleanup(func() { callbackSecretStore = prev })
}

func storeSecret(s *Server, secret, plan string) error {
	_, err := s.StoreUpgradeCallbackSecret(context.Background(), connect.NewRequest(&gatewaypb.StoreUpgradeCallbackSecretRequest{
		CallbackSecret: secret, PlanTier: plan,
	}))
	return err
}

func TestStoreUpgradeCallbackSecretCannotOverwrite(t *testing.T) {
	withFreshCallbackSecretStore(t)
	s := &Server{ctx: context.Background()}

	if err := storeSecret(s, testCallbackSecret, "pro"); err != nil {
		t.Fatalf("first store: %v", err)
	}
	err := storeSecret(s, testCallbackSecret, "enterprise")
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("overwrite: want AlreadyExists, got %v", err)
	}
	entry, ok := callbackSecretStore.Get(testCallbackSecret)
	if !ok || entry.PlanTier != "pro" {
		t.Fatalf("pending upgrade was rewritten: %+v", entry)
	}
}

func TestStoreUpgradeCallbackSecretRefusedOnSharedGateway(t *testing.T) {
	withFreshCallbackSecretStore(t)
	s := &Server{ctx: context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)}

	if err := storeSecret(s, testCallbackSecret, "pro"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("shared gateway: want PermissionDenied, got %v", err)
	}
	if _, ok := callbackSecretStore.Get(testCallbackSecret); ok {
		t.Fatal("shared gateway stored a callback secret")
	}
}

func TestStoreUpgradeCallbackSecretRejectsShortSecret(t *testing.T) {
	withFreshCallbackSecretStore(t)
	s := &Server{ctx: context.Background()}
	if err := storeSecret(s, "guessable", "pro"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("short secret: want InvalidArgument, got %v", err)
	}
}

func TestStoreUpgradeCallbackSecretIsBounded(t *testing.T) {
	withFreshCallbackSecretStore(t)
	s := &Server{ctx: context.Background()}
	for i := 0; i < maxPendingCallbackSecrets; i++ {
		if err := storeSecret(s, fmt.Sprintf("%s-%04d", testCallbackSecret, i), "pro"); err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
	}
	if err := storeSecret(s, testCallbackSecret+"-over", "pro"); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("over cap: want ResourceExhausted, got %v", err)
	}

	// Expired entries free their slots.
	callbackSecretStore.mu.Lock()
	for _, e := range callbackSecretStore.secrets {
		e.ExpiresAt = time.Now().Add(-time.Second)
	}
	callbackSecretStore.mu.Unlock()
	if err := storeSecret(s, testCallbackSecret+"-after-expiry", "pro"); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
}
