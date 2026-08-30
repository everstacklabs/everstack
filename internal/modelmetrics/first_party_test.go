package modelmetrics

import (
	"context"
	"strings"
	"testing"
	"time"
)

// capturingRepository records the query it was handed so tests can assert the
// service, not the caller, decides which tenants count as first-party.
type capturingRepository struct {
	report    RawReport
	breakdown RawBreakdown
	lastQuery Query
}

func (c *capturingRepository) LoadReport(_ context.Context, query Query) (RawReport, error) {
	c.lastQuery = query
	return c.report, nil
}

func (c *capturingRepository) LoadBreakdown(
	context.Context,
	BreakdownQuery,
) (RawBreakdown, error) {
	return c.breakdown, nil
}

func fixedNow() time.Time {
	return time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
}

func singleBucket(tenantCount, externalTenantCount, requests uint64) RawReport {
	return RawReport{Buckets: []Bucket{{
		Period:              fixedNow().Add(-2 * time.Hour),
		TenantCount:         tenantCount,
		ExternalTenantCount: externalTenantCount,
		Metrics: Metrics{
			Requests:       requests,
			InputTokens:    10,
			OutputTokens:   5,
			LatencyTotalMS: 100,
			LatencySamples: requests,
		},
	}}}
}

func reportFor(t *testing.T, repo Repository, config Config) Report {
	t.Helper()
	config.Now = fixedNow
	service := NewService(repo, config)
	report, err := service.Report(context.Background(), Query{
		Kind:     KindModel,
		Key:      "anthropic/claude-opus-4-7",
		Window:   Window30Days,
		Interval: IntervalHour,
	})
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	return report
}

// Without an operator-configured first-party list the carve-out must stay
// inert even though ExternalTenantCount is zero-valued. A repository that does
// not populate the column must not accidentally publish everything.
func TestFirstPartyCarveOutIsInertByDefault(t *testing.T) {
	t.Parallel()

	report := reportFor(t, &capturingRepository{report: singleBucket(1, 0, 3)}, Config{})

	if report.Status != StatusInsufficientData {
		t.Fatalf("status = %q, want %q", report.Status, StatusInsufficientData)
	}
	if len(report.Points) != 0 {
		t.Fatalf("points = %d, want 0", len(report.Points))
	}
	if report.Coverage.SuppressedBuckets != 1 {
		t.Fatalf("suppressed buckets = %d, want 1", report.Coverage.SuppressedBuckets)
	}
	if report.Coverage.FirstPartyBuckets != 0 {
		t.Fatalf("first-party buckets = %d, want 0", report.Coverage.FirstPartyBuckets)
	}
}

// A bucket whose contributing tenants are all first-party is self-disclosure
// and publishes below the floor.
func TestFirstPartyOnlyBucketBypassesFloor(t *testing.T) {
	t.Parallel()

	report := reportFor(t, &capturingRepository{report: singleBucket(1, 0, 3)}, Config{
		FirstPartyTenants: []string{"8515093e-16b3-43fe-80eb-e742815391aa"},
	})

	if report.Status != StatusAvailable && report.Status != StatusStale {
		t.Fatalf("status = %q, want available or stale", report.Status)
	}
	if len(report.Points) != 1 {
		t.Fatalf("points = %d, want 1", len(report.Points))
	}
	if report.Coverage.FirstPartyBuckets != 1 {
		t.Fatalf("first-party buckets = %d, want 1", report.Coverage.FirstPartyBuckets)
	}
	if report.Summary.Requests != 3 {
		t.Fatalf("summary requests = %d, want 3", report.Summary.Requests)
	}
}

// The moment a bucket also contains a tenant outside the first-party list the
// normal floor applies again. This is the property that stops a customer being
// exposed by sharing an hour with Everstack's own traffic.
func TestMixedBucketStillRequiresFloor(t *testing.T) {
	t.Parallel()

	report := reportFor(t, &capturingRepository{report: singleBucket(2, 1, 3)}, Config{
		FirstPartyTenants: []string{"8515093e-16b3-43fe-80eb-e742815391aa"},
	})

	if report.Status != StatusInsufficientData {
		t.Fatalf("status = %q, want %q", report.Status, StatusInsufficientData)
	}
	if len(report.Points) != 0 {
		t.Fatalf("points = %d, want 0", len(report.Points))
	}
	if report.Coverage.SuppressedBuckets != 1 {
		t.Fatalf("suppressed buckets = %d, want 1", report.Coverage.SuppressedBuckets)
	}
}

// A bucket that clears the real floor is published as ordinary k-anonymous
// aggregate, not counted as first-party disclosure.
func TestFloorClearingBucketIsNotLabelledFirstParty(t *testing.T) {
	t.Parallel()

	report := reportFor(
		t,
		&capturingRepository{report: singleBucket(
			MinimumPublicTenants,
			MinimumPublicTenants-1,
			MinimumPublicRequests,
		)},
		Config{FirstPartyTenants: []string{"8515093e-16b3-43fe-80eb-e742815391aa"}},
	)

	if len(report.Points) != 1 {
		t.Fatalf("points = %d, want 1", len(report.Points))
	}
	if report.Coverage.FirstPartyBuckets != 0 {
		t.Fatalf("first-party buckets = %d, want 0", report.Coverage.FirstPartyBuckets)
	}
}

// An empty bucket is never publishable, first-party or not.
func TestZeroRequestBucketIsNeverEligible(t *testing.T) {
	t.Parallel()

	report := reportFor(t, &capturingRepository{report: singleBucket(1, 0, 0)}, Config{
		FirstPartyTenants: []string{"8515093e-16b3-43fe-80eb-e742815391aa"},
	})

	if len(report.Points) != 0 {
		t.Fatalf("points = %d, want 0", len(report.Points))
	}
}

// The list is operator config: it reaches the repository from Service, is
// normalised, and is never sourced from the caller's query.
func TestServiceInjectsNormalizedFirstPartyTenantsIntoQuery(t *testing.T) {
	t.Parallel()

	repo := &capturingRepository{report: singleBucket(1, 0, 3)}
	reportFor(t, repo, Config{FirstPartyTenants: []string{
		"  8515093E-16B3-43FE-80EB-E742815391AA  ",
		"e4409d61-0ff4-45bf-b9be-47bb0bd72986",
		"e4409d61-0ff4-45bf-b9be-47bb0bd72986",
		"   ",
	}})

	got := repo.lastQuery.FirstPartyTenants
	want := []string{
		"8515093e-16b3-43fe-80eb-e742815391aa",
		"e4409d61-0ff4-45bf-b9be-47bb0bd72986",
	}
	if len(got) != len(want) {
		t.Fatalf("first-party tenants = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("first-party tenants = %v, want %v", got, want)
		}
	}
}

// The generated SQL must bind the first-party array before the WHERE clause
// arguments, because its placeholder appears earlier in the statement.
func TestReportSQLBindsFirstPartyArrayFirst(t *testing.T) {
	t.Parallel()

	query := Query{
		Kind:              KindModel,
		Key:               "anthropic/claude-opus-4-7",
		Interval:          IntervalDay,
		StartTime:         fixedNow().Add(-24 * time.Hour),
		EndTime:           fixedNow(),
		FirstPartyTenants: []string{"tenant-a"},
	}
	sql, args, err := buildReportSQL(query)
	if err != nil {
		t.Fatalf("buildReportSQL() error = %v", err)
	}
	if !strings.Contains(sql, "uniqExactIf(tenant_id, NOT has(?, lowerUTF8(tenant_id)))") {
		t.Fatalf("sql missing first-party expression:\n%s", sql)
	}
	if len(args) == 0 {
		t.Fatal("expected bound arguments")
	}
	tenants, ok := args[0].([]string)
	if !ok || len(tenants) != 1 || tenants[0] != "tenant-a" {
		t.Fatalf("args[0] = %#v, want []string{\"tenant-a\"}", args[0])
	}
	// The placeholder count must match the argument count, otherwise
	// ClickHouse binds the wrong value to the wrong slot.
	if got, want := strings.Count(sql, "?"), len(args); got != want {
		t.Fatalf("placeholders = %d, args = %d", got, want)
	}
}

// With the carve-out off the statement binds nothing extra, so the WHERE
// arguments keep their original positions.
func TestReportSQLOmitsBindWhenCarveOutDisabled(t *testing.T) {
	t.Parallel()

	sql, args, err := buildReportSQL(Query{
		Kind:      KindProvider,
		Key:       "anthropic",
		Interval:  IntervalDay,
		StartTime: fixedNow().Add(-24 * time.Hour),
		EndTime:   fixedNow(),
	})
	if err != nil {
		t.Fatalf("buildReportSQL() error = %v", err)
	}
	if strings.Contains(sql, "has(?") {
		t.Fatalf("expected no first-party bind:\n%s", sql)
	}
	if got, want := strings.Count(sql, "?"), len(args); got != want {
		t.Fatalf("placeholders = %d, args = %d", got, want)
	}
}

// Same ordering guarantee for the provider breakdown statement.
func TestBreakdownSQLBindsFirstPartyArrayFirst(t *testing.T) {
	t.Parallel()

	sql, args, err := buildBreakdownSQL(BreakdownQuery{
		Provider:          "anthropic",
		Metric:            MetricTokens,
		Interval:          IntervalDay,
		StartTime:         fixedNow().Add(-24 * time.Hour),
		EndTime:           fixedNow(),
		FirstPartyTenants: []string{"tenant-a", "tenant-b"},
	})
	if err != nil {
		t.Fatalf("buildBreakdownSQL() error = %v", err)
	}
	tenants, ok := args[0].([]string)
	if !ok || len(tenants) != 2 {
		t.Fatalf("args[0] = %#v, want the first-party array", args[0])
	}
	if got, want := strings.Count(sql, "?"), len(args); got != want {
		t.Fatalf("placeholders = %d, args = %d", got, want)
	}
}
