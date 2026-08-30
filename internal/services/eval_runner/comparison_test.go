package eval_runner

import (
	"database/sql"
	"math"
	"strings"
	"testing"
)

// Reference values in the parity tests were produced by running the ORIGINAL
// JS implementation (apps/admin/src/utils/eval-stats.ts comparePaired,
// verbatim, under node). The Go port must reproduce them: same mulberry32
// PRNG, same checksum seeding (JS Math.round + ToInt32 semantics), same
// bootstrap. If these fail, seed parity broke and stored verdicts would
// disagree with what the UI historically showed.

const parityTolerance = 1e-12

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= parityTolerance
}

func TestComparePairedImprovement(t *testing.T) {
	a := []float64{0.5, 0.6, 0.7, 0.6, 0.55, 0.65}
	b := []float64{0.8, 0.85, 0.9, 0.82, 0.88, 0.86}
	got := comparePaired(a, b)

	if got.verdict != verdictImprovement {
		t.Fatalf("verdict = %q, want %q", got.verdict, verdictImprovement)
	}
	if got.ciLow <= 0 {
		t.Fatalf("ciLow = %v, want > 0", got.ciLow)
	}
	if got.n != 6 {
		t.Fatalf("n = %d, want 6", got.n)
	}
	// JS parity (node, eval-stats.ts verbatim).
	if !almostEqual(got.meanDiff, 0.25166666666666671) {
		t.Errorf("meanDiff = %.17g, want 0.25166666666666671", got.meanDiff)
	}
	if !almostEqual(got.ciLow, 0.215) {
		t.Errorf("ciLow = %.17g, want 0.215", got.ciLow)
	}
	if !almostEqual(got.ciHigh, 0.29333333333333333) {
		t.Errorf("ciHigh = %.17g, want 0.29333333333333333", got.ciHigh)
	}
	if got.pValue != 0 {
		t.Errorf("pValue = %v, want 0", got.pValue)
	}
}

func TestComparePairedNoisyInconclusiveParity(t *testing.T) {
	a := []float64{0.5, 0.9, 0.3, 0.7, 0.6, 0.4, 0.8, 0.55}
	b := []float64{0.6, 0.7, 0.5, 0.6, 0.7, 0.3, 0.9, 0.5}
	got := comparePaired(a, b)

	if got.verdict != verdictInconclusive {
		t.Fatalf("verdict = %q, want %q", got.verdict, verdictInconclusive)
	}
	// JS parity (node, eval-stats.ts verbatim).
	if !almostEqual(got.ciLow, -0.081250000000000044) {
		t.Errorf("ciLow = %.17g, want -0.081250000000000044", got.ciLow)
	}
	if !almostEqual(got.ciHigh, 0.093749999999999986) {
		t.Errorf("ciHigh = %.17g, want 0.093749999999999986", got.ciHigh)
	}
	if !almostEqual(got.pValue, 0.983) {
		t.Errorf("pValue = %v, want 0.983", got.pValue)
	}
}

func TestComparePairedNoDifference(t *testing.T) {
	a := []float64{0.5, 0.6, 0.7, 0.6, 0.55, 0.65}
	got := comparePaired(a, a)

	if got.verdict != verdictInconclusive {
		t.Fatalf("verdict = %q, want %q", got.verdict, verdictInconclusive)
	}
	if got.ciLow != 0 || got.ciHigh != 0 {
		t.Errorf("CI = [%v, %v], want [0, 0] for identical runs", got.ciLow, got.ciHigh)
	}
	if got.meanDiff != 0 {
		t.Errorf("meanDiff = %v, want 0", got.meanDiff)
	}
}

func TestComparePairedInsufficientN(t *testing.T) {
	a := []float64{0.5, 0.6, 0.7, 0.6}
	b := []float64{0.8, 0.85, 0.9, 0.82}
	got := comparePaired(a, b)

	if got.verdict != verdictInsufficient {
		t.Fatalf("verdict = %q, want %q for n=4", got.verdict, verdictInsufficient)
	}
	if got.pValue != 1 {
		t.Errorf("pValue = %v, want 1", got.pValue)
	}
	if got.ciLow != 0 || got.ciHigh != 0 {
		t.Errorf("CI = [%v, %v], want [0, 0]", got.ciLow, got.ciHigh)
	}
	// Means are still reported even when the test is not run.
	if !almostEqual(got.meanA, 0.6) {
		t.Errorf("meanA = %v, want 0.6", got.meanA)
	}
}

func TestComparePairedDeterministic(t *testing.T) {
	a := []float64{0.5, 0.9, 0.3, 0.7, 0.6, 0.4, 0.8, 0.55}
	b := []float64{0.6, 0.7, 0.5, 0.6, 0.7, 0.3, 0.9, 0.5}
	first := comparePaired(a, b)
	for i := 0; i < 10; i++ {
		got := comparePaired(a, b)
		if got != first {
			t.Fatalf("call %d differed: got %+v, want %+v", i+1, got, first)
		}
	}
}

// --- compositeVerdict truth table -----------------------------------------

func scorer(name, verdict string) ScorerResult {
	return ScorerResult{Name: name, Verdict: verdict, N: 20}
}

// sigMaterial builds a paired cost/latency comparison that is statistically
// significant (CI excludes 0, candidate higher) AND material (>10% relative).
func sigMaterial(meanA, meanDiff float64) pairedComparison {
	return pairedComparison{
		n: 20, meanA: meanA, meanB: meanA + meanDiff, meanDiff: meanDiff,
		ciLow: meanDiff * 0.5, ciHigh: meanDiff * 1.5, pValue: 0.001,
		verdict: verdictImprovement, // "improvement" = candidate higher; for cost that is bad
	}
}

// sigImmaterial is significant but below the 10% materiality bar.
func sigImmaterial(meanA float64) pairedComparison {
	d := meanA * 0.02 // 2% relative increase
	return pairedComparison{
		n: 1000, meanA: meanA, meanB: meanA + d, meanDiff: d,
		ciLow: d * 0.5, ciHigh: d * 1.5, pValue: 0.001,
		verdict: verdictImprovement,
	}
}

func flatMetric(meanA float64) pairedComparison {
	return pairedComparison{n: 20, meanA: meanA, meanB: meanA, verdict: verdictInconclusive, pValue: 1}
}

func TestCompositeVerdictTruthTable(t *testing.T) {
	flatCost := flatMetric(0.01)
	flatLat := flatMetric(900)

	cases := []struct {
		name string
		in   compositeInputs
		want ComparisonGrade
	}{
		{
			name: "mixed scorer directions -> tradeoff",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictRegression), scorer("helpfulness", verdictImprovement)},
				Cost:     flatCost, Latency: flatLat,
			},
			want: GradeTradeoff,
		},
		{
			name: "all improve, no ops regression -> improvement",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictImprovement), scorer("helpfulness", verdictImprovement)},
				Cost:     flatCost, Latency: flatLat,
			},
			want: GradeImprovement,
		},
		{
			name: "all improve + material cost regression -> tradeoff",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictImprovement)},
				Cost:     sigMaterial(0.01, 0.005), // +50% cost
				Latency:  flatLat,
			},
			want: GradeTradeoff,
		},
		{
			name: "flat quality + material cost regression -> regression (not tie)",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictInconclusive)},
				Cost:     sigMaterial(0.01, 0.005),
				Latency:  flatLat,
			},
			want: GradeRegression,
		},
		{
			name: "flat quality + significant-but-immaterial cost bump -> tie",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictInconclusive)},
				Cost:     sigImmaterial(0.01), // +2%: jitter, not material
				Latency:  flatLat,
			},
			want: GradeTie,
		},
		{
			name: "flat quality, flat ops -> tie",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictInconclusive), scorer("helpfulness", verdictInconclusive)},
				Cost:     flatCost, Latency: flatLat,
			},
			want: GradeTie,
		},
		{
			name: "coverage below 50% -> insufficient data",
			in: compositeInputs{
				Coverage: 0.4,
				Scorers:  []ScorerResult{scorer("accuracy", verdictImprovement)},
				Cost:     flatCost, Latency: flatLat,
			},
			want: GradeInsufficientData,
		},
		{
			name: "every scorer insufficient -> insufficient data",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictInsufficient), scorer("helpfulness", verdictInsufficient)},
				Cost:     flatCost, Latency: flatLat,
			},
			want: GradeInsufficientData,
		},
		{
			name: "no shared scorers -> insufficient data",
			in: compositeInputs{
				Coverage: 1,
				Cost:     flatCost, Latency: flatLat,
			},
			want: GradeInsufficientData,
		},
		{
			name: "error-rate jump + flat quality -> regression (survivorship guard)",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictInconclusive)},
				Cost:     flatCost, Latency: flatLat,
				ErrorRateDelta: 0.12,
			},
			want: GradeRegression,
		},
		{
			name: "error-rate jump + scorer improvement -> tradeoff (survivorship guard)",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictImprovement)},
				Cost:     flatCost, Latency: flatLat,
				ErrorRateDelta: 0.12,
			},
			want: GradeTradeoff,
		},
		{
			name: "scorer regression alone -> regression",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictRegression), scorer("helpfulness", verdictInconclusive)},
				Cost:     flatCost, Latency: flatLat,
			},
			want: GradeRegression,
		},
		{
			name: "material latency regression + flat quality -> regression",
			in: compositeInputs{
				Coverage: 1,
				Scorers:  []ScorerResult{scorer("accuracy", verdictInconclusive)},
				Cost:     flatCost,
				Latency:  sigMaterial(1000, 400), // +40% latency
			},
			want: GradeRegression,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rationale := compositeVerdict(tc.in)
			if got != tc.want {
				t.Fatalf("grade = %q (rationale %q), want %q", got, rationale, tc.want)
			}
			if rationale == "" {
				t.Errorf("rationale is empty")
			}
		})
	}
}

// --- score extraction ------------------------------------------------------

func TestItemScoreMap(t *testing.T) {
	// Map form with plain numbers, booleans, and wrapped values.
	got := itemScoreMap([]byte(`{"accuracy":0.8,"exact_match":true,"judge":{"value":0.7},"nested":{"numericValue":0.4},"skip":"text"}`))
	want := map[string]float64{"accuracy": 0.8, "exact_match": 1, "judge": 0.7, "nested": 0.4}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}

	// Array form.
	got = itemScoreMap([]byte(`[{"name":"accuracy","value":0.9},{"name":"pass","score":false},{"name":"skip","value":"text"}]`))
	if got["accuracy"] != 0.9 {
		t.Errorf("array accuracy = %v, want 0.9", got["accuracy"])
	}
	if v, ok := got["pass"]; !ok || v != 0 {
		t.Errorf("array pass = %v (present=%v), want 0", v, ok)
	}
	if _, ok := got["skip"]; ok {
		t.Errorf("non-numeric score should be skipped")
	}

	// Degenerate payloads.
	if m := itemScoreMap(nil); len(m) != 0 {
		t.Errorf("nil payload: got %v", m)
	}
	if m := itemScoreMap([]byte(`not json`)); len(m) != 0 {
		t.Errorf("invalid payload: got %v", m)
	}
}

// --- pairing ---------------------------------------------------------------

func hashItem(id, hash string) comparisonItemRow {
	return comparisonItemRow{ID: id, InputHash: sql.NullString{String: hash, Valid: true}, Status: "completed"}
}

func TestPairComparisonItemsHashOccurrenceRank(t *testing.T) {
	// Duplicate hashes within a run are normal: k-th occurrence pairs with
	// k-th occurrence, never a cross-product.
	base := []comparisonItemRow{hashItem("b1", "h1"), hashItem("b2", "h1"), hashItem("b3", "h2")}
	cand := []comparisonItemRow{hashItem("c1", "h1"), hashItem("c2", "h2"), hashItem("c3", "h1"), hashItem("c4", "h1")}

	pairs := pairComparisonItems(MatchModeHash, base, cand)
	if len(pairs) != 3 {
		t.Fatalf("got %d pairs, want 3 (2x h1 by rank + 1x h2; extra c4 unmatched)", len(pairs))
	}
	wantPairs := map[string]string{"c1": "b1", "c3": "b2", "c2": "b3"}
	for _, p := range pairs {
		if wantPairs[p.cand.ID] != p.base.ID {
			t.Errorf("candidate %s paired with %s, want %s", p.cand.ID, p.base.ID, wantPairs[p.cand.ID])
		}
	}
}

func TestPairComparisonItemsDatasetItem(t *testing.T) {
	base := []comparisonItemRow{
		{ID: "b1", DatasetItemID: "d1", Status: "completed"},
		{ID: "b2", DatasetItemID: "d2", Status: "completed"},
	}
	cand := []comparisonItemRow{
		{ID: "c1", DatasetItemID: "d2", Status: "completed"},
		{ID: "c2", DatasetItemID: "d3", Status: "completed"},
	}
	pairs := pairComparisonItems(MatchModeDatasetItem, base, cand)
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want 1", len(pairs))
	}
	if pairs[0].base.ID != "b2" || pairs[0].cand.ID != "c1" {
		t.Errorf("paired %s/%s, want b2/c1", pairs[0].base.ID, pairs[0].cand.ID)
	}
}

// --- comparison rows ---------------------------------------------------------

func rowItem(id, datasetItemID, scores, input, output string) comparisonItemRow {
	return comparisonItemRow{
		ID:             id,
		DatasetItemID:  datasetItemID,
		Scores:         []byte(scores),
		InputCanonical: []byte(input),
		Output:         []byte(output),
		Status:         "completed",
	}
}

func TestBuildComparisonRows(t *testing.T) {
	base := []comparisonItemRow{
		rowItem("b1", "d1", `{"accuracy":0.9,"style":0.5,"base_only":1}`, `{"q":"one"}`, `{ "a" : 1 }`),
		rowItem("b2", "d2", `{"accuracy":0.4}`, `{"q":"two"}`, `{"a":2}`),
	}
	cand := []comparisonItemRow{
		rowItem("c1", "d1", `{"accuracy":0.7,"style":0.8,"cand_only":1}`, `{"q":"one-cand"}`, `{"a":10}`),
		rowItem("c2", "d2", `{"accuracy":0.6}`, `{"q":"two-cand"}`, `{"a":20}`),
	}

	rows := buildComparisonRows(MatchModeDatasetItem, base, cand)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	// Row 1: accuracy regressed, style improved; one-sided scorers excluded.
	r := rows[0]
	if len(r.ScorerDeltas) != 2 {
		t.Fatalf("row 0: got %d scorer deltas (%v), want 2 (shared scorers only)", len(r.ScorerDeltas), r.ScorerDeltas)
	}
	if r.ScorerDeltas[0].Name != "accuracy" || r.ScorerDeltas[1].Name != "style" {
		t.Errorf("row 0 scorer order = %s,%s, want accuracy,style", r.ScorerDeltas[0].Name, r.ScorerDeltas[1].Name)
	}
	if d := r.ScorerDeltas[0]; math.Abs(d.Delta-(-0.2)) > 1e-12 || d.Baseline != 0.9 || d.Candidate != 0.7 {
		t.Errorf("row 0 accuracy delta = %+v, want baseline 0.9, candidate 0.7, delta -0.2", d)
	}
	if !r.Regression {
		t.Errorf("row 0 should flag a regression (accuracy dropped)")
	}
	// Input preview comes from the BASE item; outputs are compacted.
	if r.InputPreview != `{"q":"one"}` {
		t.Errorf("row 0 input preview = %q", r.InputPreview)
	}
	if r.BaselineOutput != `{"a":1}` {
		t.Errorf("row 0 baseline output = %q, want compacted {\"a\":1}", r.BaselineOutput)
	}
	if r.CandidateOutput != `{"a":10}` {
		t.Errorf("row 0 candidate output = %q", r.CandidateOutput)
	}

	// Row 2: accuracy improved, no regression.
	if rows[1].Regression {
		t.Errorf("row 1 should not flag a regression")
	}
	if d := rows[1].ScorerDeltas[0]; math.Abs(d.Delta-0.2) > 1e-12 {
		t.Errorf("row 1 accuracy delta = %v, want 0.2", d.Delta)
	}
}

func TestPaginateComparisonRows(t *testing.T) {
	rows := make([]ComparisonRowData, 7)
	for i := range rows {
		rows[i].InputHash = string(rune('a' + i))
		rows[i].Regression = i%2 == 0 // a c e g regress (4 rows)
	}

	// Default limit, no filter.
	page, total := paginateComparisonRows(rows, 0, 0, false)
	if total != 7 || len(page) != 7 {
		t.Fatalf("default: got total=%d len=%d, want 7/7", total, len(page))
	}

	// Offset + limit window.
	page, total = paginateComparisonRows(rows, 2, 3, false)
	if total != 7 || len(page) != 2 || page[0].InputHash != "d" || page[1].InputHash != "e" {
		t.Fatalf("window: got total=%d page=%v", total, page)
	}

	// onlyRegressions filters BEFORE pagination; total is post-filter.
	page, total = paginateComparisonRows(rows, 2, 2, true)
	if total != 4 {
		t.Fatalf("regressions total = %d, want 4", total)
	}
	if len(page) != 2 || page[0].InputHash != "e" || page[1].InputHash != "g" {
		t.Fatalf("regressions page = %v, want [e g]", page)
	}

	// Offset past the end returns an empty page but the true total.
	page, total = paginateComparisonRows(rows, 10, 100, false)
	if total != 7 || len(page) != 0 {
		t.Fatalf("past-end: got total=%d len=%d", total, len(page))
	}

	// Limit is capped at 500.
	big := make([]ComparisonRowData, 600)
	page, total = paginateComparisonRows(big, 10000, 0, false)
	if total != 600 || len(page) != 500 {
		t.Fatalf("cap: got total=%d len=%d, want 600/500", total, len(page))
	}
}

func TestPreviewString(t *testing.T) {
	if got := previewString([]byte("short"), 200); got != "short" {
		t.Errorf("short preview = %q", got)
	}
	// The 2-byte é occupies bytes 199-200: a naive byte cut at 200 would
	// split it, so the preview must back off to byte 199.
	long := strings.Repeat("x", 199) + "éllo"
	got := previewString([]byte(long), 200)
	if len(got) != 199 || got[len(got)-1] != 'x' {
		t.Errorf("preview len = %d ending %q, want 199 ending in x (rune-safe cut)", len(got), got[len(got)-1:])
	}
	// Exact-boundary cut keeps the full rune intact.
	if got := previewString([]byte("aé"), 3); got != "aé" {
		t.Errorf("boundary preview = %q, want aé", got)
	}
}
