package eval_runner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Sentinel errors the RPC layer maps onto connect codes. Both are wrapped with
// %w so callers can errors.Is them through the descriptive message.
var (
	// ErrComparisonNotFound: no comparisons row for (id, tenant). A
	// foreign-tenant comparison id is indistinguishable from a missing one.
	ErrComparisonNotFound = errors.New("comparison not found")
	// ErrRunNotTerminal: persist=true was requested but a run is still
	// pending/running; comparisons materialize only for terminal runs.
	ErrRunNotTerminal = errors.New("eval run is not terminal")
)

// Eval-run comparison engine (design doc section 7a, PR 2b).
//
// This is the SOLE live implementation of the paired-bootstrap comparison
// math (Decision A): comparePaired is a validated port of
// apps/admin/src/utils/eval-stats.ts, bit-parity preserved via the same
// mulberry32 PRNG and seeding, so a stored verdict and a UI preview can never
// disagree. The UI copy is display-only scratch and non-authoritative.

// ComparisonGrade is the composite verdict for a run-vs-run comparison.
// Plain string type; the RPC layer (PR 2b-ii) maps it onto the proto
// ComparisonGrade enum.
type ComparisonGrade string

const (
	GradeImprovement      ComparisonGrade = "improvement"
	GradeRegression       ComparisonGrade = "regression"
	GradeTradeoff         ComparisonGrade = "tradeoff"
	GradeTie              ComparisonGrade = "tie"
	GradeInsufficientData ComparisonGrade = "insufficient_data"
)

// Match modes (per-comparison, never per-pair).
const (
	MatchModeHash        = "hash"
	MatchModeDatasetItem = "dataset_item"
)

// Per-scorer verdict strings, mirroring eval-stats.ts Verdict.
const (
	verdictImprovement  = "improvement"
	verdictRegression   = "regression"
	verdictInconclusive = "inconclusive"
	verdictInsufficient = "insufficient"
)

// Bootstrap parameters — MUST stay identical to eval-stats.ts for parity.
const (
	comparisonMinN      = 5
	comparisonBootstrap = 2000
	comparisonAlpha     = 0.05
)

// materialThreshold is the minimum relative cost/latency increase (candidate
// vs baseline) for a statistically significant regression to also count as
// material. A bootstrap flags 2ms of provider jitter at large n; materiality
// keeps that out of the verdict.
const materialThreshold = 0.10

// errorRateRegressionThreshold: candidate error rate exceeding baseline by
// more than this is treated as a reliability regression (survivorship guard —
// failed items drop from pairing, so a crashier candidate otherwise looks
// better).
const errorRateRegressionThreshold = 0.05

// ScorerResult is the per-scorer paired-bootstrap outcome, candidate vs
// baseline. JSON tags define the comparisons.scorer_results storage shape.
type ScorerResult struct {
	Name          string  `json:"name"`
	BaselineMean  float64 `json:"baseline_mean"`
	CandidateMean float64 `json:"candidate_mean"`
	MeanDiff      float64 `json:"mean_diff"`
	CILow         float64 `json:"ci_low"`
	CIHigh        float64 `json:"ci_high"`
	PValue        float64 `json:"p_value"`
	Verdict       string  `json:"verdict"`
	N             int     `json:"n"`
}

// ComparisonResult is the engine output. Plain Go struct; PR 2b-ii maps it to
// the CompareEvalRunsResponse proto.
type ComparisonResult struct {
	ComparisonID   string
	MatchMode      string
	ScorerResults  []ScorerResult
	Overall        ComparisonGrade
	Rationale      string
	LatencyDelta   float64 // mean per-item latency delta in ms (candidate - baseline)
	CostDelta      float64 // mean per-item cost delta (candidate - baseline)
	ErrorRateDelta float64 // failed/total rate delta (candidate - baseline)
	Coverage       float64 // paired / max(baselineCount, candidateCount)
}

// ---------------------------------------------------------------------------
// comparePaired — validated port of eval-stats.ts
// ---------------------------------------------------------------------------

type pairedComparison struct {
	n        int
	meanA    float64 // baseline mean
	meanB    float64 // candidate mean
	meanDiff float64 // meanB - meanA
	ciLow    float64
	ciHigh   float64
	pValue   float64
	verdict  string
}

// mulberry32 is the same tiny deterministic PRNG as eval-stats.ts. All
// arithmetic is uint32: two's complement makes JS int32 |0 / Math.imul
// semantics and Go uint32 +,*,^,| bit-identical, and JS >>> is exactly the
// uint32 shift.
func mulberry32(seed uint32) func() float64 {
	s := seed
	return func() float64 {
		s += 0x6d2b79f5
		t := (s ^ (s >> 15)) * (s | 1)
		t = (t + (t^(t>>7))*(t|61)) ^ t
		return float64(t^(t>>14)) / 4294967296.0
	}
}

// toInt32 replicates the JS ToInt32 abstract operation (the `| 0` coercion):
// NaN/Inf map to 0, otherwise truncate toward zero and wrap mod 2^32 into a
// signed 32-bit integer.
func toInt32(f float64) int32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	t := math.Trunc(f)
	m := math.Mod(t, 4294967296)
	if m < 0 {
		m += 4294967296
	}
	return int32(uint32(m))
}

func meanOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// percentile with linear interpolation over an ascending-sorted slice,
// identical to eval-stats.ts.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := float64(len(sorted)-1) * p
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(idx-float64(lo))
}

// comparePaired runs a paired bootstrap of candidate (b) vs baseline (a).
// Arrays must be aligned (a[i] and b[i] are the same item under two runs).
// Positive meanDiff means the candidate scored higher.
//
// Seed-parity notes (vs the JS original):
//   - JS Math.round(x) ties toward +infinity for ALL x (Math.round(-2.5) is
//     -2), which is math.Floor(x+0.5) — NOT Go's math.Round (ties away from
//     zero).
//   - The JS checksum accumulates in float64 and coerces with `| 0` each
//     iteration; toInt32 reproduces that exactly.
//   - `(rng() * n) | 0` truncates toward zero on a non-negative float, which
//     is Go's int(...) conversion.
func comparePaired(a, b []float64) pairedComparison {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	meanA := meanOf(a[:n])
	meanB := meanOf(b[:n])
	out := pairedComparison{
		n:        n,
		meanA:    meanA,
		meanB:    meanB,
		meanDiff: meanB - meanA,
		pValue:   1,
		verdict:  verdictInsufficient,
	}
	if n < comparisonMinN {
		return out
	}

	diffs := make([]float64, n)
	for i := 0; i < n; i++ {
		diffs[i] = b[i] - a[i]
	}

	// Seed from n + a checksum so identical inputs reproduce identical CIs.
	checksum := int32(n)
	for i := 0; i < n; i++ {
		rounded := math.Floor(diffs[i]*1e6 + 0.5) // JS Math.round semantics
		checksum = toInt32(float64(checksum)*31 + rounded)
	}
	rng := mulberry32(uint32(checksum))

	bootMeans := make([]float64, comparisonBootstrap)
	ge0 := 0
	le0 := 0
	for k := 0; k < comparisonBootstrap; k++ {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += diffs[int(rng()*float64(n))]
		}
		m := sum / float64(n)
		bootMeans[k] = m
		if m >= 0 {
			ge0++
		}
		if m <= 0 {
			le0++
		}
	}
	sort.Float64s(bootMeans)

	out.ciLow = percentile(bootMeans, comparisonAlpha/2)
	out.ciHigh = percentile(bootMeans, 1-comparisonAlpha/2)
	lower := ge0
	if le0 < ge0 {
		lower = le0
	}
	out.pValue = math.Min(1, 2*float64(lower)/comparisonBootstrap)

	out.verdict = verdictInconclusive
	if out.ciLow > 0 {
		out.verdict = verdictImprovement
	} else if out.ciHigh < 0 {
		out.verdict = verdictRegression
	}
	return out
}

// ---------------------------------------------------------------------------
// Score extraction — mirrors itemScoreMap in eval-stats.ts
// ---------------------------------------------------------------------------

// itemScoreMap pulls a {scorerName: numericValue} map out of an
// eval_run_items.scores JSONB payload, which may be either a map
// {name: value|{value}} or an array [{name, value|numericValue|score}].
// Booleans coerce to 0/1; non-numeric scores are skipped.
func itemScoreMap(raw []byte) map[string]float64 {
	out := map[string]float64{}
	if len(raw) == 0 {
		return out
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return out
	}
	switch t := v.(type) {
	case []interface{}:
		for _, e := range t {
			m, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			cand := firstNonNil(m["value"], m["numericValue"], m["score"])
			if cand == nil {
				cand = e
			}
			if val, ok := pickNum(cand); ok {
				out[name] = val
			}
		}
	case map[string]interface{}:
		for name, e := range t {
			if val, ok := pickNum(e); ok {
				out[name] = val
			}
		}
	}
	return out
}

func firstNonNil(vs ...interface{}) interface{} {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func pickNum(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return t, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case map[string]interface{}:
		for _, k := range []string{"value", "numericValue", "score", "numeric_value"} {
			switch inner := t[k].(type) {
			case float64:
				if math.IsNaN(inner) || math.IsInf(inner, 0) {
					continue
				}
				return inner, true
			case bool:
				if inner {
					return 1, true
				}
				return 0, true
			}
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Composite verdict
// ---------------------------------------------------------------------------

// compositeInputs are the pure inputs to compositeVerdict, kept as a struct
// so the truth table is directly unit-testable.
type compositeInputs struct {
	Coverage       float64
	Scorers        []ScorerResult
	Cost           pairedComparison // paired per-item cost, candidate - baseline
	Latency        pairedComparison // paired per-item latency (ms), candidate - baseline
	ErrorRateDelta float64          // candidate failed-rate - baseline failed-rate
}

// materialRegression reports whether a paired cost/latency comparison is BOTH
// statistically significant (CI excludes 0, candidate higher) AND material
// (relative increase over the baseline mean above materialThreshold).
func materialRegression(pc pairedComparison) bool {
	if pc.verdict == verdictInsufficient {
		return false
	}
	if pc.ciLow <= 0 || pc.meanDiff <= 0 {
		return false
	}
	base := math.Abs(pc.meanA)
	if base == 0 {
		// Any significant increase from a zero baseline is material.
		return true
	}
	return pc.meanDiff/base > materialThreshold
}

// compositeVerdict folds scorer verdicts, cost/latency significance+
// materiality, error-rate delta, and coverage into a single grade plus a
// human rationale naming the drivers.
//
// Note: an empty scorer list counts as "every scorer insufficient" —
// with no shared scorer there is no quality signal, so the honest answer is
// INSUFFICIENT_DATA rather than TIE.
func compositeVerdict(in compositeInputs) (ComparisonGrade, string) {
	// 1. Not enough shared data to say anything.
	if in.Coverage < 0.5 {
		return GradeInsufficientData, fmt.Sprintf(
			"only %.0f%% of items could be paired between the two runs (need at least 50%%)",
			in.Coverage*100)
	}
	allInsufficient := true
	var regressed, improved []string
	for _, s := range in.Scorers {
		if s.Verdict != verdictInsufficient {
			allInsufficient = false
		}
		switch s.Verdict {
		case verdictRegression:
			regressed = append(regressed, s.Name)
		case verdictImprovement:
			improved = append(improved, s.Name)
		}
	}
	if allInsufficient {
		return GradeInsufficientData, "no scorer had enough paired samples for a significance test"
	}

	// 2. Operational regressions: significant AND material cost/latency
	// increases; error-rate jump (survivorship guard).
	costReg := materialRegression(in.Cost)
	latReg := materialRegression(in.Latency)
	errReg := in.ErrorRateDelta > errorRateRegressionThreshold

	var drivers []string
	if len(regressed) > 0 {
		drivers = append(drivers, "scorer regression: "+strings.Join(regressed, ", "))
	}
	if len(improved) > 0 {
		drivers = append(drivers, "scorer improvement: "+strings.Join(improved, ", "))
	}
	if costReg {
		drivers = append(drivers, fmt.Sprintf("cost up %s per item", relDeltaLabel(in.Cost)))
	}
	if latReg {
		drivers = append(drivers, fmt.Sprintf("latency up %s per item", relDeltaLabel(in.Latency)))
	}
	if errReg {
		drivers = append(drivers, fmt.Sprintf("error rate up %.0f points", in.ErrorRateDelta*100))
	}

	// 3-4. Decide.
	anyScorerRegression := len(regressed) > 0
	anyScorerImprovement := len(improved) > 0
	opsRegression := costReg || latReg || errReg

	switch {
	case anyScorerRegression && anyScorerImprovement:
		return GradeTradeoff, joinRationale(drivers)
	case anyScorerRegression:
		return GradeRegression, joinRationale(drivers)
	case anyScorerImprovement && opsRegression:
		return GradeTradeoff, joinRationale(drivers)
	case anyScorerImprovement:
		return GradeImprovement, joinRationale(drivers)
	case opsRegression:
		// Quality flat but the candidate is materially more expensive,
		// slower, or crashier: that is a regression, not a tie.
		return GradeRegression, joinRationale(append(drivers, "quality unchanged"))
	default:
		return GradeTie, "no significant difference in quality, cost, latency, or error rate"
	}
}

func relDeltaLabel(pc pairedComparison) string {
	base := math.Abs(pc.meanA)
	if base == 0 {
		return fmt.Sprintf("%+.4g (from zero baseline)", pc.meanDiff)
	}
	return fmt.Sprintf("%.0f%%", pc.meanDiff/base*100)
}

func joinRationale(drivers []string) string {
	if len(drivers) == 0 {
		return "no significant drivers"
	}
	return strings.Join(drivers, "; ")
}

// ---------------------------------------------------------------------------
// ComputeComparison — load, pair, score, verdict, (optionally) materialize
// ---------------------------------------------------------------------------

type comparisonRunRow struct {
	ID        string `db:"id"`
	TenantID  string `db:"tenant_id"`
	DatasetID string `db:"dataset_id"`
	Status    string `db:"status"`
}

type comparisonItemRow struct {
	ID             string          `db:"id"`
	DatasetItemID  string          `db:"dataset_item_id"`
	InputHash      sql.NullString  `db:"input_hash"`
	InputCanonical []byte          `db:"input_canonical"`
	Scores         []byte          `db:"scores"`
	Cost           sql.NullFloat64 `db:"cost"`
	LatencyMs      sql.NullInt64   `db:"latency_ms"`
	Status         string          `db:"status"`
	Error          sql.NullString  `db:"error"`
	Output         []byte          `db:"output"`
}

func isTerminalRunStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	}
	return false
}

// ComputeComparison compares a candidate eval run against a baseline run and
// returns per-scorer paired-bootstrap results plus a composite verdict. With
// persist=true the result is materialized into the comparisons table (both
// runs must be terminal) and ComparisonID is set; with persist=false it is a
// pure preview and ComparisonID is "".
//
// Package function so the RPC handler only needs a *sqlx.DB, not a fully
// constructed Runner (the samplingRunner wiring is optional in start_api).
//
// Every query is scoped by tenant_id (defense in depth): a run id belonging
// to another tenant is indistinguishable from a missing run and hard-errors.
func ComputeComparison(ctx context.Context, db *sqlx.DB, tenantID, baselineRunID, candidateRunID string, persist bool) (*ComparisonResult, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("compare eval runs: tenant id is required")
	}
	if baselineRunID == "" || candidateRunID == "" {
		return nil, fmt.Errorf("compare eval runs: baseline and candidate run ids are required")
	}

	baseRun, err := loadComparisonRun(ctx, db, tenantID, baselineRunID)
	if err != nil {
		return nil, err
	}
	candRun, err := loadComparisonRun(ctx, db, tenantID, candidateRunID)
	if err != nil {
		return nil, err
	}
	if persist {
		if !isTerminalRunStatus(baseRun.Status) {
			return nil, fmt.Errorf("compare eval runs: baseline run %s has status %q; comparisons materialize only for terminal runs: %w", baseRun.ID, baseRun.Status, ErrRunNotTerminal)
		}
		if !isTerminalRunStatus(candRun.Status) {
			return nil, fmt.Errorf("compare eval runs: candidate run %s has status %q; comparisons materialize only for terminal runs: %w", candRun.ID, candRun.Status, ErrRunNotTerminal)
		}
	}

	// The verdict path never reads output/input_canonical, so skip loading
	// those payloads (they can be large); GetComparisonRows loads them.
	baseItems, err := loadComparisonItems(ctx, db, tenantID, baseRun.ID, false)
	if err != nil {
		return nil, err
	}
	candItems, err := loadComparisonItems(ctx, db, tenantID, candRun.ID, false)
	if err != nil {
		return nil, err
	}

	// Match MODE is per-comparison, never per-pair: same dataset pairs by
	// dataset_item_id; cross-dataset uses hashes iff BOTH runs are 100%
	// hashed, else falls back to dataset_item_id (which across datasets will
	// pair little and honestly surface INSUFFICIENT_DATA via coverage).
	matchMode := MatchModeDatasetItem
	if baseRun.DatasetID != candRun.DatasetID && fullyHashed(baseItems) && fullyHashed(candItems) {
		matchMode = MatchModeHash
	}

	pairs := pairComparisonItems(matchMode, baseItems, candItems)

	coverage := 0.0
	denom := len(baseItems)
	if len(candItems) > denom {
		denom = len(candItems)
	}
	if denom > 0 {
		coverage = float64(len(pairs)) / float64(denom)
	}

	// Per-scorer paired arrays: only scorers present on BOTH sides of a pair
	// contribute that pair.
	scorerArrays := map[string]*struct{ a, b []float64 }{}
	for _, p := range pairs {
		baseScores := itemScoreMap(p.base.Scores)
		candScores := itemScoreMap(p.cand.Scores)
		names := make([]string, 0, len(candScores))
		for name := range candScores {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			av, ok := baseScores[name]
			if !ok {
				continue
			}
			arr := scorerArrays[name]
			if arr == nil {
				arr = &struct{ a, b []float64 }{}
				scorerArrays[name] = arr
			}
			arr.a = append(arr.a, av)
			arr.b = append(arr.b, candScores[name])
		}
	}
	scorerNames := make([]string, 0, len(scorerArrays))
	for name := range scorerArrays {
		scorerNames = append(scorerNames, name)
	}
	sort.Strings(scorerNames)
	scorerResults := make([]ScorerResult, 0, len(scorerNames))
	for _, name := range scorerNames {
		arr := scorerArrays[name]
		pc := comparePaired(arr.a, arr.b)
		scorerResults = append(scorerResults, ScorerResult{
			Name:          name,
			BaselineMean:  pc.meanA,
			CandidateMean: pc.meanB,
			MeanDiff:      pc.meanDiff,
			CILow:         pc.ciLow,
			CIHigh:        pc.ciHigh,
			PValue:        pc.pValue,
			Verdict:       pc.verdict,
			N:             pc.n,
		})
	}

	// Paired cost/latency deltas over pairs where BOTH sides completed
	// (failed items have zeroed metrics that would poison the bootstrap).
	var costA, costB, latA, latB []float64
	for _, p := range pairs {
		if p.base.Status != "completed" || p.cand.Status != "completed" {
			continue
		}
		if p.base.Cost.Valid && p.cand.Cost.Valid {
			costA = append(costA, p.base.Cost.Float64)
			costB = append(costB, p.cand.Cost.Float64)
		}
		if p.base.LatencyMs.Valid && p.cand.LatencyMs.Valid {
			latA = append(latA, float64(p.base.LatencyMs.Int64))
			latB = append(latB, float64(p.cand.LatencyMs.Int64))
		}
	}
	costCmp := comparePaired(costA, costB)
	latCmp := comparePaired(latA, latB)

	errorRateDelta := failedRate(candItems) - failedRate(baseItems)

	grade, rationale := compositeVerdict(compositeInputs{
		Coverage:       coverage,
		Scorers:        scorerResults,
		Cost:           costCmp,
		Latency:        latCmp,
		ErrorRateDelta: errorRateDelta,
	})

	result := &ComparisonResult{
		MatchMode:      matchMode,
		ScorerResults:  scorerResults,
		Overall:        grade,
		Rationale:      rationale,
		LatencyDelta:   latCmp.meanDiff,
		CostDelta:      costCmp.meanDiff,
		ErrorRateDelta: errorRateDelta,
		Coverage:       coverage,
	}

	if persist {
		id, err := materializeComparison(ctx, db, tenantID, baseRun.ID, candRun.ID, result)
		if err != nil {
			return nil, err
		}
		result.ComparisonID = id
	}
	return result, nil
}

// ComputeComparison delegates to the package function using the Runner's DB.
func (r *Runner) ComputeComparison(ctx context.Context, tenantID, baselineRunID, candidateRunID string, persist bool) (*ComparisonResult, error) {
	return ComputeComparison(ctx, r.db, tenantID, baselineRunID, candidateRunID, persist)
}

func loadComparisonRun(ctx context.Context, db *sqlx.DB, tenantID, runID string) (*comparisonRunRow, error) {
	var run comparisonRunRow
	err := db.GetContext(ctx, &run, `
		SELECT id, tenant_id, dataset_id, status
		FROM eval_runs
		WHERE id = $1 AND tenant_id = $2
	`, runID, tenantID)
	if err == sql.ErrNoRows {
		// A foreign-tenant run id looks exactly like a missing one: hard
		// error, never a silent skip.
		return nil, fmt.Errorf("compare eval runs: eval run %s not found", runID)
	}
	if err != nil {
		return nil, fmt.Errorf("compare eval runs: load run %s: %w", runID, err)
	}
	return &run, nil
}

// loadComparisonItems loads one run's items in deterministic order. With
// includePayloads=false the potentially large input_canonical/output columns
// are not fetched (the verdict path never reads them); the struct fields stay
// nil. GetComparisonRows passes true.
func loadComparisonItems(ctx context.Context, db *sqlx.DB, tenantID, runID string, includePayloads bool) ([]comparisonItemRow, error) {
	cols := `id, dataset_item_id, input_hash, scores, cost, latency_ms, status, error`
	if includePayloads {
		cols += `, input_canonical, output`
	}
	var items []comparisonItemRow
	// Ordered by created_at (id tiebreak) so occurrence ranks for duplicate
	// hashes are deterministic.
	err := db.SelectContext(ctx, &items, `
		SELECT `+cols+`
		FROM eval_run_items
		WHERE eval_run_id = $1 AND tenant_id = $2
		ORDER BY created_at ASC, id ASC
	`, runID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("compare eval runs: load items for run %s: %w", runID, err)
	}
	return items, nil
}

func fullyHashed(items []comparisonItemRow) bool {
	for i := range items {
		if !items[i].InputHash.Valid || items[i].InputHash.String == "" {
			return false
		}
	}
	return true
}

func failedRate(items []comparisonItemRow) float64 {
	if len(items) == 0 {
		return 0
	}
	failed := 0
	for i := range items {
		if items[i].Status == "failed" {
			failed++
		}
	}
	return float64(failed) / float64(len(items))
}

type comparisonPair struct {
	base *comparisonItemRow
	cand *comparisonItemRow
}

// pairComparisonItems pairs the two runs' items under the given match mode.
//
// dataset_item mode pairs by dataset_item_id (unique per run; the first
// occurrence wins defensively). hash mode pairs by (input_hash,
// occurrence_rank): the k-th baseline item with a hash pairs with the k-th
// candidate item with that hash, in created_at order — duplicate inputs
// within a run are normal, and a raw hash join would cross-product them.
func pairComparisonItems(matchMode string, baseItems, candItems []comparisonItemRow) []comparisonPair {
	var pairs []comparisonPair
	if matchMode == MatchModeHash {
		byHash := map[string][]*comparisonItemRow{}
		for i := range baseItems {
			h := baseItems[i].InputHash.String
			byHash[h] = append(byHash[h], &baseItems[i])
		}
		cursor := map[string]int{}
		for i := range candItems {
			h := candItems[i].InputHash.String
			k := cursor[h]
			if k >= len(byHash[h]) {
				continue
			}
			cursor[h] = k + 1
			pairs = append(pairs, comparisonPair{base: byHash[h][k], cand: &candItems[i]})
		}
		return pairs
	}

	byItem := map[string]*comparisonItemRow{}
	for i := range baseItems {
		id := baseItems[i].DatasetItemID
		if id == "" {
			continue
		}
		if _, exists := byItem[id]; !exists {
			byItem[id] = &baseItems[i]
		}
	}
	seen := map[string]bool{}
	for i := range candItems {
		id := candItems[i].DatasetItemID
		if id == "" || seen[id] {
			continue
		}
		base, ok := byItem[id]
		if !ok {
			continue
		}
		seen[id] = true
		pairs = append(pairs, comparisonPair{base: base, cand: &candItems[i]})
	}
	return pairs
}

// materializeComparison upserts the comparisons row for this
// (tenant, baseline, candidate, key config) and returns its id. On recompute
// the existing row keeps its id; created_at moves to NOW() so it reflects
// when the stored verdict was computed. key_config_hash is the empty string
// in v1 (identity comparison key).
func materializeComparison(ctx context.Context, db *sqlx.DB, tenantID, baselineRunID, candidateRunID string, res *ComparisonResult) (string, error) {
	scorerJSON, err := json.Marshal(res.ScorerResults)
	if err != nil {
		return "", fmt.Errorf("compare eval runs: marshal scorer results: %w", err)
	}
	deltasJSON, err := json.Marshal(map[string]interface{}{
		"latency_delta":    res.LatencyDelta,
		"cost_delta":       res.CostDelta,
		"error_rate_delta": res.ErrorRateDelta,
		"coverage":         res.Coverage,
		"rationale":        res.Rationale,
	})
	if err != nil {
		return "", fmt.Errorf("compare eval runs: marshal deltas: %w", err)
	}

	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO comparisons (
			id, tenant_id, baseline_run_id, candidate_run_id,
			key_config_hash, match_mode, scorer_results, overall_verdict, deltas
		) VALUES ($1, $2, $3, $4, '', $5, $6, $7, $8)
		ON CONFLICT (tenant_id, baseline_run_id, candidate_run_id, key_config_hash)
		DO UPDATE SET
			match_mode = EXCLUDED.match_mode,
			scorer_results = EXCLUDED.scorer_results,
			overall_verdict = EXCLUDED.overall_verdict,
			deltas = EXCLUDED.deltas,
			created_at = NOW()
		RETURNING id
	`, uuid.NewString(), tenantID, baselineRunID, candidateRunID,
		res.MatchMode, scorerJSON, res.Overall, deltasJSON).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("compare eval runs: materialize comparison: %w", err)
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// GetComparisonRows — paginated per-item drill-down for a stored comparison
// ---------------------------------------------------------------------------

// ScorerCellDelta is one scorer's per-row baseline/candidate values and their
// delta (candidate - baseline). Only scorers present on BOTH sides of the
// pair appear.
type ScorerCellDelta struct {
	Name      string
	Baseline  float64
	Candidate float64
	Delta     float64
}

// ComparisonRowData is one paired item of a materialized comparison, ready
// for the ListComparisonRows RPC.
type ComparisonRowData struct {
	InputHash       string
	InputPreview    string
	BaselineOutput  string
	CandidateOutput string
	ScorerDeltas    []ScorerCellDelta
	Regression      bool
}

// Row pagination bounds: limit<=0 falls back to the default, and no page can
// exceed the cap.
const (
	comparisonRowsDefaultLimit = 100
	comparisonRowsMaxLimit     = 500
)

// comparisonMetaRow is the slice of the comparisons table GetComparisonRows
// needs to re-derive per-item pairs.
type comparisonMetaRow struct {
	BaselineRunID  string `db:"baseline_run_id"`
	CandidateRunID string `db:"candidate_run_id"`
	MatchMode      string `db:"match_mode"`
}

// GetComparisonRows re-pairs the two runs of a materialized comparison (same
// loader + pairing as the verdict path, so rows always agree with the stored
// verdict) and returns one row per pair, paginated. total is the row count
// after the onlyRegressions filter, before offset/limit.
//
// The comparison is looked up by (id, tenant_id): a foreign-tenant comparison
// id must not resolve and hard-errors with ErrComparisonNotFound.
func GetComparisonRows(ctx context.Context, db *sqlx.DB, tenantID, comparisonID string, limit, offset int, onlyRegressions bool) (rows []ComparisonRowData, total int, err error) {
	if tenantID == "" {
		return nil, 0, fmt.Errorf("list comparison rows: tenant id is required")
	}
	if comparisonID == "" {
		return nil, 0, fmt.Errorf("list comparison rows: comparison id is required")
	}

	var meta comparisonMetaRow
	err = db.GetContext(ctx, &meta, `
		SELECT baseline_run_id, candidate_run_id, match_mode
		FROM comparisons
		WHERE id = $1 AND tenant_id = $2
	`, comparisonID, tenantID)
	if err == sql.ErrNoRows {
		return nil, 0, fmt.Errorf("list comparison rows: %w: %s", ErrComparisonNotFound, comparisonID)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list comparison rows: load comparison %s: %w", comparisonID, err)
	}

	baseItems, err := loadComparisonItems(ctx, db, tenantID, meta.BaselineRunID, true)
	if err != nil {
		return nil, 0, err
	}
	candItems, err := loadComparisonItems(ctx, db, tenantID, meta.CandidateRunID, true)
	if err != nil {
		return nil, 0, err
	}

	all := buildComparisonRows(meta.MatchMode, baseItems, candItems)
	rows, total = paginateComparisonRows(all, limit, offset, onlyRegressions)
	return rows, total, nil
}

// buildComparisonRows pairs the items (pairs inherit the deterministic
// created_at order of the item loads) and materializes one row per pair.
func buildComparisonRows(matchMode string, baseItems, candItems []comparisonItemRow) []ComparisonRowData {
	pairs := pairComparisonItems(matchMode, baseItems, candItems)
	rows := make([]ComparisonRowData, 0, len(pairs))
	for _, p := range pairs {
		baseScores := itemScoreMap(p.base.Scores)
		candScores := itemScoreMap(p.cand.Scores)
		names := make([]string, 0, len(candScores))
		for name := range candScores {
			if _, ok := baseScores[name]; ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)

		deltas := make([]ScorerCellDelta, 0, len(names))
		regression := false
		for _, name := range names {
			bv := baseScores[name]
			cv := candScores[name]
			d := cv - bv
			if d < 0 {
				regression = true
			}
			deltas = append(deltas, ScorerCellDelta{
				Name:      name,
				Baseline:  bv,
				Candidate: cv,
				Delta:     d,
			})
		}

		rows = append(rows, ComparisonRowData{
			InputHash:       p.base.InputHash.String,
			InputPreview:    previewString(p.base.InputCanonical, 200),
			BaselineOutput:  compactJSONString(p.base.Output),
			CandidateOutput: compactJSONString(p.cand.Output),
			ScorerDeltas:    deltas,
			Regression:      regression,
		})
	}
	return rows
}

// paginateComparisonRows applies the onlyRegressions filter, then
// offset/limit. total is the post-filter, pre-page count.
func paginateComparisonRows(rows []ComparisonRowData, limit, offset int, onlyRegressions bool) ([]ComparisonRowData, int) {
	if onlyRegressions {
		filtered := make([]ComparisonRowData, 0, len(rows))
		for _, r := range rows {
			if r.Regression {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	total := len(rows)

	if limit <= 0 {
		limit = comparisonRowsDefaultLimit
	}
	if limit > comparisonRowsMaxLimit {
		limit = comparisonRowsMaxLimit
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return nil, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return rows[offset:end], total
}

// previewString returns the first maxBytes bytes of b as a string, cut back
// to a rune boundary so a multibyte character is never split.
func previewString(b []byte, maxBytes int) string {
	s := string(b)
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// compactJSONString compacts a JSONB payload for transport; invalid or empty
// payloads pass through as-is.
func compactJSONString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return string(b)
	}
	return buf.String()
}
