package firecracker

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Guest address pool.
//
// Every VM gets a /30 carved from one /16: host TAP at .1, guest at .2,
// third octet = the allocator's subnet index (subnet_alloc.go). The /16
// used to be hardcoded as 10.0.0.0/16, which collides with hosts whose
// own networks live there (the Hetzner node network is 10.0.1.0/24):
// guests would share addresses with real nodes, and any firewall rule
// keyed on the pool would also match node traffic such as kubelet
// probes. The pool is now configured once at agent start
// (FC_GUEST_CIDR) and every consumer derives from it: address
// assignment, the recovery parser, the cross-process TAP scan, the
// inter-guest isolation drop and the host port guard.

// DefaultGuestPoolCIDR is the historical range, kept as the default so
// existing deployments keep their addressing.
const DefaultGuestPoolCIDR = "10.0.0.0/16"

type guestPool struct {
	cidr string
	// first two octets of the /16, e.g. {10, 0}.
	o1, o2 int
	// hostIPRe matches a host-side per-VM TAP address (<pool>.<n>.1/30)
	// in `ip -o addr show` output. Anchoring on ".1/30" avoids matching
	// guest addresses.
	hostIPRe *regexp.Regexp
}

var (
	guestPoolMu      sync.RWMutex
	currentGuestPool = mustGuestPool(DefaultGuestPoolCIDR)
)

func parseGuestPool(cidr string) (*guestPool, error) {
	cidr = strings.TrimSpace(cidr)
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("firecracker: guest pool %q: %w", cidr, err)
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil, fmt.Errorf("firecracker: guest pool %q must be IPv4", cidr)
	}
	if ones, bits := ipnet.Mask.Size(); ones != 16 || bits != 32 {
		return nil, fmt.Errorf("firecracker: guest pool %q must be a /16 (one /30 per VM is carved from the third octet)", cidr)
	}
	if !ip.Equal(ipnet.IP) {
		return nil, fmt.Errorf("firecracker: guest pool %q must be the network address (e.g. %s)", cidr, ipnet.String())
	}
	o1, o2 := int(v4[0]), int(v4[1])
	return &guestPool{
		cidr:     ipnet.String(),
		o1:       o1,
		o2:       o2,
		hostIPRe: regexp.MustCompile(fmt.Sprintf(`inet %d\.%d\.(\d{1,3})\.1/30`, o1, o2)),
	}, nil
}

func mustGuestPool(cidr string) *guestPool {
	p, err := parseGuestPool(cidr)
	if err != nil {
		panic(err)
	}
	return p
}

// ConfigureGuestPool sets the /16 that per-VM subnets are carved from.
// Call once at agent start, before any VM is created; an invalid value
// leaves the current pool untouched and returns the error.
func ConfigureGuestPool(cidr string) error {
	p, err := parseGuestPool(cidr)
	if err != nil {
		return err
	}
	guestPoolMu.Lock()
	currentGuestPool = p
	guestPoolMu.Unlock()
	return nil
}

// GuestPoolCIDR returns the configured pool in canonical form.
func GuestPoolCIDR() string {
	return activeGuestPool().cidr
}

func activeGuestPool() *guestPool {
	guestPoolMu.RLock()
	defer guestPoolMu.RUnlock()
	return currentGuestPool
}

// guestHostIP is the host TAP end of subnet n.
func guestHostIP(n int) string {
	p := activeGuestPool()
	return fmt.Sprintf("%d.%d.%d.1", p.o1, p.o2, n)
}

// guestGuestIP is the guest end of subnet n.
func guestGuestIP(n int) string {
	p := activeGuestPool()
	return fmt.Sprintf("%d.%d.%d.2", p.o1, p.o2, n)
}

// subnetFromHostIP parses the subnet index out of a host TAP address
// ("<pool>.<n>.1") so recovery can pass it to ReserveSubnet without
// re-deriving the allocation scheme from the IP format. Addresses
// outside the configured pool are rejected.
func subnetFromHostIP(hostIP string) (int, error) {
	p := activeGuestPool()
	parts := strings.Split(hostIP, ".")
	if len(parts) != 4 || parts[0] != strconv.Itoa(p.o1) || parts[1] != strconv.Itoa(p.o2) {
		return 0, fmt.Errorf("firecracker: hostIP %q does not match %d.%d.<n>.1", hostIP, p.o1, p.o2)
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, fmt.Errorf("firecracker: hostIP %q has non-numeric third octet: %w", hostIP, err)
	}
	return n, nil
}
