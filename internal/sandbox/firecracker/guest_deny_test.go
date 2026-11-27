package firecracker

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func mapEnv(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func testSpec() GuestDenySpec {
	return GuestDenySpec{
		ClusterCIDRs:       []string{"10.42.0.0/16", "10.43.0.0/16"},
		HostPublicAddrs:    []string{"192.0.2.10/32"},
		AllowCIDRs:         []string{"192.168.77.0/24"},
		PublicIngressPorts: []int{443, 80},
		IPv6:               true,
	}
}

// verdictIndex returns the index of the first rule carrying target.
func verdictIndex(rules []string, target string) int {
	for i, r := range rules {
		if strings.HasSuffix(r, " "+target) {
			return i
		}
	}
	return -1
}

func lastVerdictIndex(rules []string, target string) int {
	last := -1
	for i, r := range rules {
		if strings.HasSuffix(r, " "+target) {
			last = i
		}
	}
	return last
}

func TestGuestDenyRules_EveryReturnPrecedesEveryDrop(t *testing.T) {
	for name, rules := range map[string][]string{
		"mangle": guestDenyMangleRules(testSpec()),
		"filter": guestDenyFilterRules(testSpec()),
	} {
		firstDrop := verdictIndex(rules, "-j DROP")
		lastReturn := lastVerdictIndex(rules, "-j RETURN")
		if firstDrop < 0 || lastReturn < 0 {
			t.Fatalf("%s: expected both RETURN and DROP rules, got %v", name, rules)
		}
		// The filter chain's INVALID drop is the one deliberate exception:
		// it sits right after the established RETURN and before any
		// address RETURN, so nothing untracked can satisfy ctorigdst.
		for i, r := range rules {
			if strings.HasSuffix(r, "-j DROP") && i < lastReturn && !strings.Contains(r, "--ctstate INVALID") {
				t.Fatalf("%s: DROP %q at %d precedes RETURN at %d", name, r, i, lastReturn)
			}
		}
		for _, r := range rules {
			if !strings.HasSuffix(r, "-j DROP") && !strings.HasSuffix(r, "-j RETURN") {
				t.Fatalf("%s: unexpected verdict in %q", name, r)
			}
		}
	}
}

func TestGuestDenyRules_CoverEveryRequiredRange(t *testing.T) {
	required := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
		"10.42.0.0/16", "10.43.0.0/16", // cluster CIDRs from the spec
		"192.0.2.10/32", // the node's own public address
	}
	mangle := guestDenyMangleRules(testSpec())
	filter := guestDenyFilterRules(testSpec())
	for _, cidr := range required {
		if !containsRule(mangle, "-d "+cidr+" -j DROP") {
			t.Errorf("mangle chain does not drop %s", cidr)
		}
		if !containsRule(filter, "-d "+cidr+" -j DROP") {
			t.Errorf("filter chain does not drop %s by destination", cidr)
		}
		if !containsRule(filter, "-m conntrack --ctorigdst "+ctAddr(cidr)+" -j DROP") {
			t.Errorf("filter chain does not drop %s by original destination (post-DNAT)", cidr)
		}
	}
	for _, rules := range [][]string{mangle, filter} {
		if !containsRule(rules, "-m addrtype --dst-type LOCAL -j DROP") {
			t.Errorf("host-local destinations are not dropped: %v", rules)
		}
		if rules[0] != "-m conntrack --ctstate RELATED,ESTABLISHED -j RETURN" {
			t.Errorf("first rule must exempt established flows, got %q", rules[0])
		}
	}
}

func TestGuestDenyRules_DNSOnlyToOwnGateway(t *testing.T) {
	for name, rules := range map[string][]string{
		"mangle": guestDenyMangleRules(testSpec()),
		"filter": guestDenyFilterRules(testSpec()),
	} {
		for _, proto := range []string{"udp", "tcp"} {
			want := "-p " + proto + " -m " + proto + " --dport 53 -m addrtype --dst-type LOCAL --limit-iface-in -j RETURN"
			if !containsRule(rules, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
		// No DNS exemption may be wider than the incoming interface's own
		// address, otherwise a guest can use a peer VM's allow-mode resolver.
		for _, r := range rules {
			if strings.Contains(r, "--dport 53") && !strings.Contains(r, "--limit-iface-in") {
				t.Errorf("%s: DNS exemption not limited to the incoming interface: %q", name, r)
			}
		}
	}
}

func TestGuestDenyRules_OperatorOverrideSitsBetweenReturnsAndDrops(t *testing.T) {
	rules := guestDenyMangleRules(testSpec())
	override := verdictIndex(rules, "-j RETURN")
	for i, r := range rules {
		if r == "-d 192.168.77.0/24 -j RETURN" {
			override = i
		}
	}
	if override < 0 || rules[override] != "-d 192.168.77.0/24 -j RETURN" {
		t.Fatalf("operator override missing: %v", rules)
	}
	if override < 3 {
		t.Fatalf("override must come after the established and DNS returns, got index %d", override)
	}
	if firstDrop := verdictIndex(rules, "-j DROP"); firstDrop < override {
		t.Fatalf("override at %d is after the first DROP at %d", override, firstDrop)
	}
	// The override is the only thing that can reopen 192.168/16; the
	// covering DROP is still there behind it.
	if !containsRule(rules, "-d 192.168.0.0/16 -j DROP") {
		t.Fatalf("override removed the covering drop: %v", rules)
	}

	filter := guestDenyFilterRules(testSpec())
	if !containsRule(filter, "-m conntrack --ctorigdst 192.168.77.0/24 -j RETURN") {
		t.Fatalf("filter chain does not honour the override post-DNAT: %v", filter)
	}
}

func TestGuestDenyRules_PublicIngressExceptionIsNarrow(t *testing.T) {
	spec := testSpec()
	mangle := guestDenyMangleRules(spec)
	want := "-d 192.0.2.10/32 -p tcp -m multiport --dports 80,443 -j RETURN"
	idx := -1
	for i, r := range mangle {
		if r == want {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("public ingress exception missing from %v", mangle)
	}
	localDrop := -1
	for i, r := range mangle {
		if r == "-m addrtype --dst-type LOCAL -j DROP" {
			localDrop = i
		}
	}
	if idx > localDrop {
		t.Fatalf("ingress exception at %d must precede the LOCAL drop at %d", idx, localDrop)
	}
	filter := guestDenyFilterRules(spec)
	for _, p := range []string{"80", "443"} {
		if !containsRule(filter, "-p tcp -m conntrack --ctorigdst 192.0.2.10 --ctorigdstport "+p+" -j RETURN") {
			t.Errorf("filter chain lacks the post-DNAT ingress exception for port %s: %v", p, filter)
		}
	}

	// Disabled: no exception at all, the public address is simply dropped.
	spec.PublicIngressPorts = nil
	for _, r := range guestDenyMangleRules(spec) {
		if strings.Contains(r, "multiport") {
			t.Fatalf("ingress exception present with no ports configured: %q", r)
		}
	}
	if !containsRule(guestDenyMangleRules(spec), "-d 192.0.2.10/32 -j DROP") {
		t.Fatalf("public address not dropped when the exception is disabled")
	}
	for _, r := range guestDenyFilterRules(spec) {
		if strings.Contains(r, "ctorigdstport") {
			t.Fatalf("filter ingress exception present with no ports configured: %q", r)
		}
	}
}

func TestGuestDenyRules_IPv6DropsEverything(t *testing.T) {
	if got := guestDenyV6Rules(); !reflect.DeepEqual(got, []string{"-j DROP"}) {
		t.Fatalf("v6 rules = %v", got)
	}
	layers := guestDenyLayers(testSpec())
	if len(layers[ipt6]) != 2 {
		t.Fatalf("expected mangle+filter v6 layers, got %v", layers[ipt6])
	}
	spec := testSpec()
	spec.IPv6 = false
	if _, ok := guestDenyLayers(spec)[ipt6]; ok {
		t.Fatalf("v6 layers present with IPv6 disabled")
	}
}

func TestGuestDenyLayers_HooksAndJumpWildcard(t *testing.T) {
	layers := guestDenyLayers(testSpec())[ipt4]
	hooks := map[string][]string{}
	for _, l := range layers {
		hooks[l.table+"/"+l.chain] = l.hooks
	}
	want := map[string][]string{
		"mangle/" + guestDenyMangleChain: {"PREROUTING"},
		"filter/" + guestDenyFilterChain: {"FORWARD", "INPUT"},
	}
	if !reflect.DeepEqual(hooks, want) {
		t.Fatalf("hooks = %v, want %v", hooks, want)
	}
	// The jump keys on the TAP wildcard; it must match the real naming.
	tap := tapNameForVM("sbx_any")
	if !strings.HasPrefix(tap, strings.TrimSuffix(guestIfaceWildcard, "+")) {
		t.Fatalf("tap %q does not match wildcard %q", tap, guestIfaceWildcard)
	}
	if got := guestDenyJumpBody(guestDenyFilterChain, guestDenyTag); got != "-i tap-+ -m comment --comment evs-guest-deny-v1 -j EVS-GUEST-DENY-FILTER" {
		t.Fatalf("jump body = %q", got)
	}
	// Nonce tags print unquoted only if they stay within [A-Za-z0-9-].
	tag := guestDenyJumpTag()
	if !strings.HasPrefix(tag, guestDenyTag+"-") || len(tag) != len(guestDenyTag)+9 {
		t.Fatalf("jump tag = %q", tag)
	}
	for _, r := range tag {
		if !(r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')) {
			t.Fatalf("jump tag %q contains %q, which iptables -S would quote", tag, r)
		}
	}
	if guestDenyJumpTag() == tag {
		t.Fatalf("jump tags must be unique per copy")
	}
	for _, chain := range []string{guestDenyMangleChain, guestDenyFilterChain} {
		if len(chain) > 28 {
			t.Fatalf("chain name %q exceeds the iptables limit", chain)
		}
	}
}

func TestGuestDenyRestorePayload(t *testing.T) {
	got := guestDenyRestorePayload("mangle", "EVS-GUEST-DENY", []string{"-d 10.0.0.0/8 -j DROP", "-j DROP"})
	want := "*mangle\n:EVS-GUEST-DENY - [0:0]\n-A EVS-GUEST-DENY -d 10.0.0.0/8 -j DROP\n-A EVS-GUEST-DENY -j DROP\nCOMMIT\n"
	if got != want {
		t.Fatalf("payload:\n%s\nwant:\n%s", got, want)
	}
}

func TestChainRuleBodiesAndPositions(t *testing.T) {
	dump := strings.Join([]string{
		"-P FORWARD DROP",
		"-A FORWARD -i tap-abc -j EVS-tap-abc",
		"-A FORWARD -i tap-+ -m comment --comment evs-guest-deny-v1-0a1b2c3d -j EVS-GUEST-DENY-FILTER",
		"-A FORWARD -m comment --comment \"kube-router netpol\" -j KUBE-ROUTER-FORWARD",
		"-A FORWARD -i tap-+ -m comment --comment evs-guest-deny-v1 -j EVS-GUEST-DENY-FILTER",
		"-A FORWARD -i tap-+ -m comment --comment evs-guest-deny-v1-ffff -j EVS-GUEST-DENY",
		"-A FORWARD -i tap-+ -m comment --comment stranger -j EVS-GUEST-DENY-FILTER",
		"-A FORWARD -i tap-+ -m comment --comment \"evs-guest-deny-v1 x\" -j EVS-GUEST-DENY-FILTER",
	}, "\n")
	if got := chainRuleBodies(dump, "FORWARD"); len(got) != 7 || got[0] != "-i tap-abc -j EVS-tap-abc" {
		t.Fatalf("bodies = %v", got)
	}
	pos := rulePositions(dump, "FORWARD", guestDenyJumpBody(guestDenyFilterChain, "evs-guest-deny-v1"))
	if !reflect.DeepEqual(pos, []int{4}) {
		t.Fatalf("positions = %v", pos)
	}
	// Copies: both tagged forms count, a jump to another chain, a
	// stranger's comment and a quoted comment do not.
	copies := guestDenyJumps(dump, "FORWARD", guestDenyFilterChain)
	want := []guestDenyJump{{2, "evs-guest-deny-v1-0a1b2c3d"}, {4, "evs-guest-deny-v1"}}
	if !reflect.DeepEqual(copies, want) {
		t.Fatalf("copies = %v, want %v", copies, want)
	}
	if guestDenyJumpFirst(dump, "FORWARD", guestDenyFilterChain) {
		t.Fatalf("jump reported first although a VM rule precedes it")
	}
	if got := firstRulePosition(dump, "FORWARD", []string{"-i tap-abc", "-j EVS-tap-abc"}); got != 1 {
		t.Fatalf("firstRulePosition = %d", got)
	}
	if got := firstRulePosition(dump, "FORWARD", []string{"-o tap-abc"}); got != 0 {
		t.Fatalf("firstRulePosition for absent rule = %d", got)
	}
}

func TestGuestDenyPrecedes(t *testing.T) {
	deny := "-A FORWARD " + guestDenyJumpBody(guestDenyFilterChain, guestDenyJumpTag())
	vmIn := "-A FORWARD -i tap-abc -j EVS-tap-abc"
	frag := []string{"-i tap-abc", "-j EVS-tap-abc"}
	cases := []struct {
		name string
		dump string
		want bool
	}{
		{"deny first", strings.Join([]string{"-P FORWARD DROP", deny, vmIn}, "\n"), true},
		{"vm first", strings.Join([]string{"-P FORWARD DROP", vmIn, deny}, "\n"), false},
		{"deny missing", strings.Join([]string{"-P FORWARD DROP", vmIn}, "\n"), false},
		{"vm missing", strings.Join([]string{"-P FORWARD DROP", deny}, "\n"), true},
		{"stranger first then deny then vm", strings.Join([]string{"-P FORWARD DROP", "-A FORWARD -j ts-forward", deny, vmIn}, "\n"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := guestDenyPrecedes(tc.dump, "FORWARD", guestDenyFilterChain, frag); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestBuildGuestDenySpec_ConfigWinsAndHostAddrsAreFiltered(t *testing.T) {
	addrs := []hostAddr{
		{"lo", net.ParseIP("127.0.0.1")},
		{"ens3", net.ParseIP("192.0.2.10")},
		{"ens3", net.ParseIP("2001:db8::10")},
		{"cni0", net.ParseIP("10.42.0.1")},
		{"tailscale0", net.ParseIP("100.64.0.1")},
		{"docker0", net.ParseIP("172.17.0.1")},
		{"tap-0123456789a", net.ParseIP("10.0.7.1")},
		{"ens3", net.ParseIP("192.0.2.10")}, // duplicate
		{"eth1", net.ParseIP("203.0.113.9")},
	}
	routes := parseRouteTable(`default via 192.0.2.1 dev ens3 proto dhcp src 192.0.2.10 metric 100
10.42.0.0/24 dev cni0 proto kernel scope link src 10.42.0.1
10.42.1.0/24 via 10.42.1.0 dev flannel.1 onlink
192.0.2.1 dev ens3 proto dhcp scope link src 192.0.2.10 metric 100
172.17.0.0/16 dev docker0 proto kernel scope link src 172.17.0.1 linkdown
`)
	spec := buildGuestDenySpec(mapEnv(map[string]string{
		envClusterCIDRs:            "10.42.0.0/16, 10.43.0.0/16,bogus",
		envGuestAllowPrivateCIDRs:  "192.168.77.0/24,10.9.9.9",
		envGuestPublicIngressPorts: "443",
	}), addrs, routes, true)

	if got, want := spec.ClusterCIDRs, []string{"10.42.0.0/16", "10.43.0.0/16"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ClusterCIDRs = %v, want %v", got, want)
	}
	if got, want := spec.HostPublicAddrs, []string{"192.0.2.10/32", "203.0.113.9/32"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HostPublicAddrs = %v, want %v (private, loopback, v6 and TAP addresses must be filtered)", got, want)
	}
	if got, want := spec.AllowCIDRs, []string{"10.9.9.9/32", "192.168.77.0/24"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AllowCIDRs = %v, want %v", got, want)
	}
	if got, want := spec.PublicIngressPorts, []int{443}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PublicIngressPorts = %v, want %v", got, want)
	}
	if !spec.IPv6 {
		t.Fatalf("IPv6 should be on")
	}
	if len(spec.Warnings) != 1 || !strings.Contains(spec.Warnings[0], "bogus") {
		t.Fatalf("expected one warning about the bogus entry, got %v", spec.Warnings)
	}
	// The deny list is the base set plus the cluster CIDRs, sorted, deduped.
	deny := spec.DenyCIDRs()
	if len(deny) != len(guestDenyBaseCIDRs)+2 {
		t.Fatalf("DenyCIDRs = %v", deny)
	}
	for _, want := range []string{"10.42.0.0/16", "10.43.0.0/16", "100.64.0.0/10"} {
		if !containsRule(deny, want) {
			t.Fatalf("DenyCIDRs missing %s: %v", want, deny)
		}
	}
}

func TestBuildGuestDenySpec_DetectsClusterCIDRsFromRoutesWhenUnconfigured(t *testing.T) {
	routes := parseRouteTable(`default via 192.0.2.1 dev ens3
10.42.0.0/24 dev cni0 proto kernel scope link src 10.42.0.1
10.42.1.0/24 via 10.42.1.0 dev flannel.1 onlink
192.0.2.1 dev ens3 proto dhcp scope link
172.17.0.0/16 dev docker0 proto kernel scope link
`)
	spec := buildGuestDenySpec(mapEnv(map[string]string{"KUBERNETES_SERVICE_HOST": "10.43.0.1"}), nil, routes, false)
	want := []string{"10.42.0.0/24", "10.42.1.0/24", "10.43.0.1/32"}
	if !reflect.DeepEqual(spec.ClusterCIDRs, want) {
		t.Fatalf("ClusterCIDRs = %v, want %v (CNI routes and the API server ClusterIP only; never public host routes)", spec.ClusterCIDRs, want)
	}
	if got, want := spec.PublicIngressPorts, []int{80, 443}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default PublicIngressPorts = %v, want %v", got, want)
	}
	if spec.IPv6 {
		t.Fatalf("IPv6 should follow the host")
	}
}

func TestBuildGuestDenySpec_IngressPortsCanBeDisabled(t *testing.T) {
	for _, raw := range []string{"", "none", " NONE "} {
		spec := buildGuestDenySpec(mapEnv(map[string]string{envGuestPublicIngressPorts: raw}), nil, nil, false)
		if len(spec.PublicIngressPorts) != 0 {
			t.Fatalf("%q: PublicIngressPorts = %v, want none", raw, spec.PublicIngressPorts)
		}
	}
	spec := buildGuestDenySpec(mapEnv(map[string]string{envGuestPublicIngressPorts: "443,70000,abc"}), nil, nil, false)
	if !reflect.DeepEqual(spec.PublicIngressPorts, []int{443}) || len(spec.Warnings) != 2 {
		t.Fatalf("ports = %v warnings = %v", spec.PublicIngressPorts, spec.Warnings)
	}
}

func TestCtAddr(t *testing.T) {
	if got := ctAddr("192.0.2.10/32"); got != "192.0.2.10" {
		t.Fatalf("ctAddr host = %q", got)
	}
	if got := ctAddr("10.0.0.0/8"); got != "10.0.0.0/8" {
		t.Fatalf("ctAddr network = %q", got)
	}
}

func TestNormalizeCIDRs(t *testing.T) {
	got := normalizeCIDRs([]string{"192.168.1.5/24", "10.0.0.0/8", "10.0.0.0/8", "1.2.3.4", "junk", "fe80::1/64", "0.0.0.0/8"})
	want := []string{"0.0.0.0/8", "1.2.3.4/32", "10.0.0.0/8", "192.168.1.0/24"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeCIDRs = %v, want %v", got, want)
	}
}

func containsRule(rules []string, want string) bool {
	for _, r := range rules {
		if r == want {
			return true
		}
	}
	return false
}

// fakeIptables writes a stand-in iptables that answers `-S <chain>` with
// chainOut/chainRC and the bare `-S` table listing with tableOut/tableRC.
func fakeIptables(t *testing.T, chainOut string, chainRC int, tableOut string, tableRC int) iptFamily {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"last=\"\"; for a in \"$@\"; do last=\"$a\"; done\n" +
		"if [ \"$last\" = \"-S\" ]; then printf '%s' \"$TABLE_OUT\"; exit " + strconv.Itoa(tableRC) + "; fi\n" +
		"printf '%s\\n' \"$CHAIN_OUT\" >&2; exit " + strconv.Itoa(chainRC) + "\n"
	bin := filepath.Join(dir, "iptables")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAIN_OUT", chainOut)
	t.Setenv("TABLE_OUT", tableOut)
	return iptFamily{bin: bin}
}

// Prod 2026-10-16: Alpine iptables-nft 1.8.10 on a host that also has
// legacy tables reports a missing chain as "Incompatible with this
// kernel", which the guard used to treat as fatal, so it never created
// its chains and refused every VM create.
func TestGuestDenyDump_MissingChainReportedAsIncompatible(t *testing.T) {
	const incompatible = "# Warning: iptables-legacy tables present, use iptables-legacy to see them\niptables: Incompatible with this kernel."
	table := "-P PREROUTING ACCEPT\n-P INPUT ACCEPT\n-N KUBE-IPTABLES-HINT\n-A PREROUTING -j CONNMARK --restore-mark\n"
	f := fakeIptables(t, incompatible, 1, table, 0)
	out, exists, err := f.dump("mangle", guestDenyMangleChain)
	if err != nil || exists || out != "" {
		t.Fatalf("missing chain: got (%q, %v, %v), want (\"\", false, nil)", out, exists, err)
	}
}

func TestGuestDenyDump_FailsClosedWhenChainExistsOrTableUnreadable(t *testing.T) {
	const incompatible = "iptables: Incompatible with this kernel."
	declared := "-P PREROUTING ACCEPT\n-N " + guestDenyMangleChain + "\n"
	if _, _, err := fakeIptables(t, incompatible, 1, declared, 0).dump("mangle", guestDenyMangleChain); err == nil {
		t.Fatal("chain declared but unreadable must be an error")
	}
	if _, _, err := fakeIptables(t, incompatible, 1, "", 1).dump("mangle", guestDenyMangleChain); err == nil {
		t.Fatal("table listing failed: must be an error")
	}
}

func TestTableDeclaresChain(t *testing.T) {
	dump := "# Warning: iptables-legacy tables present\n-P PREROUTING ACCEPT\n-N EVS-GUEST-DENY\n-A PREROUTING -j EVS-GUEST-DENY-X\n"
	for chain, want := range map[string]bool{"EVS-GUEST-DENY": true, "PREROUTING": true, "EVS-GUEST-DENY-X": false, "EVS": false} {
		if got := tableDeclaresChain(dump, chain); got != want {
			t.Errorf("tableDeclaresChain(%q) = %v, want %v", chain, got, want)
		}
	}
}
