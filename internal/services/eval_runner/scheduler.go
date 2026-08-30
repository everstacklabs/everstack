package eval_runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/robfig/cron/v3"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Scheduler polls eval_schedules and creates eval runs when due.
type Scheduler struct {
	db           *sqlx.DB
	pollInterval time.Duration
	parser       cron.Parser
	leaderKey    string
}

// StartScheduler creates and starts the eval scheduler polling loop.
func StartScheduler(ctx context.Context, db *sqlx.DB) *Scheduler {
	s := &Scheduler{
		db:           db,
		pollInterval: 30 * time.Second,
		parser:       cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		leaderKey:    "eval-runner-scheduler",
	}
	go s.run(ctx)
	logger.Info("eval scheduler started")
	return s
}

func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	// Run once immediately on startup
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

type scheduleRow struct {
	ID              string         `db:"id"`
	TenantID        string         `db:"tenant_id"`
	Name            string         `db:"name"`
	DatasetID       string         `db:"dataset_id"`
	EvalTargetType  string         `db:"eval_target_type"`
	EvalTargetID    string         `db:"eval_target_id"`
	EvalConfig      []byte         `db:"eval_config"`
	ScorerConfigIDs pq.StringArray `db:"scorer_config_ids"`
	CronExpression  string         `db:"cron_expression"`
	Timezone        string         `db:"timezone"`
}

func (s *Scheduler) tick(ctx context.Context) {
	lockConn, locked := s.acquireLeaderLock(ctx)
	if !locked {
		return
	}
	defer s.releaseLeaderLock(context.Background(), lockConn)

	now := time.Now().UTC()

	var schedules []scheduleRow
	err := s.db.SelectContext(ctx, &schedules, `
		SELECT id, tenant_id, name, dataset_id, eval_target_type, eval_target_id,
			eval_config, scorer_config_ids, cron_expression, timezone
		FROM eval_schedules
		WHERE enabled = TRUE AND (next_run_at IS NULL OR next_run_at <= $1)
	`, now)
	if err != nil {
		logger.WithError(err).Error("scheduler: list due schedules")
		return
	}

	for _, sched := range schedules {
		if err := s.executeSchedule(ctx, sched, now); err != nil {
			logger.WithError(err).Error("scheduler: execute schedule ", sched.ID)
		}
	}
}

func (s *Scheduler) acquireLeaderLock(ctx context.Context) (*sqlx.Conn, bool) {
	conn, err := s.db.Connx(ctx)
	if err != nil {
		logger.WithError(err).Warn("scheduler: open leader lock connection")
		return nil, false
	}
	var locked bool
	if err := conn.GetContext(ctx, &locked, `SELECT pg_try_advisory_lock(hashtext($1))`, s.leaderKey); err != nil {
		_ = conn.Close()
		logger.WithError(err).Warn("scheduler: acquire leader lock")
		return nil, false
	}
	if !locked {
		_ = conn.Close()
		return nil, false
	}
	return conn, true
}

func (s *Scheduler) releaseLeaderLock(ctx context.Context, conn *sqlx.Conn) {
	if conn == nil {
		return
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, s.leaderKey); err != nil {
		logger.WithError(err).Warn("scheduler: release leader lock")
	}
}

func (s *Scheduler) executeSchedule(ctx context.Context, sched scheduleRow, now time.Time) error {
	// Create an eval run
	runID := uuid.New().String()
	runName := fmt.Sprintf("%s — %s", sched.Name, now.Format("2006-01-02 15:04"))

	var evalConfig json.RawMessage = []byte("{}")
	if len(sched.EvalConfig) > 0 {
		evalConfig = sched.EvalConfig
	}
	datasetVersionID := datasetVersionIDFromEvalConfig(evalConfig)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO eval_runs (id, tenant_id, dataset_id, name, status,
			eval_target_type, eval_target_id, eval_config, scorer_config_ids, dataset_version_id,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7, $8, NULLIF($9, ''), NOW(), NOW())
	`, runID, sched.TenantID, sched.DatasetID, runName,
		sched.EvalTargetType, sched.EvalTargetID, evalConfig, sched.ScorerConfigIDs, datasetVersionID)
	if err != nil {
		return fmt.Errorf("create eval run: %w", err)
	}

	// Compute next run time
	nextRun, err := s.computeNextRun(sched.CronExpression, sched.Timezone, now)
	if err != nil {
		logger.WithError(err).Warn("scheduler: bad cron for schedule ", sched.ID)
		// Disable the schedule if cron is invalid
		s.db.ExecContext(ctx, `UPDATE eval_schedules SET enabled = FALSE, updated_at = NOW() WHERE id = $1`, sched.ID)
		return nil
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE eval_schedules
		SET last_run_at = $2, next_run_at = $3, updated_at = NOW()
		WHERE id = $1
	`, sched.ID, now, nextRun)

	logger.Info("scheduler: created eval run ", runID, " from schedule ", sched.ID)
	return err
}

func datasetVersionIDFromEvalConfig(evalConfig []byte) string {
	var cfg map[string]interface{}
	if len(evalConfig) == 0 || json.Unmarshal(evalConfig, &cfg) != nil {
		return ""
	}
	if value, ok := cfg["dataset_version_id"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func (s *Scheduler) computeNextRun(cronExpr, tz string, from time.Time) (time.Time, error) {
	schedule, err := s.parser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", cronExpr, err)
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}

	return schedule.Next(from.In(loc)).UTC(), nil
}

// --- Schedule CRUD ---

type EvalScheduleRecord struct {
	ID              string         `db:"id" json:"id"`
	TenantID        string         `db:"tenant_id" json:"tenant_id"`
	Name            string         `db:"name" json:"name"`
	Description     string         `db:"description" json:"description"`
	DatasetID       string         `db:"dataset_id" json:"dataset_id"`
	EvalTargetType  string         `db:"eval_target_type" json:"eval_target_type"`
	EvalTargetID    string         `db:"eval_target_id" json:"eval_target_id"`
	EvalConfig      []byte         `db:"eval_config" json:"eval_config"`
	ScorerConfigIDs pq.StringArray `db:"scorer_config_ids" json:"scorer_config_ids"`
	CronExpression  string         `db:"cron_expression" json:"cron_expression"`
	Timezone        string         `db:"timezone" json:"timezone"`
	Enabled         bool           `db:"enabled" json:"enabled"`
	LastRunAt       sql.NullString `db:"last_run_at" json:"last_run_at"`
	NextRunAt       sql.NullString `db:"next_run_at" json:"next_run_at"`
	CreatedAt       string         `db:"created_at" json:"created_at"`
	UpdatedAt       string         `db:"updated_at" json:"updated_at"`
}

func CreateSchedule(ctx context.Context, db *sqlx.DB, rec *EvalScheduleRecord) error {
	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	if rec.Timezone == "" {
		rec.Timezone = "UTC"
	}

	// Validate cron expression
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(rec.CronExpression)
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", rec.CronExpression, err)
	}

	loc, _ := time.LoadLocation(rec.Timezone)
	if loc == nil {
		loc = time.UTC
	}
	nextRun := schedule.Next(time.Now().In(loc)).UTC()

	var evalConfig json.RawMessage = []byte("{}")
	if len(rec.EvalConfig) > 0 {
		evalConfig = rec.EvalConfig
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO eval_schedules (id, tenant_id, name, description, dataset_id,
			eval_target_type, eval_target_id, eval_config, scorer_config_ids,
			cron_expression, timezone, enabled, next_run_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, TRUE, $12, NOW(), NOW())
	`, rec.ID, rec.TenantID, rec.Name, rec.Description, rec.DatasetID,
		rec.EvalTargetType, rec.EvalTargetID, evalConfig, rec.ScorerConfigIDs,
		rec.CronExpression, rec.Timezone, nextRun)
	return err
}

func GetSchedule(ctx context.Context, db *sqlx.DB, id, tenantID string) (*EvalScheduleRecord, error) {
	var rec EvalScheduleRecord
	err := db.GetContext(ctx, &rec, `
		SELECT * FROM eval_schedules WHERE id = $1 AND tenant_id = $2
	`, id, tenantID)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func ListSchedules(ctx context.Context, db *sqlx.DB, tenantID, datasetID string, limit, offset int) ([]EvalScheduleRecord, int, error) {
	q := `SELECT * FROM eval_schedules WHERE tenant_id = $1`
	countQ := `SELECT COUNT(*) FROM eval_schedules WHERE tenant_id = $1`
	args := []interface{}{tenantID}

	if datasetID != "" {
		q += ` AND dataset_id = $2`
		countQ += ` AND dataset_id = $2`
		args = append(args, datasetID)
	}

	var total int
	if err := db.GetContext(ctx, &total, countQ, args...); err != nil {
		return nil, 0, err
	}

	q += ` ORDER BY created_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d OFFSET %d`, limit, offset)
	}

	var records []EvalScheduleRecord
	if err := db.SelectContext(ctx, &records, q, args...); err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func UpdateSchedule(ctx context.Context, db *sqlx.DB, id, tenantID string, updates map[string]interface{}) error {
	// Re-compute next_run_at if cron or timezone changed
	if cronExpr, ok := updates["cron_expression"]; ok {
		tz := "UTC"
		if tzVal, ok := updates["timezone"]; ok {
			tz = tzVal.(string)
		} else {
			// Fetch current timezone
			var currentTZ string
			db.GetContext(ctx, &currentTZ, `SELECT timezone FROM eval_schedules WHERE id = $1`, id)
			if currentTZ != "" {
				tz = currentTZ
			}
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := parser.Parse(cronExpr.(string))
		if err != nil {
			return fmt.Errorf("invalid cron expression: %w", err)
		}
		loc, _ := time.LoadLocation(tz)
		if loc == nil {
			loc = time.UTC
		}
		updates["next_run_at"] = schedule.Next(time.Now().In(loc)).UTC()
	}

	setClauses := "updated_at = NOW()"
	args := []interface{}{id, tenantID}
	i := 3
	for k, v := range updates {
		setClauses += fmt.Sprintf(", %s = $%d", k, i)
		args = append(args, v)
		i++
	}

	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE eval_schedules SET %s WHERE id = $1 AND tenant_id = $2
	`, setClauses), args...)
	return err
}

func DeleteSchedule(ctx context.Context, db *sqlx.DB, id, tenantID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM eval_schedules WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}
