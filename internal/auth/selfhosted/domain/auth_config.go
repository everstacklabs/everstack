package domain

import "time"

// AuthConfig stores persisted instance authentication state.
type AuthConfig struct {
	AuthMode              string     `db:"auth_mode"`
	AllowRegistration     bool       `db:"allow_registration"`
	CloudOrganizationID   *string    `db:"cloud_organization_id"`
	CloudOrganizationSlug *string    `db:"cloud_organization_slug"`
	CloudWorkspaceID      *string    `db:"cloud_workspace_id"`
	CloudWorkspaceSlug    *string    `db:"cloud_workspace_slug"`
	CloudGatewayURL       *string    `db:"cloud_gateway_url"`
	ConnectedAt           *time.Time `db:"connected_at"`
}
