// everstack-eval — CI/CD gating CLI.
//
// Triggers an eval run against a remote Everstack instance, polls for
// completion, and exits nonzero if the resulting scores regress against
// a baseline run OR fall below per-scorer thresholds.
//
// Designed for direct use in CI workflows: a failing run blocks the PR.
//
// Usage:
//
//	everstack-eval run \
//	  --dataset <dataset_id> \
//	  [--dataset-version <version_id>] \
//	  --target-type chat \
//	  --target-id @openai/gpt-4o-mini \
//	  --scorer <score_config_id> [--scorer ...] \
//	  [--threshold scorer_name=0.9] [--threshold ...] \
//	  [--baseline <run_id>] \
//	  [--markdown-summary eval-summary.md] \
//	  [--name "PR-1234"] \
//	  [--api-key $EVERSTACK_API_KEY] \
//	  [--base-url $EVERSTACK_BASE_URL] \
//	  [--timeout 30m]
//
// Exit codes:
//
//	0  — all thresholds met, no regression
//	1  — usage / network / server error
//	2  — eval completed but failed threshold check
//	3  — eval completed but regressed against baseline
//
// Verdict gating:
//
//	--threshold-verdict win:0.85       requires win_rate >= 0.85
//	--threshold-verdict draw:0.10      requires draw_rate <= 0.10
//	--threshold-verdict fail:0.05      requires fail_rate <= 0.05
//	--threshold-verdict no_change:0.20 requires no_change_rate <= 0.20
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
	datasetsconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1/datasetsconnect"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	exitOK            = 0
	exitUsageOrServer = 1
	exitThresholdFail = 2
	exitRegression    = 3
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(exitUsageOrServer)
	}

	switch os.Args[1] {
	case "run":
		os.Exit(runCmd(os.Args[2:]))
	case "simulate":
		os.Exit(simulateCmd(os.Args[2:]))
	case "version", "-v", "--version":
		fmt.Println("everstack-eval dev")
		os.Exit(exitOK)
	case "help", "-h", "--help":
		printUsage()
		os.Exit(exitOK)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		printUsage()
		os.Exit(exitUsageOrServer)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `everstack-eval — CI/CD eval CLI

Subcommands:
  run         Trigger an eval run and gate on the result
  simulate    Run a multi-turn agent simulation against a target model

Run "everstack-eval <subcommand> --help" for flags.

Environment:
  EVERSTACK_API_KEY    API key (alternative to --api-key)
  EVERSTACK_BASE_URL   Base URL (alternative to --base-url, default https://api.everstack.ai)`)
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)

	datasetID := fs.String("dataset", "", "Dataset id to evaluate against (required)")
	datasetVersionID := fs.String("dataset-version", "", "Dataset version id to pin for this eval run (optional)")
	targetType := fs.String("target-type", "chat", "Eval target type: chat, agent, completion")
	targetID := fs.String("target-id", "", "Eval target id (model name, agent id, etc.)")
	scorerIDs := stringSliceFlag{}
	fs.Var(&scorerIDs, "scorer", "Score config id (repeat for multiple)")
	thresholds := stringSliceFlag{}
	fs.Var(&thresholds, "threshold", `Threshold predicate "name=value" or "name>=value" (repeat)`)
	verdictThresholds := stringSliceFlag{}
	fs.Var(&verdictThresholds, "threshold-verdict", `fix_attempt_verdict gate "bucket:rate" (win|fail|draw|no_change); repeatable`)
	baseline := fs.String("baseline", "", "Baseline eval-run id to diff against (optional)")
	name := fs.String("name", "", "Eval run name (defaults to timestamped)")
	apiKey := fs.String("api-key", os.Getenv("EVERSTACK_API_KEY"), "API key")
	baseURL := fs.String("base-url", envOr("EVERSTACK_BASE_URL", "https://api.everstack.ai"), "Base URL")
	timeoutFlag := fs.Duration("timeout", 30*time.Minute, "Max time to wait for run to finish")
	pollInterval := fs.Duration("poll-interval", 5*time.Second, "Poll interval while waiting")
	markdownSummaryPath := fs.String("markdown-summary", "", "Write a Markdown summary to this path (optional)")
	quiet := fs.Bool("quiet", false, "Suppress progress lines")

	if err := fs.Parse(args); err != nil {
		return exitUsageOrServer
	}

	if *datasetID == "" || *apiKey == "" || len(scorerIDs) == 0 {
		fs.Usage()
		fmt.Fprintln(os.Stderr, "\n--dataset, --api-key (or EVERSTACK_API_KEY), and at least one --scorer are required.")
		return exitUsageOrServer
	}

	parsedThresholds, err := parseThresholds(thresholds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "threshold parse error: %v\n", err)
		return exitUsageOrServer
	}
	parsedVerdictThresholds, err := parseVerdictThresholds(verdictThresholds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verdict threshold parse error: %v\n", err)
		return exitUsageOrServer
	}

	httpClient := &http.Client{
		Timeout: 0, // streaming-friendly; per-call deadlines via ctx.
		Transport: &headerRoundTripper{
			base:    http.DefaultTransport,
			headers: map[string]string{"x-api-key": *apiKey, "Authorization": "Bearer " + *apiKey},
		},
	}

	evalClient := datasetsconnect.NewEvalServiceClient(httpClient, *baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	runName := *name
	if runName == "" {
		runName = "everstack-eval " + time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}

	logf := func(format string, args ...interface{}) {
		if !*quiet {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}

	createReq := &datasetspb.CreateEvalRunRequest{
		DatasetId:        *datasetID,
		DatasetVersionId: *datasetVersionID,
		Name:             runName,
		EvalTargetType:   *targetType,
		EvalTargetId:     targetID,
		ScorerConfigIds:  scorerIDs,
	}
	createResp, err := evalClient.CreateEvalRun(ctx, connect.NewRequest(createReq))
	if err != nil {
		fmt.Fprintf(os.Stderr, "CreateEvalRun: %v\n", err)
		return exitUsageOrServer
	}
	runID := createResp.Msg.GetEvalRun().GetId()
	logf("eval run created: %s", runID)
	logf("polling for completion (timeout %s)…", *timeoutFlag)

	// Poll
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "timeout waiting for eval run to finish")
			return exitUsageOrServer
		default:
		}

		getResp, err := evalClient.GetEvalRun(ctx, connect.NewRequest(&datasetspb.GetEvalRunRequest{Id: runID}))
		if err != nil {
			fmt.Fprintf(os.Stderr, "GetEvalRun: %v\n", err)
			return exitUsageOrServer
		}
		run := getResp.Msg.GetEvalRun()
		status := strings.ToLower(run.GetStatus())
		logf("  status=%s completed=%d failed=%d total=%d",
			status, run.GetCompletedItems(), run.GetFailedItems(), run.GetTotalItems())
		if status == "completed" || status == "failed" || status == "cancelled" {
			return gateOnResult(ctx, evalClient, run, parsedThresholds, parsedVerdictThresholds, *baseline, *markdownSummaryPath, logf)
		}
		time.Sleep(*pollInterval)
	}
}

type thresholdPredicate struct {
	scorer string
	op     string // ">=" or "==" or "<="
	value  float64
}

type thresholdResult struct {
	scorer  string
	op      string
	want    float64
	got     float64
	present bool
	passed  bool
}

type verdictPredicate struct {
	bucket string
	rate   float64
}

func parseVerdictThresholds(specs []string) ([]verdictPredicate, error) {
	out := make([]verdictPredicate, 0, len(specs))
	for _, spec := range specs {
		parts := strings.SplitN(spec, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid verdict threshold %q (expected bucket:rate)", spec)
		}
		bucket := strings.TrimSpace(parts[0])
		switch bucket {
		case "win", "fail", "draw", "no_change":
		default:
			return nil, fmt.Errorf("invalid verdict bucket %q (allowed: win, fail, draw, no_change)", bucket)
		}
		rate, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid rate in %q: %w", spec, err)
		}
		if rate < 0 || rate > 1 {
			return nil, fmt.Errorf("rate must be in [0,1], got %v", rate)
		}
		out = append(out, verdictPredicate{bucket: bucket, rate: rate})
	}
	return out, nil
}

func evalVerdictPredicate(predicate verdictPredicate, actual float64) bool {
	if predicate.bucket == "win" {
		return actual >= predicate.rate
	}
	return actual <= predicate.rate
}

func parseThresholds(specs []string) ([]thresholdPredicate, error) {
	out := make([]thresholdPredicate, 0, len(specs))
	for _, s := range specs {
		op := "=="
		// Recognise >=, <=, =, == in priority order.
		switch {
		case strings.Contains(s, ">="):
			op = ">="
		case strings.Contains(s, "<="):
			op = "<="
		}
		var name, rhs string
		switch op {
		case ">=", "<=":
			parts := strings.SplitN(s, op, 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid threshold %q", s)
			}
			name = strings.TrimSpace(parts[0])
			rhs = strings.TrimSpace(parts[1])
		default:
			parts := strings.SplitN(s, "=", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid threshold %q", s)
			}
			name = strings.TrimSpace(parts[0])
			rhs = strings.TrimSpace(parts[1])
		}
		v, err := strconv.ParseFloat(rhs, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid threshold value in %q: %w", s, err)
		}
		out = append(out, thresholdPredicate{scorer: name, op: op, value: v})
	}
	return out, nil
}

func gateOnResult(
	ctx context.Context,
	client datasetsconnect.EvalServiceClient,
	run *datasetspb.EvalRun,
	thresholds []thresholdPredicate,
	verdictThresholds []verdictPredicate,
	baseline string,
	markdownSummaryPath string,
	logf func(string, ...interface{}),
) int {
	scores := extractScoreSummary(run)
	logf("scores: %v", scores)

	// 1. Threshold gate
	thresholdResults := evaluateThresholds(scores, thresholds)
	fail := false
	for _, r := range thresholdResults {
		if !r.present {
			fmt.Fprintf(os.Stderr, "threshold %q: scorer not present in run\n", r.scorer)
			fail = true
			continue
		}
		if !r.passed {
			fmt.Fprintf(os.Stderr, "FAIL %s: got %.4f, want %s %.4f\n", r.scorer, r.got, r.op, r.want)
			fail = true
		} else {
			logf("PASS %s: got %.4f %s %.4f", r.scorer, r.got, r.op, r.want)
		}
	}

	if len(verdictThresholds) > 0 {
		rates, ok := extractVerdictRates(run)
		if !ok {
			fmt.Fprintf(os.Stderr, "verdict thresholds specified but run.score_summary[%q] is missing; label traces and re-run\n", verdictScoreName)
			fail = true
		} else {
			for _, predicate := range verdictThresholds {
				actual := rates[predicate.bucket]
				op := "<="
				if predicate.bucket == "win" {
					op = ">="
				}
				if evalVerdictPredicate(predicate, actual) {
					logf("PASS verdict.%s: got %.4f %s %.4f", predicate.bucket, actual, op, predicate.rate)
				} else {
					fmt.Fprintf(os.Stderr, "FAIL verdict.%s: got %.4f, want %s %.4f\n", predicate.bucket, actual, op, predicate.rate)
					fail = true
				}
			}
		}
	}
	if fail {
		if err := writeMarkdownSummary(markdownSummaryPath, buildThresholdMarkdownSummary(run, scores, thresholdResults, false)); err != nil {
			fmt.Fprintf(os.Stderr, "write markdown summary: %v\n", err)
		}
		return exitThresholdFail
	}

	// 2. Regression vs baseline
	if baseline != "" {
		cmpResp, err := client.CompareEvalRuns(ctx, connect.NewRequest(&datasetspb.CompareEvalRunsRequest{
			EvalRunIds: []string{baseline, run.GetId()},
			Persist:    true,
		}))
		if err != nil {
			fmt.Fprintf(os.Stderr, "CompareEvalRuns: %v\n", err)
			return exitUsageOrServer
		}

		hasRegression := false
		overall := cmpResp.Msg.GetOverall()
		if overall != nil {
			logf("comparison overall grade=%s rationale=%s", comparisonGradeLabel(overall.GetGrade()), overall.GetRationale())
			hasRegression = overall.GetGrade() == datasetspb.ComparisonGrade_COMPARISON_GRADE_REGRESSION
		} else {
			hasRegression = readRegressionFlag(cmpResp.Msg.GetComparison())
			logf("comparison overall grade=unavailable rationale=legacy has_regression=%t", hasRegression)
		}

		summaryErr := writeMarkdownSummary(markdownSummaryPath, buildComparisonMarkdownSummary(baseline, run, cmpResp.Msg))
		if hasRegression {
			if overall != nil {
				fmt.Fprintf(os.Stderr, "REGRESSION detected vs baseline; blocking. grade=%s rationale=%s\n", comparisonGradeLabel(overall.GetGrade()), overall.GetRationale())
			} else {
				fmt.Fprintln(os.Stderr, "REGRESSION detected vs baseline; blocking.")
			}
			if summaryErr != nil {
				fmt.Fprintf(os.Stderr, "write markdown summary: %v\n", summaryErr)
			}
			return exitRegression
		}
		if summaryErr != nil {
			fmt.Fprintf(os.Stderr, "write markdown summary: %v\n", summaryErr)
			return exitUsageOrServer
		}
		logf("no regression vs baseline %s", baseline)
	}

	if baseline == "" {
		if err := writeMarkdownSummary(markdownSummaryPath, buildThresholdMarkdownSummary(run, scores, thresholdResults, true)); err != nil {
			fmt.Fprintf(os.Stderr, "write markdown summary: %v\n", err)
			return exitUsageOrServer
		}
	}

	logf("all gates passed.")
	return exitOK
}

func evaluateThresholds(scores map[string]float64, thresholds []thresholdPredicate) []thresholdResult {
	out := make([]thresholdResult, 0, len(thresholds))
	for _, t := range thresholds {
		got, ok := scores[t.scorer]
		out = append(out, thresholdResult{
			scorer:  t.scorer,
			op:      t.op,
			want:    t.value,
			got:     got,
			present: ok,
			passed:  ok && evalOp(t.op, got, t.value),
		})
	}
	return out
}

func evalOp(op string, got, want float64) bool {
	switch op {
	case ">=":
		return got >= want
	case "<=":
		return got <= want
	default:
		return got == want
	}
}

const verdictScoreName = "fix_attempt_verdict"

func extractVerdictRates(run *datasetspb.EvalRun) (map[string]float64, bool) {
	summary := run.GetScoreSummary()
	if summary == nil {
		return nil, false
	}
	field, ok := summary.GetFields()[verdictScoreName]
	if !ok || field == nil || field.GetStructValue() == nil {
		return nil, false
	}
	rates := map[string]float64{}
	for _, bucket := range []string{"win", "fail", "draw", "no_change"} {
		if value := field.GetStructValue().GetFields()[bucket]; value != nil {
			rates[bucket] = value.GetNumberValue()
		}
	}
	return rates, len(rates) > 0
}

// extractScoreSummary pulls per-scorer mean values out of the EvalRun.
// score_summary is a google.protobuf.Struct, so we navigate it as a map.
func extractScoreSummary(run *datasetspb.EvalRun) map[string]float64 {
	out := map[string]float64{}
	summary := run.GetScoreSummary()
	if summary == nil {
		return out
	}
	for name, val := range summary.GetFields() {
		// Each scorer summary is itself a struct with a "mean" field.
		if val == nil {
			continue
		}
		nested := val.GetStructValue()
		if nested == nil {
			// Sometimes the summary is flat numeric values
			if n := val.GetNumberValue(); n != 0 || val.GetKind() != nil {
				out[name] = n
			}
			continue
		}
		if meanVal, ok := nested.GetFields()["mean"]; ok && meanVal != nil {
			out[name] = meanVal.GetNumberValue()
		} else if avgVal, ok := nested.GetFields()["avg"]; ok && avgVal != nil {
			out[name] = avgVal.GetNumberValue()
		}
	}
	return out
}

// readRegressionFlag inspects the CompareEvalRuns `comparison` struct
// for a regression signal. The eval-runner emits this as a top-level
// `has_regression` or `hasRegression` boolean (see
// internal/services/eval_runner/regression.go). Accept either spelling.
func readRegressionFlag(s *structpb.Struct) bool {
	if s == nil {
		return false
	}
	fields := s.GetFields()
	for _, k := range []string{"has_regression", "hasRegression", "regressed"} {
		v, ok := fields[k]
		if !ok || v == nil {
			continue
		}
		if v.GetBoolValue() {
			return true
		}
	}
	return false
}

func writeMarkdownSummary(path, body string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(body), 0644)
}

func buildComparisonMarkdownSummary(baseline string, run *datasetspb.EvalRun, cmp *datasetspb.CompareEvalRunsResponse) string {
	var b strings.Builder
	overall := cmp.GetOverall()
	if overall != nil {
		grade := comparisonGradeLabel(overall.GetGrade())
		rationale := strings.TrimSpace(overall.GetRationale())
		if rationale != "" {
			fmt.Fprintf(&b, "# Everstack Eval: %s — %s\n\n", grade, rationale)
		} else {
			fmt.Fprintf(&b, "# Everstack Eval: %s\n\n", grade)
		}
	} else {
		legacy := "no regression"
		if readRegressionFlag(cmp.GetComparison()) {
			legacy = "regression"
		}
		fmt.Fprintf(&b, "# Everstack Eval: legacy comparison — %s\n\n", legacy)
	}

	fmt.Fprintf(&b, "- Candidate run: `%s`\n", run.GetId())
	fmt.Fprintf(&b, "- Baseline run: `%s`\n", baseline)
	if cmp.GetComparisonId() != "" {
		fmt.Fprintf(&b, "- Comparison: `%s`\n", cmp.GetComparisonId())
	}
	b.WriteString("\n")

	b.WriteString("| Scorer | Baseline | Candidate | Δ | CI | Verdict | n |\n")
	b.WriteString("|---|---:|---:|---:|---|---|---:|\n")
	if len(cmp.GetScorerResults()) == 0 {
		b.WriteString("| _No scorer comparison results returned_ |  |  |  |  |  |  |\n")
	} else {
		for _, r := range cmp.GetScorerResults() {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %d |\n",
				mdCell(r.GetName()),
				formatFloat(r.GetBaselineMean()),
				formatFloat(r.GetCandidateMean()),
				formatSignedFloat(r.GetMeanDiff()),
				mdCell(fmt.Sprintf("[%s, %s]", formatSignedFloat(r.GetCiLow()), formatSignedFloat(r.GetCiHigh()))),
				mdCell(r.GetVerdict()),
				r.GetN(),
			)
		}
	}

	b.WriteString("\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|---|---:|\n")
	if overall != nil {
		fmt.Fprintf(&b, "| Latency Δ (ms) | %s |\n", formatSignedFloat(overall.GetLatencyDelta()))
		fmt.Fprintf(&b, "| Cost Δ | %s |\n", formatSignedFloat(overall.GetCostDelta()))
		fmt.Fprintf(&b, "| Error-rate Δ | %s |\n", formatSignedPercent(overall.GetErrorRateDelta()))
		fmt.Fprintf(&b, "| Coverage | %s |\n", formatPercent(overall.GetCoverage()))
	} else {
		b.WriteString("| Latency Δ (ms) | n/a |\n")
		b.WriteString("| Cost Δ | n/a |\n")
		b.WriteString("| Error-rate Δ | n/a |\n")
		b.WriteString("| Coverage | n/a |\n")
	}
	fmt.Fprintf(&b, "| Match mode | %s |\n", mdCell(valueOrNA(cmp.GetMatchMode())))

	return b.String()
}

func buildThresholdMarkdownSummary(run *datasetspb.EvalRun, scores map[string]float64, results []thresholdResult, passed bool) string {
	var b strings.Builder
	if passed {
		b.WriteString("# Everstack Eval: thresholds passed\n\n")
	} else {
		b.WriteString("# Everstack Eval: thresholds failed\n\n")
	}
	fmt.Fprintf(&b, "- Eval run: `%s`\n", run.GetId())
	fmt.Fprintf(&b, "- Status: `%s`\n\n", run.GetStatus())

	if len(results) > 0 {
		b.WriteString("| Scorer | Score | Threshold | Result |\n")
		b.WriteString("|---|---:|---|---|\n")
		for _, r := range results {
			score := "missing"
			if r.present {
				score = formatFloat(r.got)
			}
			result := "PASS"
			if !r.passed {
				result = "FAIL"
			}
			fmt.Fprintf(&b, "| %s | %s | %s %.4f | %s |\n",
				mdCell(r.scorer),
				score,
				r.op,
				r.want,
				result,
			)
		}
		return b.String()
	}

	b.WriteString("| Scorer | Score |\n")
	b.WriteString("|---|---:|\n")
	if len(scores) == 0 {
		b.WriteString("| _No score summary returned_ |  |\n")
		return b.String()
	}
	names := make([]string, 0, len(scores))
	for name := range scores {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "| %s | %s |\n", mdCell(name), formatFloat(scores[name]))
	}
	return b.String()
}

func comparisonGradeLabel(grade datasetspb.ComparisonGrade) string {
	switch grade {
	case datasetspb.ComparisonGrade_COMPARISON_GRADE_IMPROVEMENT:
		return "Improvement"
	case datasetspb.ComparisonGrade_COMPARISON_GRADE_REGRESSION:
		return "Regression"
	case datasetspb.ComparisonGrade_COMPARISON_GRADE_TRADEOFF:
		return "Tradeoff"
	case datasetspb.ComparisonGrade_COMPARISON_GRADE_TIE:
		return "Tie"
	case datasetspb.ComparisonGrade_COMPARISON_GRADE_INSUFFICIENT_DATA:
		return "Insufficient data"
	case datasetspb.ComparisonGrade_COMPARISON_GRADE_UNSPECIFIED:
		return "Unspecified"
	default:
		return grade.String()
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 4, 64)
}

func formatSignedFloat(v float64) string {
	if v > 0 {
		return "+" + formatFloat(v)
	}
	return formatFloat(v)
}

func formatPercent(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 2, 64) + "%"
}

func formatSignedPercent(v float64) string {
	if v > 0 {
		return "+" + formatPercent(v)
	}
	return formatPercent(v)
}

func valueOrNA(v string) string {
	if strings.TrimSpace(v) == "" {
		return "n/a"
	}
	return v
}

func mdCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// simulateCmd drives a multi-turn agent simulation against a target model.
//
// Persona model + target model both talk through the configured base URL
// (default: production gateway). Auth comes from --api-key. Each turn lands
// in /observability/traces with the scenario id stamped as metadata.
func simulateCmd(args []string) int {
	fs := flag.NewFlagSet("simulate", flag.ContinueOnError)
	persona := fs.String("persona", "", "Persona prompt for the simulated user (required)")
	initial := fs.String("initial", "", "Optional pre-seeded first message from the persona")
	target := fs.String("target", "", "Target chat model the persona talks to, e.g. @openai/gpt-4o-mini (required)")
	personaModel := fs.String("persona-model", "", "Model used to act as the persona (defaults to --target)")
	maxTurns := fs.Int("max-turns", 5, "Maximum back-and-forth turns")
	scenarioID := fs.String("id", "", "Scenario id (auto-generated if empty)")
	apiKey := fs.String("api-key", os.Getenv("EVERSTACK_API_KEY"), "API key")
	baseURL := fs.String("base-url", envOr("EVERSTACK_BASE_URL", "https://api.everstack.ai"), "Base URL of the gateway")
	timeout := fs.Duration("timeout", 5*time.Minute, "Max wall-clock for the whole simulation")
	quiet := fs.Bool("quiet", false, "Suppress per-turn streaming output")
	if err := fs.Parse(args); err != nil {
		return exitUsageOrServer
	}
	if *persona == "" || *target == "" || *apiKey == "" {
		fs.Usage()
		fmt.Fprintln(os.Stderr, "\n--persona, --target, --api-key (or EVERSTACK_API_KEY) are required.")
		return exitUsageOrServer
	}
	if *personaModel == "" {
		*personaModel = *target
	}
	if *scenarioID == "" {
		*scenarioID = fmt.Sprintf("sim-%d", time.Now().UnixNano())
	}

	logf := func(format string, a ...interface{}) {
		if !*quiet {
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	personaSystem := *persona + "\n\nReply only as the user. When the conversation has reached a natural end, output [END] alone on a line."
	personaHistory := []chatMsg{{Role: "system", Content: personaSystem}}
	targetHistory := []chatMsg{}
	nextPersonaText := *initial

	logf("scenario %s · target %s · persona %s · max %d turns", *scenarioID, *target, *personaModel, *maxTurns)

	for i := 0; i < *maxTurns; i++ {
		// Persona turn
		var personaText string
		if nextPersonaText != "" {
			personaText = nextPersonaText
			nextPersonaText = ""
		} else {
			seed := "(start of conversation — initiate your role)"
			for k := len(targetHistory) - 1; k >= 0; k-- {
				if targetHistory[k].Role == "assistant" {
					seed = targetHistory[k].Content
					break
				}
			}
			personaHistory = append(personaHistory, chatMsg{Role: "user", Content: seed})
			out, err := cliChatCompletion(ctx, *baseURL, *apiKey, *personaModel, personaHistory, *scenarioID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "persona model failed: %v\n", err)
				return exitUsageOrServer
			}
			personaText = out
			personaHistory = append(personaHistory, chatMsg{Role: "assistant", Content: personaText})
		}
		logf("\nturn %d  USER: %s", i+1, truncate(personaText, 200))
		if strings.Contains(strings.ToLower(personaText), "[end]") {
			logf("(persona signalled [END])")
			break
		}

		// Target turn
		targetHistory = append(targetHistory, chatMsg{Role: "user", Content: personaText})
		out, err := cliChatCompletion(ctx, *baseURL, *apiKey, *target, targetHistory, *scenarioID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "target model failed: %v\n", err)
			return exitUsageOrServer
		}
		targetHistory = append(targetHistory, chatMsg{Role: "assistant", Content: out})
		logf("turn %d  AGENT: %s", i+1, truncate(out, 200))
		if strings.Contains(strings.ToLower(out), "[end]") {
			logf("(target signalled [END])")
			break
		}
	}

	logf("\nDone. Traces grouped under metadata.everstack.scenario_id=%s — view at %s/observability/traces?metadata=everstack.scenario_id=%s",
		*scenarioID, *baseURL, *scenarioID)
	return exitOK
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func cliChatCompletion(ctx context.Context, baseURL, apiKey, model string, messages []chatMsg, scenarioID string) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   false,
		"metadata": map[string]string{
			"everstack.scenario_id": scenarioID,
			"everstack.source":      "simulation",
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upstream %d: %s", resp.StatusCode, string(rb))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rb, &m); err != nil {
		return "", err
	}
	choices, _ := m["choices"].([]interface{})
	if len(choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}
	first, _ := choices[0].(map[string]interface{})
	msg, _ := first["message"].(map[string]interface{})
	if msg == nil {
		return "", fmt.Errorf("no message in choices")
	}
	s, _ := msg["content"].(string)
	return s, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// headerRoundTripper injects auth headers on every outgoing request.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for k, v := range h.headers {
		if clone.Header.Get(k) == "" {
			clone.Header.Set(k, v)
		}
	}
	return h.base.RoundTrip(clone)
}
