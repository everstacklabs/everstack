package voice_clone

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

type dbProfile struct {
	ID                           string         `db:"id"`
	OrgID                        string         `db:"org_id"`
	Name                         string         `db:"name"`
	Description                  string         `db:"description"`
	ReferenceAudioObjectID       sql.NullString `db:"reference_audio_object_id"`
	ReferenceAudioDurationSeconds float64       `db:"reference_audio_duration_seconds"`
	ReferenceText                string         `db:"reference_text"`
	Provider                     string         `db:"provider"`
	Model                        string         `db:"model"`
	ProviderVoiceID              string         `db:"provider_voice_id"`
	Metadata                     []byte         `db:"metadata"`
	CreatedBy                    string         `db:"created_by"`
	CreatedAt                    time.Time      `db:"created_at"`
	UpdatedAt                    time.Time      `db:"updated_at"`
	DeletedAt                    sql.NullTime   `db:"deleted_at"`
}

func fromDB(d *dbProfile) *VoiceCloneProfile {
	p := &VoiceCloneProfile{
		ID:                           d.ID,
		OrgID:                        d.OrgID,
		Name:                         d.Name,
		Description:                  d.Description,
		ReferenceAudioDurationSeconds: d.ReferenceAudioDurationSeconds,
		ReferenceText:                d.ReferenceText,
		Provider:                     d.Provider,
		Model:                        d.Model,
		ProviderVoiceID:              d.ProviderVoiceID,
		CreatedBy:                    d.CreatedBy,
		CreatedAt:                    d.CreatedAt,
		UpdatedAt:                    d.UpdatedAt,
	}
	if d.ReferenceAudioObjectID.Valid {
		p.ReferenceAudioObjectID = d.ReferenceAudioObjectID.String
	}
	if d.DeletedAt.Valid {
		p.DeletedAt = &d.DeletedAt.Time
	}
	if len(d.Metadata) > 0 {
		_ = json.Unmarshal(d.Metadata, &p.Metadata)
	}
	return p
}

// PostgresRepository implements Repository using Postgres.
type PostgresRepository struct {
	db *sqlx.DB
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *sqlx.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Create(ctx context.Context, p *VoiceCloneProfile) error {
	metadataJSON, _ := json.Marshal(p.Metadata)
	if metadataJSON == nil {
		metadataJSON = []byte("{}")
	}

	query := `
		INSERT INTO voice_clone_profiles (
			id, org_id, name, description, reference_audio_object_id,
			reference_audio_duration_seconds, reference_text,
			provider, model, provider_voice_id, metadata, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING created_at, updated_at`

	var refAudioObjID sql.NullString
	if p.ReferenceAudioObjectID != "" {
		refAudioObjID = sql.NullString{String: p.ReferenceAudioObjectID, Valid: true}
	}

	return r.db.QueryRowContext(ctx, query,
		p.ID, p.OrgID, p.Name, p.Description, refAudioObjID,
		p.ReferenceAudioDurationSeconds, p.ReferenceText,
		p.Provider, p.Model, p.ProviderVoiceID, metadataJSON, p.CreatedBy,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
}

func (r *PostgresRepository) GetByID(ctx context.Context, id, orgID string) (*VoiceCloneProfile, error) {
	if orgID == "" {
		// Defense-in-depth: refuse to query without a tenant scope.
		// Returning (nil, nil) collapses with the not-found case so
		// the API surface can't distinguish "wrong tenant" from
		// "deleted".
		return nil, nil
	}
	var d dbProfile
	err := r.db.GetContext(ctx, &d,
		`SELECT * FROM voice_clone_profiles WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`, id, orgID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get voice clone profile: %w", err)
	}
	return fromDB(&d), nil
}

func (r *PostgresRepository) ListByOrg(ctx context.Context, orgID string) ([]*VoiceCloneProfile, error) {
	if orgID == "" {
		return nil, nil
	}
	var rows []dbProfile
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM voice_clone_profiles WHERE org_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list voice clone profiles: %w", err)
	}

	profiles := make([]*VoiceCloneProfile, len(rows))
	for i, d := range rows {
		profiles[i] = fromDB(&d)
	}
	return profiles, nil
}

func (r *PostgresRepository) Update(ctx context.Context, p *VoiceCloneProfile) error {
	if p.OrgID == "" {
		return fmt.Errorf("voice_clone Update: org id is required")
	}
	query := `
		UPDATE voice_clone_profiles
		SET name = $3, description = $4, reference_text = $5, updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
		RETURNING updated_at`
	return r.db.QueryRowContext(ctx, query,
		p.ID, p.OrgID, p.Name, p.Description, p.ReferenceText,
	).Scan(&p.UpdatedAt)
}

func (r *PostgresRepository) Delete(ctx context.Context, id, orgID string) error {
	if orgID == "" {
		return fmt.Errorf("voice_clone Delete: org id is required")
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE voice_clone_profiles SET deleted_at = NOW() WHERE id = $1 AND org_id = $2`, id, orgID)
	return err
}
