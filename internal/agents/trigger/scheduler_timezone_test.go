package trigger

import (
	"testing"
	"time"
)

func TestSchedulerIsDueInTriggerTimezone(t *testing.T) {
	scheduler := NewScheduler(nil, nil, nil)
	trigger := &Trigger{CronExpression: "0 9 * * *", CronTimezone: "America/New_York"}
	if !scheduler.isDue(trigger, time.Date(2026, 1, 15, 14, 0, 5, 0, time.UTC)) {
		t.Fatal("09:00 New York trigger was not due at 14:00 UTC")
	}
	if scheduler.isDue(trigger, time.Date(2026, 1, 15, 9, 0, 5, 0, time.UTC)) {
		t.Fatal("09:00 New York trigger fired at 09:00 UTC")
	}
}

func TestNextRunWithTimezoneTracksDST(t *testing.T) {
	next, err := NextRunWithTimezone(
		"0 9 * * *",
		"Europe/Dublin",
		time.Date(2026, 3, 28, 9, 30, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestNextRunWithTimezoneRejectsInvalidZone(t *testing.T) {
	if _, err := NextRunWithTimezone("0 9 * * *", "Mars/Olympus", time.Now()); err == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestNextRunWithTimezoneRejectsImpossibleSchedule(t *testing.T) {
	if _, err := NextRunWithTimezone("0 0 31 2 *", "UTC", time.Now()); err == nil {
		t.Fatal("expected impossible schedule error")
	}
}
