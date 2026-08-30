package browserpool

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestUsagePricingAppliesMinimumAndPerSecondBilling(t *testing.T) {
	t.Parallel()

	pricing, err := NewUsagePricing(0.01, 1, 60)
	if err != nil {
		t.Fatalf("NewUsagePricing() error = %v", err)
	}
	start := time.Unix(1_000, 0)

	tests := []struct {
		name           string
		elapsed        time.Duration
		wantDuration   int64
		wantBillable   int64
		wantCostMicros int64
	}{
		{name: "sub-second still gets minimum", elapsed: 400 * time.Millisecond, wantDuration: 1, wantBillable: 60, wantCostMicros: 167},
		{name: "under a minute gets minimum", elapsed: 45 * time.Second, wantDuration: 45, wantBillable: 60, wantCostMicros: 167},
		{name: "per second after minimum", elapsed: 61 * time.Second, wantDuration: 61, wantBillable: 61, wantCostMicros: 170},
		{name: "one hour is one cent", elapsed: time.Hour, wantDuration: 3600, wantBillable: 3600, wantCostMicros: 10_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration, billable, cost := pricing.PriceWindow(start, start.Add(tt.elapsed))
			if duration != tt.wantDuration || billable != tt.wantBillable || cost != tt.wantCostMicros {
				t.Fatalf(
					"PriceWindow() = duration %d, billable %d, cost %d; want %d, %d, %d",
					duration, billable, cost,
					tt.wantDuration, tt.wantBillable, tt.wantCostMicros,
				)
			}
		})
	}
}

func TestUsagePricingRejectsIdleOrZeroPriceContract(t *testing.T) {
	t.Parallel()

	if _, err := NewUsagePricing(0, 1, 60); err == nil {
		t.Fatal("zero browser rate accepted")
	}
	if _, err := NewUsagePricing(0.01, 0, 60); err == nil {
		t.Fatal("zero billing increment accepted")
	}
	if _, err := NewUsagePricing(0.01, 60, 1); err == nil {
		t.Fatal("minimum shorter than increment accepted")
	}
}

func TestPostgresUsageMeterTotalsMapsAggregateAliases(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	pricing, err := NewUsagePricing(0.01, 1, 60)
	if err != nil {
		t.Fatal(err)
	}
	meter, err := NewPostgresUsageMeter(sqlx.NewDb(db, "sqlmock"), pricing)
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(`COALESCE\(SUM\(duration_seconds\), 0\) AS runtime_seconds`).
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"runtime_seconds", "cost_micros"}).
			AddRow(int64(125), int64(42)))
	mock.ExpectQuery(`SELECT started_at[\s\S]*ended_at IS NULL`).
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"started_at"}))

	totals, err := meter.TotalsForTenant(context.Background(), "tenant-1", time.Unix(1_000, 0))
	if err != nil {
		t.Fatalf("TotalsForTenant() error = %v", err)
	}
	if totals.RuntimeSeconds != 125 || totals.CostMicros != 42 {
		t.Fatalf("TotalsForTenant() = %+v, want runtime_seconds=125 cost_micros=42", totals)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
