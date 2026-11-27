//go:build linux && integration

package firecracker

// Network-namespace integration test for the guest private-destination
// deny guard. Needs root, NET_ADMIN, iptables/ip6tables/iproute2 and a
// kernel with netfilter. Run it in a throwaway privileged container:
//
//	GOOS=linux GOARCH=$(docker run --rm alpine:3.20 sh -c 'uname -m | sed s/aarch64/arm64/ | sed s/x86_64/amd64/') CGO_ENABLED=0 \
//	  go test -c -tags integration -o /tmp/fc.test ./internal/sandbox/firecracker
//	docker run --rm --privileged -v /tmp/fc.test:/fc.test alpine:3.20 \
//	  sh -c 'apk add -q iptables ip6tables iproute2 && /fc.test -test.run GuestDenyIntegration -test.v'
//
// Topology (all in the container; the test process is "the host"):
//
//	guest1  10.0.250.2  <-tap-itest1->  10.0.250.1   host
//	guest2  10.0.251.2  <-tap-itest2->  10.0.251.1   host
//	pod     10.42.0.5   <-cni-itest-->  10.42.0.1    host   (emulated pod network)
//	world   198.51.100.2 <-wan-itest->  198.51.100.1 host   (emulated public internet)
//	svc     10.43.0.10:80 -> DNAT -> pod:8080               (emulated kube-proxy)
//	hostport 10.42.0.1:31000 -> DNAT -> guest1:8080          (applyPortMapping)

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	itNSGuest1 = "evsg1"
	itNSGuest2 = "evsg2"
	itNSPod    = "evsp"
	itNSWorld  = "evsw"

	itTap1  = "tap-itest1"
	itTap2  = "tap-itest2"
	itCNI   = "cni-itest"
	itWAN   = "wan-itest"
	itProbe = 2 * time.Second
)

func itRun(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func itTry(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}

// inNetns runs fn with the calling thread switched into ns.
func inNetns(ns string, fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	orig, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		return err
	}
	defer orig.Close()
	target, err := os.Open("/run/netns/" + ns)
	if err != nil {
		return err
	}
	defer target.Close()
	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("setns %s: %w", ns, err)
	}
	defer func() { _ = unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET) }()
	return fn()
}

// listenTCP starts an accept-and-close listener on addr inside ns ("" for
// the host). Connect success is all the probes need.
func listenTCP(t *testing.T, ns, addr string) {
	t.Helper()
	start := func() error {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = l.Close() })
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				_, _ = c.Write([]byte("hello\n"))
				_ = c.Close()
			}
		}()
		return nil
	}
	var err error
	if ns == "" {
		err = start()
	} else {
		err = inNetns(ns, start)
	}
	if err != nil {
		t.Fatalf("listen %s in %q: %v", addr, ns, err)
	}
}

// listenUDPEcho answers every datagram on addr with "pong".
func listenUDPEcho(t *testing.T, addr string) {
	t.Helper()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("listen udp %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			_, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo([]byte("pong"), from)
		}
	}()
}

// dialFrom attempts a TCP connect to addr from inside ns.
func dialFrom(ns, addr string) error {
	dial := func() error {
		c, err := net.DialTimeout("tcp", addr, itProbe)
		if err != nil {
			return err
		}
		_ = c.SetReadDeadline(time.Now().Add(itProbe))
		buf := make([]byte, 16)
		if _, err := c.Read(buf); err != nil {
			_ = c.Close()
			return fmt.Errorf("connected but no banner: %w", err)
		}
		return c.Close()
	}
	if ns == "" {
		return dial()
	}
	return inNetns(ns, dial)
}

// udpPingFrom sends "ping" to addr from ns and expects "pong".
func udpPingFrom(ns, addr string) error {
	return inNetns(ns, func() error {
		c, err := net.DialTimeout("udp", addr, itProbe)
		if err != nil {
			return err
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(itProbe))
		if _, err := c.Write([]byte("ping")); err != nil {
			return err
		}
		buf := make([]byte, 16)
		n, err := c.Read(buf)
		if err != nil {
			return err
		}
		if string(buf[:n]) != "pong" {
			return fmt.Errorf("unexpected reply %q", buf[:n])
		}
		return nil
	})
}

// udpRecorder listens on addr inside ns and records every payload.
type udpRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *udpRecorder) has(payload string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.seen {
		if s == payload {
			return true
		}
	}
	return false
}

func listenUDPRecord(t *testing.T, ns, addr string) *udpRecorder {
	t.Helper()
	r := &udpRecorder{}
	start := func() error {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = pc.Close() })
		go func() {
			buf := make([]byte, 512)
			for {
				n, _, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				r.mu.Lock()
				r.seen = append(r.seen, string(buf[:n]))
				r.mu.Unlock()
			}
		}()
		return nil
	}
	var err error
	if ns == "" {
		err = start()
	} else {
		err = inNetns(ns, start)
	}
	if err != nil {
		t.Fatalf("listen udp %s in %q: %v", addr, ns, err)
	}
	return r
}

// podUDPFlow opens a UDP socket in the pod namespace, sends one datagram
// to remote (creating a conntrack entry through the host's MASQUERADE)
// and returns the socket's local port plus a recorder of what comes back
// to it. This is the shape of a CoreDNS upstream query.
func podUDPFlow(t *testing.T, ns, local, remote string) (int, *udpRecorder) {
	t.Helper()
	r := &udpRecorder{}
	port := 0
	err := inNetns(ns, func() error {
		pc, err := net.ListenPacket("udp", local+":0")
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = pc.Close() })
		port = pc.LocalAddr().(*net.UDPAddr).Port
		raddr, err := net.ResolveUDPAddr("udp", remote)
		if err != nil {
			return err
		}
		if _, err := pc.WriteTo([]byte("query"), raddr); err != nil {
			return err
		}
		go func() {
			buf := make([]byte, 512)
			for {
				n, _, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				r.mu.Lock()
				r.seen = append(r.seen, string(buf[:n]))
				r.mu.Unlock()
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("pod udp flow: %v", err)
	}
	return port, r
}

// sendForgedUDP sends, from inside ns, a UDP datagram whose IP source is
// src (not the namespace's own address) through a raw socket with
// IP_HDRINCL. The kernel fills in the IP checksum; the UDP checksum is
// left zero, which IPv4 permits.
func sendForgedUDP(ns string, src string, sport int, dst string, dport int, payload string) error {
	return inNetns(ns, func() error {
		fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_RAW)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_HDRINCL, 1); err != nil {
			return err
		}
		srcIP := net.ParseIP(src).To4()
		dstIP := net.ParseIP(dst).To4()
		if srcIP == nil || dstIP == nil {
			return fmt.Errorf("bad address %q/%q", src, dst)
		}
		udpLen := 8 + len(payload)
		pkt := make([]byte, 20+udpLen)
		pkt[0] = 0x45
		binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)))
		pkt[8] = 64
		pkt[9] = unix.IPPROTO_UDP
		copy(pkt[12:16], srcIP)
		copy(pkt[16:20], dstIP)
		binary.BigEndian.PutUint16(pkt[20:], uint16(sport))
		binary.BigEndian.PutUint16(pkt[22:], uint16(dport))
		binary.BigEndian.PutUint16(pkt[24:], uint16(udpLen))
		copy(pkt[28:], payload)
		var sa unix.SockaddrInet4
		copy(sa.Addr[:], dstIP)
		return unix.Sendto(fd, pkt, 0, &sa)
	})
}

func mustReach(t *testing.T, what, ns, addr string) {
	t.Helper()
	if err := dialFrom(ns, addr); err != nil {
		t.Fatalf("%s: expected %s -> %s to connect, got %v", what, ns, addr, err)
	}
}

func mustRefuse(t *testing.T, what, ns, addr string) {
	t.Helper()
	if err := dialFrom(ns, addr); err == nil {
		t.Fatalf("%s: %s -> %s connected, expected it to be refused", what, ns, addr)
	}
}

func itTeardown() {
	for _, ns := range []string{itNSGuest1, itNSGuest2, itNSPod, itNSWorld} {
		itTry("ip", "netns", "del", ns)
	}
	for _, link := range []string{itTap1, itTap2, itCNI, itWAN} {
		itTry("ip", "link", "del", link)
	}
}

// itVeth creates ns and a veth whose host end is hostName with hostCIDR,
// and whose guest end is eth0 in ns with nsCIDR and a default route via
// the host address.
func itVeth(t *testing.T, ns, hostName, hostCIDR, nsCIDR, gw string) {
	t.Helper()
	itRun(t, "ip", "netns", "add", ns)
	itRun(t, "ip", "link", "add", hostName, "type", "veth", "peer", "name", "eth0", "netns", ns)
	itRun(t, "ip", "addr", "add", hostCIDR, "dev", hostName)
	itRun(t, "ip", "link", "set", hostName, "up")
	itTry("ip", "netns", "exec", ns, "sysctl", "-w", "net.ipv6.conf.all.disable_ipv6=0")
	itRun(t, "ip", "netns", "exec", ns, "ip", "addr", "add", nsCIDR, "dev", "eth0")
	itRun(t, "ip", "netns", "exec", ns, "ip", "link", "set", "eth0", "up")
	itRun(t, "ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
	itRun(t, "ip", "netns", "exec", ns, "ip", "route", "add", "default", "via", gw)
}

func TestGuestDenyIntegration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables not installed")
	}
	itTeardown()
	t.Cleanup(itTeardown)

	itRun(t, "sysctl", "-w", "net.ipv4.ip_forward=1")
	// Loose reverse-path filtering, as on the prod node. all=2 wins over
	// any per-interface value, which is why the source pin is an iptables
	// rule and not a sysctl.
	itRun(t, "sysctl", "-w", "net.ipv4.conf.all.rp_filter=2")
	itRun(t, "sysctl", "-w", "net.ipv4.conf.default.rp_filter=2")
	// Containers often boot with IPv6 off; the v6 control below needs
	// link-local addresses on both ends of the guest veth.
	itTry("sysctl", "-w", "net.ipv6.conf.all.disable_ipv6=0")
	itTry("sysctl", "-w", "net.ipv6.conf.default.disable_ipv6=0")
	itVeth(t, itNSGuest1, itTap1, "10.0.250.1/30", "10.0.250.2/30", "10.0.250.1")
	itVeth(t, itNSGuest2, itTap2, "10.0.251.1/30", "10.0.251.2/30", "10.0.251.1")
	itVeth(t, itNSPod, itCNI, "10.42.0.1/24", "10.42.0.5/24", "10.42.0.1")
	itVeth(t, itNSWorld, itWAN, "198.51.100.1/24", "198.51.100.2/24", "198.51.100.1")

	// Emulated kube-proxy: a ClusterIP, and the ingress LoadBalancer on
	// the node's public address (svclb shape: node IP:80 DNATs to the
	// ingress pod, as does a non-ingress port). Plus a prod-like FORWARD
	// policy. INPUT stays ACCEPT (the weakest host) so every refusal below
	// is attributable to the guard, not to the policy.
	dnats := [][]string{
		{"-d", "10.43.0.10/32", "-p", "tcp", "--dport", "80", "-j", "DNAT", "--to-destination", "10.42.0.5:8080"},
		{"-d", "198.51.100.1/32", "-p", "tcp", "--dport", "80", "-j", "DNAT", "--to-destination", "10.42.0.5:8080"},
		{"-d", "198.51.100.1/32", "-p", "tcp", "--dport", "9091", "-j", "DNAT", "--to-destination", "10.42.0.5:8080"},
	}
	for _, d := range dnats {
		itRun(t, "iptables", append([]string{"-t", "nat", "-A", "PREROUTING"}, d...)...)
	}
	// Pods egress through the node like on a CNI host (MASQUERADE).
	podMasq := []string{"-t", "nat", "-A", "POSTROUTING", "-s", "10.42.0.0/24", "-o", itWAN, "-j", "MASQUERADE"}
	itRun(t, "iptables", podMasq...)
	t.Cleanup(func() {
		for _, d := range dnats {
			itTry("iptables", append([]string{"-t", "nat", "-D", "PREROUTING"}, d...)...)
		}
		itTry("iptables", append([]string{"-t", "nat", "-D", "POSTROUTING"}, podMasq[4:]...)...)
		itTry("iptables", "-P", "FORWARD", "ACCEPT")
	})

	listenTCP(t, itNSPod, "10.42.0.5:8080")
	listenTCP(t, itNSWorld, "198.51.100.2:8080")
	listenTCP(t, itNSGuest1, "10.0.250.2:8080")
	listenTCP(t, itNSGuest2, "10.0.251.2:8080")
	listenTCP(t, "", "10.0.250.1:9090")   // a control-plane port on the TAP gateway
	listenTCP(t, "", "198.51.100.1:9090") // the same on the public address
	listenTCP(t, "", "198.51.100.1:443")  // public ingress
	listenUDPEcho(t, "10.0.250.1:53")     // guest1's resolver
	listenUDPEcho(t, "10.0.251.1:53")     // guest2's resolver

	// Positive control: with FORWARD ACCEPT and no guard, everything is
	// reachable. This is the issue class the guard closes.
	mustReach(t, "control: pod", itNSGuest1, "10.42.0.5:8080")
	mustReach(t, "control: service", itNSGuest1, "10.43.0.10:80")
	mustReach(t, "control: non-ingress port DNATed to a pod", itNSGuest1, "198.51.100.1:9091")
	mustReach(t, "control: host control port", itNSGuest1, "10.0.250.1:9090")
	mustReach(t, "control: peer guest", itNSGuest1, "10.0.251.2:8080")
	if err := udpPingFrom(itNSGuest1, "10.0.251.1:53"); err != nil {
		t.Fatalf("control: peer resolver should answer before the guard: %v", err)
	}

	// IPv6 control: before the guard the guest's link-local reaches the
	// host's. If this host cannot do that at all, the v6 assertion below
	// would prove nothing, so it is skipped.
	v6Control := false
	for i := 0; i < 10 && !v6Control; i++ { // link-local is tentative for ~1s after link up
		if hostLL := linkLocalOf(t, itTap1); hostLL != "" {
			v6Control = ping6From(itNSGuest1, hostLL+"%eth0") == nil
		}
		if !v6Control {
			time.Sleep(500 * time.Millisecond)
		}
	}
	t.Logf("ipv6 control reachable before guard: %v", v6Control)

	t.Setenv(envClusterCIDRs, "10.42.0.0/16,10.43.0.0/16")
	spec := DetectGuestDenySpec()
	if !containsRule(spec.HostPublicAddrs, "198.51.100.1/32") {
		t.Fatalf("host public address not detected: %+v", spec)
	}

	// Forged-source injection, the case the per-VM source pin exists for.
	// A pod has a UDP flow to a public resolver, SNATed on the way out.
	// Guest1 forges a datagram carrying that flow's reply tuple (source =
	// resolver, destination = node public address and SNAT port). With the
	// deny guard alone it is ESTABLISHED, exempted, de-NATed and delivered
	// into the pod. Positive control first, guard installed, no per-VM
	// rules yet.
	worldDNS := listenUDPRecord(t, itNSWorld, "198.51.100.2:53")
	// Port 53 to anything but the proxy is dropped by the per-VM DNS gate,
	// so guest-originated probes use a plain UDP port.
	worldUDP := listenUDPRecord(t, itNSWorld, "198.51.100.2:5353")
	podPort, podReplies := podUDPFlow(t, itNSPod, "10.42.0.5", "198.51.100.2:53")
	waitFor(t, "pod query reaches the resolver", func() bool { return worldDNS.has("query") })
	if _, err := EnsureGuestDeny(spec); err != nil {
		t.Fatalf("EnsureGuestDeny: %v", err)
	}
	if err := sendForgedUDP(itNSGuest1, "198.51.100.2", 53, "198.51.100.1", podPort, "FORGED-1"); err != nil {
		t.Fatalf("send forged datagram: %v", err)
	}
	waitFor(t, "control: forged reply is injected into the pod with the guard alone", func() bool { return podReplies.has("FORGED-1") })

	cfg1 := &NetworkConfig{TapDevice: itTap1, HostIP: "10.0.250.1", GuestIP: "10.0.250.2", HostIface: itWAN, Mode: "allow", MTU: 1400}
	cfg2 := &NetworkConfig{TapDevice: itTap2, HostIP: "10.0.251.1", GuestIP: "10.0.251.2", HostIface: itWAN, Mode: "allow", MTU: 1400}
	t.Cleanup(func() {
		cleanupIptables(cfg1)
		cleanupIptables(cfg2)
	})
	// The production path: guard first, per-VM chain, guard re-asserted,
	// verification including ordering.
	if err := configureIptables(cfg1); err != nil {
		t.Fatalf("configureIptables(guest1): %v", err)
	}
	if err := configureIptables(cfg2); err != nil {
		t.Fatalf("configureIptables(guest2): %v", err)
	}
	itRun(t, "iptables", "-P", "FORWARD", "DROP")

	pm := &firecrackerPortMapping{HostPort: 31000, GuestPort: 8080, GuestIP: "10.0.250.2", Protocol: "tcp"}
	if err := applyPortMapping(pm); err != nil {
		t.Fatalf("applyPortMapping: %v", err)
	}
	t.Cleanup(func() { _ = removePortMapping(pm) })

	// Source pin: the same forged reply is now dropped before conntrack,
	// and a guest cannot impersonate a peer guest's address either.
	if err := sendForgedUDP(itNSGuest1, "198.51.100.2", 53, "198.51.100.1", podPort, "FORGED-2"); err != nil {
		t.Fatalf("send forged datagram: %v", err)
	}
	if err := sendForgedUDP(itNSGuest1, "10.0.251.2", 4000, "198.51.100.2", 5353, "IMPERSONATE"); err != nil {
		t.Fatalf("send impersonating datagram: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if podReplies.has("FORGED-2") {
		t.Fatalf("forged reply reached the pod despite the source pin")
	}
	if worldUDP.has("IMPERSONATE") {
		t.Fatalf("datagram with a peer guest's source left the host despite the source pin")
	}
	// The pin leaves the guest's own traffic alone.
	if err := inNetns(itNSGuest1, func() error {
		c, err := net.DialTimeout("udp", "198.51.100.2:5353", itProbe)
		if err != nil {
			return err
		}
		defer c.Close()
		_, err = c.Write([]byte("GENUINE"))
		return err
	}); err != nil {
		t.Fatalf("guest udp send: %v", err)
	}
	waitFor(t, "genuine guest datagram still leaves the host", func() bool { return worldUDP.has("GENUINE") })

	// Denied.
	mustRefuse(t, "pod network", itNSGuest1, "10.42.0.5:8080")
	mustRefuse(t, "service CIDR (pre-DNAT)", itNSGuest1, "10.43.0.10:80")
	mustRefuse(t, "host-local private (TAP gateway control port)", itNSGuest1, "10.0.250.1:9090")
	mustRefuse(t, "host public, non-ingress port", itNSGuest1, "198.51.100.1:9090")
	mustRefuse(t, "host public, non-ingress port DNATed to a pod", itNSGuest1, "198.51.100.1:9091")
	mustRefuse(t, "peer guest", itNSGuest1, "10.0.251.2:8080")
	mustRefuse(t, "peer guest via host port on pod bridge", itNSGuest2, "10.42.0.1:31000")
	mustRefuse(t, "peer guest via host port on public address", itNSGuest2, "198.51.100.1:31000")
	if err := udpPingFrom(itNSGuest1, "10.0.251.1:53"); err == nil {
		t.Fatalf("peer resolver answered guest1; DNS exemption must be limited to the incoming interface")
	}

	// Allowed.
	mustReach(t, "public egress via FORWARD + MASQUERADE", itNSGuest1, "198.51.100.2:8080")
	mustReach(t, "public ingress exception (host-local listener)", itNSGuest1, "198.51.100.1:443")
	mustReach(t, "public ingress exception (DNATed to the ingress pod, the tenant gateway path)", itNSGuest1, "198.51.100.1:80")
	if err := udpPingFrom(itNSGuest1, "10.0.250.1:53"); err != nil {
		t.Fatalf("own resolver must answer: %v", err)
	}
	mustReach(t, "host -> guest (replies are established)", "", "10.0.250.2:8080")
	mustReach(t, "pod -> guest via host port DNAT", itNSPod, "10.42.0.1:31000")

	// A contender inserting an ACCEPT at FORWARD position 1 (what
	// tailscale and kube-router do) must not reopen anything.
	itRun(t, "iptables", "-I", "FORWARD", "1", "-o", itCNI, "-j", "ACCEPT")
	itRun(t, "iptables", "-I", "FORWARD", "1", "-d", "10.42.0.5/32", "-j", "ACCEPT")
	mustRefuse(t, "pod network with a contender ACCEPT ahead in FORWARD", itNSGuest1, "10.42.0.5:8080")
	mustRefuse(t, "service CIDR with a contender ACCEPT ahead in FORWARD", itNSGuest1, "10.43.0.10:80")
	itRun(t, "iptables", "-D", "FORWARD", "-d", "10.42.0.5/32", "-j", "ACCEPT")
	itRun(t, "iptables", "-D", "FORWARD", "-o", itCNI, "-j", "ACCEPT")

	// Idempotent: once the state is settled, an ensure writes nothing
	// (proves the rule bodies are in the form `iptables -S` prints, so the
	// periodic loop does not rewrite the chain every tick). The port
	// mapping above re-asserted the jumps itself, so nothing should move.
	if changed, err := EnsureGuestDeny(spec); err != nil || changed {
		t.Fatalf("EnsureGuestDeny on a settled host: changed=%v err=%v", changed, err)
	}

	// Re-assertion: strangers displacing our jumps are undone, stale
	// copies (an older agent's plain tag, another agent's nonce) are
	// removed by spec, exactly one copy is left at position 1, and every
	// stranger rule around them survives untouched.
	strangers := []struct {
		table, hook string
		args        []string
	}{
		{"filter", "FORWARD", []string{"-m", "comment", "--comment", "stranger-a", "-j", "RETURN"}},
		{"filter", "FORWARD", []string{"-m", "comment", "--comment", "stranger-b", "-j", "RETURN"}},
		{"filter", "INPUT", []string{"-m", "comment", "--comment", "stranger-c", "-j", "RETURN"}},
		{"mangle", "PREROUTING", []string{"-m", "comment", "--comment", "stranger-d", "-j", "RETURN"}},
	}
	// FORWARD ends up: stranger-a, copy(other nonce), stranger-b, [ours], ..., copy(plain tag)
	itRun(t, "iptables", append([]string{"-I", "FORWARD", "1"}, strangers[1].args...)...)
	itRun(t, "iptables", append([]string{"-I", "FORWARD", "1"}, strings.Fields(guestDenyJumpBody(guestDenyFilterChain, guestDenyTag+"-00000000"))...)...)
	itRun(t, "iptables", append([]string{"-I", "FORWARD", "1"}, strangers[0].args...)...)
	itRun(t, "iptables", append([]string{"-A", "FORWARD"}, strings.Fields(guestDenyJumpBody(guestDenyFilterChain, guestDenyTag))...)...)
	itRun(t, "iptables", append([]string{"-I", "INPUT", "1"}, strangers[2].args...)...)
	itRun(t, "iptables", append([]string{"-t", "mangle", "-I", "PREROUTING", "1"}, strangers[3].args...)...)
	if changed, err := EnsureGuestDeny(spec); err != nil || !changed {
		t.Fatalf("re-assert EnsureGuestDeny: changed=%v err=%v", changed, err)
	}
	for _, hc := range []struct{ table, hook, chain string }{
		{"filter", "FORWARD", guestDenyFilterChain},
		{"filter", "INPUT", guestDenyFilterChain},
		{"mangle", "PREROUTING", guestDenyMangleChain},
	} {
		dump, _, err := ipt4.dump(hc.table, hc.hook)
		if err != nil {
			t.Fatal(err)
		}
		if copies := guestDenyJumps(dump, hc.hook, hc.chain); len(copies) != 1 || copies[0].pos != 1 {
			t.Fatalf("%s/%s: deny jump copies %v after re-assert; dump:\n%s", hc.table, hc.hook, copies, dump)
		}
	}
	for _, st := range strangers {
		dump, _, err := ipt4.dump(st.table, st.hook)
		if err != nil {
			t.Fatal(err)
		}
		if firstRulePosition(dump, st.hook, []string{strings.Join(st.args, " ")}) == 0 {
			t.Fatalf("%s/%s: stranger rule %v was removed by re-assert; dump:\n%s", st.table, st.hook, st.args, dump)
		}
		itRun(t, "iptables", append([]string{"-t", st.table, "-D", st.hook}, st.args...)...)
	}

	// A VM whose jump ends up ahead of the guard is refused by verification.
	itRun(t, "iptables", "-I", "FORWARD", "1", "-i", itTap1, "-j", vmChainName(itTap1))
	if err := verifyIptables(cfg1); err == nil || !strings.Contains(err.Error(), "does not precede") {
		t.Fatalf("verifyIptables should refuse a VM jump ahead of the guard, got %v", err)
	}
	itRun(t, "iptables", "-D", "FORWARD", "1")
	if err := verifyIptables(cfg1); err != nil {
		t.Fatalf("verifyIptables after restoring order: %v", err)
	}

	// Operator override reopens exactly the listed range.
	t.Setenv(envGuestAllowPrivateCIDRs, "10.42.0.5/32")
	if _, err := EnsureGuestDeny(DetectGuestDenySpec()); err != nil {
		t.Fatalf("EnsureGuestDeny with override: %v", err)
	}
	mustReach(t, "operator override", itNSGuest1, "10.42.0.5:8080")
	mustRefuse(t, "service CIDR still denied with a pod override", itNSGuest1, "10.43.0.10:80")
	t.Setenv(envGuestAllowPrivateCIDRs, "")
	if _, err := EnsureGuestDeny(DetectGuestDenySpec()); err != nil {
		t.Fatalf("EnsureGuestDeny after clearing override: %v", err)
	}
	mustRefuse(t, "pod network after clearing the override", itNSGuest1, "10.42.0.5:8080")

	// IPv6: the guest's only IPv6 is link-local; it must not reach the
	// host's link-local address once the guard is up.
	if spec.IPv6 && v6Control {
		if err := ping6From(itNSGuest1, linkLocalOf(t, itTap1)+"%eth0"); err == nil {
			t.Fatalf("IPv6 link-local to the host answered despite the guard")
		}
	}

	// Per-VM teardown removes the source pin and leaves the shared guard.
	cleanupIptables(cfg2)
	if err := verifyAntiSpoof(cfg2); err == nil {
		t.Fatalf("anti-spoof rule for guest2 survived cleanup")
	}
	if err := verifyAntiSpoof(cfg1); err != nil {
		t.Fatalf("anti-spoof rule for guest1 removed by guest2's cleanup: %v", err)
	}
	for _, hc := range []struct{ table, chain string }{{"mangle", guestDenyMangleChain}, {"filter", guestDenyFilterChain}} {
		if _, exists, err := ipt4.dump(hc.table, hc.chain); err != nil || !exists {
			t.Fatalf("%s/%s gone after per-VM cleanup (err=%v)", hc.table, hc.chain, err)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(itProbe)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: condition not met within %s", what, itProbe)
}

func linkLocalOf(t *testing.T, iface string) string {
	t.Helper()
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() == nil && n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	return ""
}

func ping6From(ns, target string) error {
	out, err := exec.Command("ip", "netns", "exec", ns, "ping", "-6", "-c", "1", "-W", "1", target).CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("ping failed: %s", strings.TrimSpace(string(out)))
		}
		return err
	}
	return nil
}
