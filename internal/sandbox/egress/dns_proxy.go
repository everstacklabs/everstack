package egress

import (
	"net"
	"strings"
	"sync"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/miekg/dns"
)

// DNSProxy is a DNS server that filters queries against an allowlist.
// Allowed domains are resolved normally via upstream; blocked domains get NXDOMAIN.
type DNSProxy struct {
	allowedHosts     map[string]bool // exact matches
	wildcards        []string        // wildcard patterns (e.g., "*.npmjs.org")
	upstream         []string        // upstream DNS servers
	listenAddr       string
	server           *dns.Server
	audit            AuditSink
	sandboxID        string
	enforceAllowlist bool
	mu               sync.RWMutex
}

// DNSProxyConfig configures the DNS proxy.
type DNSProxyConfig struct {
	AllowedHosts []string
	Upstream     []string
	ListenAddr   string
	SandboxID    string
	Audit        AuditSink

	// EnforceAllowlist controls whether AllowedHosts gates resolution.
	// false (default): forward every query upstream — used by the
	// Firecracker backend's Allow mode, where the proxy exists purely
	// to give the guest a stable resolver and normalize upstream choice
	// (no ClusterIPs leaking into the guest's resolv.conf).
	// true: enforce — non-matching names return NXDOMAIN. Used by
	// Whitelist mode in both backends.
	EnforceAllowlist bool
}

// NewDNSProxy creates a DNS proxy with the given allowlist.
func NewDNSProxy(cfg DNSProxyConfig) *DNSProxy {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:15353"
	}
	if len(cfg.Upstream) == 0 {
		cfg.Upstream = []string{"8.8.8.8:53", "1.1.1.1:53"}
	}

	p := &DNSProxy{
		allowedHosts:     make(map[string]bool),
		upstream:         cfg.Upstream,
		listenAddr:       cfg.ListenAddr,
		audit:            cfg.Audit,
		sandboxID:        cfg.SandboxID,
		enforceAllowlist: cfg.EnforceAllowlist,
	}

	for _, h := range cfg.AllowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if strings.HasPrefix(h, "*.") {
			p.wildcards = append(p.wildcards, h[1:]) // store ".npmjs.org"
		} else {
			p.allowedHosts[h] = true
		}
	}

	return p
}

// Start begins listening for DNS queries. Blocks until stopped.
func (p *DNSProxy) Start() error {
	mux := dns.NewServeMux()
	mux.HandleFunc(".", p.handleDNS)

	p.server = &dns.Server{
		Addr:    p.listenAddr,
		Net:     "udp",
		Handler: mux,
	}

	logger.WithFields("addr", p.listenAddr, "sandbox_id", p.sandboxID, "allowed_hosts", len(p.allowedHosts)+len(p.wildcards)).
		Info("egress_dns: starting DNS proxy")

	return p.server.ListenAndServe()
}

// Stop shuts down the DNS proxy.
func (p *DNSProxy) Stop() {
	if p.server != nil {
		p.server.Shutdown()
	}
}

// handleDNS processes a single DNS query.
func (p *DNSProxy) handleDNS(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 0 {
		dns.HandleFailed(w, req)
		return
	}

	q := req.Question[0]
	domain := strings.TrimSuffix(strings.ToLower(q.Name), ".")
	qtype := dns.TypeToString[q.Qtype]

	if p.isAllowed(domain) {
		p.emitAudit(domain, "allowed", qtype)
		p.forwardUpstream(w, req)
		return
	}

	p.emitAudit(domain, "blocked", qtype)

	logger.WithFields("domain", domain, "sandbox_id", p.sandboxID).
		Debug("egress_dns: blocked query")

	// Return NXDOMAIN
	resp := new(dns.Msg)
	resp.SetRcode(req, dns.RcodeNameError)
	w.WriteMsg(resp)
}

// isAllowed checks if a domain is in the allowlist. When the proxy was
// created with EnforceAllowlist=false (Allow mode), every query is
// allowed regardless of list contents — the proxy still runs because
// it normalizes upstream resolver choice, but it doesn't gate anything.
func (p *DNSProxy) isAllowed(domain string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.enforceAllowlist {
		return true
	}

	// Exact match
	if p.allowedHosts[domain] {
		return true
	}

	// Wildcard match: *.example.com matches foo.example.com and bar.foo.example.com
	for _, suffix := range p.wildcards {
		if strings.HasSuffix(domain, suffix) {
			return true
		}
	}

	return false
}

// forwardUpstream resolves the query via an upstream DNS server.
func (p *DNSProxy) forwardUpstream(w dns.ResponseWriter, req *dns.Msg) {
	c := new(dns.Client)
	c.Net = "udp"

	for _, upstream := range p.upstream {
		resp, _, err := c.Exchange(req, upstream)
		if err != nil {
			continue
		}
		w.WriteMsg(resp)
		return
	}

	// All upstreams failed — return SERVFAIL
	resp := new(dns.Msg)
	resp.SetRcode(req, dns.RcodeServerFailure)
	w.WriteMsg(resp)
}

func (p *DNSProxy) emitAudit(domain, action, queryType string) {
	if p.audit == nil {
		return
	}
	p.audit.Emit(AuditEvent{
		SandboxID: p.sandboxID,
		Domain:    domain,
		Action:    action,
		QueryType: queryType,
	})
}

// IsAllowedDomain is an exported helper for testing domain matching logic.
func IsAllowedDomain(domain string, allowedHosts []string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	for _, h := range allowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if strings.HasPrefix(h, "*.") {
			suffix := h[1:] // ".example.com"
			if strings.HasSuffix(domain, suffix) {
				return true
			}
		} else if domain == h {
			return true
		}
	}
	return false
}

// ResolveAllowed resolves a domain if it's in the allowlist, using standard net resolver.
// Returns the IP addresses or an error if blocked.
func ResolveAllowed(domain string, allowedHosts []string) ([]net.IP, error) {
	if !IsAllowedDomain(domain, allowedHosts) {
		return nil, &net.DNSError{
			Err:        "domain blocked by egress policy",
			Name:       domain,
			IsNotFound: true,
		}
	}
	return net.LookupIP(domain)
}
