package trigger

import (
	"strings"
	"testing"
)

func TestValidateConfigurationRejectsIncompleteAndInvalidTriggers(t *testing.T) {
	tests := []struct {
		name    string
		trigger Trigger
		want    string
	}{
		{name: "cron schedule missing", trigger: Trigger{Type: TriggerCron, CronTimezone: "UTC"}, want: "cron expression is required"},
		{name: "cron schedule invalid", trigger: Trigger{Type: TriggerCron, CronExpression: "not a cron", CronTimezone: "UTC"}, want: "invalid cron expression"},
		{name: "cron timezone invalid", trigger: Trigger{Type: TriggerCron, CronExpression: "0 9 * * *", CronTimezone: "Mars/Olympus"}, want: "invalid cron timezone"},
		{name: "event source missing", trigger: Trigger{Type: TriggerEvent, EventType: "session.end"}, want: "event source agent is required"},
		{name: "event type missing", trigger: Trigger{Type: TriggerEvent, EventSourceAgentID: "agent-1"}, want: "event type is required"},
		{name: "type invalid", trigger: Trigger{Type: TriggerType("queue")}, want: "unsupported trigger type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfiguration(&tt.trigger)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateConfiguration() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateConfigurationAcceptsCompleteTriggers(t *testing.T) {
	for _, candidate := range []*Trigger{
		{Type: TriggerCron, CronExpression: "0 9 * * *", CronTimezone: "Europe/Dublin"},
		{Type: TriggerWebhook},
		{Type: TriggerEvent, EventSourceAgentID: "agent-1", EventType: "session.end"},
	} {
		if err := ValidateConfiguration(candidate); err != nil {
			t.Fatalf("ValidateConfiguration(%+v) error = %v", candidate, err)
		}
	}
}

func TestValidateCronConfigurationDefaultsTimezone(t *testing.T) {
	if err := ValidateCronConfiguration("0 9 * * *", ""); err != nil {
		t.Fatalf("ValidateCronConfiguration() error = %v", err)
	}
}
