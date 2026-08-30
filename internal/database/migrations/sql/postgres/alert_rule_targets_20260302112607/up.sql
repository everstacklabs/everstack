CREATE TABLE IF NOT EXISTS alert_rule_targets (
    alert_rule_id UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    target_id UUID NOT NULL REFERENCES alert_notification_targets(id) ON DELETE CASCADE,
    PRIMARY KEY (alert_rule_id, target_id)
);

CREATE INDEX IF NOT EXISTS idx_alert_rule_targets_rule ON alert_rule_targets(alert_rule_id);
CREATE INDEX IF NOT EXISTS idx_alert_rule_targets_target ON alert_rule_targets(target_id);
