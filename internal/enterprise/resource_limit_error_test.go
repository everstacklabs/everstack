package enterprise

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// TestResourceLimitErrorsAreTagged: callers map ErrResourceLimit to a
// user-facing ResourceExhausted and everything else to a generic Internal,
// so only a genuine quota refusal may carry the tag, and a failed count
// query (which wraps raw driver text) must not.
func TestResourceLimitErrorsAreTagged(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds do not enforce limits")
	}
	mon := stubMonitor{state: &LicenseState{
		Active: true, Status: "active", Tier: "basic",
		UsageLimits: []UsageLimit{{Type: UsageTypeAgents, Limit: 2}},
	}}
	savedCE := CEUsageLimits[UsageTypeAgents]
	CEUsageLimits[UsageTypeAgents] = 2
	t.Cleanup(func() { CEUsageLimits[UsageTypeAgents] = savedCE })

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	err = CheckResourceLimit(context.Background(), db, mon, UsageTypeAgents, "SELECT COUNT(*) FROM agents", nil, 1, "agent")
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("over-limit err = %v, want ErrResourceLimit", err)
	}
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != 2 || le.Used != 2 || le.Type != UsageTypeAgents {
		t.Fatalf("over-limit err = %#v, want a *LimitError carrying 2/2 AGENTS", err)
	}
	if strings.ContainsRune(err.Error(), '\u2014') || !strings.Contains(err.Error(), "https://everstack.ai/pricing") {
		t.Fatalf("over-limit message must name an upgrade path and carry no em dash: %q", err.Error())
	}

	mock.ExpectQuery(`SELECT COUNT`).WillReturnError(errors.New("dial tcp 10.0.3.7:5432: connection refused"))
	err = CheckResourceLimit(context.Background(), db, mon, UsageTypeAgents, "SELECT COUNT(*) FROM agents", nil, 1, "agent")
	if err == nil || errors.Is(err, ErrResourceLimit) {
		t.Fatalf("count failure err = %v, must not be tagged ErrResourceLimit", err)
	}

	CEUsageLimits[UsageTypeAgents] = 0
	zero := stubMonitor{state: &LicenseState{
		Active: true, Status: "active", Tier: "basic",
		UsageLimits: []UsageLimit{{Type: UsageTypeAgents, Limit: 0}},
	}}
	if _, err := ReserveResourceSlot(context.Background(), nil, zero, UsageTypeAgents, "SELECT 1", nil, 1, "agent", "t1"); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("limit-0 err = %v, want ErrResourceLimit", err)
	}
}
