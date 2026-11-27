package firecracker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// manifestStore serves one manifest and refuses everything else.
type manifestStore struct {
	snapshot.Disabled
	manifest *snapshot.Manifest
}

func (s manifestStore) GetManifest(context.Context, string, string) (*snapshot.Manifest, error) {
	return s.manifest, nil
}

type setupCall struct {
	mode         sandbox.NetworkMode
	allowedHosts []string
	dnsServers   []string
}

var errStopAfterSetup = errors.New("stop after network setup")

// stubSetupNetwork records SetupNetwork calls and fails each one, so
// RestoreVMSnapshot stops before it needs a firecracker binary.
func stubSetupNetwork(t *testing.T) *[]setupCall {
	t.Helper()
	calls := &[]setupCall{}
	prev := setupNetworkFn
	setupNetworkFn = func(_ string, mode sandbox.NetworkMode, allowedHosts, dnsServers []string) (*NetworkConfig, error) {
		*calls = append(*calls, setupCall{mode: mode, allowedHosts: allowedHosts, dnsServers: dnsServers})
		return nil, errStopAfterSetup
	}
	t.Cleanup(func() { setupNetworkFn = prev })
	return calls
}

// legacyNetworkedManifest is what SaveSnapshot wrote before the fix: the
// guest's resolver (its old TAP host IP) in DNSServers, and no policy.
func legacyNetworkedManifest() *snapshot.Manifest {
	return &snapshot.Manifest{
		SandboxID: "sbx-1",
		TenantID:  "tenant-a",
		Backend:   "firecracker",
		Network: &snapshot.NetworkSpec{
			IfaceID:    "eth0",
			GuestMAC:   "06:00:0a:00:05:02",
			GuestIP:    "10.0.5.2",
			HostIP:     "10.0.5.1",
			DNSServers: []string{"10.0.5.1"},
		},
	}
}

func restoreWith(t *testing.T, policy sandbox.RestoreNetworkPolicy, m *snapshot.Manifest) error {
	t.Helper()
	b := &FirecrackerBackend{}
	_, err := b.RestoreVMSnapshot(context.Background(), "sbx-1", "tenant-a", "", policy, manifestStore{manifest: m})
	return err
}

func TestRestoreVMSnapshot_NeverUsesManifestDNSAsUpstream(t *testing.T) {
	calls := stubSetupNetwork(t)
	err := restoreWith(t, sandbox.RestoreNetworkPolicy{Mode: sandbox.NetworkAllow}, legacyNetworkedManifest())
	if !errors.Is(err, errStopAfterSetup) {
		t.Fatalf("err = %v, want the stubbed setup error", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("SetupNetwork calls = %d, want 1", len(*calls))
	}
	if got := (*calls)[0].dnsServers; got != nil {
		t.Fatalf("DNS upstreams = %v, want nil so the operator upstreams apply", got)
	}
}

func TestRestoreVMSnapshot_WhitelistKeepsModeAndAllowlist(t *testing.T) {
	calls := stubSetupNetwork(t)
	hosts := []string{"pypi.org", "*.github.com"}
	err := restoreWith(t, sandbox.RestoreNetworkPolicy{Mode: sandbox.NetworkWhitelist, AllowedHosts: hosts}, legacyNetworkedManifest())
	if !errors.Is(err, errStopAfterSetup) {
		t.Fatalf("err = %v, want the stubbed setup error", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("SetupNetwork calls = %d, want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.mode != sandbox.NetworkWhitelist {
		t.Fatalf("mode = %q, want whitelist", got.mode)
	}
	if !reflect.DeepEqual(got.allowedHosts, hosts) {
		t.Fatalf("allowed hosts = %v, want %v", got.allowedHosts, hosts)
	}
}

func TestRestoreVMSnapshot_FailClosedPolicyIsEmptyWhitelist(t *testing.T) {
	calls := stubSetupNetwork(t)
	// The manager hands this policy over when the record is unreadable.
	_ = restoreWith(t, sandbox.RestoreNetworkPolicy{Mode: sandbox.NetworkWhitelist}, legacyNetworkedManifest())
	if len(*calls) != 1 {
		t.Fatalf("SetupNetwork calls = %d, want 1", len(*calls))
	}
	if got := (*calls)[0]; got.mode != sandbox.NetworkWhitelist || len(got.allowedHosts) != 0 {
		t.Fatalf("setup = %+v, want whitelist with an empty allowlist", got)
	}
}

func TestRestoreVMSnapshot_DenyNeverGetsANetwork(t *testing.T) {
	calls := stubSetupNetwork(t)
	err := restoreWith(t, sandbox.RestoreNetworkPolicy{Mode: sandbox.NetworkDeny}, legacyNetworkedManifest())
	if err == nil || !strings.Contains(err.Error(), "deny") {
		t.Fatalf("err = %v, want a refusal naming deny", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("SetupNetwork called %d times for a deny sandbox", len(*calls))
	}
}

func TestSetupRestoreNetwork_DenySnapshotWithoutNetworkRestoresWithoutOne(t *testing.T) {
	calls := stubSetupNetwork(t)
	m := legacyNetworkedManifest()
	m.Network = nil
	for _, mode := range []sandbox.NetworkMode{sandbox.NetworkDeny, sandbox.NetworkAllow} {
		cfg, err := setupRestoreNetwork("sbx-1", m, sandbox.RestoreNetworkPolicy{Mode: mode})
		if err != nil || cfg != nil {
			t.Fatalf("mode %q: cfg = %v, err = %v, want no network and no error", mode, cfg, err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("SetupNetwork called %d times for a snapshot with no network", len(*calls))
	}
}

func TestSetupRestoreNetwork_PinsSnapshotMAC(t *testing.T) {
	prev := setupNetworkFn
	setupNetworkFn = func(string, sandbox.NetworkMode, []string, []string) (*NetworkConfig, error) {
		return &NetworkConfig{GuestMAC: "06:00:0a:00:09:02"}, nil
	}
	t.Cleanup(func() { setupNetworkFn = prev })

	cfg, err := setupRestoreNetwork("sbx-1", legacyNetworkedManifest(), sandbox.RestoreNetworkPolicy{Mode: sandbox.NetworkAllow})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GuestMAC != "06:00:0a:00:05:02" {
		t.Fatalf("GuestMAC = %q, want the snapshot's", cfg.GuestMAC)
	}
}
