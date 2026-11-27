package sandbox

import (
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Compute admission surfaces reported to the G1 shadow
// (docs/design/prepaid-credits-v2.md S4). The values are the metric labels.
const (
	ComputeSurfaceSandboxCreate = "sandbox_create"
	ComputeSurfaceSandboxRevive = "sandbox_revive"
)

// SetComputeAdmissionShadow installs the G1 dry-run compute admission hook.
// Only a managed gateway with a platform DB installs one; nil (the default,
// and every self-hosted gateway) means nothing is evaluated. The hook must
// not block: it is called on the allocation path.
func (m *SandboxManager) SetComputeAdmissionShadow(fn func(tenantID, surface string)) {
	if m == nil {
		return
	}
	if fn == nil {
		m.computeAdmissionShadow.Store(nil)
		return
	}
	m.computeAdmissionShadow.Store(&fn)
}

// shadowAdmitCompute reports one real compute allocation to the shadow hook.
// It never fails or delays the allocation: a panicking hook is recovered.
func (m *SandboxManager) shadowAdmitCompute(tenantID, surface string) {
	if m == nil || strings.TrimSpace(tenantID) == "" {
		return
	}
	ptr := m.computeAdmissionShadow.Load()
	if ptr == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logger.WithFields("tenant_id", tenantID, "surface", surface, "panic", r).
				Error("sandbox_manager: compute admission shadow panicked; allocation unaffected")
		}
	}()
	(*ptr)(tenantID, surface)
}
