// Package bots implements tenant-scoped personal bot connections.
package bots

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

var (
	ErrNoTenant  = errors.New("tenant_required")
	ErrNotFound  = errors.New("not_found")
	ErrNameTaken = errors.New("name_taken")
)

type Usage struct {
	Calls30d  int64            `json:"calls_30d"`
	BySurface map[string]int64 `json:"by_surface"`
}

func emptyUsage() Usage {
	return Usage{BySurface: map[string]int64{"inference": 0, "mcp": 0, "telemetry": 0, "a2a": 0}}
}

type Bot struct {
	RateLimitRPM *int           `db:"rate_limit_rpm" json:"rate_limit_rpm"`
	Usage        Usage          `db:"-" json:"usage"`
	ID           string         `db:"id" json:"id"`
	TenantID     string         `db:"tenant_id" json:"-"`
	Name         string         `db:"name" json:"name"`
	Kind         string         `db:"kind" json:"kind"`
	APIKeyID     string         `db:"api_key_id" json:"api_key_id"`
	Surfaces     pq.StringArray `db:"surfaces" json:"surfaces"`
	KeyHint      string         `db:"key_hint" json:"key_hint"`
	RemoteAgent  *string        `db:"remote_agent" json:"remote_agent"`
	CreatedBy    *string        `db:"created_by" json:"-"`
	CreatedAt    time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time      `db:"updated_at" json:"-"`
	LastSeenAt   *time.Time     `db:"last_seen_at" json:"last_seen_at"`
}

type Repository interface {
	List(context.Context, string) ([]Bot, error)
	Get(context.Context, string, string) (*Bot, error)
	Insert(context.Context, *Bot) error
	Update(context.Context, *Bot) error
	Delete(context.Context, string, string, time.Time) error
	Lock(context.Context, string, string) (func(), error)
}

type Store struct {
	db           *sqlx.DB
	mutationGate chan struct{}
}

func NewStore(db *sqlx.DB) *Store { return &Store{db: db, mutationGate: make(chan struct{}, 1)} }

const botSelect = `SELECT b.id,b.tenant_id,b.name,b.kind,b.api_key_id,b.remote_agent,b.created_by,b.created_at,b.updated_at,k.last_used_at AS last_seen_at,k.rate_limit_rpm,
 COALESCE(k.surfaces,'{}'::text[]) AS surfaces,COALESCE(k.sensitive_id,'') AS key_hint
 FROM bot_connections b LEFT JOIN api_keys k ON k.id::text=b.api_key_id AND k.org_id=b.tenant_id`

func (s *Store) List(ctx context.Context, tenant string) ([]Bot, error) {
	if tenant == "" {
		return nil, ErrNoTenant
	}
	out := []Bot{}
	err := s.db.SelectContext(ctx, &out, botSelect+` WHERE b.tenant_id=$1 ORDER BY b.created_at,b.id`, tenant)
	if err == nil {
		err = s.loadUsage(ctx, tenant, out)
	}
	return out, err
}
func (s *Store) Get(ctx context.Context, tenant, id string) (*Bot, error) {
	if tenant == "" {
		return nil, ErrNoTenant
	}
	var out Bot
	// Text comparison also makes malformed UUIDs indistinguishable from missing IDs.
	err := s.db.GetContext(ctx, &out, botSelect+` WHERE b.tenant_id=$1 AND b.id::text=$2`, tenant, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err == nil {
		list := []Bot{out}
		err = s.loadUsage(ctx, tenant, list)
		out = list[0]
	}
	return &out, err
}
func (s *Store) Insert(ctx context.Context, b *Bot) error {
	if b.TenantID == "" {
		return ErrNoTenant
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `INSERT INTO bot_connections (tenant_id,name,kind,api_key_id,remote_agent,created_by)
 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id,created_at,updated_at`, b.TenantID, b.Name, b.Kind, b.APIKeyID, b.RemoteAgent, b.CreatedBy).Scan(&b.ID, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return storeError(err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE api_keys SET rate_limit_rpm=$3 WHERE org_id=$1 AND id::text=$2`, b.TenantID, b.APIKeyID, b.RateLimitRPM)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}
func (s *Store) Update(ctx context.Context, b *Bot) error {
	if b.TenantID == "" {
		return ErrNoTenant
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var updatedAt time.Time
	err = tx.QueryRowContext(ctx, `UPDATE bot_connections SET name=$3,api_key_id=$4,remote_agent=$5,
 updated_at=GREATEST(clock_timestamp(),updated_at+INTERVAL '1 microsecond')
 WHERE tenant_id=$1 AND id::text=$2 AND updated_at=$6 RETURNING updated_at`, b.TenantID, b.ID, b.Name, b.APIKeyID, b.RemoteAgent, b.UpdatedAt).Scan(&updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return storeError(err)
	}

	// The credential belongs to the same tenant. No unscoped key update.
	res, err := tx.ExecContext(ctx, `UPDATE api_keys SET surfaces=$3,rate_limit_rpm=$4,updated_at=NOW() WHERE org_id=$1 AND id::text=$2`, b.TenantID, b.APIKeyID, pq.Array(b.Surfaces), b.RateLimitRPM)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.UpdatedAt = updatedAt
	return nil
}
func (s *Store) Delete(ctx context.Context, tenant, id string, updatedAt time.Time) error {
	if tenant == "" {
		return ErrNoTenant
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM bot_connections WHERE tenant_id=$1 AND id::text=$2 AND updated_at=$3`, tenant, id, updatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// Lock serializes mutations in this process without retaining a pool
// connection. Update and Delete compare updated_at to protect against stale
// writes from other processes. Failed rotations compensate their new key.
func (s *Store) Lock(ctx context.Context, tenant, id string) (func(), error) {
	if tenant == "" {
		return nil, ErrNoTenant
	}
	select {
	case s.mutationGate <- struct{}{}:
		return func() { <-s.mutationGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func storeError(err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code == "23505" && (pg.Constraint == "idx_bot_connections_tenant_name" || pg.Constraint == "bot_connections_tenant_id_name_key") {
		return ErrNameTaken
	}
	// pgx and pq both expose SQLState; constraint names above are stable, but
	// keep driver-specific extraction out of the API's response.
	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() == "23505" {
		// api_key_id is server-generated and cannot conflict in a valid create.
		return ErrNameTaken
	}
	return err
}

// One tenant-scoped aggregate for the entire list, restricted to current keys.
// Thirty UTC calendar days includes today and the preceding 29 days.
func (s *Store) loadUsage(ctx context.Context, tenant string, bots []Bot) error {
	ids := make([]string, 0, len(bots))
	for i := range bots {
		bots[i].Usage = emptyUsage()
		ids = append(ids, bots[i].APIKeyID)
	}
	if len(ids) == 0 {
		return nil
	}
	var totals []struct {
		Key     string `db:"api_key_id"`
		Surface string `db:"surface"`
		Calls   int64  `db:"calls"`
	}
	// Ownership is checked through the key's org_id, the same rule every other
	// bot query uses. The activity row's tenant_id must be the tenant that key
	// resolves to: the instance id on a gateway that runs with EVS_INSTANCE_ID,
	// the org id otherwise. Comparing it with the dashboard tenant alone would
	// hide usage on an instance-scoped gateway.
	err := s.db.SelectContext(ctx, &totals, `SELECT a.api_key_id,a.surface,SUM(a.calls) AS calls FROM api_key_activity_daily a
 JOIN api_keys k ON k.id::text=a.api_key_id AND k.org_id=$1
 WHERE a.api_key_id=ANY($2)
 AND (a.tenant_id=k.instance_id OR (NULLIF(k.instance_id,'') IS NULL AND a.tenant_id=k.org_id)) AND a.day >= (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date - 29
 AND a.day <= (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date GROUP BY a.api_key_id,a.surface`, tenant, pq.Array(ids))
	if err != nil {
		return err
	}
	byID := map[string]int{}
	for i := range bots {
		byID[bots[i].APIKeyID] = i
	}
	for _, v := range totals {
		if i, ok := byID[v.Key]; ok {
			bots[i].Usage.Calls30d += v.Calls
			bots[i].Usage.BySurface[v.Surface] = v.Calls
		}
	}
	return nil
}
