package enterprise

import (
	"context"
	"encoding/json"
	"strings"
)

// LicensedIdentity returns the organization (license tenant) and instance
// this self-hosted gateway is activated under. The license monitor is the
// primary source; when it has not learned them yet (for example after a
// restart with the license service unreachable) the stored activation is
// consulted. Empty strings mean the gateway is not activated.
//
// This is the identity a caller-supplied organization_id / instance_id must
// match before the gateway acts on it with its own service credentials.
func LicensedIdentity(ctx context.Context) (orgID, instanceID string) {
	monitor := LicenseMonitorFromContext(ctx)
	orgID = strings.TrimSpace(monitor.GetOrganizationID())
	instanceID = strings.TrimSpace(monitor.GetInstanceID())
	if orgID != "" && instanceID != "" {
		return orgID, instanceID
	}
	info, err := InstanceManagerFromContext(ctx).GetActiveInstance(ctx)
	if err != nil || info == nil {
		return orgID, instanceID
	}
	if instanceID == "" {
		instanceID = strings.TrimSpace(info.InstanceID)
	}
	if orgID == "" && len(info.LicenseState) > 0 {
		var stored struct {
			TenantID      string `json:"tenant_id"`
			TenantIDCamel string `json:"tenantId"`
		}
		if json.Unmarshal(info.LicenseState, &stored) == nil {
			orgID = strings.TrimSpace(stored.TenantID)
			if orgID == "" {
				orgID = strings.TrimSpace(stored.TenantIDCamel)
			}
		}
	}
	return orgID, instanceID
}
