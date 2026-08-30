package hosting

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	internalcfg "github.com/everstacklabs/everstack/internal/config"
)

func TestQuotaForTier(t *testing.T) {
	plans := &internalcfg.PlansConfig{Plans: map[string]internalcfg.PlanConfig{
		"free": {
			Tier: "free",
			UsageLimits: []internalcfg.PlanUsageLimit{
				{Type: UsageLimitHostedSites, Value: 3},
				{Type: UsageLimitHostingStorageBytes, Value: 500},
			},
		},
		"pro": {
			Tier: "pro",
			UsageLimits: []internalcfg.PlanUsageLimit{
				{Type: UsageLimitHostedSites, Value: 30},
				{Type: UsageLimitHostingStorageBytes, Value: 50_000},
			},
		},
	}}

	tests := []struct {
		name        string
		tier        string
		wantTier    string
		wantSites   int64
		wantStorage int64
	}{
		{name: "explicit tier", tier: "pro", wantTier: "pro", wantSites: 30, wantStorage: 50_000},
		{name: "empty tier is free", tier: "", wantTier: "free", wantSites: 3, wantStorage: 500},
		{name: "unknown tier fails closed to free", tier: "legacy", wantTier: "free", wantSites: 3, wantStorage: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := QuotaForTier(plans, tt.tier)
			if err != nil {
				t.Fatalf("QuotaForTier: %v", err)
			}
			if got.Tier != tt.wantTier || got.MaxSites != tt.wantSites || got.MaxStorageBytes != tt.wantStorage {
				t.Fatalf("quota = %+v, want tier=%q sites=%d storage=%d", got, tt.wantTier, tt.wantSites, tt.wantStorage)
			}
		})
	}
}

func TestQuotaForTierRequiresBothHostingLimits(t *testing.T) {
	plans := &internalcfg.PlansConfig{Plans: map[string]internalcfg.PlanConfig{
		"free": {
			Tier: "free",
			UsageLimits: []internalcfg.PlanUsageLimit{
				{Type: UsageLimitHostedSites, Value: 3},
			},
		},
	}}

	if _, err := QuotaForTier(plans, "free"); err == nil {
		t.Fatal("missing hosting storage limit should fail closed")
	}
}

func TestTenantQuotaCheck(t *testing.T) {
	quota := TenantQuota{Tier: "free", MaxSites: 3, MaxStorageBytes: 500}

	if err := quota.Check(TenantUsage{Sites: 2, StorageBytes: 400}, TenantUsage{Sites: 1, StorageBytes: 100}); err != nil {
		t.Fatalf("exactly at quota should pass: %v", err)
	}

	err := quota.Check(TenantUsage{Sites: 3, StorageBytes: 100}, TenantUsage{Sites: 1})
	var exceeded *QuotaExceededError
	if !errors.As(err, &exceeded) || exceeded.Resource != QuotaResourceSites {
		t.Fatalf("site error = %v, want site quota error", err)
	}

	err = quota.Check(TenantUsage{Sites: 1, StorageBytes: 450}, TenantUsage{StorageBytes: 51})
	if !errors.As(err, &exceeded) || exceeded.Resource != QuotaResourceStorage {
		t.Fatalf("storage error = %v, want storage quota error", err)
	}

	unlimited := TenantQuota{Tier: "enterprise", MaxSites: -1, MaxStorageBytes: -1}
	if err := unlimited.Check(TenantUsage{Sites: 10_000, StorageBytes: 1 << 60}, TenantUsage{Sites: 1, StorageBytes: 1 << 40}); err != nil {
		t.Fatalf("unlimited quota should pass: %v", err)
	}

	// A downgrade can leave existing usage above a new ceiling. Operations
	// that do not increase that resource must remain possible so the tenant
	// can update or delete its way back under the limit.
	if err := quota.Check(TenantUsage{Sites: 4, StorageBytes: 600}, TenantUsage{}); err != nil {
		t.Fatalf("zero-increase operation above quota should pass: %v", err)
	}
}

func TestPlanQuotaResolverUsesOrganizationTier(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	plans := &internalcfg.PlansConfig{Plans: map[string]internalcfg.PlanConfig{
		"free": {
			Tier: "free",
			UsageLimits: []internalcfg.PlanUsageLimit{
				{Type: UsageLimitHostedSites, Value: 3},
				{Type: UsageLimitHostingStorageBytes, Value: 500},
			},
		},
		"pro": {
			Tier: "pro",
			UsageLimits: []internalcfg.PlanUsageLimit{
				{Type: UsageLimitHostedSites, Value: 30},
				{Type: UsageLimitHostingStorageBytes, Value: 50_000},
			},
		},
	}}
	mock.ExpectQuery("SELECT COALESCE\\(NULLIF\\(o\\.plan_tier, ''\\), 'free'\\)").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"plan_tier"}).AddRow("pro"))

	quota, err := NewPlanQuotaResolver(db, plans).Resolve(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if quota.Tier != "pro" || quota.MaxSites != 30 || quota.MaxStorageBytes != 50_000 {
		t.Fatalf("quota = %+v", quota)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanQuotaResolverMapsInstanceToOrganizationTier(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	plans := &internalcfg.PlansConfig{Plans: map[string]internalcfg.PlanConfig{
		"free": {
			Tier: "free",
			UsageLimits: []internalcfg.PlanUsageLimit{
				{Type: UsageLimitHostedSites, Value: 3},
				{Type: UsageLimitHostingStorageBytes, Value: 500},
			},
		},
		"pro": {
			Tier: "pro",
			UsageLimits: []internalcfg.PlanUsageLimit{
				{Type: UsageLimitHostedSites, Value: 30},
				{Type: UsageLimitHostingStorageBytes, Value: 50_000},
			},
		},
	}}
	mock.ExpectQuery("(?s)FROM everstack\\.organizations o.*FROM everstack\\.tenant_config tc").
		WithArgs("instance-1").
		WillReturnRows(sqlmock.NewRows([]string{"plan_tier"}).AddRow("pro"))

	quota, err := NewPlanQuotaResolver(db, plans).Resolve(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if quota.Tier != "pro" || quota.MaxSites != 30 || quota.MaxStorageBytes != 50_000 {
		t.Fatalf("quota = %+v", quota)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanQuotaResolverFallsBackOnlyWhenPlatformOrganizationIsMissing(t *testing.T) {
	platformRaw, platformMock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer platformRaw.Close()
	gatewayRaw, gatewayMock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer gatewayRaw.Close()
	platformDB := sqlx.NewDb(platformRaw, "sqlmock")
	gatewayDB := sqlx.NewDb(gatewayRaw, "sqlmock")
	plans := &internalcfg.PlansConfig{Plans: map[string]internalcfg.PlanConfig{
		"free": {
			Tier: "free",
			UsageLimits: []internalcfg.PlanUsageLimit{
				{Type: UsageLimitHostedSites, Value: 3},
				{Type: UsageLimitHostingStorageBytes, Value: 500},
			},
		},
	}}

	platformMock.ExpectQuery("SELECT COALESCE\\(NULLIF\\(o\\.plan_tier, ''\\), 'free'\\)").
		WithArgs("local-claim-org").
		WillReturnError(sql.ErrNoRows)
	gatewayMock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM everstack.organizations WHERE id = \\$1\\)").
		WithArgs("local-claim-org").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	quota, err := NewPlanQuotaResolverWithFallback(platformDB, gatewayDB, plans).
		Resolve(context.Background(), "local-claim-org")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if quota.Tier != "free" || quota.MaxSites != 3 {
		t.Fatalf("quota = %+v", quota)
	}
	if err := platformMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := gatewayMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
