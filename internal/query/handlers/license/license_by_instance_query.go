package license

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/query"
)

const GetLicenseByInstanceIDType = "license.get_license_by_instance_id"

type GetLicenseByInstanceID struct {
	query.BaseQuery
	InstanceID string
}

func (q GetLicenseByInstanceID) QueryType() string { return GetLicenseByInstanceIDType }
func (q GetLicenseByInstanceID) Validate() error {
	if q.InstanceID == "" {
		return fmt.Errorf("instance_id is required")
	}
	return nil
}

type LicenseByInstanceResult struct {
	Tier         string
	IsPaid       bool
	Status       string
	ExpiresAt    *time.Time
	TrialExpires *time.Time
}

type GetLicenseByInstanceIDHandler struct{ db *sqlx.DB }

func NewGetLicenseByInstanceIDHandler(db *sqlx.DB) *GetLicenseByInstanceIDHandler {
	return &GetLicenseByInstanceIDHandler{db: db}
}

func (h *GetLicenseByInstanceIDHandler) QueryType() string { return GetLicenseByInstanceIDType }

func (h *GetLicenseByInstanceIDHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	if h.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	req := q.(GetLicenseByInstanceID)

	// Read license state from system.instances (locally cached after activation)
	// Use json.RawMessage for JSON/JSONB columns. Choose placeholder per driver.
	var licenseStateJSON json.RawMessage
	driver := h.db.DriverName()
	placeholder := "$1"
	if strings.Contains(strings.ToLower(driver), "sqlite") || strings.Contains(strings.ToLower(driver), "mysql") {
		placeholder = "?"
	}
	query := "SELECT signed_payload FROM system.instances WHERE instance_kid = " + placeholder + " AND instance_status = 'active' ORDER BY updated_at DESC LIMIT 1"
	err := h.db.QueryRowContext(ctx, query, req.InstanceID).Scan(&licenseStateJSON)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if len(licenseStateJSON) == 0 {
		return nil, nil
	}

	// Parse the license state JSON (protobuf fields have different casing)
	var licenseState struct {
		PlanTier  int32 `json:"plan_tier"` // protobuf enum as integer
		Status    int32 `json:"status"`    // protobuf enum as integer
		ExpiresAt *struct {
			Seconds int64 `json:"seconds"`
			Nanos   int32 `json:"nanos"`
		} `json:"expires_at"`
	}

	if err := json.Unmarshal(licenseStateJSON, &licenseState); err != nil {
		return nil, fmt.Errorf("failed to parse license state: %w", err)
	}

	// Convert plan tier enum to string (protobuf enums are 0-indexed)
	tier := "free"
	switch licenseState.PlanTier {
	case 0:
		tier = "free" // LICENSE_TYPE_UNSPECIFIED or FREE
	case 1:
		tier = "free" // LICENSE_TYPE_FREE
	case 2:
		tier = "basic" // LICENSE_TYPE_BASIC
	case 3:
		tier = "pro" // LICENSE_TYPE_PRO
	case 4:
		tier = "enterprise" // LICENSE_TYPE_ENTERPRISE
	default:
		tier = "free"
	}

	// Determine if paid (anything other than free)
	isPaid := tier != "free"

	// Parse expiration
	var expiresAt *time.Time
	var trialExpires *time.Time
	if licenseState.ExpiresAt != nil && licenseState.ExpiresAt.Seconds > 0 {
		t := time.Unix(licenseState.ExpiresAt.Seconds, 0)
		expiresAt = &t
		if !isPaid {
			// For free tier, trial expires same as license expires
			trialExpires = &t
		}
	}

	// Map status enum to string (1 = ACTIVE)
	status := "active"
	switch licenseState.Status {
	case 0:
		status = "unspecified"
	case 1:
		status = "active"
	case 2:
		status = "inactive"
	case 3:
		status = "expired"
	case 4:
		status = "suspended"
	case 5:
		status = "cancelled"
	}

	return &LicenseByInstanceResult{
		Tier:         tier,
		IsPaid:       isPaid,
		Status:       status,
		ExpiresAt:    expiresAt,
		TrialExpires: trialExpires,
	}, nil
}
