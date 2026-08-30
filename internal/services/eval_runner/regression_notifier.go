package eval_runner

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/alerts"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// AlertRegressionNotifier bridges regression detection to the alert system.
type AlertRegressionNotifier struct {
	store    alerts.AlertStore
	notifier *alerts.NotificationRouter
}

// NewAlertRegressionNotifier creates a notifier that fires alert events on regressions.
func NewAlertRegressionNotifier(store alerts.AlertStore, notifier *alerts.NotificationRouter) *AlertRegressionNotifier {
	return &AlertRegressionNotifier{store: store, notifier: notifier}
}

func (n *AlertRegressionNotifier) NotifyRegression(ctx context.Context, tenantID, runID string, result *RegressionResult) {
	// Find the builtin regression rule for this tenant
	rule, err := n.store.GetAlertRuleByBuiltinKey(ctx, tenantID, "eval_regression")
	if err != nil {
		logger.WithError(err).Warn("regression notifier: failed to find builtin rule")
		return
	}
	if rule == nil || !rule.Enabled {
		return
	}

	// Count regressed scores as metric value
	regressedCount := 0
	var details []string
	for _, s := range result.Scores {
		if s.Regressed {
			regressedCount++
			details = append(details, fmt.Sprintf("%s: %.2f → %.2f (%.1f%% drop)",
				s.ScoreName, s.BaselineAvg, s.CurrentAvg, s.DeltaPercent*-100))
		}
	}

	// Fire the notification
	delivered := n.notifier.NotifyFiring(ctx, rule, float64(regressedCount))

	// Create an alert event
	event := &alerts.AlertEventRecord{
		ID:                uuid.New().String(),
		TenantID:          tenantID,
		AlertRuleID:       rule.ID,
		Status:            "firing",
		MetricValue:       float64(regressedCount),
		Threshold:         rule.Threshold,
		FiredAt:           time.Now(),
		LastNotifiedAt:    sql.NullTime{Time: time.Now(), Valid: delivered > 0},
		NotificationCount: int32(delivered),
	}
	if err := n.store.CreateAlertEvent(ctx, event); err != nil {
		logger.WithError(err).Warn("regression notifier: failed to create alert event")
	}

	logger.WithFields(
		"run_id", runID,
		"baseline_run_id", result.BaselineRunID,
		"regressed_scores", strings.Join(details, "; "),
		"delivered", delivered,
	).Warn("regression alert fired")
}
