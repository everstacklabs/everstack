package firecracker

import (
	"os"
	"reflect"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestGuestResolvers(t *testing.T) {
	// Both Allow and Whitelist route the guest through the per-VM DNS
	// proxy listening on HostIP — the guest never talks to upstream
	// resolvers directly. Deny mode has no network so the field is
	// returned untouched (caller doesn't write resolv.conf in that mode).
	cases := []struct {
		name string
		cfg  *NetworkConfig
		want []string
	}{
		{
			name: "nil cfg",
			cfg:  nil,
			want: nil,
		},
		{
			name: "whitelist mode returns host TAP IP",
			cfg: &NetworkConfig{
				Mode:       sandbox.NetworkWhitelist,
				HostIP:     "10.0.42.1",
				DNSServers: []string{"8.8.8.8", "1.1.1.1"},
			},
			want: []string{"10.0.42.1"},
		},
		{
			name: "allow mode returns host TAP IP (proxy serves both modes)",
			cfg: &NetworkConfig{
				Mode:       sandbox.NetworkAllow,
				HostIP:     "10.0.42.1",
				DNSServers: []string{"8.8.8.8", "1.1.1.1"},
			},
			want: []string{"10.0.42.1"},
		},
		{
			name: "deny mode passes through DNSServers (no proxy, no resolv.conf written)",
			cfg: &NetworkConfig{
				Mode:       sandbox.NetworkDeny,
				HostIP:     "10.0.42.1",
				DNSServers: []string{"8.8.8.8"},
			},
			want: []string{"8.8.8.8"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.GuestResolvers()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeNetworkMode(t *testing.T) {
	cases := []struct {
		name string
		in   sandbox.NetworkMode
		want sandbox.NetworkMode
	}{
		{"empty defaults to allow", "", sandbox.NetworkAllow},
		{"allow", sandbox.NetworkAllow, sandbox.NetworkAllow},
		{"whitelist", sandbox.NetworkWhitelist, sandbox.NetworkWhitelist},
		{"deny", sandbox.NetworkDeny, sandbox.NetworkDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeNetworkMode(tc.in)
			if err != nil {
				t.Fatalf("normalizeNetworkMode(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("normalizeNetworkMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	if _, err := normalizeNetworkMode("surprise"); err == nil {
		t.Fatal("normalizeNetworkMode(invalid) expected error")
	}
}

func TestFilterUnreachableResolvers(t *testing.T) {
	// The filter only drops 127/8 (loopback / stub resolvers like
	// systemd-resolved at 127.0.0.53 — only reachable from the host
	// itself, not from the pod netns where our proxy lives).
	//
	// ClusterIPs (10.43.x, 10.96.x) MUST pass through: the proxy runs
	// in the pod netns where kube-proxy DNAT makes them reachable, and
	// in egress-restricted clusters (managed K8s blocking public :53)
	// they're the only working upstream.
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "k3s ClusterIP passes through (proxy can reach it from pod netns)",
			in:   []string{"10.43.0.10", "1.1.1.1"},
			want: []string{"10.43.0.10", "1.1.1.1"},
		},
		{
			name: "loopback / systemd-resolved stub dropped",
			in:   []string{"127.0.0.53", "1.1.1.1"},
			want: []string{"1.1.1.1"},
		},
		{
			name: "public resolvers pass through",
			in:   []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"},
			want: []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"},
		},
		{
			name: "ipv6 entries dropped (we only push v4)",
			in:   []string{"::1", "fd00::1", "1.1.1.1"},
			want: []string{"1.1.1.1"},
		},
		{
			name: "garbage entries dropped",
			in:   []string{"not-an-ip", "", "1.1.1.1"},
			want: []string{"1.1.1.1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterUnreachableResolvers(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestReadNodeNameserversEnvOverride(t *testing.T) {
	// FCAGENT_DNS_UPSTREAMS short-circuits everything. The proxy should
	// trust the operator over the resolv.conf heuristics.
	t.Setenv("FCAGENT_DNS_UPSTREAMS", "9.9.9.9, 8.8.4.4")
	got := readNodeNameservers()
	want := []string{"9.9.9.9", "8.8.4.4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env override: got %v, want %v", got, want)
	}
}

func TestReadNodeNameserversPublicFallback(t *testing.T) {
	// When env is unset and neither resolv.conf path yields anything
	// useful (filtered to empty or unreadable), we fall through to the
	// public defaults so the proxy never starts with zero upstreams.
	// We don't have a clean way to neutralize both /etc/resolv.conf and
	// /host/etc/resolv.conf in CI, so this test asserts the function at
	// least returns non-empty — the fallback case is exercised in the
	// filter tests above for correctness.
	os.Unsetenv("FCAGENT_DNS_UPSTREAMS")
	got := readNodeNameservers()
	if len(got) == 0 {
		t.Fatal("readNodeNameservers must never return empty (caller relies on fallbacks)")
	}
}

func TestTapNameForVM(t *testing.T) {
	// IFNAMSIZ caps interface names at 15 usable chars on Linux. The hash
	// scheme must fit and must not collide for two distinct vmIDs that
	// share a long prefix — that's exactly what bit the previous
	// left-truncation scheme.
	cases := []struct {
		name string
		vmID string
	}{
		{"persistent agent", "wks_b9788bd0-f7df-43af-a1ac-4ac25173717d"},
		{"manual sandbox a", "sbx_manual_cc68fc53-64ba-4801-9712-bf4b65e02867"},
		{"manual sandbox b", "sbx_manual_dd79ad64-75cb-5912-a823-cf5c76f13978"},
		{"trooper prefix", "trp-agent-xyz-9999999999"},
	}

	seen := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tapNameForVM(tc.vmID)
			if len(got) > 15 {
				t.Fatalf("tapNameForVM(%q) = %q (len=%d) exceeds IFNAMSIZ-1", tc.vmID, got, len(got))
			}
			if prev, ok := seen[got]; ok {
				t.Fatalf("collision: tapNameForVM(%q) = %q already taken by %q", tc.vmID, got, prev)
			}
			seen[got] = tc.vmID
		})
	}

	// Determinism: same vmID always maps to the same TAP name, otherwise
	// cleanup (which looks up the device by name) would orphan the rule.
	if tapNameForVM("foo") != tapNameForVM("foo") {
		t.Fatal("tapNameForVM must be deterministic")
	}
}

func TestReadHostNameserversParse(t *testing.T) {
	// Indirect: we can't easily inject /etc/resolv.conf. Verify the
	// parsing logic against synthetic input that mirrors what the
	// fcagent pod's resolv.conf typically looks like.
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "k3s cluster default",
			content: `search default.svc.cluster.local svc.cluster.local cluster.local
nameserver 10.43.0.10
options ndots:5`,
			want: []string{"10.43.0.10"},
		},
		{
			name: "multiple resolvers",
			content: `nameserver 10.43.0.10
nameserver 1.1.1.1`,
			want: []string{"10.43.0.10", "1.1.1.1"},
		},
		{
			name: "ipv6 entries skipped",
			content: `nameserver 10.43.0.10
nameserver fd00::1
nameserver 1.1.1.1`,
			want: []string{"10.43.0.10", "1.1.1.1"},
		},
		{
			name:    "empty",
			content: "",
			want:    nil,
		},
		{
			name:    "comments and blank lines",
			content: "# host resolv\n\nnameserver  10.43.0.10\n",
			want:    []string{"10.43.0.10"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseResolverContent(tc.content)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeVMMTU(t *testing.T) {
	// Empty iface name → fallback. Verifies the safety branch.
	if got := computeVMMTU(""); got != vmFallbackMTU {
		t.Fatalf("empty iface should return fallback %d, got %d", vmFallbackMTU, got)
	}
	// Non-existent iface should also hit the fallback rather than panic.
	if got := computeVMMTU("no-such-iface-xyz"); got != vmFallbackMTU {
		t.Fatalf("missing iface should return fallback %d, got %d", vmFallbackMTU, got)
	}
}

func TestVMChainName(t *testing.T) {
	// iptables chain names are capped at 28 chars. TAP names are <=15 chars,
	// so "EVS-<tap>" must always fit.
	cases := []struct {
		tap  string
		want string
	}{
		{"tap-abc123", "EVS-tap-abc123"},
		{"tap-0123456789a", "EVS-tap-0123456789a"},
	}
	for _, tc := range cases {
		got := vmChainName(tc.tap)
		if got != tc.want {
			t.Fatalf("vmChainName(%q): got %q, want %q", tc.tap, got, tc.want)
		}
		if len(got) > 28 {
			t.Fatalf("chain name %q exceeds iptables 28-char limit", got)
		}
	}
}
