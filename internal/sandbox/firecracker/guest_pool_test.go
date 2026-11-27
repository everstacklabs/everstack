package firecracker

import (
	"strings"
	"testing"
)

func withGuestPool(t *testing.T, cidr string) {
	t.Helper()
	prev := GuestPoolCIDR()
	if err := ConfigureGuestPool(cidr); err != nil {
		t.Fatalf("ConfigureGuestPool(%q): %v", cidr, err)
	}
	t.Cleanup(func() { _ = ConfigureGuestPool(prev) })
}

func TestGuestPool_DefaultIsLegacyRange(t *testing.T) {
	if got := GuestPoolCIDR(); got != "10.0.0.0/16" {
		t.Fatalf("default guest pool = %q, want 10.0.0.0/16", got)
	}
}

func TestConfigureGuestPool_RejectsNonSlash16(t *testing.T) {
	for _, bad := range []string{"10.0.0.0/24", "10.0.0.0/8", "10.0.0.0", "not-a-cidr", "2001:db8::/32", "10.0.5.0/16"} {
		if err := ConfigureGuestPool(bad); err == nil {
			t.Errorf("ConfigureGuestPool(%q) accepted, want error", bad)
		}
	}
	if got := GuestPoolCIDR(); got != "10.0.0.0/16" {
		t.Fatalf("rejected input must not change the pool, got %q", got)
	}
}

func TestConfigureGuestPool_DerivesAddressesAndRules(t *testing.T) {
	withGuestPool(t, "10.200.0.0/16")

	if got := guestHostIP(7); got != "10.200.7.1" {
		t.Fatalf("guestHostIP(7) = %q", got)
	}
	if got := guestGuestIP(7); got != "10.200.7.2" {
		t.Fatalf("guestGuestIP(7) = %q", got)
	}

	// Recovery parses the host IP back into a subnet index.
	n, err := subnetFromHostIP("10.200.7.1")
	if err != nil || n != 7 {
		t.Fatalf("subnetFromHostIP = (%d, %v), want (7, nil)", n, err)
	}
	if _, err := subnetFromHostIP("10.0.7.1"); err == nil {
		t.Fatal("subnetFromHostIP must reject an address outside the configured pool")
	}

	// The cross-process TAP scan must follow the configured pool too.
	taken := parseAssignedSubnets("1: tap-a inet 10.200.3.1/30 scope global tap-a\n2: tap-b inet 10.0.4.1/30 scope global tap-b\n")
	if _, ok := taken[3]; !ok {
		t.Fatal("expected 10.200.3.1 to be recognised as subnet 3")
	}
	if _, ok := taken[4]; ok {
		t.Fatal("10.0.4.1 is outside the configured pool and must be ignored")
	}

	// Firewall rules derive from the same value.
	rules := HostPortGuardRules([]int{9090})
	if len(rules) != 2 || !strings.Contains(strings.Join(rules[1], " "), "-s 10.200.0.0/16") {
		t.Fatalf("guard rules do not use the configured pool: %v", rules)
	}
}
