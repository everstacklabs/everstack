package trigger

import (
	"context"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
	"github.com/robfig/cron/v3"
)

const (
	schedulerInterval = 10 * time.Second
	schedulerTimeout  = 30 * time.Second
)

// Scheduler periodically checks for due cron triggers and fires them.
// Uses PostgreSQL advisory locks for single-leader election in multi-instance deployments.
type Scheduler struct {
	store    Store
	executor *Executor
	db       *sqlx.DB
	parser   cron.Parser
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewScheduler creates a new trigger scheduler.
func NewScheduler(store Store, executor *Executor, db *sqlx.DB) *Scheduler {
	return &Scheduler{
		store:    store,
		executor: executor,
		db:       db,
		parser:   cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		done:     make(chan struct{}),
	}
}

// Start begins the scheduler loop. Call this in a goroutine.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	defer close(s.done)

	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()

	logger.Info("trigger_scheduler: started")

	for {
		select {
		case <-ctx.Done():
			logger.Info("trigger_scheduler: stopped")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// Stop shuts down the scheduler and waits for it to finish.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	<-s.done
}

func (s *Scheduler) tick(ctx context.Context) {
	// Try to acquire advisory lock for single-leader election
	var locked bool
	_ = s.db.GetContext(ctx, &locked, `SELECT pg_try_advisory_lock(hashtext('agent_trigger_scheduler'))`)
	if !locked {
		return
	}
	defer s.db.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('agent_trigger_scheduler'))`)

	tickCtx, cancel := context.WithTimeout(ctx, schedulerTimeout)
	defer cancel()

	triggers, err := s.store.ListEnabledCronTriggers(tickCtx)
	if err != nil {
		logger.WithFields("error", err.Error()).Debug("trigger_scheduler: failed to list cron triggers")
		return
	}

	now := time.Now()
	for _, t := range triggers {
		if s.isDue(t, now) {
			logger.WithFields("trigger_id", t.ID, "name", t.Name, "cron", t.CronExpression).
				Info("trigger_scheduler: firing cron trigger")
			go s.executor.Execute(tickCtx, t, nil)
		}
	}
}

// isDue checks whether a cron trigger should fire based on the current time.
// A trigger is due if the current time falls within the same minute window as
// the cron expression's most recent scheduled time. We check if the next-run
// (from 11 seconds ago) is in the past-or-now window.
func (s *Scheduler) isDue(t *Trigger, now time.Time) bool {
	if t.CronExpression == "" {
		return false
	}

	sched, err := s.parser.Parse(t.CronExpression)
	if err != nil {
		logger.WithFields("trigger_id", t.ID, "error", err.Error()).Debug("trigger_scheduler: invalid cron expression")
		return false
	}
	timezone := t.CronTimezone
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		logger.WithFields("trigger_id", t.ID, "error", err.Error()).Debug("trigger_scheduler: invalid cron timezone")
		return false
	}

	// Check if the cron expression says we should run within the scheduler window
	// (10s interval + 1s buffer to avoid missing a tick)
	checkFrom := now.In(location).Add(-(schedulerInterval + time.Second))
	nextRun := sched.Next(checkFrom)
	if nextRun.IsZero() {
		return false
	}
	return !nextRun.After(now.In(location))
}

// NextRun returns the next scheduled run time for a cron expression.
func NextRun(expression string) (*time.Time, error) {
	return NextRunWithTimezone(expression, "UTC", time.Now())
}

// NextRunWithTimezone returns the next UTC instant for a five-field cron in
// an IANA timezone.
func NextRunWithTimezone(expression, timezone string, from time.Time) (*time.Time, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("parse cron: %w", err)
	}
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load cron timezone: %w", err)
	}
	next := sched.Next(from.In(location)).UTC()
	if next.IsZero() {
		return nil, fmt.Errorf("cron has no future run time")
	}
	return &next, nil
}
