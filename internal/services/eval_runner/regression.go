package eval_runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// RegressionThreshold is the minimum score drop (as a fraction) to flag a regression.
// A 5% drop means: if baseline avg is 0.90 and current avg is 0.84, that's a 6.7% drop → regression.
const RegressionThreshold = 0.05

// ScoreRegression captures a single score that regressed relative to the baseline.
type ScoreRegression struct {
	ScoreName     string  `json:"score_name"`
	BaselineAvg   float64 `json:"baseline_avg"`
	CurrentAvg    float64 `json:"current_avg"`
	Delta         float64 `json:"delta"`          // current - baseline (negative = worse)
	DeltaPercent  float64 `json:"delta_percent"`   // relative change as fraction
	Regressed     bool    `json:"regressed"`
}

// RegressionResult is the full comparison between the current run and its baseline.
type RegressionResult struct {
	BaselineRunID string            `json:"baseline_run_id"`
	HasRegression bool              `json:"has_regression"`
	Scores        []ScoreRegression `json:"scores"`
}

// CheckRegression compares a completed run against the baseline for the same
// dataset + target combination. Returns nil if no baseline exists.
func CheckRegression(ctx context.Context, db *sqlx.DB, runID string) (*RegressionResult, error) {
	// Load the completed run
	var run struct {
		TenantID       string `db:"tenant_id"`
		DatasetID      string `db:"dataset_id"`
		EvalTargetType string `db:"eval_target_type"`
		EvalTargetID   string `db:"eval_target_id"`
		ScoreSummary   []byte `db:"score_summary"`
	}
	err := db.GetContext(ctx, &run, `
		SELECT tenant_id, dataset_id, eval_target_type, eval_target_id, score_summary
		FROM eval_runs WHERE id = $1
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}

	// Find the baseline for this dataset+target combo
	var baselineID string
	var baselineSummary []byte
	err = db.QueryRowContext(ctx, `
		SELECT id, score_summary FROM eval_runs
		WHERE tenant_id = $1
			AND dataset_id = $2
			AND eval_target_type = $3
			AND eval_target_id = $4
			AND is_baseline = TRUE
			AND id != $5
		LIMIT 1
	`, run.TenantID, run.DatasetID, run.EvalTargetType, run.EvalTargetID, runID).Scan(&baselineID, &baselineSummary)
	if err == sql.ErrNoRows {
		return nil, nil // no baseline — nothing to compare
	}
	if err != nil {
		return nil, fmt.Errorf("find baseline: %w", err)
	}

	// Parse score summaries
	baselineScores := parseScoreSummary(baselineSummary)
	currentScores := parseScoreSummary(run.ScoreSummary)

	if len(baselineScores) == 0 || len(currentScores) == 0 {
		return nil, nil
	}

	result := &RegressionResult{
		BaselineRunID: baselineID,
	}

	for name, baselineAvg := range baselineScores {
		currentAvg, ok := currentScores[name]
		if !ok {
			continue
		}

		delta := currentAvg - baselineAvg
		var deltaPct float64
		if baselineAvg != 0 {
			deltaPct = delta / math.Abs(baselineAvg)
		}

		regressed := deltaPct < -RegressionThreshold

		result.Scores = append(result.Scores, ScoreRegression{
			ScoreName:    name,
			BaselineAvg:  baselineAvg,
			CurrentAvg:   currentAvg,
			Delta:        delta,
			DeltaPercent: deltaPct,
			Regressed:    regressed,
		})

		if regressed {
			result.HasRegression = true
		}
	}

	return result, nil
}

// StoreRegressionResult persists the regression result on the eval run and links it to the baseline.
func StoreRegressionResult(ctx context.Context, db *sqlx.DB, runID string, result *RegressionResult) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, `
		UPDATE eval_runs
		SET regression_result = $2, baseline_run_id = $3, updated_at = NOW()
		WHERE id = $1
	`, runID, resultJSON, result.BaselineRunID)
	return err
}

// SetBaseline marks a run as the baseline and clears any previous baseline
// for the same dataset+target combination.
func SetBaseline(ctx context.Context, db *sqlx.DB, tenantID, runID string) error {
	// Get the run's dataset+target info
	var run struct {
		DatasetID      string `db:"dataset_id"`
		EvalTargetType string `db:"eval_target_type"`
		EvalTargetID   string `db:"eval_target_id"`
		Status         string `db:"status"`
	}
	err := db.GetContext(ctx, &run, `
		SELECT dataset_id, eval_target_type, eval_target_id, status
		FROM eval_runs WHERE id = $1 AND tenant_id = $2
	`, runID, tenantID)
	if err != nil {
		return fmt.Errorf("run not found: %w", err)
	}
	if run.Status != "completed" {
		return fmt.Errorf("only completed runs can be set as baseline (status: %s)", run.Status)
	}

	// Clear previous baseline for this combo
	_, err = db.ExecContext(ctx, `
		UPDATE eval_runs SET is_baseline = FALSE, updated_at = NOW()
		WHERE tenant_id = $1
			AND dataset_id = $2
			AND eval_target_type = $3
			AND eval_target_id = $4
			AND is_baseline = TRUE
	`, tenantID, run.DatasetID, run.EvalTargetType, run.EvalTargetID)
	if err != nil {
		return fmt.Errorf("clear previous baseline: %w", err)
	}

	// Set this run as baseline
	_, err = db.ExecContext(ctx, `
		UPDATE eval_runs SET is_baseline = TRUE, updated_at = NOW()
		WHERE id = $1
	`, runID)
	return err
}

// parseScoreSummary extracts score_name → avgScore from the score_summary JSONB.
// Expected format: {"scores": [{"name": "foo", "avgScore": 0.95, ...}, ...]}
func parseScoreSummary(data []byte) map[string]float64 {
	if len(data) == 0 {
		return nil
	}
	var summary struct {
		Scores []struct {
			Name     string  `json:"name"`
			AvgScore float64 `json:"avgScore"`
		} `json:"scores"`
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		logger.WithError(err).Warn("failed to parse score summary for regression check")
		return nil
	}
	result := make(map[string]float64, len(summary.Scores))
	for _, s := range summary.Scores {
		result[s.Name] = s.AvgScore
	}
	return result
}
