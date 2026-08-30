package moderation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

const actionColumns = `id, site_id, slug, generation, action, status,
	COALESCE(reason, '') AS reason,
	COALESCE(note, '') AS note,
	requested_by, idempotency_key, attempt_count,
	COALESCE(last_error, '') AS last_error,
	COALESCE(lease_token, '') AS lease_token,
	created_at, applied_at`

type PostgresStore struct {
	db *sqlx.DB
}

func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Overview(ctx context.Context) (Overview, error) {
	var overview Overview
	err := s.db.GetContext(ctx, &overview, `
		SELECT
			COUNT(*)::bigint AS total_sites,
			COUNT(*) FILTER (
				WHERE status = 'active' AND (expires_at IS NULL OR expires_at > NOW())
			)::bigint AS active_sites,
			COUNT(*) FILTER (WHERE tenant_id IS NULL)::bigint AS anonymous_sites,
			COUNT(*) FILTER (
				WHERE expires_at > NOW() AND expires_at <= NOW() + INTERVAL '24 hours'
			)::bigint AS expiring_sites,
			COUNT(*) FILTER (WHERE kill_switch OR status = 'disabled')::bigint AS disabled_sites,
			COALESCE(SUM(total_bytes), 0)::bigint AS total_bytes,
			(SELECT COUNT(*)::bigint FROM site_abuse_reports WHERE status = 'open') AS open_reports,
			(SELECT COUNT(*)::bigint FROM site_moderation_actions WHERE status = 'pending') AS pending_actions
		FROM sites
		WHERE status <> 'deleted'`)
	return overview, err
}

func (s *PostgresStore) ListSites(ctx context.Context, options ListOptions) (SitePage, error) {
	if options.Limit <= 0 || options.Limit > 200 {
		options.Limit = 50
	}
	if options.Offset < 0 {
		options.Offset = 0
	}
	var sites []OperatorSite
	err := s.db.SelectContext(ctx, &sites, `
		SELECT
			s.id, s.slug, s.tenant_id, s.owner_user_id, s.status, s.access, s.spa_fallback,
			s.current_version, s.total_bytes, s.file_count, s.kill_switch,
			COALESCE(s.takedown_reason, '') AS takedown_reason,
			s.expires_at, s.created_at, s.last_published_at,
			(
				SELECT COUNT(*)::integer
				FROM site_abuse_reports r
				WHERE r.slug = s.slug AND r.status = 'open'
			) AS open_report_count,
			COALESCE((
				SELECT a.status
				FROM site_moderation_actions a
				WHERE a.slug = s.slug
				ORDER BY a.created_at DESC
				LIMIT 1
			), '') AS enforcement_status
		FROM sites s
		WHERE (
			$1 = '' OR s.slug ILIKE '%' || $1 || '%'
			OR COALESCE(s.tenant_id::text, '') ILIKE '%' || $1 || '%'
		)
		AND s.status <> 'deleted'
		AND ($2 = '' OR s.status = $2)
		ORDER BY s.created_at DESC
		LIMIT $3 OFFSET $4`,
		options.Search, options.Status, options.Limit+1, options.Offset,
	)
	if err != nil {
		return SitePage{}, err
	}
	page := SitePage{Sites: sites}
	if len(page.Sites) > options.Limit {
		page.Sites = page.Sites[:options.Limit]
		page.HasMore = true
		page.NextOffset = options.Offset + options.Limit
	}
	return page, nil
}

func (s *PostgresStore) ListReports(ctx context.Context, options ReportListOptions) (ReportPage, error) {
	if options.Limit <= 0 || options.Limit > 200 {
		options.Limit = 50
	}
	if options.Offset < 0 {
		options.Offset = 0
	}
	var reports []AbuseReport
	err := s.db.SelectContext(ctx, &reports, `
		SELECT
			r.id, r.slug, r.reason,
			COALESCE(r.details, '') AS details,
			COALESCE(r.page_path, '') AS page_path,
			r.status, r.created_at, r.updated_at, r.resolved_at
		FROM site_abuse_reports r
		WHERE ($1 = '' OR r.status = $1)
		AND (
			$2 = '' OR r.slug ILIKE '%' || $2 || '%'
			OR r.reason ILIKE '%' || $2 || '%'
		)
		ORDER BY r.created_at DESC
		LIMIT $3 OFFSET $4`,
		options.Status, options.Search, options.Limit+1, options.Offset,
	)
	if err != nil {
		return ReportPage{}, err
	}
	page := ReportPage{Reports: reports}
	if len(page.Reports) > options.Limit {
		page.Reports = page.Reports[:options.Limit]
		page.HasMore = true
		page.NextOffset = options.Offset + options.Limit
	}
	return page, nil
}

func (s *PostgresStore) ReviewReport(ctx context.Context, input ReportReview) (AbuseReport, error) {
	input.ReportID = strings.TrimSpace(input.ReportID)
	input.Note = strings.TrimSpace(input.Note)
	input.ActorID = strings.TrimSpace(input.ActorID)
	if err := input.Validate(); err != nil {
		return AbuseReport{}, err
	}
	var report AbuseReport
	err := s.db.GetContext(ctx, &report, `
		UPDATE site_abuse_reports
		SET status = $2,
			reviewed_by = $3,
			resolution_note = NULLIF($4, ''),
			resolved_at = NOW(),
			updated_at = NOW()
		WHERE id = $1
		RETURNING id, slug, reason,
			COALESCE(details, '') AS details,
			COALESCE(page_path, '') AS page_path,
			status, created_at, updated_at, resolved_at`,
		input.ReportID, input.Status, input.ActorID, input.Note,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AbuseReport{}, ErrSiteNotFound
	}
	return report, err
}

func (s *PostgresStore) CreateReport(ctx context.Context, report Report) error {
	var id string
	err := s.db.GetContext(ctx, &id, `
		INSERT INTO site_abuse_reports (
			site_id, slug, reporter_ip, reason, details, page_path, status, updated_at
		)
		SELECT id, slug, NULLIF($2, '')::inet, $3, NULLIF($4, ''), NULLIF($5, ''), 'open', NOW()
		FROM sites
		WHERE slug = $1 AND status NOT IN ('deleted', 'deleting')
		RETURNING id`,
		report.Slug, report.ReporterIP, string(report.Reason), report.Details, report.PagePath,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSiteNotFound
	}
	return err
}

func (s *PostgresStore) BeginAction(ctx context.Context, command Command) (Action, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Action{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		siteID     string
		generation int64
		siteStatus string
	)
	if err := tx.QueryRowxContext(ctx, `
		SELECT id, moderation_generation, status
		FROM sites
		WHERE slug = $1 AND status <> 'deleted'
		FOR UPDATE`, command.Slug).Scan(&siteID, &generation, &siteStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Action{}, ErrSiteNotFound
		}
		return Action{}, err
	}
	if siteStatus == "deleting" && command.Kind == ActionKindRestore {
		return Action{}, fmt.Errorf("%w: a deleting site cannot be restored", ErrInvalidAction)
	}
	generation++

	row := tx.QueryRowxContext(ctx, `
		INSERT INTO site_moderation_actions (
			site_id, slug, generation, action, reason, note, requested_by, idempotency_key
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING `+actionColumns,
		siteID, command.Slug, generation, string(command.Kind), string(command.Reason), command.Note,
		command.ActorID, command.IdempotencyKey,
	)
	action, err := scanAction(row)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.GetContext(ctx, &action,
			`SELECT `+actionColumns+` FROM site_moderation_actions WHERE idempotency_key = $1`,
			command.IdempotencyKey,
		); err != nil {
			return Action{}, err
		}
		if action.Slug != command.Slug || action.Kind != command.Kind || action.Reason != command.Reason ||
			action.Note != command.Note || action.ActorID != command.ActorID {
			return Action{}, fmt.Errorf("%w: idempotency key already belongs to another action", ErrInvalidAction)
		}
		if err := tx.Commit(); err != nil {
			return Action{}, err
		}
		return action, nil
	}
	if err != nil {
		return Action{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE site_moderation_actions
		SET status = 'superseded',
			last_error = 'superseded by a newer moderation action',
			updated_at = NOW()
		WHERE slug = $1 AND id <> $2 AND status = 'pending'`,
		command.Slug, action.ID,
	); err != nil {
		return Action{}, err
	}

	switch command.Kind {
	case ActionKindTakedown:
		if _, err := tx.ExecContext(ctx, `
			UPDATE sites
			SET status = CASE WHEN status = 'deleting' THEN 'deleting' ELSE 'disabled' END,
				kill_switch = TRUE, takedown_reason = NULLIF($2, ''),
				moderation_generation = $3, updated_at = NOW()
			WHERE id = $1`, siteID, string(command.Reason), generation); err != nil {
			return Action{}, err
		}
	case ActionKindRestore:
		if _, err := tx.ExecContext(ctx, `
			UPDATE sites
			SET status = CASE
				WHEN expires_at IS NOT NULL AND expires_at <= NOW() THEN 'expired'
				ELSE 'active'
			END,
			kill_switch = FALSE, takedown_reason = NULL,
			moderation_generation = $2, updated_at = NOW()
			WHERE id = $1`, siteID, generation); err != nil {
			return Action{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Action{}, err
	}
	return action, nil
}

func (s *PostgresStore) GetAction(ctx context.Context, actionID string) (Action, error) {
	row := s.db.QueryRowxContext(ctx,
		`SELECT `+actionColumns+` FROM site_moderation_actions WHERE id = $1`,
		actionID,
	)
	return scanAction(row)
}

func (s *PostgresStore) CompleteAttempt(ctx context.Context, action Action, outcome AttemptOutcome) (Action, error) {
	status := ActionStatusPending
	var appliedAt any
	if outcome.Applied {
		status = ActionStatusApplied
		appliedAt = time.Now()
	}
	row := s.db.QueryRowxContext(ctx, `
		UPDATE site_moderation_actions
		SET status = $2,
			attempt_count = attempt_count + 1,
			last_error = NULLIF($3, ''),
			applied_at = COALESCE($4, applied_at),
			lease_token = NULL,
			lease_expires_at = NULL,
			updated_at = NOW()
		WHERE id = $1 AND generation = $5 AND status = 'pending'
			AND COALESCE(lease_token, '') = $6
		RETURNING `+actionColumns,
		action.ID, string(status), outcome.Error, appliedAt, action.Generation, action.LeaseToken,
	)
	updated, err := scanAction(row)
	if !errors.Is(err, sql.ErrNoRows) {
		return updated, err
	}
	// A newer decision may have superseded this action while its edge request
	// was in flight. Return the durable state without rewriting it.
	return s.GetAction(ctx, action.ID)
}

func (s *PostgresStore) ListPending(ctx context.Context, limit int) ([]Action, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var actions []Action
	if err := s.db.SelectContext(ctx, &actions, `
		WITH candidates AS (
			SELECT a.id
			FROM site_moderation_actions a
			JOIN sites s ON s.id = a.site_id
			WHERE a.status = 'pending'
				AND a.generation = s.moderation_generation
				AND s.status <> 'deleted'
				AND (a.lease_expires_at IS NULL OR a.lease_expires_at <= NOW())
			ORDER BY a.created_at ASC
			FOR UPDATE OF a SKIP LOCKED
			LIMIT $1
		), claimed AS (
			UPDATE site_moderation_actions a
			SET lease_token = gen_random_uuid()::text,
				lease_expires_at = NOW() + INTERVAL '2 minutes',
				updated_at = NOW()
			FROM candidates c
			WHERE a.id = c.id
			RETURNING a.*
		)
		SELECT `+actionColumns+`
		FROM claimed
		ORDER BY created_at ASC`, limit); err != nil {
		return nil, err
	}
	return actions, nil
}

func (s *PostgresStore) WithProjectionLock(ctx context.Context, slug string, fn func(context.Context) error) error {
	conn, err := s.db.Connx(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, slug); err != nil {
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, slug)
	}()
	return fn(ctx)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAction(row rowScanner) (Action, error) {
	var (
		action    Action
		reason    sql.NullString
		note      sql.NullString
		lastError sql.NullString
		appliedAt sql.NullTime
	)
	err := row.Scan(
		&action.ID, &action.SiteID, &action.Slug, &action.Generation, &action.Kind, &action.Status,
		&reason, &note, &action.ActorID, &action.IdempotencyKey, &action.AttemptCount,
		&lastError, &action.LeaseToken, &action.CreatedAt, &appliedAt,
	)
	if err != nil {
		return Action{}, err
	}
	if reason.Valid {
		action.Reason = Reason(reason.String)
	}
	if note.Valid {
		action.Note = note.String
	}
	if lastError.Valid {
		action.LastError = lastError.String
	}
	if appliedAt.Valid {
		action.AppliedAt = &appliedAt.Time
	}
	return action, nil
}
