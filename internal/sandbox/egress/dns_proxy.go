package egress

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

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
	allowInternal    bool
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

	// AllowInternalNames lets cluster-internal names (cluster.local,
	// *.svc, reverse lookups of private ranges) through to upstream.
	// Off by default: a sandbox must not be able to enumerate the
	// cluster's services by name even though it can no longer connect
	// to them. Operator-level only (FCAGENT_DNS_ALLOW_CLUSTER_NAMES).
	AllowInternalNames bool
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
		allowInternal:    cfg.AllowInternalNames,
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

	if p.refusesInternal(domain) {
		p.emitAudit(domain, "blocked", qtype)
		logger.WithFields("domain", domain, "sandbox_id", p.sandboxID).
			Debug("egress_dns: refused cluster-internal name")
		resp := new(dns.Msg)
		resp.SetRcode(req, dns.RcodeNameError)
		w.WriteMsg(resp)
		return
	}

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

// refusesInternal reports whether the proxy answers NXDOMAIN for domain
// because it names something inside the cluster. Applies in every mode
// and ahead of the tenant allowlist: an allowlist entry cannot reopen
// cluster names.
func (p *DNSProxy) refusesInternal(domain string) bool {
	return !p.allowInternal && IsClusterInternalName(domain)
}

// clusterNameSuffixes are DNS suffixes that only exist inside a
// Kubernetes cluster. "svc" alone covers "name.ns.svc" search-path
// forms with any cluster domain.
var clusterNameSuffixes = []string{"cluster.local", "svc", "localhost"}

// privateReverseRanges are the IPv4 ranges whose reverse (in-addr.arpa)
// names a guest has no business resolving; they mirror the ranges the
// guest deny guard drops.
var privateReverseRanges = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// IsClusterInternalName reports whether domain (lower case, no trailing
// dot) is a cluster-internal or private-range name: anything under
// cluster.local or svc, localhost, every ip6.arpa reverse name (guests
// carry no IPv6), and in-addr.arpa reverse names that fall inside a
// private, link-local, loopback, multicast or reserved IPv4 range.
// Partial reverse names (a zone rather than a host) count when the
// prefix they describe lies inside such a range.
func IsClusterInternalName(domain string) bool {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		return false
	}
	for _, suffix := range clusterNameSuffixes {
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return true
		}
	}
	if domain == "ip6.arpa" || strings.HasSuffix(domain, ".ip6.arpa") {
		return true
	}
	if domain == "in-addr.arpa" {
		return false
	}
	if rest, ok := strings.CutSuffix(domain, ".in-addr.arpa"); ok {
		labels := strings.Split(rest, ".")
		if len(labels) > 4 {
			return false
		}
		// Reverse names list octets least significant first.
		ip := make(net.IP, 4)
		for i, l := range labels {
			n, err := strconv.Atoi(l)
			if err != nil || n < 0 || n > 255 {
				return false
			}
			ip[len(labels)-1-i] = byte(n)
		}
		// A partial name describes a prefix of 8*len(labels) bits; it is
		// internal when the whole prefix lies inside one range.
		prefix := net.IPNet{IP: ip, Mask: net.CIDRMask(8*len(labels), 32)}
		for _, r := range privateReverseRanges {
			rOnes, _ := r.Mask.Size()
			pOnes, _ := prefix.Mask.Size()
			if pOnes >= rOnes && r.Contains(prefix.IP) {
				return true
			}
		}
	}
	return false
}

// upstreamTimeout bounds one exchange with one upstream over one
// transport.
const upstreamTimeout = 2 * time.Second

// forwardBudget caps how long one guest query may spend walking the
// upstream list. The guest's resolver gives up after 5s (no options in
// its resolv.conf), and an answer that lands after that is wasted: Go's
// resolver retries with a fresh ID and ignores it.
const forwardBudget = 4 * time.Second

// udpDeadFor is how long an upstream whose UDP exchange failed is sent
// straight to TCP. Without it every guest query on a node that cannot
// use UDP 53 pays the full UDP timeout first.
const udpDeadFor = time.Minute

// udpDeadUntil maps upstream -> time.Time before which UDP is skipped.
var udpDeadUntil sync.Map

func udpLooksDead(upstream string) bool {
	v, ok := udpDeadUntil.Load(upstream)
	return ok && time.Now().Before(v.(time.Time))
}

// ExchangeUpstream sends req to upstream over UDP and falls back to TCP
// when UDP fails or the answer is truncated. Some hosts cannot reach
// public resolvers over UDP 53 while TCP 53 works (the prod OVH node on
// 2026-10-16), and a truncated UDP answer must be retried over TCP
// anyway. An upstream whose UDP failed goes straight to TCP for
// udpDeadFor. Returns the transport that produced the answer.
func ExchangeUpstream(req *dns.Msg, upstream string) (*dns.Msg, string, error) {
	var resp *dns.Msg
	var udpErr error
	if udpLooksDead(upstream) {
		udpErr = fmt.Errorf("skipped: UDP to %s failed within the last %s", upstream, udpDeadFor)
	} else {
		udp := &dns.Client{Net: "udp", Timeout: upstreamTimeout}
		resp, _, udpErr = udp.Exchange(req, upstream)
		if udpErr != nil {
			udpDeadUntil.Store(upstream, time.Now().Add(udpDeadFor))
		} else {
			udpDeadUntil.Delete(upstream)
			if !resp.Truncated {
				return resp, "udp", nil
			}
		}
	}
	tcp := &dns.Client{Net: "tcp", Timeout: upstreamTimeout}
	tresp, _, tcpErr := tcp.Exchange(req, upstream)
	if tcpErr == nil {
		return tresp, "tcp", nil
	}
	if udpErr == nil {
		// Truncated over UDP and TCP failed: the partial answer is
		// still better than SERVFAIL; the TC bit tells the guest.
		return resp, "udp", nil
	}
	return nil, "", fmt.Errorf("udp: %v; tcp: %v", udpErr, tcpErr)
}

// forwardUpstream resolves the query via an upstream DNS server.
func (p *DNSProxy) forwardUpstream(w dns.ResponseWriter, req *dns.Msg) {
	start := time.Now()
	for _, upstream := range p.upstream {
		if time.Since(start) >= forwardBudget {
			break
		}
		resp, _, err := ExchangeUpstream(req, upstream)
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
