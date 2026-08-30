package voice_clone

import (
	"context"
	"time"
)

// VoiceCloneProfile represents a cloned voice profile created from reference audio.
type VoiceCloneProfile struct {
	ID                           string
	OrgID                        string
	Name                         string
	Description                  string
	ReferenceAudioObjectID       string
	ReferenceAudioDurationSeconds float64
	ReferenceText                string
	Provider                     string
	Model                        string
	ProviderVoiceID              string
	Metadata                     map[string]interface{}
	CreatedBy                    string
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	DeletedAt                    *time.Time
}

// Repository defines the interface for managing voice clone profiles.
//
// Every method that targets a single profile takes the owning org id
// alongside the profile id. The org id is the trust anchor — without
// it, the SQL `WHERE id = $1` would let any caller fetch / update /
// delete any tenant's profile by guessing or harvesting an id. After
// the 2026-05-06 cross-tenant P0 we treat the absence of an org id as
// the same shape of leak.
type Repository interface {
	// Create creates a new voice clone profile.
	Create(ctx context.Context, profile *VoiceCloneProfile) error

	// GetByID retrieves a voice clone profile by ID, scoped to the
	// caller's org. Returns (nil, nil) when no row matches both id and
	// org_id — this collapses the "not found" and "wrong tenant"
	// branches so callers can't tell the difference.
	GetByID(ctx context.Context, id, orgID string) (*VoiceCloneProfile, error)

	// ListByOrg returns all voice clone profiles for an organization.
	ListByOrg(ctx context.Context, orgID string) ([]*VoiceCloneProfile, error)

	// Update updates a voice clone profile's mutable fields. The
	// profile's OrgID is included in the WHERE clause so a forged id
	// from another tenant cannot be used to mutate a row.
	Update(ctx context.Context, profile *VoiceCloneProfile) error

	// Delete soft-deletes a voice clone profile by ID, scoped to org.
	Delete(ctx context.Context, id, orgID string) error
}
