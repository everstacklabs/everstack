package channels

import (
	"context"
	"testing"
)

// TestPostgresStore_EmptyTenantGuards locks the defense-in-depth guards
// in postgres_store.go: every method that takes a tenantID short-circuits
// before any SQL when the tenant is empty. The 2026-05-06 P0 leak was
// caused by repository methods running unscoped queries when the
// handler had failed to enforce a tenant; these guards make the repo
// safe even if a future caller forgets the handler-level check.
//
// We pass a nil *sqlx.DB: if any code path tried to actually execute a
// query, the test would panic. Reaching the assertions instead proves
// the early return.
func TestPostgresStore_EmptyTenantGuards(t *testing.T) {
	s := &PostgresStore{db: nil}
	ctx := context.Background()

	t.Run("GetChannelConfig with empty tenant returns (nil, nil)", func(t *testing.T) {
		cfg, err := s.GetChannelConfig(ctx, "some-id", "")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if cfg != nil {
			t.Fatalf("expected nil cfg, got %+v", cfg)
		}
	})

	t.Run("DeleteChannelConfig with empty tenant errors before SQL", func(t *testing.T) {
		err := s.DeleteChannelConfig(ctx, "some-id", "")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("UpdateChannelConfig with empty tenant errors before SQL", func(t *testing.T) {
		err := s.UpdateChannelConfig(ctx, &ChannelConfigRecord{ID: "x", TenantID: ""})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("ListChannelConfigs with empty tenant returns empty page", func(t *testing.T) {
		records, total, err := s.ListChannelConfigs(ctx, "", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if len(records) != 0 || total != 0 {
			t.Fatalf("expected empty page, got len=%d total=%d", len(records), total)
		}
	})
}
