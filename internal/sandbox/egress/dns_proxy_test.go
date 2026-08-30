package egress

import (
	"testing"
)

func TestIsAllowedDomain_ExactMatch(t *testing.T) {
	hosts := []string{"registry.npmjs.org", "pypi.org"}

	tests := []struct {
		domain string
		want   bool
	}{
		{"registry.npmjs.org", true},
		{"pypi.org", true},
		{"evil.com", false},
		{"", false},
		{"REGISTRY.NPMJS.ORG", true}, // case-insensitive
	}

	for _, tt := range tests {
		got := IsAllowedDomain(tt.domain, hosts)
		if got != tt.want {
			t.Errorf("IsAllowedDomain(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestIsAllowedDomain_Wildcard(t *testing.T) {
	hosts := []string{"*.npmjs.org", "*.github.com", "crates.io"}

	tests := []struct {
		domain string
		want   bool
	}{
		{"registry.npmjs.org", true},
		{"foo.bar.npmjs.org", true},    // multi-level subdomain
		{"npmjs.org", false},           // wildcard requires at least one subdomain
		{"api.github.com", true},
		{"github.com", false},
		{"crates.io", true},
		{"static.crates.io", false},    // no wildcard for crates.io
		{"evil.npmjs.org.evil.com", false},
	}

	for _, tt := range tests {
		got := IsAllowedDomain(tt.domain, hosts)
		if got != tt.want {
			t.Errorf("IsAllowedDomain(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestIsAllowedDomain_EmptyAllowlist(t *testing.T) {
	if IsAllowedDomain("anything.com", nil) {
		t.Error("empty allowlist should block everything")
	}
	if IsAllowedDomain("anything.com", []string{}) {
		t.Error("empty allowlist should block everything")
	}
}

func TestDNSProxy_IsAllowed(t *testing.T) {
	p := NewDNSProxy(DNSProxyConfig{
		AllowedHosts: []string{
			"registry.npmjs.org",
			"*.yarnpkg.com",
			"pypi.org",
			"files.pythonhosted.org",
			"crates.io",
			"static.crates.io",
		},
		SandboxID:        "test-sandbox",
		EnforceAllowlist: true,
	})

	tests := []struct {
		domain string
		want   bool
	}{
		{"registry.npmjs.org", true},
		{"cdn.yarnpkg.com", true},
		{"deep.nested.yarnpkg.com", true},
		{"yarnpkg.com", false},         // wildcard needs subdomain
		{"pypi.org", true},
		{"files.pythonhosted.org", true},
		{"crates.io", true},
		{"static.crates.io", true},
		{"evil.com", false},
		{"registry.npmjs.org.evil.com", false},
	}

	for _, tt := range tests {
		got := p.isAllowed(tt.domain)
		if got != tt.want {
			t.Errorf("isAllowed(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestDNSProxy_IsAllowed_NoEnforcement(t *testing.T) {
	// Allow mode: the proxy still runs (so the guest has a stable
	// resolver and we control upstream choice) but never filters. The
	// AllowedHosts list is ignored entirely — even names not in it
	// must resolve.
	p := NewDNSProxy(DNSProxyConfig{
		AllowedHosts:     []string{"only-this.example.com"},
		SandboxID:        "test-sandbox-allow",
		EnforceAllowlist: false,
	})
	for _, domain := range []string{
		"only-this.example.com",
		"not-in-allowlist.com",
		"evil.com",
		"",
	} {
		if !p.isAllowed(domain) {
			t.Errorf("EnforceAllowlist=false should allow %q", domain)
		}
	}
}

func TestDNSProxy_IsAllowed_EnforceWithEmptyList(t *testing.T) {
	// Whitelist mode with an empty allowlist blocks everything. The
	// gateway should prevent this configuration before we get here, but
	// the proxy itself stays strict — no implicit "empty means allow."
	p := NewDNSProxy(DNSProxyConfig{
		AllowedHosts:     nil,
		SandboxID:        "test-sandbox-empty-enforce",
		EnforceAllowlist: true,
	})
	if p.isAllowed("anything.com") {
		t.Error("EnforceAllowlist=true with empty list must block")
	}
}

// collectingAuditSink collects audit events for testing.
type collectingAuditSink struct {
	events []AuditEvent
}

func (s *collectingAuditSink) Emit(event AuditEvent) {
	s.events = append(s.events, event)
}

func TestDNSProxy_AuditEmission(t *testing.T) {
	sink := &collectingAuditSink{}
	p := NewDNSProxy(DNSProxyConfig{
		AllowedHosts: []string{"allowed.com"},
		SandboxID:    "test-sbx",
		Audit:        sink,
	})

	p.emitAudit("allowed.com", "allowed", "A")
	p.emitAudit("blocked.com", "blocked", "A")

	if len(sink.events) != 2 {
		t.Fatalf("expected 2 audit events, got %d", len(sink.events))
	}
	if sink.events[0].Action != "allowed" || sink.events[0].Domain != "allowed.com" {
		t.Errorf("first event wrong: %+v", sink.events[0])
	}
	if sink.events[1].Action != "blocked" || sink.events[1].Domain != "blocked.com" {
		t.Errorf("second event wrong: %+v", sink.events[1])
	}
}
