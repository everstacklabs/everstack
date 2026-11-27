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
		{"foo.bar.npmjs.org", true}, // multi-level subdomain
		{"npmjs.org", false},        // wildcard requires at least one subdomain
		{"api.github.com", true},
		{"github.com", false},
		{"crates.io", true},
		{"static.crates.io", false}, // no wildcard for crates.io
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
		{"yarnpkg.com", false}, // wildcard needs subdomain
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

func TestIsClusterInternalName(t *testing.T) {
	cases := map[string]bool{
		"postgres-gateway.everstack-prod.svc.cluster.local": true,
		"kubernetes.default.svc.cluster.local.":             true,
		"cluster.local":                                     true,
		"redis.everstack-prod.svc":                          true,
		"svc":                                               true,
		"localhost":                                         true,
		"api.localhost":                                     true,
		"10.0.43.10.in-addr.arpa":                           true, // 10.43.0.10
		"225.0.42.10.in-addr.arpa":                          true, // 10.42.0.225
		"1.0.0.127.in-addr.arpa":                            true,
		"254.169.in-addr.arpa":                              true, // zone inside 169.254/16
		"42.10.in-addr.arpa":                                true, // zone inside 10/8
		"10.in-addr.arpa":                                   true,
		"1.0.64.100.in-addr.arpa":                           true, // CGNAT
		"1.16.172.in-addr.arpa":                             true,
		"a.b.c.d.ip6.arpa":                                  true,
		"ip6.arpa":                                          true,
		"10.2.0.192.in-addr.arpa":                           false, // RFC 5737 public documentation address
		"in-addr.arpa":                                      false,
		"172.in-addr.arpa":                                  false, // 172/8 is wider than 172.16/12
		"100.in-addr.arpa":                                  false, // wider than 100.64/10
		"example.com":                                       false,
		"svc.example.com":                                   false,
		"clusterlocal.example.com":                          false,
		"registry.npmjs.org":                                false,
		"":                                                  false,
	}
	for domain, want := range cases {
		if got := IsClusterInternalName(domain); got != want {
			t.Errorf("IsClusterInternalName(%q) = %v, want %v", domain, got, want)
		}
	}
}

func TestDNSProxy_RefusesInternalNamesAheadOfAllowlist(t *testing.T) {
	// Allow mode, and even an explicit allowlist entry, cannot reopen a
	// cluster name; only the operator switch can.
	p := NewDNSProxy(DNSProxyConfig{AllowedHosts: []string{"*.svc.cluster.local"}})
	if !p.refusesInternal("postgres.everstack-prod.svc.cluster.local") {
		t.Fatal("cluster name must be refused despite the allowlist")
	}
	if p.refusesInternal("registry.npmjs.org") {
		t.Fatal("public name must not be refused")
	}
	p = NewDNSProxy(DNSProxyConfig{AllowInternalNames: true})
	if p.refusesInternal("postgres.everstack-prod.svc.cluster.local") {
		t.Fatal("operator override must let cluster names through")
	}
}
