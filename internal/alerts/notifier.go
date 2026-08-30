package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/channels"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Severity → color mapping (hex RGB as int for embeds).
const (
	colorCritical = 0xDC2626 // red-600
	colorWarning  = 0xF59E0B // amber-500
	colorInfo     = 0x3B82F6 // blue-500
	colorResolved = 0x22C55E // green-500
)

// ChannelNotifier sends rich OutboundMessages to a channel connector.
type ChannelNotifier interface {
	SendRichNotification(ctx context.Context, channelConfigID, channelRef string, msg channels.OutboundMessage) error
}

// NotificationRouter dispatches alert notifications to the appropriate target.
type NotificationRouter struct {
	store        AlertStore
	channelMgr   ChannelNotifier
	httpClient   *http.Client
	dashboardURL string // optional base URL for deep-linking (e.g. "https://app.everstack.dev")
}

// NewNotificationRouter creates a new NotificationRouter.
func NewNotificationRouter(store AlertStore, channelMgr ChannelNotifier, dashboardURL string) *NotificationRouter {
	return &NotificationRouter{
		store:        store,
		channelMgr:   channelMgr,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		dashboardURL: strings.TrimRight(dashboardURL, "/"),
	}
}

// alertURL returns a deep-link to the alerts page, or empty if no dashboard URL is configured.
func (n *NotificationRouter) alertURL(ruleID string) string {
	if n.dashboardURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/observability/alerts?rule=%s", n.dashboardURL, ruleID)
}

// NotifyFiring sends a "firing" notification for a rule to all linked targets.
// Returns the number of targets that were successfully notified.
func (n *NotificationRouter) NotifyFiring(ctx context.Context, rule *AlertRuleRecord, metricValue float64) int {
	targets, err := n.store.GetTargetsForRule(ctx, rule.ID)
	if err != nil {
		logger.WithError(err).Warn("alerts: failed to get targets for rule")
		return 0
	}

	if len(targets) == 0 {
		logger.WithFields("rule", rule.Name, "rule_id", rule.ID).
			Warn("alerts: rule has no notification targets linked — add targets in Alerts → Targets and link them to this rule")
		return 0
	}

	severity := rule.Severity
	delivered := 0

	for _, target := range targets {
		if !severityMeetsMin(severity, target.MinSeverity) {
			logger.WithFields("rule", rule.Name, "target", target.Name, "rule_severity", severity, "target_min_severity", target.MinSeverity).
				Debug("alerts: skipping target — rule severity below target minimum")
			continue
		}
		if err := n.sendFiring(ctx, target, rule, metricValue); err != nil {
			logger.WithFields("rule", rule.Name, "target", target.Name, "target_type", target.TargetType).
				WithError(err).Warn("alerts: failed to deliver notification")
		} else {
			delivered++
		}
	}
	return delivered
}

// NotifyResolved sends a "resolved" notification for a rule.
func (n *NotificationRouter) NotifyResolved(ctx context.Context, rule *AlertRuleRecord) {
	targets, err := n.store.GetTargetsForRule(ctx, rule.ID)
	if err != nil {
		logger.WithError(err).Warn("alerts: failed to get targets for resolution")
		return
	}

	if len(targets) == 0 {
		return
	}

	for _, target := range targets {
		if err := n.sendResolved(ctx, target, rule); err != nil {
			logger.WithFields("rule", rule.Name, "target", target.Name, "target_type", target.TargetType).
				WithError(err).Warn("alerts: failed to deliver resolution notification")
		}
	}
}

// SendTestNotification sends a test message to a single target.
func (n *NotificationRouter) SendTestNotification(ctx context.Context, target *NotificationTargetRecord) error {
	switch target.TargetType {
	case "channel":
		return n.sendViaChannel(ctx, target, channels.OutboundMessage{
			Text: "Everstack Alert — Test Notification",
			Embeds: []channels.Embed{{
				Color:       colorInfo,
				Description: "🔔 *Test Notification*\n\nThis target is configured correctly and receiving alerts.",
			}},
		})
	case "webhook":
		return n.sendViaWebhook(ctx, target, "Everstack Alert — Test Notification: this target is working correctly.")
	case "email":
		logger.Info("alerts: email notifications not yet implemented")
		return nil
	default:
		return fmt.Errorf("unknown target type: %s", target.TargetType)
	}
}

// ── Firing ──────────────────────────────────────────────────────────

func (n *NotificationRouter) sendFiring(ctx context.Context, target *NotificationTargetRecord, rule *AlertRuleRecord, metricValue float64) error {
	switch target.TargetType {
	case "channel":
		return n.sendViaChannel(ctx, target, n.buildFiringMessage(rule, metricValue))
	case "webhook":
		return n.sendViaWebhook(ctx, target, formatFiringPlaintext(rule, metricValue))
	case "email":
		logger.Info("alerts: email notifications not yet implemented")
		return nil
	default:
		return fmt.Errorf("unknown target type: %s", target.TargetType)
	}
}

func (n *NotificationRouter) sendResolved(ctx context.Context, target *NotificationTargetRecord, rule *AlertRuleRecord) error {
	switch target.TargetType {
	case "channel":
		return n.sendViaChannel(ctx, target, n.buildResolvedMessage(rule))
	case "webhook":
		return n.sendViaWebhook(ctx, target, fmt.Sprintf("Resolved: %s — the condition is no longer breaching the threshold.", rule.Name))
	case "email":
		logger.Info("alerts: email notifications not yet implemented")
		return nil
	default:
		return fmt.Errorf("unknown target type: %s", target.TargetType)
	}
}

// ── Channel delivery ────────────────────────────────────────────────

func (n *NotificationRouter) sendViaChannel(ctx context.Context, target *NotificationTargetRecord, msg channels.OutboundMessage) error {
	if n.channelMgr == nil {
		return fmt.Errorf("channel manager not available — channels service may not be initialized")
	}
	if !target.ChannelConfigID.Valid {
		return fmt.Errorf("channel config ID not set on target %q (%s) — edit the target and select a channel integration", target.Name, target.ID)
	}
	if !target.PlatformChannelRef.Valid || target.PlatformChannelRef.String == "" {
		return fmt.Errorf("platform channel/room not set on target %q — edit the target and select a channel", target.Name)
	}

	logger.WithFields("target", target.Name, "channel_config_id", target.ChannelConfigID.String, "channel_ref", target.PlatformChannelRef.String).
		Debug("alerts: sending channel notification")

	err := n.channelMgr.SendRichNotification(ctx, target.ChannelConfigID.String, target.PlatformChannelRef.String, msg)
	if err != nil {
		logger.WithFields("target", target.Name, "channel_config_id", target.ChannelConfigID.String, "channel_ref", target.PlatformChannelRef.String).WithError(err).
			Warn("alerts: failed to send channel notification — verify the bot has Send Messages permission in this channel")
		return err
	}
	return nil
}

// ── Webhook delivery ────────────────────────────────────────────────

func (n *NotificationRouter) sendViaWebhook(ctx context.Context, target *NotificationTargetRecord, message string) error {
	if !target.WebhookURL.Valid {
		return fmt.Errorf("webhook URL not set on target %s", target.ID)
	}

	payload, _ := json.Marshal(map[string]string{
		"text":    message,
		"message": message,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.WebhookURL.String, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if len(target.WebhookHeaders) > 0 {
		var headers map[string]string
		if json.Unmarshal(target.WebhookHeaders, &headers) == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
	}

	resp, err := n.httpClient.Do(req)
	if err != nil {
		logger.WithFields("target", target.Name).WithError(err).Warn("alerts: webhook delivery failed")
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// ── Message builders ────────────────────────────────────────────────

func (n *NotificationRouter) buildFiringMessage(rule *AlertRuleRecord, metricValue float64) channels.OutboundMessage {
	color := severityColor(rule.Severity)
	emoji := severityEmoji(rule.Severity)

	var b strings.Builder

	// Title line
	fmt.Fprintf(&b, "%s *Alert triggered: %s*\n\n", emoji, rule.Name)

	// Metric sentence — factual, operator-focused
	fmt.Fprintf(&b, "The value of `%s` is *%.2f*, which %s the threshold of *%.2f* over *%s*.\n",
		rule.Metric, metricValue, operatorVerb(rule.Operator), rule.Threshold, formatDuration(rule.DurationSeconds))

	// Tags line — compact key:value pairs from filters + severity + category
	if tags := buildTags(rule); tags != "" {
		fmt.Fprintf(&b, "\n%s", tags)
	}

	// Deep link
	if link := n.alertURL(rule.ID); link != "" {
		fmt.Fprintf(&b, "\n\n<%s|View in Dashboard>", link)
	}

	return channels.OutboundMessage{
		Text: fmt.Sprintf("%s Alert triggered: %s", emoji, rule.Name),
		Embeds: []channels.Embed{{
			Color:       color,
			Description: b.String(),
		}},
	}
}

func (n *NotificationRouter) buildResolvedMessage(rule *AlertRuleRecord) channels.OutboundMessage {
	var b strings.Builder

	fmt.Fprintf(&b, "✅ *Alert recovered: %s*\n\n", rule.Name)
	fmt.Fprintf(&b, "The value of `%s` is no longer breaching the threshold of *%.2f*.\n",
		rule.Metric, rule.Threshold)

	if tags := buildTags(rule); tags != "" {
		fmt.Fprintf(&b, "\n%s", tags)
	}

	if link := n.alertURL(rule.ID); link != "" {
		fmt.Fprintf(&b, "\n\n<%s|View in Dashboard>", link)
	}

	return channels.OutboundMessage{
		Text: fmt.Sprintf("✅ Alert recovered: %s", rule.Name),
		Embeds: []channels.Embed{{
			Color:       colorResolved,
			Description: b.String(),
		}},
	}
}

// buildTags extracts scope tags from rule filters + metadata into a compact tag line.
// Output: `severity:warning  category:performance  model:gpt-4  provider:openai  env:production`
func buildTags(rule *AlertRuleRecord) string {
	var tags []string
	tags = append(tags, "`severity:"+rule.Severity+"`")
	if rule.Category != "" {
		tags = append(tags, "`category:"+rule.Category+"`")
	}

	// Extract dimension filters (model, provider, environment, user_id, session_id)
	if len(rule.Filters) > 0 {
		var filters map[string]interface{}
		if json.Unmarshal(rule.Filters, &filters) == nil {
			for _, key := range []string{"model", "provider", "environment", "user_id", "session_id"} {
				if v, ok := filters[key].(string); ok && v != "" {
					tag := key
					if key == "environment" {
						tag = "env"
					}
					tags = append(tags, "`"+tag+":"+v+"`")
				}
			}
		}
	}

	return strings.Join(tags, "  ")
}

// operatorVerb returns a human-readable verb for the threshold comparison.
func operatorVerb(op string) string {
	switch op {
	case ">":
		return "is above"
	case ">=":
		return "is at or above"
	case "<":
		return "is below"
	case "<=":
		return "is at or below"
	default:
		return "breaches"
	}
}

// formatFiringPlaintext returns a plain-text fallback for webhooks.
func formatFiringPlaintext(rule *AlertRuleRecord, metricValue float64) string {
	return fmt.Sprintf(
		"Alert triggered: %s — %s is %.2f, which %s the threshold of %.2f over %s [severity:%s category:%s]",
		rule.Name, rule.Metric, metricValue, operatorVerb(rule.Operator), rule.Threshold,
		formatDuration(rule.DurationSeconds), rule.Severity, rule.Category,
	)
}

func severityColor(severity string) int {
	switch severity {
	case "critical":
		return colorCritical
	case "warning":
		return colorWarning
	default:
		return colorInfo
	}
}

func severityEmoji(severity string) string {
	switch severity {
	case "critical":
		return "🚨"
	case "warning":
		return "⚠️"
	default:
		return "🔵"
	}
}

func formatDuration(seconds int32) string {
	if seconds >= 3600 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}

// severityMeetsMin checks if the alert severity is at least as severe as the target's minimum.
func severityMeetsMin(alertSeverity, minSeverity string) bool {
	order := map[string]int{"critical": 3, "warning": 2, "info": 1}
	return order[alertSeverity] >= order[minSeverity]
}
