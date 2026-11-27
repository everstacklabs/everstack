package provider_api_keys

// Values of provider_api_keys.source. The column records how a row was
// written, not who owns it: "config" rows come from gateway YAML/env seeding
// (seedDatabaseFromYAML, config_sync) but also from HandleConfigureProvider
// when a tenant configures a provider without naming the key. Only the
// owning provider_configurations.organization_id says whose key it is.
const (
	// SourcePlatform marks a row written by a config path. Billing does not
	// key on it any more (provenance is derived from ownership at the
	// provider dispatch seam, internal/providers/billing); it survives as
	// the label BillingSource reports for platform-owned rows.
	SourcePlatform = "config"
	// SourceBYOK marks a user-managed row. BYOK inference is never metered.
	SourceBYOK = "manual"
)

// BillingSource returns the Source label a served key carries for
// attribution. Ownership is the truth: a key belongs to the platform only
// when its provider configuration is platform-owned (organization_id IS
// NULL, loaded from gateway YAML/env by the operator), and then it is
// reported as platform whatever label its row was written with (a NULL-owned
// row labelled "manual" used to read as BYOK and go unbilled). Any
// tenant-owned configuration is BYOK, whatever its label: the label is
// mutable and written by tenant-facing paths, ownership is not.
func BillingSource(ownerOrgID *string, labelled string) string {
	if ownerOrgID != nil && *ownerOrgID != "" {
		return SourceBYOK
	}
	return SourcePlatform
}
