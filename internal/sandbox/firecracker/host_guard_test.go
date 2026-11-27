package firecracker

import (
	"reflect"
	"strings"
	"testing"
)

func TestHostPortGuardRules_BuildsDropsForGuestInterfacesAndSubnet(t *testing.T) {
	rules := HostPortGuardRules([]int{9091, 9090, 9090, 0, -1})
	want := [][]string{
		{"-I", "INPUT", "1", "-i", guestTapWildcard, "-p", "tcp", "-m", "multiport", "--dports", "9090,9091", "-j", "DROP"},
		{"-I", "INPUT", "1", "-s", GuestPoolCIDR(), "-p", "tcp", "-m", "multiport", "--dports", "9090,9091", "-j", "DROP"},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("rules = %v\nwant   %v", rules, want)
	}
}

func TestHostPortGuardRules_EmptyPorts(t *testing.T) {
	if got := HostPortGuardRules(nil); len(got) != 0 {
		t.Fatalf("expected no rules for no ports, got %v", got)
	}
	if got := HostPortGuardRules([]int{0, 70000}); len(got) != 0 {
		t.Fatalf("expected no rules for invalid ports, got %v", got)
	}
}

// The guard must match the per-VM TAP naming scheme, otherwise a guest
// interface slips past the wildcard.
func TestHostPortGuardRules_WildcardMatchesTapNaming(t *testing.T) {
	tap := tapNameForVM("sbx_example")
	prefix := strings.TrimSuffix(guestTapWildcard, "+")
	if !strings.HasPrefix(tap, prefix) {
		t.Fatalf("tap name %q does not start with guard prefix %q", tap, prefix)
	}
}

func TestHostPortGuardPresent_MatchesInstalledRule(t *testing.T) {
	dump := strings.Join([]string{
		"-P INPUT DROP",
		"-A INPUT -i tap-+ -p tcp -m multiport --dports 9090,9091 -j DROP",
		"-A INPUT -s 10.0.0.0/16 -p tcp -m multiport --dports 9090,9091 -j DROP",
		"-A INPUT -j ufw-before-input",
	}, "\n")
	for _, rule := range HostPortGuardRules([]int{9090, 9091}) {
		if !hostPortGuardPresent(dump, rule) {
			t.Fatalf("rule %v not detected in dump", rule)
		}
	}
	// Different port set must not be mistaken for ours.
	for _, rule := range HostPortGuardRules([]int{9092, 9093}) {
		if hostPortGuardPresent(dump, rule) {
			t.Fatalf("rule %v wrongly detected in dump", rule)
		}
	}
}
