package sandbox

import "strings"

// TenantInstanceScope is the canonical scope for listing or creating sandbox
// resources under one organization/workspace/instance boundary.
//
// During the transition to fully separated org/workspace/instance storage,
// sandbox_instances.tenant_id remains the effective sandbox owner in several
// paths. In cloud deployments that value is often the instance ID. Use
// SandboxTenantID when querying current sandbox tables.
type TenantInstanceScope struct {
	OrganizationID string
	TenantID       string
	InstanceID     string
}

// SandboxScope is the canonical scope for operations on one sandbox.
type SandboxScope struct {
	OrganizationID string
	TenantID       string
	InstanceID     string
	SandboxID      string
}

// Normalize trims all scope fields and returns a copy.
func (s TenantInstanceScope) Normalize() TenantInstanceScope {
	return TenantInstanceScope{
		OrganizationID: strings.TrimSpace(s.OrganizationID),
		TenantID:       strings.TrimSpace(s.TenantID),
		InstanceID:     strings.TrimSpace(s.InstanceID),
	}
}

// Normalize trims all scope fields and returns a copy.
func (s SandboxScope) Normalize() SandboxScope {
	return SandboxScope{
		OrganizationID: strings.TrimSpace(s.OrganizationID),
		TenantID:       strings.TrimSpace(s.TenantID),
		InstanceID:     strings.TrimSpace(s.InstanceID),
		SandboxID:      strings.TrimSpace(s.SandboxID),
	}
}

// TenantInstance returns the parent tenant/instance scope.
func (s SandboxScope) TenantInstance() TenantInstanceScope {
	s = s.Normalize()
	return TenantInstanceScope{
		OrganizationID: s.OrganizationID,
		TenantID:       s.TenantID,
		InstanceID:     s.InstanceID,
	}
}

// SandboxTenantID returns the value that should be used against the current
// sandbox_instances.tenant_id column. Prefer instance_id when available because
// cloud auth scopes sandbox ownership at the gateway instance boundary.
func (s TenantInstanceScope) SandboxTenantID() string {
	s = s.Normalize()
	if s.InstanceID != "" {
		return s.InstanceID
	}
	return s.TenantID
}

// SandboxTenantID returns the effective current-table owner for this sandbox.
func (s SandboxScope) SandboxTenantID() string {
	return s.TenantInstance().SandboxTenantID()
}

// HasSandboxTenant reports whether the scope can safely query current sandbox
// tables without falling back to an unscoped lookup.
func (s TenantInstanceScope) HasSandboxTenant() bool {
	return s.SandboxTenantID() != ""
}

// Complete reports whether the scope is sufficient for a public operation on a
// specific sandbox in the current storage model.
func (s SandboxScope) Complete() bool {
	s = s.Normalize()
	return s.SandboxID != "" && s.SandboxTenantID() != ""
}

// WithSandbox returns a sandbox-scoped child of the tenant/instance scope.
func (s TenantInstanceScope) WithSandbox(sandboxID string) SandboxScope {
	s = s.Normalize()
	return SandboxScope{
		OrganizationID: s.OrganizationID,
		TenantID:       s.TenantID,
		InstanceID:     s.InstanceID,
		SandboxID:      strings.TrimSpace(sandboxID),
	}
}

// MatchesInstance reports whether a loaded instance belongs to this scope under
// the current storage model. It intentionally uses SandboxTenantID so callers do
// not accidentally compare workspace tenant IDs against rows keyed by instance.
func (s TenantInstanceScope) MatchesInstance(inst *Instance) bool {
	if inst == nil {
		return false
	}
	want := s.SandboxTenantID()
	if want == "" {
		return false
	}
	if inst.InstanceID != "" {
		return inst.InstanceID == want
	}
	return inst.Config.TenantID == want
}

// MatchesInstance reports whether a loaded instance matches this sandbox scope.
func (s SandboxScope) MatchesInstance(inst *Instance) bool {
	s = s.Normalize()
	if s.SandboxID == "" || inst == nil || inst.ID != s.SandboxID {
		return false
	}
	return s.TenantInstance().MatchesInstance(inst)
}
