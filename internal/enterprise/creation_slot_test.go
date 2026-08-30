package enterprise

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

func TestReserveResourceSlotNoOpPaths(t *testing.T) {
	ctx := context.Background()

	// Managed tenant: unlimited (bypass), no lock, no error.
	slot, err := ReserveResourceSlot(managedCtx(), nil, nil, UsageTypeAgents, "SELECT 1", nil, 1, "agent", "t1")
	if err != nil || slot == nil || slot.conn != nil {
		t.Fatalf("managed tenant must get a no-op slot, got slot=%+v err=%v", slot, err)
	}
	slot.Confirm(ctx)
	slot.Release() // must be safe repeatedly on a no-op slot

	// Unlimited via explicit -1.
	mon := stubMonitor{state: &LicenseState{
		Active: true, Status: "active", Tier: "enterprise",
		UsageLimits: []UsageLimit{{Type: UsageTypeAgents, Limit: -1}},
	}}
	slot, err = ReserveResourceSlot(ctx, nil, mon, UsageTypeAgents, "SELECT 1", nil, 1, "agent", "t1")
	if err != nil || slot.conn != nil {
		t.Fatalf("unlimited plan must get a no-op slot, got err=%v", err)
	}

	// Limit 0: unavailable, error without any DB. (Dev builds resolve all
	// entitlements as unlimited, so the gate cannot fire there.)
	//
	// No shipped tier grants 0 channel bindings any more, and an untagged
	// (CE) test binary resolves through CEUsageLimits rather than the stub
	// license state, so seed both sources to keep the assertion about the
	// limit-0 branch itself, in either edition.
	if !IsDev() {
		saved := CEUsageLimits[UsageTypeChannelBindings]
		CEUsageLimits[UsageTypeChannelBindings] = 0
		t.Cleanup(func() { CEUsageLimits[UsageTypeChannelBindings] = saved })

		mon = stubMonitor{state: &LicenseState{
			Active: true, Status: "active", Tier: "basic",
			UsageLimits: []UsageLimit{{Type: UsageTypeChannelBindings, Limit: 0}},
		}}
		if _, err = ReserveResourceSlot(ctx, nil, mon, UsageTypeChannelBindings, "SELECT 1", nil, 1, "channel binding", "t1"); err == nil {
			t.Fatal("limit 0 must reserve nothing and error")
		}
	}

	// Capped but nil DB: no read model to count, no-op slot.
	mon = stubMonitor{state: &LicenseState{
		Active: true, Status: "active", Tier: "basic",
		UsageLimits: []UsageLimit{{Type: UsageTypeAgents, Limit: 10}},
	}}
	slot, err = ReserveResourceSlot(ctx, nil, mon, UsageTypeAgents, "SELECT 1", nil, 1, "agent", "t1")
	if err != nil || slot.conn != nil {
		t.Fatalf("nil db must get a no-op slot, got err=%v", err)
	}
}

// TestReserveResourceSlotSerializesConcurrentCreators drives the real
// advisory-lock path against local Postgres: N concurrent creators race for a
// cap of 3; exactly 3 must win, even though each winner's INSERT is delayed
// to model async projection lag. Skipped when Postgres is unreachable.
func TestReserveResourceSlotSerializesConcurrentCreators(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds do not enforce limits")
	}
	db, err := sqlx.Connect("postgres", "postgres://postgres:postgres@localhost:5432/everstack?sslmode=disable")
	if err != nil {
		t.Skipf("local postgres unavailable: %v", err)
	}
	defer db.Close()

	table := fmt.Sprintf("slot_test_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE %s (tenant_id text NOT NULL, id serial)`, table)); err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer db.Exec(fmt.Sprintf(`DROP TABLE %s`, table)) //nolint:errcheck

	const cap = 3
	const creators = 8
	mon := stubMonitor{state: &LicenseState{
		Active: true, Status: "active", Tier: "basic",
		UsageLimits: []UsageLimit{{Type: UsageTypeAgents, Limit: cap}},
	}}
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE tenant_id = $1`, table)

	var wins, losses atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < creators; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			slot, err := ReserveResourceSlot(ctx, db, mon, UsageTypeAgents, countQuery, []interface{}{"t1"}, 1, "agent", "t1")
			if err != nil {
				if !strings.Contains(err.Error(), "limit reached") {
					t.Errorf("unexpected error: %v", err)
				}
				losses.Add(1)
				return
			}
			defer slot.Release()
			// Model async projection lag between command dispatch and the
			// read-model write: without the Confirm barrier this is exactly
			// the window that lets concurrent creators past the cap.
			time.Sleep(30 * time.Millisecond)
			if _, err := db.Exec(fmt.Sprintf(`INSERT INTO %s (tenant_id) VALUES ($1)`, table), "t1"); err != nil {
				t.Errorf("insert: %v", err)
				return
			}
			wins.Add(1)
			slot.Confirm(ctx)
		}()
	}
	wg.Wait()

	if wins.Load() != cap {
		t.Fatalf("expected exactly %d creators to pass, got %d (losses %d)", cap, wins.Load(), losses.Load())
	}
	var rows int64
	if err := db.Get(&rows, countQuery, "t1"); err != nil {
		t.Fatalf("final count: %v", err)
	}
	if rows != cap {
		t.Fatalf("read model has %d rows, cap is %d — race not closed", rows, cap)
	}

	// A different tenant must not be blocked by t1's lock or count.
	slot, err := ReserveResourceSlot(context.Background(), db, mon, UsageTypeAgents, countQuery, []interface{}{"t2"}, 1, "agent", "t2")
	if err != nil {
		t.Fatalf("tenant t2 must have its own slot space: %v", err)
	}
	slot.Release()
}
