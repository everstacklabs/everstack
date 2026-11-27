package firecracker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Guest private-destination deny.
//
// Every Firecracker guest runs customer code and sits on a TAP in the
// host root netns with ip_forward=1. The per-VM chain in network.go ends
// in an unconditional ACCEPT so public egress works on CNI hosts whose
// FORWARD policy is DROP. Nothing in that chain distinguishes "the
// internet" from the node's own pod network, the service CIDR, the
// Tailscale overlay or the node's public address, so until this guard
// existed a guest could open connections to in-cluster Postgres, Redis,
// internal RPC ports, the control plane, and other tenants.
//
// Two shared chains close that, installed once per host and re-asserted
// periodically (and on every VM create, before the per-VM rules land):
//
//   - mangle PREROUTING -> EVS-GUEST-DENY. This is the authority. The
//     mangle prerouting hook runs after conntrack, before nat DNAT and
//     before any filter chain, so the destination is still the one the
//     guest addressed (a ClusterIP is matched as a ClusterIP, not as the
//     pod kube-proxy would have DNATed it to), conntrack state is
//     available to exempt replies, and both INPUT-bound and FORWARD-
//     bound traffic pass through it. Nothing else on our nodes installs
//     terminal rules in mangle PREROUTING, so there is no position-1 race
//     with kube-router, tailscale or ufw, all of which re-insert their
//     own FORWARD / INPUT jumps at the top.
//   - filter FORWARD and INPUT -> EVS-GUEST-DENY-FILTER. Second layer in
//     the table operators look at first. Post-DNAT, so it matches the
//     conntrack original destination as well as the packet destination.
//     Its jumps are re-asserted to position 1 after each VM's own jumps
//     are inserted, and verifyIptables refuses a VM whose jump precedes
//     the deny.
//
// What a guest may still reach, in order of evaluation:
//
//   - replies on flows the host or a pod initiated (RELATED,ESTABLISHED):
//     exec / shell / file transfer into the guest, port-mapped services.
//   - DNS to its OWN TAP gateway (addrtype LOCAL limited to the incoming
//     interface, so another VM's allow-mode resolver is not reachable).
//   - operator-listed CIDRs (FC_GUEST_ALLOW_PRIVATE_CIDRS). This is the
//     only way to open a private range; tenant allowlists (AllowedHosts)
//     are name-level and cannot reopen anything here by construction.
//   - the public ingress ports (default 80,443) on the node's PUBLIC
//     addresses. On single-node clusters the LoadBalancer IP for the
//     ingress is the node IP itself and tenant gateway hostnames resolve
//     straight to it; without this a sandbox could not call its own
//     gateway. The internet already reaches these, so nothing is widened.
//
// Everything else that is host-local, private, link-local, multicast,
// reserved, CGNAT, a pod/service CIDR, or IPv6 (guests have no IPv6
// configuration at all) is dropped.
const (
	// guestDenyMangleChain is the authoritative chain in the mangle table.
	guestDenyMangleChain = "EVS-GUEST-DENY"
	// guestDenyFilterChain is the second layer in the filter table.
	guestDenyFilterChain = "EVS-GUEST-DENY-FILTER"
	// guestDenyTag marks the hook jumps so re-assertion recognises its own
	// rules and never counts a stranger's.
	guestDenyTag = "evs-guest-deny-v1"
	// guestIfaceWildcard matches every per-VM TAP (see tapNameForVM).
	guestIfaceWildcard = "tap-+"

	// envClusterCIDRs lists pod/service CIDRs explicitly (comma separated).
	// Detection from routes is the fallback; RFC1918 covers the common
	// defaults regardless.
	envClusterCIDRs = "FC_CLUSTER_CIDRS"
	// envGuestAllowPrivateCIDRs is the operator-level override that
	// reopens specific private ranges for every guest on the node.
	envGuestAllowPrivateCIDRs = "FC_GUEST_ALLOW_PRIVATE_CIDRS"
	// envGuestPublicIngressPorts narrows or disables the public-ingress
	// exception. Unset: 80,443. Empty or "none": no exception.
	envGuestPublicIngressPorts = "FC_GUEST_PUBLIC_INGRESS_PORTS"

	guestDenyMaxPort = 65535
)

// guestDenyBaseCIDRs are always denied, whatever the node looks like.
var guestDenyBaseCIDRs = []string{
	"0.0.0.0/8",      // "this" network
	"10.0.0.0/8",     // RFC1918 (includes the guest pool and k3s defaults)
	"100.64.0.0/10",  // CGNAT, Tailscale
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local, cloud metadata
	"172.16.0.0/12",  // RFC1918, docker bridges
	"192.168.0.0/16", // RFC1918
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // reserved, broadcast
}

// guestDenyDefaultIngressPorts is the default public-ingress exception.
var guestDenyDefaultIngressPorts = []int{80, 443}

// cniRouteInterfaces are the interface names whose routes are taken as
// pod-network prefixes when FC_CLUSTER_CIDRS is not set.
var cniRouteInterfaces = map[string]bool{
	"cni0": true, "cbr0": true, "flannel.1": true, "kube-bridge": true,
	"cilium_host": true, "tunl0": true, "vxlan.calico": true, "weave": true,
}

// GuestDenySpec is everything the rule generator needs. Build it with
// DetectGuestDenySpec on a host or by hand in tests.
type GuestDenySpec struct {
	// ClusterCIDRs are pod and service ranges, from config or detection.
	ClusterCIDRs []string
	// HostPublicAddrs are the node's own addresses that no denied CIDR
	// already covers, as /32 strings. These are what the public-ingress
	// exception applies to and what the FORWARD layer drops post-DNAT.
	HostPublicAddrs []string
	// AllowCIDRs are operator overrides, returned before any drop.
	AllowCIDRs []string
	// PublicIngressPorts are TCP ports reachable on HostPublicAddrs.
	PublicIngressPorts []int
	// IPv6 installs the ip6tables layer. Off only when the kernel has
	// no IPv6 at all.
	IPv6 bool
	// Warnings collects config problems Detect could not act on (bad
	// CIDRs, bad ports). Logged by the caller; never fatal.
	Warnings []string
}

// DenyCIDRs returns the complete, normalised, sorted deny list.
func (s GuestDenySpec) DenyCIDRs() []string {
	return normalizeCIDRs(append(append([]string(nil), guestDenyBaseCIDRs...), s.ClusterCIDRs...))
}

// guestDenyMangleRules returns the rule bodies for the mangle chain, in
// evaluation order. Bodies are written exactly as `iptables -S` prints
// them so the installed chain can be compared verbatim.
func guestDenyMangleRules(s GuestDenySpec) []string {
	rules := []string{
		"-m conntrack --ctstate RELATED,ESTABLISHED -j RETURN",
		"-p udp -m udp --dport 53 -m addrtype --dst-type LOCAL --limit-iface-in -j RETURN",
		"-p tcp -m tcp --dport 53 -m addrtype --dst-type LOCAL --limit-iface-in -j RETURN",
	}
	for _, c := range normalizeCIDRs(s.AllowCIDRs) {
		rules = append(rules, "-d "+c+" -j RETURN")
	}
	if ports := portList(s.PublicIngressPorts); ports != "" {
		for _, a := range normalizeCIDRs(s.HostPublicAddrs) {
			rules = append(rules, "-d "+a+" -p tcp -m multiport --dports "+ports+" -j RETURN")
		}
	}
	rules = append(rules, "-m addrtype --dst-type LOCAL -j DROP")
	for _, c := range s.DenyCIDRs() {
		rules = append(rules, "-d "+c+" -j DROP")
	}
	for _, a := range normalizeCIDRs(s.HostPublicAddrs) {
		rules = append(rules, "-d "+a+" -j DROP")
	}
	return rules
}

// guestDenyFilterRules returns the rule bodies for the filter chain. It
// runs after nat, so every destination test is doubled: the packet
// destination and the conntrack original destination (what the guest
// actually addressed before DNAT).
func guestDenyFilterRules(s GuestDenySpec) []string {
	rules := []string{
		"-m conntrack --ctstate RELATED,ESTABLISHED -j RETURN",
		"-m conntrack --ctstate INVALID -j DROP",
		"-p udp -m udp --dport 53 -m addrtype --dst-type LOCAL --limit-iface-in -j RETURN",
		"-p tcp -m tcp --dport 53 -m addrtype --dst-type LOCAL --limit-iface-in -j RETURN",
	}
	for _, c := range normalizeCIDRs(s.AllowCIDRs) {
		rules = append(rules,
			"-d "+c+" -j RETURN",
			"-m conntrack --ctorigdst "+ctAddr(c)+" -j RETURN",
		)
	}
	for _, a := range normalizeCIDRs(s.HostPublicAddrs) {
		for _, p := range sortedPorts(s.PublicIngressPorts) {
			rules = append(rules,
				"-p tcp -m conntrack --ctorigdst "+ctAddr(a)+" --ctorigdstport "+strconv.Itoa(p)+" -j RETURN")
		}
	}
	rules = append(rules, "-m addrtype --dst-type LOCAL -j DROP")
	for _, c := range s.DenyCIDRs() {
		rules = append(rules,
			"-d "+c+" -j DROP",
			"-m conntrack --ctorigdst "+ctAddr(c)+" -j DROP",
		)
	}
	for _, a := range normalizeCIDRs(s.HostPublicAddrs) {
		rules = append(rules,
			"-d "+a+" -j DROP",
			"-m conntrack --ctorigdst "+ctAddr(a)+" -j DROP",
		)
	}
	return rules
}

// ctAddr renders a CIDR the way `-m conntrack` prints it: a single host
// has no prefix length, a network keeps it.
func ctAddr(cidr string) string {
	return strings.TrimSuffix(cidr, "/32")
}

// guestDenyV6Rules is the IPv6 chain body for both tables: guests get no
// IPv6 configuration, the host never initiates IPv6 to them, so there is
// nothing to exempt.
func guestDenyV6Rules() []string {
	return []string{"-j DROP"}
}

// guestDenyJumpBody is the hook rule body (without "-A HOOK ") for a
// jump tagged with comment. Every inserted copy carries its own nonce
// (guestDenyJumpTag) so stale copies can be removed by exact spec rather
// than by position: positions shift under us whenever another agent,
// kube-router or a VM create inserts at 1, and a positional delete would
// then remove a stranger's rule.
func guestDenyJumpBody(chain, comment string) string {
	return "-i " + guestIfaceWildcard + " -m comment --comment " + comment + " -j " + chain
}

// guestDenyJumpTag returns a fresh, unique comment for one jump copy.
// Only [A-Za-z0-9-] so `iptables -S` prints it unquoted.
func guestDenyJumpTag() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return guestDenyTag + "-" + hex.EncodeToString(b[:])
}

// guestDenyJump is one installed copy of the hook jump.
type guestDenyJump struct {
	pos     int    // 1-based position in the hook
	comment string // the exact comment it carries
}

// guestDenyJumps returns every copy of the jump into chain found in an
// `-S hook` dump, in order. A copy is a rule of exactly our shape whose
// comment starts with guestDenyTag.
func guestDenyJumps(dump, hook, chain string) []guestDenyJump {
	prefix := "-A " + hook + " -i " + guestIfaceWildcard + " -m comment --comment "
	suffix := " -j " + chain
	var out []guestDenyJump
	n := 0
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-A "+hook+" ") {
			continue
		}
		n++
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
			continue
		}
		comment := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
		if comment == "" || strings.ContainsAny(comment, " \"") || !strings.HasPrefix(comment, guestDenyTag) {
			continue
		}
		out = append(out, guestDenyJump{pos: n, comment: comment})
	}
	return out
}

// guestDenyJumpFirst reports whether a copy of the jump into chain is the
// first rule of hook.
func guestDenyJumpFirst(dump, hook, chain string) bool {
	j := guestDenyJumps(dump, hook, chain)
	return len(j) > 0 && j[0].pos == 1
}

// guestDenyRestorePayload renders one chain for iptables-restore --noflush.
// Declaring the chain flushes it when it exists and creates it when it
// does not; the whole file commits as one transaction, so there is never
// a window with the chain half-filled or empty while a hook jumps to it.
func guestDenyRestorePayload(table, chain string, rules []string) string {
	var b strings.Builder
	b.WriteString("*" + table + "\n")
	b.WriteString(":" + chain + " - [0:0]\n")
	for _, r := range rules {
		b.WriteString("-A " + chain + " " + r + "\n")
	}
	b.WriteString("COMMIT\n")
	return b.String()
}

// chainRuleBodies extracts the rule bodies of chain from an `-S chain`
// dump, in order.
func chainRuleBodies(dump, chain string) []string {
	prefix := "-A " + chain + " "
	var out []string
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			out = append(out, strings.TrimPrefix(line, prefix))
		}
	}
	return out
}

// rulePositions returns the 1-based positions in hook (from an `-S hook`
// dump) of every rule whose body equals body.
func rulePositions(dump, hook, body string) []int {
	prefix := "-A " + hook + " "
	var out []int
	n := 0
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		n++
		if strings.TrimPrefix(line, prefix) == body {
			out = append(out, n)
		}
	}
	return out
}

// firstRulePosition returns the 1-based position of the first rule in
// hook that contains all fragments, or 0.
func firstRulePosition(dump, hook string, fragments []string) int {
	prefix := "-A " + hook + " "
	n := 0
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		n++
		ok := true
		for _, f := range fragments {
			if !strings.Contains(line, f) {
				ok = false
				break
			}
		}
		if ok {
			return n
		}
	}
	return 0
}

// guestDenyPrecedes reports whether a deny jump into chain sits ahead of
// the first rule matching vmFragments in hook. A missing deny jump is
// never "preceding".
func guestDenyPrecedes(dump, hook, chain string, vmFragments []string) bool {
	deny := guestDenyJumps(dump, hook, chain)
	if len(deny) == 0 {
		return false
	}
	vm := firstRulePosition(dump, hook, vmFragments)
	if vm == 0 {
		// Nothing of the VM's to precede; the deny is in place.
		return true
	}
	return deny[0].pos < vm
}

// ---- host detection ---------------------------------------------------

// hostAddr is one address on one interface, as DetectGuestDenySpec sees it.
type hostAddr struct {
	Iface string
	IP    net.IP
}

// hostRoute is one entry of `ip -4 route show`.
type hostRoute struct {
	Dst *net.IPNet
	Dev string
}

// DetectGuestDenySpec builds the spec from this host: environment,
// interface addresses and the IPv4 routing table.
func DetectGuestDenySpec() GuestDenySpec {
	return buildGuestDenySpec(os.LookupEnv, listHostAddrs(), listHostRoutes(), hostHasIPv6())
}

// buildGuestDenySpec is the pure core of DetectGuestDenySpec. env is
// os.LookupEnv-shaped so "unset" and "set to empty" stay distinct.
func buildGuestDenySpec(lookup func(string) (string, bool), addrs []hostAddr, routes []hostRoute, ipv6 bool) GuestDenySpec {
	spec := GuestDenySpec{IPv6: ipv6}
	env := func(key string) string {
		v, _ := lookup(key)
		return v
	}

	// Cluster CIDRs: explicit config wins, else routes on CNI interfaces
	// plus the API server ClusterIP every pod knows.
	if raw := strings.TrimSpace(env(envClusterCIDRs)); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if c, ok := parseCIDROrIP(item); ok {
				spec.ClusterCIDRs = append(spec.ClusterCIDRs, c)
			} else {
				spec.Warnings = append(spec.Warnings, envClusterCIDRs+": ignoring unparseable entry "+strconv.Quote(item))
			}
		}
	} else {
		for _, r := range routes {
			if r.Dst == nil || !cniRouteInterfaces[r.Dev] {
				continue
			}
			spec.ClusterCIDRs = append(spec.ClusterCIDRs, r.Dst.String())
		}
		if h := strings.TrimSpace(env("KUBERNETES_SERVICE_HOST")); h != "" {
			if c, ok := parseCIDROrIP(h); ok {
				spec.ClusterCIDRs = append(spec.ClusterCIDRs, c)
			}
		}
	}
	spec.ClusterCIDRs = normalizeCIDRs(spec.ClusterCIDRs)

	// Operator override.
	if raw := strings.TrimSpace(env(envGuestAllowPrivateCIDRs)); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if c, ok := parseCIDROrIP(item); ok {
				spec.AllowCIDRs = append(spec.AllowCIDRs, c)
			} else {
				spec.Warnings = append(spec.Warnings, envGuestAllowPrivateCIDRs+": ignoring unparseable entry "+strconv.Quote(item))
			}
		}
		spec.AllowCIDRs = normalizeCIDRs(spec.AllowCIDRs)
	}

	// Public ingress ports.
	spec.PublicIngressPorts = append([]int(nil), guestDenyDefaultIngressPorts...)
	if raw, set := lookup(envGuestPublicIngressPorts); set {
		spec.PublicIngressPorts = nil
		raw = strings.TrimSpace(raw)
		if raw != "" && !strings.EqualFold(raw, "none") {
			for _, item := range strings.Split(raw, ",") {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				p, err := strconv.Atoi(item)
				if err != nil || p <= 0 || p > guestDenyMaxPort {
					spec.Warnings = append(spec.Warnings, envGuestPublicIngressPorts+": ignoring invalid port "+strconv.Quote(item))
					continue
				}
				spec.PublicIngressPorts = append(spec.PublicIngressPorts, p)
			}
		}
	}
	spec.PublicIngressPorts = sortedPorts(spec.PublicIngressPorts)

	// Host public addresses: every IPv4 the host owns that the deny list
	// does not already cover. TAP gateways are skipped so the chain does
	// not churn on every VM create (addrtype LOCAL covers them anyway).
	denied := parseCIDRList(spec.DenyCIDRs())
	seen := map[string]bool{}
	for _, a := range addrs {
		ip := a.IP.To4()
		if ip == nil || strings.HasPrefix(a.Iface, "tap-") || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		if containedIn(ip, denied) {
			continue
		}
		key := ip.String() + "/32"
		if !seen[key] {
			seen[key] = true
			spec.HostPublicAddrs = append(spec.HostPublicAddrs, key)
		}
	}
	spec.HostPublicAddrs = normalizeCIDRs(spec.HostPublicAddrs)
	return spec
}

func listHostAddrs() []hostAddr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []hostAddr
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil {
				out = append(out, hostAddr{Iface: ifc.Name, IP: ip})
			}
		}
	}
	return out
}

func listHostRoutes() []hostRoute {
	out, err := exec.Command("ip", "-4", "route", "show").Output()
	if err != nil {
		return nil
	}
	return parseRouteTable(string(out))
}

// parseRouteTable parses `ip -4 route show` output into prefixes and
// their devices. The default route and host routes without a prefix
// length are kept as-is (the latter as /32).
func parseRouteTable(text string) []hostRoute {
	var out []hostRoute
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "default" {
			continue
		}
		dst, ok := parseCIDROrIP(fields[0])
		if !ok {
			continue
		}
		_, n, err := net.ParseCIDR(dst)
		if err != nil {
			continue
		}
		r := hostRoute{Dst: n}
		for i, f := range fields {
			if f == "dev" && i+1 < len(fields) {
				r.Dev = fields[i+1]
			}
		}
		out = append(out, r)
	}
	return out
}

func hostHasIPv6() bool {
	_, err := os.Stat("/proc/net/if_inet6")
	return err == nil
}

// ---- helpers -----------------------------------------------------------

// parseCIDROrIP accepts "a.b.c.d/n" or "a.b.c.d" (as /32), IPv4 only, and
// returns the canonical network string.
func parseCIDROrIP(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil {
			return "", false
		}
		return ip.To4().String() + "/32", true
	}
	_, n, err := net.ParseCIDR(s)
	if err != nil || n.IP.To4() == nil {
		return "", false
	}
	return n.String(), true
}

// normalizeCIDRs canonicalises, de-duplicates and sorts; unparseable
// entries are dropped.
func normalizeCIDRs(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		c, ok := parseCIDROrIP(s)
		if !ok || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _, _ := net.ParseCIDR(out[i])
		b, _, _ := net.ParseCIDR(out[j])
		if c := bytes.Compare(a.To4(), b.To4()); c != 0 {
			return c < 0
		}
		return out[i] < out[j]
	})
	return out
}

func parseCIDRList(in []string) []*net.IPNet {
	var out []*net.IPNet
	for _, s := range in {
		if _, n, err := net.ParseCIDR(s); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func containedIn(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func sortedPorts(ports []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, p := range ports {
		if p <= 0 || p > guestDenyMaxPort || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func portList(ports []int) string {
	ps := sortedPorts(ports)
	strs := make([]string, len(ps))
	for i, p := range ps {
		strs[i] = strconv.Itoa(p)
	}
	return strings.Join(strs, ",")
}

// ---- installation ------------------------------------------------------

// iptFamily is one address family's tool set.
type iptFamily struct {
	bin     string
	restore string
}

var (
	ipt4 = iptFamily{bin: "iptables", restore: "iptables-restore"}
	ipt6 = iptFamily{bin: "ip6tables", restore: "ip6tables-restore"}
)

func (f iptFamily) run(args ...string) error {
	full := append([]string{"-w", strconv.Itoa(iptablesWaitSeconds)}, args...)
	return runCmd(f.bin, full...)
}

// dump returns `-t table -S chain`. A chain that does not exist yields
// ("", false, nil) so callers can treat it as empty.
//
// A missing chain is not recognised from the error text: builds disagree
// on it. Legacy says "No chain/target/match by that name"; Alpine's
// iptables-nft 1.8.10 on a host that also has legacy tables says
// "iptables: Incompatible with this kernel." for a chain that simply is
// not there. Instead, when `-S chain` fails, list the whole table (which
// works on every backend) and ask whether it declares the chain. Only a
// table listing that succeeds and lacks the chain counts as "missing";
// anything else stays an error, so the guard still fails closed.
func (f iptFamily) dump(table, chain string) (string, bool, error) {
	args := []string{"-w", strconv.Itoa(iptablesWaitSeconds), "-t", table, "-S", chain}
	out, err := exec.Command(f.bin, args...).CombinedOutput()
	if err == nil {
		return string(out), true, nil
	}
	tableArgs := []string{"-w", strconv.Itoa(iptablesWaitSeconds), "-t", table, "-S"}
	if all, terr := exec.Command(f.bin, tableArgs...).Output(); terr == nil && !tableDeclaresChain(string(all), chain) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("%s %s: %w (%s)", f.bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
}

// tableDeclaresChain reports whether an `iptables -t table -S` dump
// declares chain, as a user chain (-N) or a built-in policy (-P).
func tableDeclaresChain(dump, chain string) bool {
	for _, line := range strings.Split(dump, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "-N" || fields[0] == "-P") && fields[1] == chain {
			return true
		}
	}
	return false
}

func (f iptFamily) restoreChain(table, chain string, rules []string) error {
	payload := guestDenyRestorePayload(table, chain, rules)
	cmd := exec.Command(f.restore, "-w", strconv.Itoa(iptablesWaitSeconds), "-n")
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s -n (%s/%s): %w (%s)", f.restore, table, chain, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureChain makes table/chain contain exactly rules. Returns whether
// anything was written.
func (f iptFamily) ensureChain(table, chain string, rules []string) (bool, error) {
	dump, exists, err := f.dump(table, chain)
	if err != nil {
		return false, err
	}
	if exists && guestDenyEqualStrings(chainRuleBodies(dump, chain), rules) {
		return false, nil
	}
	if err := f.restoreChain(table, chain, rules); err != nil {
		return true, err
	}
	dump, exists, err = f.dump(table, chain)
	if err != nil {
		return true, err
	}
	if !exists {
		return true, fmt.Errorf("%s: chain %s/%s missing after restore", f.bin, table, chain)
	}
	if got := chainRuleBodies(dump, chain); !guestDenyEqualStrings(got, rules) {
		return true, fmt.Errorf("%s: chain %s/%s did not persist as written\nwant:\n  %s\ngot:\n  %s",
			f.bin, table, chain, strings.Join(rules, "\n  "), strings.Join(got, "\n  "))
	}
	return true, nil
}

// ensureJumpFirst makes a tagged jump from hook to chain the first rule
// of hook. If no copy is first, a new copy with a fresh nonce is inserted
// at 1 before anything else happens, so there is never a window without
// a leading jump. Every other copy is then removed by its exact spec,
// never by position: between the dump and the delete, another agent on
// the node, kube-router or a VM create may insert at 1 and shift every
// index, and a positional delete would remove whatever moved into the
// slot. A copy that vanished meanwhile (the other agent removed it) is
// not an error. Returns whether anything was written.
func (f iptFamily) ensureJumpFirst(table, hook, chain string) (bool, error) {
	dump, _, err := f.dump(table, hook)
	if err != nil {
		return false, err
	}
	copies := guestDenyJumps(dump, hook, chain)
	changed := false
	if len(copies) == 0 || copies[0].pos != 1 {
		body := guestDenyJumpBody(chain, guestDenyJumpTag())
		args := append([]string{"-t", table, "-I", hook, "1"}, strings.Fields(body)...)
		if err := f.run(args...); err != nil {
			return true, fmt.Errorf("insert jump %s/%s -> %s: %w", table, hook, chain, err)
		}
		changed = true
		if dump, _, err = f.dump(table, hook); err != nil {
			return true, err
		}
		copies = guestDenyJumps(dump, hook, chain)
	}
	if len(copies) == 0 || copies[0].pos != 1 {
		return changed, fmt.Errorf("%s: jump %s/%s -> %s is not the first rule after insert (copies %v)", f.bin, table, hook, chain, copies)
	}
	// Keep the leading copy; delete the rest by spec. Two agents doing
	// this concurrently see the same leading copy and delete the same
	// others, so they converge instead of fighting.
	for _, c := range copies[1:] {
		args := append([]string{"-t", table, "-D", hook}, strings.Fields(guestDenyJumpBody(chain, c.comment))...)
		if err := f.run(args...); err != nil {
			// Already gone is fine; anything else is reported after we
			// confirm the leading copy is still in place.
			if dump, _, derr := f.dump(table, hook); derr == nil && !hasGuestDenyJump(dump, hook, chain, c.comment) {
				continue
			}
			return true, fmt.Errorf("delete stale jump %s/%s (%s): %w", table, hook, c.comment, err)
		}
		changed = true
	}
	if changed {
		if dump, _, err = f.dump(table, hook); err != nil {
			return true, err
		}
		if !guestDenyJumpFirst(dump, hook, chain) {
			return true, fmt.Errorf("%s: jump %s/%s -> %s is not the first rule after re-assert", f.bin, table, hook, chain)
		}
	}
	return changed, nil
}

func hasGuestDenyJump(dump, hook, chain, comment string) bool {
	for _, c := range guestDenyJumps(dump, hook, chain) {
		if c.comment == comment {
			return true
		}
	}
	return false
}

// guestDenyLayout lists, per family, the chains to install and the hooks
// that must jump to them first.
type guestDenyLayer struct {
	table string
	chain string
	hooks []string
	rules []string
}

func guestDenyLayers(spec GuestDenySpec) map[iptFamily][]guestDenyLayer {
	layers := map[iptFamily][]guestDenyLayer{
		ipt4: {
			{table: "mangle", chain: guestDenyMangleChain, hooks: []string{"PREROUTING"}, rules: guestDenyMangleRules(spec)},
			{table: "filter", chain: guestDenyFilterChain, hooks: []string{"FORWARD", "INPUT"}, rules: guestDenyFilterRules(spec)},
		},
	}
	if spec.IPv6 {
		layers[ipt6] = []guestDenyLayer{
			{table: "mangle", chain: guestDenyMangleChain, hooks: []string{"PREROUTING"}, rules: guestDenyV6Rules()},
			{table: "filter", chain: guestDenyFilterChain, hooks: []string{"FORWARD", "INPUT"}, rules: guestDenyV6Rules()},
		}
	}
	return layers
}

// EnsureGuestDeny installs or repairs the guest deny chains and their
// hook jumps for spec. Idempotent and safe to run concurrently from two
// agents on one node. Returns whether anything was written. Any error
// means the guard is NOT known to be in place; callers creating VMs must
// fail.
func EnsureGuestDeny(spec GuestDenySpec) (bool, error) {
	changed := false
	for _, fam := range []iptFamily{ipt4, ipt6} {
		layers := guestDenyLayers(spec)[fam]
		for _, l := range layers {
			c, err := fam.ensureChain(l.table, l.chain, l.rules)
			changed = changed || c
			if err != nil {
				return changed, fmt.Errorf("guest deny: %w", err)
			}
			for _, hook := range l.hooks {
				c, err := fam.ensureJumpFirst(l.table, hook, l.chain)
				changed = changed || c
				if err != nil {
					return changed, fmt.Errorf("guest deny: %w", err)
				}
			}
		}
	}
	return changed, nil
}

// reassertGuestDenyJumps puts the IPv4 hook jumps back at position 1.
// Called after a VM inserts its own position-1 rules so the deny stays
// ahead of every per-VM ACCEPT.
func reassertGuestDenyJumps() error {
	for _, l := range guestDenyLayers(GuestDenySpec{})[ipt4] {
		for _, hook := range l.hooks {
			if _, err := ipt4.ensureJumpFirst(l.table, hook, l.chain); err != nil {
				return err
			}
		}
	}
	return nil
}

// MaintainGuestDeny installs the guard now and re-asserts it every
// interval until ctx is done, re-detecting the host each time so a new
// address or route is picked up. report is called after every attempt
// with nil or the error, so the agent can take itself out of readiness
// while the guard is not known to be in place; the per-VM path
// (configureIptables) fails closed independently.
func MaintainGuestDeny(ctx context.Context, interval time.Duration, report func(error)) {
	if report == nil {
		report = func(error) {}
	}
	assert := func() {
		spec := DetectGuestDenySpec()
		for _, w := range spec.Warnings {
			logger.WithFields("warning", w).Warn("firecracker_network: guest deny config")
		}
		changed, err := EnsureGuestDeny(spec)
		recordGuestDenyAssert(err)
		report(err)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("firecracker_network: guest deny guard NOT in place")
			return
		}
		if changed {
			logger.WithFields(
				"deny_cidrs", strings.Join(spec.DenyCIDRs(), ","),
				"host_public_addrs", strings.Join(spec.HostPublicAddrs, ","),
				"allow_cidrs", strings.Join(spec.AllowCIDRs, ","),
				"public_ingress_ports", portList(spec.PublicIngressPorts),
				"ipv6", spec.IPv6,
			).Info("firecracker_network: guest deny guard installed")
		}
	}
	assert()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			assert()
		}
	}
}

// recordGuestDenyAssert updates the guard metrics after an assert.
func recordGuestDenyAssert(err error) {
	if err != nil {
		guestDenyGuardOK.Set(0)
		guestDenyGuardAsserts.WithLabelValues("error").Inc()
		return
	}
	guestDenyGuardOK.Set(1)
	guestDenyGuardAsserts.WithLabelValues("ok").Inc()
}

func guestDenyEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
