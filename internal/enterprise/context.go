package enterprise

import (
	"context"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// Fallback hooks — overridden by context_ee.go in enterprise builds to also
// recognise concrete types (e.g. *middleware.LicenseEnforcer) that start_api.go
// currently stores in context.  A fallback returns nil when it finds nothing;
// the FromContext accessors then consult the process-global registry before
// giving up with a no-op.
var (
	enforcerFallback = func(context.Context) LicenseEnforcer { return nil }
	monitorFallback  = func(context.Context) LicenseMonitor { return nil }
	instMgrFallback  = func(context.Context) InstanceManager { return nil }
)

// Process-global registry. The license monitor/enforcer are process-wide
// singletons created once at startup, but start_api.go historically stored
// them only on the STARTUP context — request contexts (built fresh per
// request by net/http and connect) never carried them, so every
// LicenseMonitorFromContext call at an enforcement callsite silently got the
// no-op monitor and licensed instances fell back to CE limits. The globals
// close that gap: request-scoped values still win when present, the global
// is the backstop. Set once during startup wiring.
var (
	globalEnforcer LicenseEnforcer
	globalMonitor  LicenseMonitor
	globalInstMgr  InstanceManager
)

// SetGlobalLicenseEnforcer registers the process-wide enforcer backstop.
func SetGlobalLicenseEnforcer(e LicenseEnforcer) { globalEnforcer = e }

// SetGlobalLicenseMonitor registers the process-wide monitor backstop.
func SetGlobalLicenseMonitor(m LicenseMonitor) { globalMonitor = m }

// SetGlobalInstanceManager registers the process-wide instance manager backstop.
func SetGlobalInstanceManager(m InstanceManager) { globalInstMgr = m }

// LicenseEnforcerFromContext retrieves the LicenseEnforcer from ctx, falling
// back to the process-global enforcer, then to a safe no-op.  This eliminates
// nil-check boilerplate at every call site.
func LicenseEnforcerFromContext(ctx context.Context) LicenseEnforcer {
	if v, ok := ctx.Value(contextkeys.LicenseEnforcer).(LicenseEnforcer); ok && v != nil {
		return v
	}
	if v := enforcerFallback(ctx); v != nil {
		return v
	}
	if globalEnforcer != nil {
		return globalEnforcer
	}
	return noopEnforcer
}

// LicenseMonitorFromContext retrieves the LicenseMonitor from ctx, falling
// back to the process-global monitor, then to a safe no-op.
// The returned value may also satisfy PersistentMonitor — callers needing the
// extended interface should type-assert:
//
//	if pm, ok := monitor.(enterprise.PersistentMonitor); ok { ... }
func LicenseMonitorFromContext(ctx context.Context) LicenseMonitor {
	if v, ok := ctx.Value(contextkeys.LicenseMonitor).(LicenseMonitor); ok && v != nil {
		return v
	}
	if v := monitorFallback(ctx); v != nil {
		return v
	}
	if globalMonitor != nil {
		return globalMonitor
	}
	return noopMonitor
}

// InstanceManagerFromContext retrieves the InstanceManager from ctx, falling
// back to the process-global manager, then to a safe no-op.
func InstanceManagerFromContext(ctx context.Context) InstanceManager {
	if v, ok := ctx.Value(contextkeys.InstanceManager).(InstanceManager); ok && v != nil {
		return v
	}
	if v := instMgrFallback(ctx); v != nil {
		return v
	}
	if globalInstMgr != nil {
		return globalInstMgr
	}
	return noopInstMgr
}
