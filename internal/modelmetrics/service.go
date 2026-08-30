// Package modelmetrics builds privacy-safe, cross-tenant model activity
// reports. The repository returns tenant-aware aggregate buckets; Service is
// the only place allowed to turn those buckets into a public response.
package modelmetrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SchemaVersion                 = "1.0"
	SemanticVersion               = "2026-07-30"
	MinimumPublicTenants          = uint64(5)
	MinimumPublicRequests         = uint64(100)
	MaximumTestingThresholdWindow = 48 * time.Hour
	DefaultBreakdownSeriesLimit   = uint32(10)
	MaximumBreakdownSeriesLimit   = uint32(12)
)

type Kind string

const (
	KindModel     Kind = "model"
	KindProvider  Kind = "provider"
	KindPublisher Kind = "publisher"
)

type Window string

const (
	Window7Days   Window = "7d"
	Window30Days  Window = "30d"
	Window90Days  Window = "90d"
	Window6Months Window = "6m"
	Window1Year   Window = "1y"
	WindowAll     Window = "all"
)

type Interval string

const (
	IntervalHour  Interval = "hour"
	IntervalDay   Interval = "day"
	IntervalMonth Interval = "month"
)

type Metric string

const (
	MetricTokens      Metric = "tokens"
	MetricRequests    Metric = "requests"
	MetricTTFT        Metric = "ttft"
	MetricThroughput  Metric = "throughput"
	MetricSuccessRate Metric = "success_rate"
)

type Status string

const (
	StatusAvailable        Status = "available"
	StatusInsufficientData Status = "insufficient_data"
	StatusStale            Status = "stale"
)

type Query struct {
	Kind     Kind
	Key      string
	Window   Window
	Interval Interval

	// StartTime and EndTime are resolved by Service after validation. They are
	// deliberately not part of the public wire contract.
	StartTime time.Time
	EndTime   time.Time
	// FirstPartyTenants is injected by Service from operator config. It is
	// never accepted from a caller.
	FirstPartyTenants []string
}

type CompareQuery struct {
	Kind     Kind
	Keys     []string
	Metric   Metric
	Window   Window
	Interval Interval
}

type BreakdownQuery struct {
	Provider string
	Metric   Metric
	Window   Window
	Interval Interval
	Limit    uint32

	// StartTime and EndTime are resolved by Service after validation.
	StartTime time.Time
	EndTime   time.Time
	// FirstPartyTenants is injected by Service from operator config. It is
	// never accepted from a caller.
	FirstPartyTenants []string
}

// Metrics is the additive representation stored in the internal hourly fact
// table. Ratios and averages are derived only after aggregation.
type Metrics struct {
	Requests             uint64
	Errors               uint64
	InputTokens          uint64
	OutputTokens         uint64
	ReasoningTokens      uint64
	CacheReadTokens      uint64
	CacheWriteTokens     uint64
	CostUSD              float64
	LatencyTotalMS       float64
	LatencySamples       uint64
	TTFTTotalMS          float64
	TTFTSamples          uint64
	StreamOutputTokens   uint64
	GenerationDurationMS float64
}

type Bucket struct {
	Period      time.Time
	TenantCount uint64
	// ExternalTenantCount is how many of TenantCount are NOT first-party.
	// Zero with a non-zero TenantCount means the bucket is pure first-party.
	ExternalTenantCount uint64
	Metrics             Metrics
}

type RawReport struct {
	DataSince time.Time
	Buckets   []Bucket
}

type BreakdownBucket struct {
	Period      time.Time
	Key         string
	TenantCount uint64
	// ExternalTenantCount mirrors Bucket.ExternalTenantCount.
	ExternalTenantCount uint64
	Metrics             Metrics
}

type RawBreakdown struct {
	DataSince time.Time
	Buckets   []BreakdownBucket
}

type Repository interface {
	LoadReport(ctx context.Context, query Query) (RawReport, error)
	LoadBreakdown(ctx context.Context, query BreakdownQuery) (RawBreakdown, error)
}

type Config struct {
	MinimumTenants  uint64
	MinimumRequests uint64
	// TestingThresholdsUntil permits lower thresholds only for a short,
	// self-expiring managed-cloud validation window. NewService rejects
	// windows longer than MaximumTestingThresholdWindow.
	TestingThresholdsUntil time.Time
	// FirstPartyTenants lists tenant ids operated by Everstack itself. A
	// bucket built exclusively from these tenants is self-disclosure, not a
	// disclosure about somebody else, so it bypasses the k-anonymity floor.
	// The moment a bucket also contains a tenant outside this list the normal
	// floor applies again, so no customer is ever revealed by the carve-out.
	// Empty (the default) makes the carve-out inert.
	FirstPartyTenants []string
	Now               func() time.Time
}

type Service struct {
	repository Repository
	config     Config
}

func NewService(repository Repository, config Config) *Service {
	if config.Now == nil {
		config.Now = time.Now
	}
	now := config.Now().UTC()
	testingUntil := config.TestingThresholdsUntil.UTC()
	if !testingUntil.After(now) ||
		testingUntil.Sub(now) > MaximumTestingThresholdWindow {
		config.TestingThresholdsUntil = time.Time{}
	} else {
		config.TestingThresholdsUntil = testingUntil
	}
	config.MinimumTenants, config.MinimumRequests = effectiveThresholds(config, now)
	config.FirstPartyTenants = normalizeTenantIDs(config.FirstPartyTenants)
	return &Service{repository: repository, config: config}
}

// normalizeTenantIDs lower-cases, trims, drops blanks and de-duplicates the
// operator-supplied first-party tenant list so bucket comparisons in
// ClickHouse are stable regardless of how the ids were written down.
func normalizeTenantIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.ToLower(strings.TrimSpace(id))
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// bucketEligibility decides whether a bucket may appear in a public response.
//
// Two independent ways to qualify:
//
//   - k-anonymity: enough distinct tenants AND enough requests that no single
//     tenant's behaviour is recoverable from the aggregate.
//   - first-party self-disclosure: every contributing tenant is operated by
//     Everstack, so publishing the bucket reveals only Everstack's own usage.
//
// The second returns true for firstParty so callers can label the result.
// A bucket mixing first-party and customer tenants only ever qualifies via
// the floor, which is what keeps a customer from being exposed by proximity.
// carveOutEnabled must be false whenever the operator configured no
// first-party tenants. ExternalTenantCount is zero-valued for any repository
// that does not populate it, so an ungated carve-out would fail open and
// publish every bucket. The gate makes the default fail closed.
func bucketEligibility(
	carveOutEnabled bool,
	tenantCount, externalTenantCount, requests, minimumTenants, minimumRequests uint64,
) (eligible bool, firstParty bool) {
	if tenantCount == 0 || requests == 0 {
		return false, false
	}
	if tenantCount >= minimumTenants && requests >= minimumRequests {
		return true, false
	}
	if carveOutEnabled && externalTenantCount == 0 {
		return true, true
	}
	return false, false
}

func effectiveThresholds(config Config, now time.Time) (uint64, uint64) {
	minimumTenants := config.MinimumTenants
	if minimumTenants == 0 {
		minimumTenants = MinimumPublicTenants
	}
	minimumRequests := config.MinimumRequests
	if minimumRequests == 0 {
		minimumRequests = MinimumPublicRequests
	}

	testingWindowActive := !config.TestingThresholdsUntil.IsZero() &&
		now.Before(config.TestingThresholdsUntil)
	if !testingWindowActive {
		if minimumTenants < MinimumPublicTenants {
			minimumTenants = MinimumPublicTenants
		}
		if minimumRequests < MinimumPublicRequests {
			minimumRequests = MinimumPublicRequests
		}
	}

	return minimumTenants, minimumRequests
}

type PublicMetrics struct {
	Requests                 uint64  `json:"requests"`
	Successes                uint64  `json:"successes"`
	Errors                   uint64  `json:"errors"`
	InputTokens              uint64  `json:"input_tokens"`
	OutputTokens             uint64  `json:"output_tokens"`
	ReasoningTokens          uint64  `json:"reasoning_tokens"`
	NonReasoningOutputTokens uint64  `json:"non_reasoning_output_tokens"`
	CacheReadTokens          uint64  `json:"cache_read_tokens"`
	CacheWriteTokens         uint64  `json:"cache_write_tokens"`
	TotalTokens              uint64  `json:"total_tokens"`
	CostUSD                  float64 `json:"cost_usd"`
	AvgLatencyMS             float64 `json:"avg_latency_ms"`
	AvgTTFTMS                float64 `json:"avg_ttft_ms"`
	AvgThroughputTPS         float64 `json:"avg_throughput_tps"`
	SuccessRate              float64 `json:"success_rate"`
}

type Point struct {
	Timestamp  time.Time     `json:"timestamp"`
	Increment  PublicMetrics `json:"increment"`
	Cumulative PublicMetrics `json:"cumulative"`
}

type Coverage struct {
	EligibleBuckets     uint64 `json:"eligible_buckets"`
	SuppressedBuckets   uint64 `json:"suppressed_buckets"`
	ContributingTenants uint64 `json:"contributing_tenants"`
	SampleRequests      uint64 `json:"sample_requests"`
	MinimumTenants      uint64 `json:"minimum_tenants"`
	MinimumRequests     uint64 `json:"minimum_requests"`
	// FirstPartyBuckets counts eligible buckets that only cleared the floor
	// because every contributing tenant is first-party.
	FirstPartyBuckets uint64 `json:"first_party_buckets"`
}

type Report struct {
	SchemaVersion   string        `json:"schema_version"`
	SemanticVersion string        `json:"semantic_version"`
	GeneratedAt     time.Time     `json:"generated_at"`
	DataSince       time.Time     `json:"data_since,omitempty"`
	DataThrough     time.Time     `json:"data_through,omitempty"`
	Status          Status        `json:"status"`
	Kind            Kind          `json:"kind"`
	Key             string        `json:"key"`
	Window          Window        `json:"window"`
	Interval        Interval      `json:"interval"`
	Summary         PublicMetrics `json:"summary"`
	Points          []Point       `json:"points"`
	Coverage        Coverage      `json:"coverage"`
}

type ComparisonPoint struct {
	Timestamp  time.Time `json:"timestamp"`
	Increment  float64   `json:"increment"`
	Cumulative float64   `json:"cumulative"`
}

type ComparisonSeries struct {
	Key         string            `json:"key"`
	Status      Status            `json:"status"`
	Total       float64           `json:"total"`
	Points      []ComparisonPoint `json:"points"`
	DataThrough time.Time         `json:"data_through,omitempty"`
}

type Comparison struct {
	SchemaVersion   string             `json:"schema_version"`
	SemanticVersion string             `json:"semantic_version"`
	GeneratedAt     time.Time          `json:"generated_at"`
	Kind            Kind               `json:"kind"`
	Metric          Metric             `json:"metric"`
	Window          Window             `json:"window"`
	Interval        Interval           `json:"interval"`
	Series          []ComparisonSeries `json:"series"`
}

type Breakdown struct {
	SchemaVersion   string             `json:"schema_version"`
	SemanticVersion string             `json:"semantic_version"`
	GeneratedAt     time.Time          `json:"generated_at"`
	DataSince       time.Time          `json:"data_since,omitempty"`
	DataThrough     time.Time          `json:"data_through,omitempty"`
	Status          Status             `json:"status"`
	Provider        string             `json:"provider"`
	Metric          Metric             `json:"metric"`
	Window          Window             `json:"window"`
	Interval        Interval           `json:"interval"`
	Series          []ComparisonSeries `json:"series"`
	Coverage        Coverage           `json:"coverage"`
}

func (s *Service) Report(ctx context.Context, query Query) (Report, error) {
	if s == nil || s.repository == nil {
		return Report{}, errors.New("model metrics repository is required")
	}
	now := s.config.Now().UTC()
	resolved, err := resolveQuery(query, now)
	if err != nil {
		return Report{}, err
	}
	minimumTenants, minimumRequests := effectiveThresholds(s.config, now)
	resolved.FirstPartyTenants = s.config.FirstPartyTenants
	carveOut := len(s.config.FirstPartyTenants) > 0

	raw, err := s.repository.LoadReport(ctx, resolved)
	if err != nil {
		return Report{}, fmt.Errorf("load model metrics: %w", err)
	}

	result := Report{
		SchemaVersion:   SchemaVersion,
		SemanticVersion: SemanticVersion,
		GeneratedAt:     now,
		Status:          StatusInsufficientData,
		Kind:            resolved.Kind,
		Key:             resolved.Key,
		Window:          resolved.Window,
		Interval:        resolved.Interval,
		Points:          make([]Point, 0, len(raw.Buckets)),
		Coverage: Coverage{
			MinimumTenants:  minimumTenants,
			MinimumRequests: minimumRequests,
		},
	}

	buckets := append([]Bucket(nil), raw.Buckets...)
	sort.SliceStable(buckets, func(i, j int) bool {
		return buckets[i].Period.Before(buckets[j].Period)
	})

	var cumulative Metrics
	var cumulativeSuccesses uint64
	var cumulativeNonReasoning uint64
	for _, bucket := range buckets {
		eligible, firstParty := bucketEligibility(
			carveOut,
			bucket.TenantCount,
			bucket.ExternalTenantCount,
			bucket.Metrics.Requests,
			minimumTenants,
			minimumRequests,
		)
		if !eligible {
			result.Coverage.SuppressedBuckets++
			continue
		}
		if firstParty {
			result.Coverage.FirstPartyBuckets++
		}

		if result.DataSince.IsZero() {
			result.DataSince = bucket.Period.UTC()
		}
		result.DataThrough = bucketEnd(bucket.Period.UTC(), resolved.Interval)
		result.Coverage.EligibleBuckets++
		result.Coverage.SampleRequests += bucket.Metrics.Requests
		if bucket.TenantCount > result.Coverage.ContributingTenants {
			// Exact unique tenants across the whole window would require
			// retaining tenant identifiers at the public boundary. The maximum
			// eligible bucket cardinality is a safe, useful lower bound.
			result.Coverage.ContributingTenants = bucket.TenantCount
		}

		cumulative = addMetrics(cumulative, bucket.Metrics)
		increment := derivePublicMetrics(bucket.Metrics)
		cumulativeSuccesses += increment.Successes
		cumulativeNonReasoning += increment.NonReasoningOutputTokens
		cumulativePublic := derivePublicMetrics(cumulative)
		cumulativePublic.Successes = cumulativeSuccesses
		cumulativePublic.NonReasoningOutputTokens = cumulativeNonReasoning
		cumulativePublic.SuccessRate = safeRatio(
			float64(cumulativeSuccesses),
			float64(cumulative.Requests),
		)
		result.Points = append(result.Points, Point{
			Timestamp:  bucket.Period.UTC(),
			Increment:  increment,
			Cumulative: cumulativePublic,
		})
	}

	if len(result.Points) == 0 {
		return result, nil
	}

	result.Summary = result.Points[len(result.Points)-1].Cumulative
	result.Status = StatusAvailable
	if now.Sub(result.DataThrough) > staleAfter(resolved.Interval) {
		result.Status = StatusStale
	}
	return result, nil
}

func (s *Service) Compare(ctx context.Context, query CompareQuery) (Comparison, error) {
	if err := ValidateCompareQuery(query); err != nil {
		return Comparison{}, err
	}

	now := s.config.Now().UTC()
	result := Comparison{
		SchemaVersion:   SchemaVersion,
		SemanticVersion: SemanticVersion,
		GeneratedAt:     now,
		Kind:            query.Kind,
		Metric:          query.Metric,
		Window:          query.Window,
		Interval:        query.Interval,
		Series:          make([]ComparisonSeries, 0, len(query.Keys)),
	}

	for _, key := range query.Keys {
		report, err := s.Report(ctx, Query{
			Kind:     query.Kind,
			Key:      key,
			Window:   query.Window,
			Interval: query.Interval,
		})
		if err != nil {
			return Comparison{}, err
		}
		series := ComparisonSeries{
			Key:         report.Key,
			Status:      report.Status,
			Total:       metricValue(report.Summary, query.Metric),
			Points:      make([]ComparisonPoint, 0, len(report.Points)),
			DataThrough: report.DataThrough,
		}
		for _, point := range report.Points {
			series.Points = append(series.Points, ComparisonPoint{
				Timestamp:  point.Timestamp,
				Increment:  metricValue(point.Increment, query.Metric),
				Cumulative: metricValue(point.Cumulative, query.Metric),
			})
		}
		result.Series = append(result.Series, series)
	}
	return result, nil
}

func (s *Service) ProviderModelBreakdown(
	ctx context.Context,
	query BreakdownQuery,
) (Breakdown, error) {
	if s == nil || s.repository == nil {
		return Breakdown{}, errors.New("model metrics repository is required")
	}
	resolved, err := resolveBreakdownQuery(query, s.config.Now().UTC())
	if err != nil {
		return Breakdown{}, err
	}

	now := s.config.Now().UTC()
	minimumTenants, minimumRequests := effectiveThresholds(s.config, now)
	resolved.FirstPartyTenants = s.config.FirstPartyTenants
	carveOut := len(s.config.FirstPartyTenants) > 0
	raw, err := s.repository.LoadBreakdown(ctx, resolved)
	if err != nil {
		return Breakdown{}, fmt.Errorf("load provider model breakdown: %w", err)
	}

	result := Breakdown{
		SchemaVersion:   SchemaVersion,
		SemanticVersion: SemanticVersion,
		GeneratedAt:     now,
		Status:          StatusInsufficientData,
		Provider:        resolved.Provider,
		Metric:          resolved.Metric,
		Window:          resolved.Window,
		Interval:        resolved.Interval,
		Series:          []ComparisonSeries{},
		Coverage: Coverage{
			MinimumTenants:  minimumTenants,
			MinimumRequests: minimumRequests,
		},
	}

	buckets := append([]BreakdownBucket(nil), raw.Buckets...)
	sort.SliceStable(buckets, func(i, j int) bool {
		if buckets[i].Period.Equal(buckets[j].Period) {
			return buckets[i].Key < buckets[j].Key
		}
		return buckets[i].Period.Before(buckets[j].Period)
	})

	valuesByKey := make(map[string]map[time.Time]float64)
	totalsByKey := make(map[string]float64)
	dataThroughByKey := make(map[string]time.Time)
	for _, bucket := range buckets {
		eligible, firstParty := bucketEligibility(
			carveOut,
			bucket.TenantCount,
			bucket.ExternalTenantCount,
			bucket.Metrics.Requests,
			minimumTenants,
			minimumRequests,
		)
		if !eligible {
			result.Coverage.SuppressedBuckets++
			continue
		}
		if firstParty {
			result.Coverage.FirstPartyBuckets++
		}

		period := bucket.Period.UTC()
		if result.DataSince.IsZero() || period.Before(result.DataSince) {
			result.DataSince = period
		}
		through := bucketEnd(period, resolved.Interval)
		if through.After(result.DataThrough) {
			result.DataThrough = through
		}
		if through.After(dataThroughByKey[bucket.Key]) {
			dataThroughByKey[bucket.Key] = through
		}
		result.Coverage.EligibleBuckets++
		result.Coverage.SampleRequests += bucket.Metrics.Requests
		if bucket.TenantCount > result.Coverage.ContributingTenants {
			result.Coverage.ContributingTenants = bucket.TenantCount
		}

		value := metricValue(derivePublicMetrics(bucket.Metrics), resolved.Metric)
		if valuesByKey[bucket.Key] == nil {
			valuesByKey[bucket.Key] = make(map[time.Time]float64)
		}
		valuesByKey[bucket.Key][period] += value
		totalsByKey[bucket.Key] += value
	}

	if len(valuesByKey) == 0 {
		return result, nil
	}

	keys := make([]string, 0, len(valuesByKey))
	for key := range valuesByKey {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if totalsByKey[keys[i]] == totalsByKey[keys[j]] {
			return keys[i] < keys[j]
		}
		return totalsByKey[keys[i]] > totalsByKey[keys[j]]
	})

	visibleKeys := keys
	hiddenKeys := []string{}
	if len(keys) > int(resolved.Limit) {
		visibleCount := int(resolved.Limit) - 1
		visibleKeys = keys[:visibleCount]
		hiddenKeys = keys[visibleCount:]
	}

	for _, key := range visibleKeys {
		result.Series = append(
			result.Series,
			comparisonSeriesFromValues(
				key,
				valuesByKey[key],
				dataThroughByKey[key],
				resolved.Interval,
				now,
			),
		)
	}
	if len(hiddenKeys) > 0 {
		others := make(map[time.Time]float64)
		var othersThrough time.Time
		for _, key := range hiddenKeys {
			for period, value := range valuesByKey[key] {
				others[period] += value
			}
			if dataThroughByKey[key].After(othersThrough) {
				othersThrough = dataThroughByKey[key]
			}
		}
		result.Series = append(
			result.Series,
			comparisonSeriesFromValues(
				"others",
				others,
				othersThrough,
				resolved.Interval,
				now,
			),
		)
	}
	sort.SliceStable(result.Series, func(i, j int) bool {
		if result.Series[i].Total == result.Series[j].Total {
			return result.Series[i].Key < result.Series[j].Key
		}
		return result.Series[i].Total > result.Series[j].Total
	})

	result.Status = StatusAvailable
	if now.Sub(result.DataThrough) > staleAfter(resolved.Interval) {
		result.Status = StatusStale
	}
	return result, nil
}

func comparisonSeriesFromValues(
	key string,
	values map[time.Time]float64,
	dataThrough time.Time,
	interval Interval,
	now time.Time,
) ComparisonSeries {
	periods := make([]time.Time, 0, len(values))
	for period := range values {
		periods = append(periods, period)
	}
	sort.Slice(periods, func(i, j int) bool {
		return periods[i].Before(periods[j])
	})

	status := StatusAvailable
	if now.Sub(dataThrough) > staleAfter(interval) {
		status = StatusStale
	}
	series := ComparisonSeries{
		Key:         key,
		Status:      status,
		Points:      make([]ComparisonPoint, 0, len(periods)),
		DataThrough: dataThrough,
	}
	var cumulative float64
	for _, period := range periods {
		increment := finite(values[period])
		cumulative += increment
		series.Points = append(series.Points, ComparisonPoint{
			Timestamp:  period,
			Increment:  increment,
			Cumulative: finite(cumulative),
		})
	}
	series.Total = finite(cumulative)
	return series
}

func resolveBreakdownQuery(query BreakdownQuery, now time.Time) (BreakdownQuery, error) {
	query.Provider = strings.TrimSpace(query.Provider)
	if err := validatePublicKey(query.Provider); err != nil {
		return BreakdownQuery{}, fmt.Errorf("invalid provider: %w", err)
	}
	if query.Metric != MetricTokens && query.Metric != MetricRequests {
		return BreakdownQuery{}, errors.New("provider model breakdown metric must be tokens or requests")
	}
	if query.Limit == 0 {
		query.Limit = DefaultBreakdownSeriesLimit
	}
	if query.Limit < 2 || query.Limit > MaximumBreakdownSeriesLimit {
		return BreakdownQuery{}, fmt.Errorf(
			"provider model breakdown limit must be between 2 and %d",
			MaximumBreakdownSeriesLimit,
		)
	}
	resolved, err := resolveQuery(Query{
		Kind:     KindProvider,
		Key:      query.Provider,
		Window:   query.Window,
		Interval: query.Interval,
	}, now)
	if err != nil {
		return BreakdownQuery{}, err
	}
	query.StartTime = resolved.StartTime
	query.EndTime = resolved.EndTime
	return query, nil
}

func ValidateCompareQuery(query CompareQuery) error {
	if query.Kind != KindModel && query.Kind != KindProvider {
		return fmt.Errorf("kind must be %q or %q", KindModel, KindProvider)
	}
	if len(query.Keys) < 2 || len(query.Keys) > 4 {
		return errors.New("comparison requires between two and four keys")
	}
	seen := make(map[string]struct{}, len(query.Keys))
	for _, key := range query.Keys {
		key = strings.TrimSpace(key)
		if err := validatePublicKey(key); err != nil {
			return fmt.Errorf("invalid comparison key: %w", err)
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("comparison key %q is duplicated", key)
		}
		seen[key] = struct{}{}
	}
	switch query.Metric {
	case MetricTokens, MetricRequests, MetricTTFT, MetricThroughput, MetricSuccessRate:
	default:
		return fmt.Errorf("unsupported comparison metric %q", query.Metric)
	}
	_, err := resolveQuery(Query{
		Kind:     query.Kind,
		Key:      query.Keys[0],
		Window:   query.Window,
		Interval: query.Interval,
	}, time.Now().UTC())
	return err
}

func ValidateBreakdownQuery(query BreakdownQuery) error {
	_, err := resolveBreakdownQuery(query, time.Now().UTC())
	return err
}

func ValidateQuery(query Query) error {
	_, err := resolveQuery(query, time.Now().UTC())
	return err
}

func resolveQuery(query Query, now time.Time) (Query, error) {
	query.Key = strings.TrimSpace(query.Key)
	if err := validatePublicKey(query.Key); err != nil {
		return Query{}, err
	}
	switch query.Kind {
	case KindModel, KindProvider, KindPublisher:
	default:
		return Query{}, fmt.Errorf("unsupported metrics kind %q", query.Kind)
	}
	switch query.Interval {
	case IntervalHour, IntervalDay, IntervalMonth:
	default:
		return Query{}, fmt.Errorf("unsupported metrics interval %q", query.Interval)
	}

	// Only completed buckets are public. A moving, partially filled bucket
	// would let callers recover small increments by repeatedly differencing
	// otherwise privacy-safe responses.
	query.EndTime = bucketStart(now, query.Interval)
	switch query.Window {
	case Window7Days:
		if query.Interval == IntervalMonth {
			query.StartTime = query.EndTime.AddDate(0, -1, 0)
		} else {
			query.StartTime = query.EndTime.AddDate(0, 0, -7)
		}
	case Window30Days:
		if query.Interval == IntervalMonth {
			query.StartTime = query.EndTime.AddDate(0, -1, 0)
		} else {
			query.StartTime = query.EndTime.AddDate(0, 0, -30)
		}
	case Window90Days:
		if query.Interval == IntervalMonth {
			query.StartTime = query.EndTime.AddDate(0, -3, 0)
		} else {
			query.StartTime = query.EndTime.AddDate(0, 0, -90)
		}
	case Window6Months:
		query.StartTime = query.EndTime.AddDate(0, -6, 0)
	case Window1Year:
		query.StartTime = query.EndTime.AddDate(-1, 0, 0)
	case WindowAll:
		query.StartTime = time.Time{}
	default:
		return Query{}, fmt.Errorf("unsupported metrics window %q", query.Window)
	}
	if query.Window == WindowAll && query.Interval == IntervalHour {
		return Query{}, errors.New("all-time reports require day or month interval")
	}
	return query, nil
}

func bucketStart(value time.Time, interval Interval) time.Time {
	value = value.UTC()
	switch interval {
	case IntervalHour:
		return value.Truncate(time.Hour)
	case IntervalDay:
		return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	case IntervalMonth:
		return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return value
	}
}

func bucketEnd(start time.Time, interval Interval) time.Time {
	switch interval {
	case IntervalHour:
		return start.Add(time.Hour)
	case IntervalDay:
		return start.AddDate(0, 0, 1)
	case IntervalMonth:
		return start.AddDate(0, 1, 0)
	default:
		return start
	}
}

func validatePublicKey(key string) error {
	if key == "" {
		return errors.New("key is required")
	}
	if !utf8.ValidString(key) || len(key) > 256 {
		return errors.New("key must be valid UTF-8 and no longer than 256 bytes")
	}
	for _, character := range key {
		if unicode.IsControl(character) {
			return errors.New("key cannot contain control characters")
		}
	}
	return nil
}

func addMetrics(a, b Metrics) Metrics {
	return Metrics{
		Requests:             a.Requests + b.Requests,
		Errors:               a.Errors + b.Errors,
		InputTokens:          a.InputTokens + b.InputTokens,
		OutputTokens:         a.OutputTokens + b.OutputTokens,
		ReasoningTokens:      a.ReasoningTokens + b.ReasoningTokens,
		CacheReadTokens:      a.CacheReadTokens + b.CacheReadTokens,
		CacheWriteTokens:     a.CacheWriteTokens + b.CacheWriteTokens,
		CostUSD:              a.CostUSD + b.CostUSD,
		LatencyTotalMS:       a.LatencyTotalMS + b.LatencyTotalMS,
		LatencySamples:       a.LatencySamples + b.LatencySamples,
		TTFTTotalMS:          a.TTFTTotalMS + b.TTFTTotalMS,
		TTFTSamples:          a.TTFTSamples + b.TTFTSamples,
		StreamOutputTokens:   a.StreamOutputTokens + b.StreamOutputTokens,
		GenerationDurationMS: a.GenerationDurationMS + b.GenerationDurationMS,
	}
}

func derivePublicMetrics(metrics Metrics) PublicMetrics {
	successes := metrics.Requests
	if metrics.Errors < successes {
		successes -= metrics.Errors
	} else {
		successes = 0
	}
	nonReasoning := metrics.OutputTokens
	if metrics.ReasoningTokens < nonReasoning {
		nonReasoning -= metrics.ReasoningTokens
	} else {
		nonReasoning = 0
	}

	return PublicMetrics{
		Requests:                 metrics.Requests,
		Successes:                successes,
		Errors:                   metrics.Errors,
		InputTokens:              metrics.InputTokens,
		OutputTokens:             metrics.OutputTokens,
		ReasoningTokens:          metrics.ReasoningTokens,
		NonReasoningOutputTokens: nonReasoning,
		CacheReadTokens:          metrics.CacheReadTokens,
		CacheWriteTokens:         metrics.CacheWriteTokens,
		TotalTokens:              metrics.InputTokens + metrics.OutputTokens,
		CostUSD:                  finite(metrics.CostUSD),
		AvgLatencyMS:             safeRatio(metrics.LatencyTotalMS, float64(metrics.LatencySamples)),
		AvgTTFTMS:                safeRatio(metrics.TTFTTotalMS, float64(metrics.TTFTSamples)),
		AvgThroughputTPS:         safeRatio(float64(metrics.StreamOutputTokens)*1000, metrics.GenerationDurationMS),
		SuccessRate:              safeRatio(float64(successes), float64(metrics.Requests)),
	}
}

func metricValue(metrics PublicMetrics, metric Metric) float64 {
	switch metric {
	case MetricTokens:
		return float64(metrics.TotalTokens)
	case MetricRequests:
		return float64(metrics.Requests)
	case MetricTTFT:
		return metrics.AvgTTFTMS
	case MetricThroughput:
		return metrics.AvgThroughputTPS
	case MetricSuccessRate:
		return metrics.SuccessRate
	default:
		return 0
	}
}

func safeRatio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return finite(numerator / denominator)
}

func finite(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func staleAfter(interval Interval) time.Duration {
	if interval == IntervalHour {
		return 3 * time.Hour
	}
	if interval == IntervalMonth {
		return 62 * 24 * time.Hour
	}
	return 48 * time.Hour
}
