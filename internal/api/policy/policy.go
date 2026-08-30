package policy

import (
	"strings"

	"github.com/spf13/viper"
)

// Policy defines enforcement bypass rules.
type Policy struct {
	BypassServices map[string]struct{} // service FQDNs to bypass enforcement entirely
	BypassPrefixes []string            // HTTP path prefixes to bypass enforcement entirely

	// MeteredPathPrefixes are HTTP path prefixes that count against trial/usage limits.
	// Only these paths will have requests recorded for metering purposes.
	// This is an allowlist approach - if a path doesn't match, it won't be metered.
	MeteredPathPrefixes []string
}

// NewDefaultPolicy returns a policy with minimal bypasses for bootstrap only.
// All other endpoints require authentication. See POR-53 for full auth-on-startup plan.
func NewDefaultPolicy() *Policy {
	return &Policy{
		BypassServices: map[string]struct{}{
			"everstack.health.v1.HealthService": {}, // Health checks must always be accessible
			"everstack.auth.v1.AuthService":     {}, // Auth endpoints (login/register) must work pre-auth
		},
		BypassPrefixes: []string{
			"/v1/auth/", // REST auth endpoints (grpc-gateway)

			// Gateway bootstrap endpoints — minimum required for activation flow.
			// Once POR-53 (auth-on-startup) is implemented, these move behind auth too.
			"/everstack.gateway.v1.GatewayService/GetGatewayInstanceStatus",
			"/everstack.gateway.v1.GatewayService/ActivateGatewayInstance",
			"/everstack.gateway.v1.GatewayService/GetTrialStatus", // Needed by activation guard to check trial mode
			"/everstack.gateway.v1.GatewayService/ActivationCallback",
			"/everstack.gateway.v1.GatewayService/SubscriptionStatusCallback",
			"/everstack.gateway.v1.GatewayService/GetLicenseMonitorStatus",    // Billing page needs license/usage info to render
			"/everstack.gateway.v1.GatewayService/RefreshLicenseMonitor",      // Billing page manual refresh button
			"/everstack.gateway.v1.GatewayService/GetPlans",                   // Billing page needs plan list for upgrade
			"/everstack.gateway.v1.GatewayService/StoreUpgradeCallbackSecret", // Upgrade flow requires storing callback secret before redirect

			// REST equivalents (grpc-gateway)
			"/v1/gateway/instance/activate",
			"/v1/gateway/instance/status",
			"/v1/gateway/instance/trial-status",
			"/v1/gateway/activation-callback",
			"/v1/gateway/subscription-status-callback",
		},
		MeteredPathPrefixes: []string{
			// Only these AI Gateway proxy paths count against trial/usage limits.
			// All other endpoints (admin UI, internal services) are not metered.

			// REST/HTTP endpoints (OpenAI-compatible API)
			"/v1/chat/completions",
			"/v1/completions",
			"/v1/embeddings",
			"/v1/images",
			"/v1/audio",
			"/v1/moderations",

			// ConnectRPC/gRPC endpoints (GatewayService AI methods)
			"/everstack.gateway.v1.GatewayService/ChatCompletion",
			"/everstack.gateway.v1.GatewayService/Embeddings",
		},
	}
}

// FromViper loads a policy from the provided viper instance (or global if nil).
// Config values are MERGED with defaults, not replaced, to ensure critical internal
// endpoints are always bypassed.
func FromViper(v *viper.Viper) *Policy {
	p := NewDefaultPolicy()
	vv := v
	if vv == nil {
		vv = viper.GetViper()
	}
	// Merge bypass services from config with defaults
	svcBypass := vv.GetStringSlice("services.security.policy.bypass_services")
	for _, s := range svcBypass {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		p.BypassServices[s] = struct{}{}
	}
	// Merge bypass prefixes from config with defaults
	svcPrefixes := vv.GetStringSlice("services.security.policy.bypass_prefixes")
	for _, pr := range svcPrefixes {
		pr = strings.TrimSpace(pr)
		if pr == "" {
			continue
		}
		// Avoid duplicates
		found := false
		for _, existing := range p.BypassPrefixes {
			if existing == pr {
				found = true
				break
			}
		}
		if !found {
			p.BypassPrefixes = append(p.BypassPrefixes, pr)
		}
	}
	return p
}

// FromGlobal is a convenience wrapper that uses the global viper instance.
func FromGlobal() *Policy { return FromViper(nil) }

// ShouldBypassProcedure returns true if the Connect procedure belongs to a bypassed service
// or matches a bypass prefix.
// proc format: "/package.Service/Method"
func (p *Policy) ShouldBypassProcedure(proc string) bool {
	if p == nil || proc == "" {
		return false
	}
	if isPublicModelMetricsPath(proc) {
		return true
	}
	// Check explicit bypass prefixes first (e.g., "/everstack.gateway.v1.GatewayService/GetTrialStatus")
	for _, pref := range p.BypassPrefixes {
		if strings.HasPrefix(proc, pref) {
			return true
		}
	}
	// Then check if procedure belongs to a bypassed service
	for svc := range p.BypassServices {
		prefix := "/" + svc + "/"
		if strings.HasPrefix(proc, prefix) {
			return true
		}
	}
	return false
}

// ShouldMeterRequest returns true if the path should count against trial/usage limits.
// Only paths matching MeteredPathPrefixes are metered (allowlist approach).
func (p *Policy) ShouldMeterRequest(path string) bool {
	if p == nil || path == "" || len(p.MeteredPathPrefixes) == 0 {
		return false
	}
	for _, prefix := range p.MeteredPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// ShouldBypassPath returns true if the HTTP path starts with any bypass prefix
// or if the path belongs to a bypassed service.
func (p *Policy) ShouldBypassPath(path string) bool {
	if p == nil || path == "" {
		return false
	}
	if isPublicModelMetricsPath(path) {
		return true
	}
	// Check explicit bypass prefixes
	for _, pref := range p.BypassPrefixes {
		if strings.HasPrefix(path, pref) {
			return true
		}
	}
	// Also check if path belongs to a bypassed service (e.g., "/everstack.license.v1.LicenseService/...")
	for svc := range p.BypassServices {
		prefix := "/" + svc + "/"
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isPublicModelMetricsPath(path string) bool {
	switch path {
	case "/api/model-metrics/v1/report",
		"/api/model-metrics/v1/compare",
		"/api/model-metrics/v1/provider-models",
		"/everstack.modelmetrics.v1.PublicModelMetricsService/GetReport",
		"/everstack.modelmetrics.v1.PublicModelMetricsService/Compare",
		"/everstack.modelmetrics.v1.PublicModelMetricsService/GetProviderModelBreakdown":
		return true
	default:
		return false
	}
}
