package firecracker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/egress"
)

// vmMTUSafetyBuffer is subtracted from the pod's outbound interface MTU when
// sizing the TAP and the guest's eth0. K8s clusters commonly carry pod
// traffic over VXLAN (Flannel: 50 bytes), Wireguard (60 bytes), or other
// encapsulation. If the guest sends a packet at the pod's nominal MTU,
// it can fail to fit once encapsulated, get silently dropped, and the
// "fragmentation needed" ICMP often doesn't make it back to the originator
// — producing the classic black hole where TCP handshakes succeed but
// data packets stall (curl reports "SSL connection timeout"). 50 bytes is
// the largest encapsulation overhead we expect to see in practice; cuts
// the MTU enough that any reasonable encap fits.
const vmMTUSafetyBuffer = 50

// vmFallbackMTU is what we use when /sys/class/net/<iface>/mtu can't be
// read. 1400 is the empirical sweet spot for VXLAN-backed K8s networks
// and is the same value most cloud providers' overlay networks settle on.
const vmFallbackMTU = 1400

// mssTCPOverhead is subtracted from the TAP MTU to derive the MSS value
// for iptables TCPMSS clamping. 60 = 20 (IP) + 20 (TCP base) + 12
// (timestamps, present on virtually all modern stacks) + 8 (safety for
// SACK or other options). The naive MTU-40 formula ignores TCP options;
// with timestamps alone, a full-MSS segment exceeds the TAP MTU by 12
// bytes, hits the DF+PMTU black hole, and TLS hangs after TCP connect.
const mssTCPOverhead = 60

// iptablesWaitSeconds is the value passed to `iptables -w N`, the xtables
// lock wait. Concurrent VM creates on the same agent will race to grab the
// lock; without -w, the loser exits 4 immediately and we ship a half-
// configured VM. 5s is generous given each call takes <50ms.
const iptablesWaitSeconds = 5

// nodeResolvConfPath is where the fcagent DaemonSet mounts the host node's
// /etc/resolv.conf (hostPath, read-only). Preferred over the pod's own
// /etc/resolv.conf because the pod's resolv.conf typically points at the
// cluster CoreDNS ClusterIP — which is unreliable to reach from inside the
// guest after MASQUERADE through the TAP. The node sees real upstreams.
const nodeResolvConfPath = "/host/etc/resolv.conf"

// publicResolverFallbacks is the last-resort upstream for the per-VM DNS
// proxy when the node's resolv.conf produces no usable entries (or the
// hostPath mount isn't present in a dev environment).
var publicResolverFallbacks = []string{"1.1.1.1", "8.8.8.8"}

// NetworkConfig holds the network configuration for a Firecracker VM.
type NetworkConfig struct {
	TapDevice    string
	HostIP       string
	HostIface    string
	GuestIP      string
	GuestMAC     string
	SubnetMask   string
	AllowedHosts []string
	DNSServers   []string
	Mode         sandbox.NetworkMode

	// MTU is the maximum transmission unit applied to both the TAP and
	// the guest's eth0. Sized below the pod's outbound interface MTU so
	// VXLAN / IPIP / Wireguard encapsulation on the K8s overlay never
	// causes a "small packet works, big packet vanishes" black hole.
	MTU int

	// DNSProxy runs on the host TAP IP and gates DNS resolution for the
	// guest in whitelist mode. Nil for allow/deny modes. We keep a handle
	// so CleanupNetwork can stop it when the VM is torn down. Excluded
	// from JSON because it owns a live net.Conn — state recovery
	// restarts the proxy from scratch using the persisted AllowedHosts.
	DNSProxy *egress.DNSProxy `json:"-"`
}

func normalizeNetworkMode(mode sandbox.NetworkMode) (sandbox.NetworkMode, error) {
	switch mode {
	case "", sandbox.NetworkAllow:
		return sandbox.NetworkAllow, nil
	case sandbox.NetworkDeny, sandbox.NetworkWhitelist:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported network mode %q", mode)
	}
}

// GuestResolvers returns the list of nameservers the guest should write into
// /etc/resolv.conf. The per-VM DNS proxy listens on the host TAP IP in both
// Allow and Whitelist modes, so the guest always talks to us rather than
// upstream directly. This is what lets the proxy:
//   - normalize resolution (no leaking cluster CoreDNS ClusterIPs into the
//     guest, which were silently unrouteable after MASQUERADE)
//   - enforce the allowlist in Whitelist mode without depending on the
//     guest cooperating
//   - cache responses
//   - swap upstream resolvers without touching the guest
//
// Deny mode has no networking; the field is returned for completeness but
// no resolv.conf gets written.
func (c *NetworkConfig) GuestResolvers() []string {
	if c == nil {
		return nil
	}
	if c.Mode == sandbox.NetworkDeny {
		return c.DNSServers
	}
	return []string{c.HostIP}
}

// SetupNetwork creates a TAP device and configures iptables for the VM.
func SetupNetwork(vmID string, mode sandbox.NetworkMode, allowedHosts []string, dnsServers []string) (*NetworkConfig, error) {
	normalizedMode, err := normalizeNetworkMode(mode)
	if err != nil {
		return nil, err
	}
	mode = normalizedMode

	if len(dnsServers) == 0 {
		// Read the node's resolv.conf (mounted at /host/etc/resolv.conf
		// by the fcagent DaemonSet), filtering out ClusterIPs and stub
		// resolvers that the per-VM DNS proxy cannot reliably reach.
		// The proxy itself runs inside the pod netns and talks to these
		// upstreams; the guest only ever talks to the proxy on the host
		// TAP IP. See readNodeNameservers for the filter rationale.
		dnsServers = readNodeNameservers()
	}
	hostIface := defaultOutboundInterface()

	tapName := tapNameForVM(vmID)

	// Allocate a /30 subnet from the per-host pool. Fail loudly when
	// the pool is exhausted rather than picking a colliding slot —
	// silently re-allocating a busy subnet was the root cause of
	// "sandbox networking randomly breaks when N other sandboxes are
	// running."
	subnet, allocErr := AllocateSubnet()
	if allocErr != nil {
		return nil, allocErr
	}
	// Release the subnet on any error return so a failed setup
	// doesn't leak the slot. The success path clears the deferred
	// release at the bottom of this function.
	setupOK := false
	defer func() {
		if !setupOK {
			ReleaseSubnet(subnet)
		}
	}()
	hostIP := fmt.Sprintf("10.0.%d.1", subnet)
	guestIP := fmt.Sprintf("10.0.%d.2", subnet)
	guestMAC := generateMAC()

	mtu := computeVMMTU(hostIface)

	cfg := &NetworkConfig{
		TapDevice:    tapName,
		HostIP:       hostIP,
		HostIface:    hostIface,
		GuestIP:      guestIP,
		GuestMAC:     guestMAC,
		SubnetMask:   "/30",
		AllowedHosts: allowedHosts,
		DNSServers:   dnsServers,
		Mode:         mode,
		MTU:          mtu,
	}

	// With hostNetwork: true the TAP device survives fcagent pod
	// replacement (it's in the host netns, not the pod's). If a
	// previous instance created the TAP for this VM and we're
	// re-provisioning after a restart, reuse the existing device
	// instead of failing with "device already exists."
	if _, err := net.InterfaceByName(tapName); err != nil {
		if err := runCmd("ip", "tuntap", "add", tapName, "mode", "tap"); err != nil {
			return nil, fmt.Errorf("failed to create TAP device: %w", err)
		}
		if err := runCmd("ip", "addr", "add", hostIP+"/30", "dev", tapName); err != nil {
			cleanupTap(tapName)
			return nil, fmt.Errorf("failed to assign IP: %w", err)
		}
		if err := runCmd("ip", "link", "set", tapName, "mtu", strconv.Itoa(mtu)); err != nil {
			cleanupTap(tapName)
			return nil, fmt.Errorf("failed to set TAP MTU=%d: %w", mtu, err)
		}
		if err := runCmd("ip", "link", "set", tapName, "up"); err != nil {
			cleanupTap(tapName)
			return nil, fmt.Errorf("failed to bring up TAP: %w", err)
		}
	} else {
		logger.WithFields("tap", tapName, "vm_id", vmID).
			Info("firecracker_network: TAP already exists, reusing (host netns survived restart)")
	}

	// Ensure the host forwards packets between the TAP interface and its
	// outbound interface. This is usually enabled on Kubernetes nodes, but dev
	// Firecracker hosts are often plain VMs where the kernel default is off.
	if err := runCmd("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		cleanupTap(tapName)
		return nil, fmt.Errorf("failed to enable IPv4 forwarding: %w", err)
	}

	// Start the per-VM DNS proxy before iptables so the host is ready to
	// answer queries the moment the guest comes up. Runs in both Allow
	// and Whitelist modes — they share the same network plumbing and
	// differ only in whether the proxy enforces an allowlist (empty
	// AllowedHosts ⇒ allow-all). Deny mode has no networking.
	if mode != sandbox.NetworkDeny {
		proxy, err := startVMDNSProxy(vmID, hostIP, mode, allowedHosts, dnsServers)
		if err != nil {
			cleanupTap(tapName)
			return nil, fmt.Errorf("failed to start DNS proxy: %w", err)
		}
		cfg.DNSProxy = proxy
	}

	// Configure iptables based on network mode. On failure clean up any
	// rules that DID land before the failure point — without this the
	// chain leaks (FORWARD jumps reference it), and the next provision
	// on the same node hits "Chain already exists" + cascading errors
	// every subsequent attempt. The cleanup is best-effort by design;
	// most rules will not exist on early-failure paths and that's fine.
	if err := configureIptables(cfg); err != nil {
		if cfg.DNSProxy != nil {
			cfg.DNSProxy.Stop()
		}
		cleanupIptables(cfg)
		cleanupTap(tapName)
		return nil, fmt.Errorf("failed to configure iptables: %w", err)
	}

	// Dump the resulting state so operators have evidence in the logs
	// when "the sandbox has no network" reports come in. Past incidents
	// have been a mix of CNI rules above ours, missing iptables modules
	// in the agent container, and pod-egress NetworkPolicy — all of
	// which look identical from inside the guest. The snapshot tells
	// us where the gap actually is.
	logNetworkDiagnostics(cfg)

	setupOK = true
	return cfg, nil
}

// logNetworkDiagnostics logs the iptables and routing state after the
// per-VM chain is installed, plus an upstream DNS reachability probe from
// the fcagent's netns. Best-effort and never fails the create — its only
// job is to make troubleshooting possible without shelling into the node.
func logNetworkDiagnostics(cfg *NetworkConfig) {
	if cfg == nil {
		return
	}
	chain := vmChainName(cfg.TapDevice)

	dump := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Sprintf("ERR: %s (%s)", err.Error(), strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out))
	}

	logger.WithFields(
		"tap", cfg.TapDevice,
		"host_ip", cfg.HostIP,
		"guest_ip", cfg.GuestIP,
		"host_iface", cfg.HostIface,
		"mode", string(cfg.Mode),
		"chain_rules", dump("iptables", "-S", chain),
		"forward_jumps", dump("sh", "-c", fmt.Sprintf("iptables -S FORWARD | grep -E '%s' || true", chain)),
		"nat_postrouting", dump("sh", "-c", fmt.Sprintf("iptables -t nat -S POSTROUTING | grep -E '%s/' || true", cfg.GuestIP)),
		"forward_policy", dump("sh", "-c", "iptables -S FORWARD | head -n1"),
		"ip_route", dump("ip", "route"),
		"tap_state", dump("ip", "-br", "addr", "show", cfg.TapDevice),
		"ip_forward_sysctl", dump("sysctl", "-n", "net.ipv4.ip_forward"),
	).Info("firecracker_network: applied per-VM iptables state")

	// DNS reachability probe — does the fcagent's own netns reach the
	// configured upstream resolver? If this fails the guest never had a
	// chance, and the user gets a clean "egress blocked at the pod /
	// node, not at the VM" signal instead of a 20-second timeout inside
	// the VM. Uses the first dns server; falls back to 8.8.8.8.
	upstream := "8.8.8.8:53"
	if len(cfg.DNSServers) > 0 {
		s := strings.TrimSpace(cfg.DNSServers[0])
		if s != "" {
			if !strings.Contains(s, ":") {
				s = s + ":53"
			}
			upstream = s
		}
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer probeCancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(probeCtx, "udp", upstream)
	if err != nil {
		logger.WithFields(
			"tap", cfg.TapDevice,
			"upstream", upstream,
			"error", err.Error(),
		).Warn("firecracker_network: pod-netns cannot reach upstream DNS — egress is blocked above us (NetworkPolicy, node firewall, or missing kernel modules); guest will time out resolving names")
		return
	}
	_ = conn.Close()
	logger.WithFields("tap", cfg.TapDevice, "upstream", upstream).
		Info("firecracker_network: pod-netns can reach upstream DNS")
}

// startVMDNSProxy spins up an egress DNSProxy bound to the host TAP IP on
// port 53 for one VM. The guest is configured (via /etc/resolv.conf) to send
// all DNS queries here. In Whitelist mode an allowlist is enforced (non-
// matching names return NXDOMAIN); in Allow mode the allowlist is empty and
// every query is forwarded upstream. Upstream resolvers come from the node's
// resolv.conf via readNodeNameservers, NOT the pod's resolv.conf, so we
// never push a cluster CoreDNS ClusterIP into a context that can't route
// to it.
func startVMDNSProxy(vmID, hostIP string, mode sandbox.NetworkMode, allowedHosts, dnsServers []string) (*egress.DNSProxy, error) {
	upstream := make([]string, 0, len(dnsServers))
	for _, s := range dnsServers {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, ":") {
			s = s + ":53"
		}
		upstream = append(upstream, s)
	}

	proxy := egress.NewDNSProxy(egress.DNSProxyConfig{
		AllowedHosts:     allowedHosts,
		Upstream:         upstream,
		ListenAddr:       hostIP + ":53",
		SandboxID:        vmID,
		EnforceAllowlist: mode == sandbox.NetworkWhitelist,
	})

	errCh := make(chan error, 1)
	go func() {
		if err := proxy.Start(); err != nil {
			errCh <- err
		}
	}()

	// Give ListenAndServe a brief window to bind. If it errors immediately
	// (port in use, permission denied) we want to surface that to the
	// caller rather than discover it when the first DNS query hangs.
	select {
	case err := <-errCh:
		return nil, err
	case <-time.After(150 * time.Millisecond):
	}

	logger.WithFields("vm_id", vmID, "listen", hostIP+":53", "allowed_hosts", len(allowedHosts)).
		Info("firecracker_network: DNS proxy started for whitelist mode")
	return proxy, nil
}

// CleanupNetwork removes the TAP device and associated iptables rules.
func CleanupNetwork(cfg *NetworkConfig) {
	if cfg == nil {
		return
	}

	// Stop the per-VM DNS proxy before tearing down iptables — once the
	// FORWARD rules are gone the proxy can't service in-flight queries
	// anyway, and we want the listener socket closed before the next
	// VM tries to bind the same hostIP:53.
	if cfg.DNSProxy != nil {
		cfg.DNSProxy.Stop()
	}

	// Remove iptables rules
	cleanupIptables(cfg)

	// Remove TAP device
	cleanupTap(cfg.TapDevice)

	// Return the subnet slot to the allocator. Derive the third
	// octet from HostIP so we don't have to thread it through the
	// NetworkConfig struct (and risk an old persisted state.json
	// missing a new field).
	if n, err := subnetFromHostIP(cfg.HostIP); err == nil {
		ReleaseSubnet(n)
	}
}

// tapNameForVM returns a per-VM TAP device name that fits in IFNAMSIZ (15
// usable chars). The previous scheme — `tap-<vmID>` left-truncated to 15 —
// silently collided whenever the truncation window cut before the unique
// suffix: `tap-sbx_manual_a…` and `tap-sbx_manual_b…` both became
// `tap-sbx_manual_`, and the second `ip tuntap add` failed with "File
// exists." That left the VM running with no network and a misleading
// "failed to setup network, continuing without" warning.
//
// We hash the full vmID instead so the suffix is unique regardless of how
// much shared prefix the IDs carry. 11 hex chars (44 bits) is enough that
// a collision among the thousands of sandboxes a node ever sees is
// astronomically unlikely.
func tapNameForVM(vmID string) string {
	sum := sha256.Sum256([]byte(vmID))
	return "tap-" + hex.EncodeToString(sum[:])[:11]
}

// vmChainName returns the per-VM iptables chain name. All FORWARD rules for
// a given VM live inside this chain, and FORWARD jumps to it at position 1
// so CNI / default-DROP rules above us cannot preempt egress.
func vmChainName(tapDevice string) string {
	// iptables caps chain names at 28 chars; tap names are <=15, so
	// "EVS-<tap>" stays well under the limit.
	return "EVS-" + tapDevice
}

func configureIptables(cfg *NetworkConfig) error {
	if cfg.Mode == sandbox.NetworkDeny {
		return nil // no rules needed
	}

	chain := vmChainName(cfg.TapDevice)

	// Build the per-VM chain up front, then jump into it from FORWARD at
	// position 1. Doing the inner rules inside our own chain means we don't
	// have to fight for position against CNI rules that live in FORWARD —
	// they get one shot at a packet (the jump) and our chain owns the
	// decision from there.
	//
	// If a previous instance with the same TAP name crashed without cleanup,
	// the chain may already exist. Flushing first keeps us idempotent. The
	// flush/delete calls intentionally ignore errors — they fail when the
	// chain doesn't exist (the common case on a fresh VM), which is fine.
	_ = runIptables("-F", chain)
	_ = runIptables("-X", chain)
	if err := runIptables("-N", chain); err != nil {
		return fmt.Errorf("create chain %s: %w", chain, err)
	}

	// Return traffic for established connections is always allowed (both
	// directions of an existing flow).
	if err := runIptables("-A", chain,
		"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("conntrack accept: %w", err)
	}

	// MSS clamping for the VM's TCP traffic — belt-and-braces alongside
	// the explicit MTU. Rewrites the MSS option in TCP SYNs traversing
	// FORWARD so neither end ever advertises a segment size larger than
	// the path can carry. Required: xt_TCPMSS ships in stock Linux
	// netfilter and is loaded automatically on first rule install. If
	// the call fails here, the agent's iptables build is missing the
	// module and *every* TLS-heavy workload in any sandbox on this node
	// will silently break — fail loud so the node gets pulled out of
	// rotation rather than serving broken sandboxes.
	//
	// 60 = 20 IP + 20 TCP + 12 timestamps + 8 safety. The standard
	// formula (MTU-40) ignores TCP options; timestamps alone add 12
	// bytes to every data segment, pushing full-MSS packets past the
	// TAP MTU. The server then sends 1412-byte packets into a 1400-byte
	// path, they hit DF+PMTU black hole, and TLS hangs after connect.
	if cfg.MTU > 0 {
		mss := strconv.Itoa(cfg.MTU - mssTCPOverhead)
		if err := runIptables("-t", "mangle", "-A", "FORWARD",
			"-o", cfg.TapDevice, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
			"-j", "TCPMSS", "--set-mss", mss); err != nil {
			return fmt.Errorf("mss clamp egress: %w", err)
		}
		if err := runIptables("-t", "mangle", "-A", "FORWARD",
			"-i", cfg.TapDevice, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
			"-j", "TCPMSS", "--set-mss", mss); err != nil {
			return fmt.Errorf("mss clamp ingress: %w", err)
		}
	}

	// DNS gate: drop any :53 traffic from the guest that isn't destined
	// for our per-VM proxy. Applied in BOTH Allow and Whitelist modes —
	// the guest must talk to our proxy, never to 8.8.8.8 directly,
	// because the proxy is what enforces the allowlist (in Whitelist) and
	// translates between guest-routable space and the upstream resolvers
	// we picked from the node's resolv.conf (in both). Without this, a
	// well-meaning guest userland with `nameserver 8.8.8.8` baked into
	// the image would either work or silently fail depending on the
	// host's egress firewall — a confusing surface.
	if err := runIptables("-A", chain, "-i", cfg.TapDevice,
		"!", "-d", cfg.HostIP, "-p", "udp", "--dport", "53", "-j", "DROP"); err != nil {
		return fmt.Errorf("dns gate udp: %w", err)
	}
	if err := runIptables("-A", chain, "-i", cfg.TapDevice,
		"!", "-d", cfg.HostIP, "-p", "tcp", "--dport", "53", "-j", "DROP"); err != nil {
		return fmt.Errorf("dns gate tcp: %w", err)
	}

	// SSRF / metadata hardening. The DNS proxy gates only NAME resolution; the
	// general ACCEPT below would otherwise permit direct-IP egress, letting
	// caller-supplied code reach the cloud metadata service (169.254.169.254 ->
	// node IAM credentials) or anything else on the link-local range by IP, with
	// no DNS lookup to intercept. No legitimate sandbox workload needs link-local
	// egress (the guest has a static 10.0.x.2 address, never APIPA), so drop the
	// whole 169.254.0.0/16 range outright. Applied in both allow and whitelist
	// modes (deny has no egress). NOTE: blocking internal RFC1918 ranges is a
	// larger follow-up — it must allow-list the legitimate internal endpoints
	// (the model proxy, cluster DNS path) first, or it breaks them.
	if err := runIptables("-A", chain, "-i", cfg.TapDevice, "-d", "169.254.0.0/16", "-j", "DROP"); err != nil {
		return fmt.Errorf("metadata/link-local block: %w", err)
	}

	// Tenant isolation: drop this guest's traffic to any OTHER VM in the
	// shared 10.0.0.0/16 pool. Every VM gets its own /30 (subnet_alloc.go),
	// but all TAPs live in the host root netns with ip_forward=1, so without
	// this the terminal ACCEPT below would forward a packet from this guest
	// straight to a peer guest's 10.0.<n>.2 — reaching that tenant's
	// UNAUTHENTICATED :8080 agent (exec / file read-write / shell), its SSH,
	// and any port it exposed. That is unauthenticated cross-tenant RCE
	// between co-resident sandboxes.
	//
	// This is the "block internal RFC1918" follow-up flagged above, scoped to
	// the sandbox /16 ONLY (never all of RFC1918) so it cannot break the paths
	// that warning was about: the guest's DNS proxy listens on THIS VM's host
	// TAP .1, reached as a local address (INPUT, not FORWARD — untouched here);
	// the model proxy and cluster DNS resolve to cluster/external IPs outside
	// 10.0.0.0/16. The only thing 10.0.0.0/16 contains is {host TAP .1, guest
	// .2} per /30, and .1 is local — so this rule blocks exactly guest->guest
	// transit and nothing legitimate. Scoped by -i <tap> and placed inside the
	// per-VM chain so it rides the same position-1 FORWARD jump and wins over
	// the node's baseline FORWARD policy. host->guest (fcagent :8080, source is
	// not a TAP) and guest->its-own-host-.1 DNS (INPUT) are unaffected.
	if err := runIptables("-A", chain, "-i", cfg.TapDevice, "-d", "10.0.0.0/16", "-j", "DROP"); err != nil {
		return fmt.Errorf("inter-guest isolation block: %w", err)
	}

	// Whitelist mode: enforcement happens at the DNS layer (proxy returns
	// NXDOMAIN for non-matching names). Allow mode: same code path,
	// empty allowlist means everything resolves. Once a name resolves to
	// an IP, all egress is permitted; the DNS layer is the single
	// chokepoint.
	//
	// Earlier implementations used `iptables -A FORWARD -d <hostname>`
	// which only resolved the hostname at rule-install time — CDN
	// rotations and wildcard entries silently failed, and the trailing
	// DROP rule killed legitimate egress. That was the user-visible
	// "no network" bug; rotating egress enforcement to the DNS layer
	// matches what the Docker backend already does.
	if err := runIptables("-A", chain, "-i", cfg.TapDevice, "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("accept egress: %w", err)
	}

	// INPUT accept for DNS queries from the guest to the host TAP IP
	// (where the proxy listens). Position 1 so a stricter default policy
	// in INPUT can't block us before this rule fires.
	if err := runIptables("-I", "INPUT", "1", "-i", cfg.TapDevice, "-d", cfg.HostIP,
		"-p", "udp", "--dport", "53", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("input accept udp: %w", err)
	}
	if err := runIptables("-I", "INPUT", "1", "-i", cfg.TapDevice, "-d", cfg.HostIP,
		"-p", "tcp", "--dport", "53", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("input accept tcp: %w", err)
	}

	if cfg.Mode == sandbox.NetworkWhitelist && len(cfg.AllowedHosts) > 0 {
		logger.WithFields("tap", cfg.TapDevice, "allowed_hosts", len(cfg.AllowedHosts)).
			Debug("firecracker_network: whitelist gated at DNS proxy, allowing MASQUERADEd egress")
	}

	// Jump into the per-VM chain at the very top of FORWARD for both
	// directions. Position 1 is essential — hosts running CNI (k3s/k8s,
	// flannel, calico) often have DROP rules above us, and `-A FORWARD`
	// would put our jumps at the bottom where they never see a packet.
	// That's why "allow" mode appeared to give no network despite the
	// MASQUERADE being in place.
	if err := runIptables("-I", "FORWARD", "1", "-i", cfg.TapDevice, "-j", chain); err != nil {
		return fmt.Errorf("forward jump in: %w", err)
	}
	if err := runIptables("-I", "FORWARD", "1", "-o", cfg.TapDevice, "-j", chain); err != nil {
		return fmt.Errorf("forward jump out: %w", err)
	}

	// Source-NAT for outbound traffic. Same logic — insert at the top of
	// POSTROUTING so we run before any CNI MASQUERADE that might rewrite
	// our packets onto a different interface than we expect.
	if err := runIptables("-t", "nat", "-I", "POSTROUTING", "1",
		"-o", cfg.HostIface, "-s", cfg.GuestIP+"/32", "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("masquerade: %w", err)
	}

	// Verify all the rules we just installed are actually present. Catches
	// the "iptables exited 0 but the rule didn't land" failure modes that
	// xtables-restore-in-progress, concurrent kube-proxy reconcile, or a
	// missing kernel module can produce. We've eaten this bug before:
	// SetupNetwork returned nil, the VM came up, and curl hung because
	// half the chain was empty. Better to fail loud here.
	if err := verifyIptables(cfg); err != nil {
		return fmt.Errorf("iptables verification failed (rules did not persist): %w", err)
	}

	return nil
}

// verifyIptables re-reads the chains we just wrote to and asserts the
// per-VM rules are present.
//
// We deliberately do NOT use `iptables -C` even though it would be the
// cleanest API: `-C` is over-strict for rules inside user-defined chains
// that match on `-i interface`, and we ate this bug in production —
// every fresh provision failed verification with "Bad rule (does a
// matching rule exist in that chain?)" despite the `-A` having
// succeeded just before. Different iptables builds canonicalize the
// stored form slightly differently than the `-C` lookup, particularly
// around interface matches in user chains. Switching to `iptables -S`
// (which dumps the chain's actual stored rules as add-commands) and
// substring-matching against expected fragments is far more tolerant
// of those quirks while still catching the failure mode that motivated
// verification: an `iptables -A` exit 0 that didn't actually land the
// rule (xtables-restore race, missing kernel module, etc.).
func verifyIptables(cfg *NetworkConfig) error {
	chain := vmChainName(cfg.TapDevice)
	tap := cfg.TapDevice
	host := cfg.HostIP

	// Each entry: (description, table, chain, expected substrings — ALL
	// must appear in some rule line). Substring matching survives
	// iptables canonicalization quirks (e.g. flag-set normalization,
	// implicit IP suffixes, optional table prefixes).
	type chainCheck struct {
		name      string
		table     string // "" for filter
		chain     string
		fragments []string
	}
	checks := []chainCheck{
		{name: "conntrack accept", chain: chain, fragments: []string{"-m conntrack", "RELATED,ESTABLISHED", "-j ACCEPT"}},
		{name: "dns gate udp drop", chain: chain, fragments: []string{"-i " + tap, "! -d " + host, "udp", "--dport 53", "-j DROP"}},
		{name: "dns gate tcp drop", chain: chain, fragments: []string{"-i " + tap, "! -d " + host, "tcp", "--dport 53", "-j DROP"}},
		{name: "metadata block", chain: chain, fragments: []string{"-i " + tap, "-d 169.254.0.0/16", "-j DROP"}},
		{name: "inter-guest isolation block", chain: chain, fragments: []string{"-i " + tap, "-d 10.0.0.0/16", "-j DROP"}},
		{name: "general accept", chain: chain, fragments: []string{"-i " + tap, "-j ACCEPT"}},
		{name: "input accept udp", chain: "INPUT", fragments: []string{"-i " + tap, "-d " + host, "udp", "--dport 53", "-j ACCEPT"}},
		{name: "input accept tcp", chain: "INPUT", fragments: []string{"-i " + tap, "-d " + host, "tcp", "--dport 53", "-j ACCEPT"}},
		{name: "forward jump in", chain: "FORWARD", fragments: []string{"-i " + tap, "-j " + chain}},
		{name: "forward jump out", chain: "FORWARD", fragments: []string{"-o " + tap, "-j " + chain}},
		{name: "masquerade", table: "nat", chain: "POSTROUTING", fragments: []string{"-o " + cfg.HostIface, cfg.GuestIP + "/32", "-j MASQUERADE"}},
	}
	if cfg.MTU > 0 {
		mss := strconv.Itoa(cfg.MTU - mssTCPOverhead)
		checks = append(checks,
			chainCheck{name: "mss clamp egress", table: "mangle", chain: "FORWARD", fragments: []string{"-o " + tap, "tcp", "TCPMSS", "--set-mss " + mss}},
			chainCheck{name: "mss clamp ingress", table: "mangle", chain: "FORWARD", fragments: []string{"-i " + tap, "tcp", "TCPMSS", "--set-mss " + mss}},
		)
	}

	// Cache chain dumps so we don't re-shell-out for every check on the
	// same chain. Multi-tens-of-ms iptables call * 9 checks adds up on
	// the cold-start path.
	dumps := map[string]string{}
	dumpKey := func(table, chain string) string { return table + "|" + chain }
	dumpChain := func(table, chain string) (string, error) {
		k := dumpKey(table, chain)
		if out, ok := dumps[k]; ok {
			return out, nil
		}
		args := []string{}
		if table != "" {
			args = append(args, "-t", table)
		}
		args = append(args, "-S", chain)
		full := append([]string{"-w", strconv.Itoa(iptablesWaitSeconds)}, args...)
		out, err := exec.Command("iptables", full...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("iptables %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		s := string(out)
		dumps[k] = s
		return s, nil
	}

	containsAll := func(line string, fragments []string) bool {
		for _, f := range fragments {
			if !strings.Contains(line, f) {
				return false
			}
		}
		return true
	}

	for _, c := range checks {
		dump, err := dumpChain(c.table, c.chain)
		if err != nil {
			return fmt.Errorf("dump chain %s/%s for %q: %w", c.table, c.chain, c.name, err)
		}
		found := false
		for _, line := range strings.Split(dump, "\n") {
			if containsAll(line, c.fragments) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing %s rule in %s/%s (expected fragments: %s); chain dump:\n%s",
				c.name, c.table, c.chain, strings.Join(c.fragments, " "), strings.TrimSpace(dump))
		}
	}

	return nil
}

func cleanupIptables(cfg *NetworkConfig) {
	if cfg.Mode == sandbox.NetworkDeny {
		return
	}

	chain := vmChainName(cfg.TapDevice)

	// Remove the FORWARD jumps before flushing the chain — iptables
	// refuses to delete a chain that's still referenced. Best-effort:
	// teardown of a half-installed VM may find rules missing, which is
	// fine.
	_ = runIptables("-D", "FORWARD", "-i", cfg.TapDevice, "-j", chain)
	_ = runIptables("-D", "FORWARD", "-o", cfg.TapDevice, "-j", chain)

	// INPUT accepts: now installed in both Allow and Whitelist (was
	// previously Whitelist-only). Clean up regardless of mode.
	_ = runIptables("-D", "INPUT", "-i", cfg.TapDevice, "-d", cfg.HostIP,
		"-p", "udp", "--dport", "53", "-j", "ACCEPT")
	_ = runIptables("-D", "INPUT", "-i", cfg.TapDevice, "-d", cfg.HostIP,
		"-p", "tcp", "--dport", "53", "-j", "ACCEPT")

	_ = runIptables("-t", "nat", "-D", "POSTROUTING",
		"-o", cfg.HostIface, "-s", cfg.GuestIP+"/32", "-j", "MASQUERADE")

	if cfg.MTU > 0 {
		mss := strconv.Itoa(cfg.MTU - mssTCPOverhead)
		_ = runIptables("-t", "mangle", "-D", "FORWARD",
			"-o", cfg.TapDevice, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
			"-j", "TCPMSS", "--set-mss", mss)
		_ = runIptables("-t", "mangle", "-D", "FORWARD",
			"-i", cfg.TapDevice, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
			"-j", "TCPMSS", "--set-mss", mss)
	}

	_ = runIptables("-F", chain)
	_ = runIptables("-X", chain)
}

func cleanupTap(tapName string) {
	_ = runCmd("ip", "link", "del", tapName)
}

// runIptables wraps iptables(8) with the xtables lock wait so concurrent
// VM creates on the same agent don't lose a race to write the table. Old
// pre-2015 iptables don't grok `-w N` — but the fcagent image ships a
// modern build, so this is safe.
func runIptables(args ...string) error {
	full := append([]string{"-w", strconv.Itoa(iptablesWaitSeconds)}, args...)
	return runCmd("iptables", full...)
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// readNodeNameservers returns the upstream resolvers the per-VM DNS proxy
// should forward to. Order of preference:
//
//  1. /host/etc/resolv.conf — mounted hostPath of the node's resolv.conf.
//     This is the *node's* view of DNS, populated by cloud-init / netplan /
//     systemd-resolved. Contains real upstreams (1.1.1.1, the cloud
//     provider's resolver, etc.) — not a ClusterIP.
//  2. /etc/resolv.conf — the fcagent pod's own resolv.conf. Kept as a
//     fallback for dev environments where the hostPath isn't mounted, but
//     normally yields a cluster CoreDNS ClusterIP which we then filter out.
//  3. publicResolverFallbacks — last resort.
//
// Filtering: we drop entries that the proxy cannot reliably reach from
// inside the pod netns: loopback / stub resolvers (127.0.0.0/8 — typically
// systemd-resolved on the host, only reachable from the host itself), and
// k8s service ClusterIPs (kube-proxy DNAT applies in the host root netns
// only, and behaves erratically for MASQUERADE'd traffic transiting a TAP).
//
// The earlier "use cluster CoreDNS" workaround for managed-cluster egress
// firewalls is reversed here: clusters that block public :53 should
// configure FCAGENT_DNS_UPSTREAMS explicitly. The default has to favor
// reliability of "any sandbox can resolve names" over corner-case
// hardening that none of our current deployments need.
func readNodeNameservers() []string {
	// Environment override wins: operators can set a comma-separated
	// list of upstreams when the node's resolv.conf isn't appropriate
	// (CI runners, hardened clusters with internal-only resolvers).
	if env := strings.TrimSpace(os.Getenv("FCAGENT_DNS_UPSTREAMS")); env != "" {
		out := []string{}
		for _, s := range strings.Split(env, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	for _, path := range []string{nodeResolvConfPath, "/etc/resolv.conf"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parsed := parseResolverContent(string(data))
		filtered := filterUnreachableResolvers(parsed)
		if len(filtered) > 0 {
			return filtered
		}
	}

	return append([]string(nil), publicResolverFallbacks...)
}

// filterUnreachableResolvers drops 127/8 (loopback / stub) entries. We do
// NOT filter k8s ClusterIPs (10.43/16, 10.96/12, etc.): the per-VM DNS
// proxy runs inside the pod netns where ClusterIPs are reachable via
// kube-proxy DNAT (PREROUTING applies to pod-originating traffic). An
// earlier version of this code filtered ClusterIPs out as a precaution
// for guests that talk to upstream resolvers directly — but in the
// current design the guest only ever talks to the proxy on the host TAP
// IP, never to upstreams directly. Stripping ClusterIPs from the proxy's
// upstream chain in clusters that block public :53 left the proxy
// unable to resolve names and broke every sandbox provision.
//
// Kept separate so tests can exercise the policy without touching disk.
func filterUnreachableResolvers(servers []string) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		ip := net.ParseIP(strings.TrimSpace(s))
		if ip == nil || ip.To4() == nil {
			continue
		}
		if ip.IsLoopback() {
			continue
		}
		out = append(out, ip.String())
	}
	return out
}

// parseResolverContent extracts IPv4 nameserver entries from a resolv.conf
// blob. Kept separate so tests can exercise it without touching /etc/.
func parseResolverContent(content string) []string {
	var servers []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "nameserver") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ns := strings.TrimSpace(fields[1])
		// IPv4 only — IPv6 cluster-DNS rarely works in dev clusters
		// and the guest resolver will fall through to v4 anyway.
		if ns == "" || strings.Contains(ns, ":") {
			continue
		}
		servers = append(servers, ns)
	}
	return servers
}

// computeVMMTU returns the MTU to use for the TAP and the guest's eth0,
// derived from the pod's outbound interface MTU minus a safety buffer for
// overlay-network encapsulation overhead. Without this, a guest sending at
// 1500-byte MTU into a 1450-byte VXLAN pod network produces the textbook
// black hole: TCP three-way handshake completes on small packets, then
// the first large server response (e.g. a TLS cert payload) gets dropped
// silently and curl reports "SSL connection timeout".
func computeVMMTU(hostIface string) int {
	if hostIface == "" {
		return vmFallbackMTU
	}
	raw, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/mtu", hostIface))
	if err != nil {
		return vmFallbackMTU
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n <= 0 {
		return vmFallbackMTU
	}
	mtu := n - vmMTUSafetyBuffer
	// Don't go below the IPv6 minimum — anything that low and we have
	// bigger problems than MTU.
	if mtu < 1280 {
		mtu = 1280
	}
	return mtu
}

func defaultOutboundInterface() string {
	cmd := exec.Command("ip", "route", "show", "default")
	output, err := cmd.Output()
	if err != nil {
		return "eth0"
	}
	fields := strings.Fields(string(output))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return "eth0"
}

func generateMAC() string {
	// Generate a locally administered unicast MAC address
	mac := make([]byte, 6)
	rand.Read(mac)
	mac[0] = (mac[0] & 0xFE) | 0x02 // Set locally administered, clear multicast
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}
