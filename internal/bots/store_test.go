package bots

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStoreRejectsMissingTenantBeforeDatabase(t *testing.T) {
	store := NewStore(nil)
	ctx := context.Background()
	for _, call := range []func() error{
		func() error { _, err := store.List(ctx, ""); return err }, func() error { _, err := store.Get(ctx, "", "id"); return err },
		func() error { return store.Insert(ctx, &Bot{}) }, func() error { return store.Update(ctx, &Bot{}) }, func() error { return store.Delete(ctx, "", "id", time.Time{}) },
		func() error { _, err := store.Lock(ctx, "", "id"); return err },
	} {
		if err := call(); !errors.Is(err, ErrNoTenant) {
			t.Fatalf("missing tenant: %v", err)
		}
	}
}
func TestMutationLockDoesNotUseDatabaseAndHonorsCancellation(t *testing.T) {
	store := NewStore(nil)
	release, err := store.Lock(context.Background(), "tenant", "bot")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = store.Lock(ctx, "tenant", "bot"); !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked mutation: %v", err)
	}
	release()
	release, err = store.Lock(context.Background(), "tenant", "bot")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
