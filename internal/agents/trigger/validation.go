package trigger

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var standardCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ValidateConfiguration rejects trigger records that the runtime cannot
// execute. Persistence historically allowed incomplete records, so callers
// must validate before creating or updating a trigger.
func ValidateConfiguration(candidate *Trigger) error {
	if candidate == nil {
		return fmt.Errorf("trigger is required")
	}
	switch candidate.Type {
	case TriggerCron:
		expression := strings.TrimSpace(candidate.CronExpression)
		if expression == "" {
			return fmt.Errorf("cron expression is required")
		}
		if _, err := standardCronParser.Parse(expression); err != nil {
			return fmt.Errorf("invalid cron expression: %w", err)
		}
		timezone := strings.TrimSpace(candidate.CronTimezone)
		if timezone == "" {
			timezone = "UTC"
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return fmt.Errorf("invalid cron timezone %q: %w", timezone, err)
		}
	case TriggerWebhook:
	case TriggerEvent:
		if strings.TrimSpace(candidate.EventSourceAgentID) == "" {
			return fmt.Errorf("event source agent is required")
		}
		if strings.TrimSpace(candidate.EventType) == "" {
			return fmt.Errorf("event type is required")
		}
	default:
		return fmt.Errorf("unsupported trigger type %q", candidate.Type)
	}
	return nil
}

// ValidateCronConfiguration exposes the runtime's exact five-field cron and
// IANA timezone validation to other input boundaries, including the CLI.
func ValidateCronConfiguration(expression, timezone string) error {
	return ValidateConfiguration(&Trigger{
		Type:           TriggerCron,
		CronExpression: expression,
		CronTimezone:   timezone,
	})
}
