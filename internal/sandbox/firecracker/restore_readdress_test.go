package firecracker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// fakeGuest records what the restore path asks the guest agent to do.
type fakeGuest struct {
	udsPath    string
	readyErr   error
	execResult *sandbox.ExecResult
	execErr    error

	files  map[string]string
	tokens []string
	execs  []string
}

func (g *fakeGuest) WaitReady(context.Context, time.Duration) error { return g.readyErr }

func (g *fakeGuest) WriteFile(_ context.Context, path string, content []byte) error {
	if g.files == nil {
		g.files = map[string]string{}
	}
	g.files[path] = string(content)
	return nil
}

func (g *fakeGuest) SetAgentToken(_ context.Context, token string) error {
	g.tokens = append(g.tokens, token)
	return nil
}

func (g *fakeGuest) Exec(_ context.Context, cmd ExecCommand) (*sandbox.ExecResult, error) {
	g.execs = append(g.execs, strings.Join(cmd.Command, " "))
	if g.execErr != nil {
		return nil, g.execErr
	}
	if g.execResult != nil {
		return g.execResult, nil
	}
	return &sandbox.ExecResult{ExitCode: 0}, nil
}

// restoreHarness stubs every host-side effect of a restore: network
// setup and teardown, the firecracker boot, the vsock guest agent and
// the HTTP readiness probe.
type restoreHarness struct {
	netCfg    *NetworkConfig
	guest     *fakeGuest
	kills     int
	cleanups  []*NetworkConfig
	probedIPs []string
	probeErr  error
}

func newRestoreHarness(t *testing.T) *restoreHarness {
	t.Helper()
	h := &restoreHarness{
		// The restore's new /30, deliberately not the manifest's 10.0.5.0/30.
		netCfg: &NetworkConfig{
			TapDevice: "tap-new",
			HostIP:    "10.0.9.1",
			GuestIP:   "10.0.9.2",
			Mode:      sandbox.NetworkAllow,
			MTU:       1400,
		},
		guest: &fakeGuest{},
	}

	prevSetup, prevCleanup := setupNetworkFn, cleanupNetworkFn
	prevBoot, prevClient, prevProbe := bootFromSnapshotFn, newGuestAgentClientFn, waitForAgentReadyFn
	t.Cleanup(func() {
		setupNetworkFn, cleanupNetworkFn = prevSetup, prevCleanup
		bootFromSnapshotFn, newGuestAgentClientFn, waitForAgentReadyFn = prevBoot, prevClient, prevProbe
	})

	setupNetworkFn = func(string, sandbox.NetworkMode, []string, []string) (*NetworkConfig, error) {
		return h.netCfg, nil
	}
	cleanupNetworkFn = func(cfg *NetworkConfig) { h.cleanups = append(h.cleanups, cfg) }
	bootFromSnapshotFn = func(_ context.Context, _ snapshot.Store, opts RestoreOpts, overrides []NetworkOverride) (*snapshotBoot, error) {
		if len(overrides) != 1 || overrides[0].HostDevName != "tap-new" {
			t.Errorf("overrides = %+v, want eth0 on tap-new", overrides)
		}
		return &snapshotBoot{
			socketPath: opts.WorkDirRoot + "/" + opts.SandboxID + "/firecracker.sock",
			vsockPath:  opts.WorkDirRoot + "/" + opts.SandboxID + "/vsock.sock",
			workDir:    opts.WorkDirRoot + "/" + opts.SandboxID,
			kill:       func() { h.kills++ },
		}, nil
	}
	newGuestAgentClientFn = func(udsPath string) guestAgentClient {
		h.guest.udsPath = udsPath
		return h.guest
	}
	waitForAgentReadyFn = func(_ context.Context, guestIP string) error {
		h.probedIPs = append(h.probedIPs, guestIP)
		return h.probeErr
	}
	return h
}

func (h *restoreHarness) restore(t *testing.T) (*FirecrackerBackend, *sandbox.Instance, error) {
	t.Helper()
	b := &FirecrackerBackend{
		config: FirecrackerConfig{BinaryPath: "/nonexistent/firecracker", WorkDir: t.TempDir()},
		vms:    map[string]*MicroVM{},
	}
	inst, err := b.RestoreVMSnapshot(context.Background(), "sbx-1", "tenant-a", "agent-1",
		sandbox.RestoreNetworkPolicy{Mode: sandbox.NetworkAllow}, manifestStore{manifest: legacyNetworkedManifest()})
	return b, inst, err
}

func TestRestoreVMSnapshot_ReaddressesGuestOntoNewSubnet(t *testing.T) {
	h := newRestoreHarness(t)
	b, inst, err := h.restore(t)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got, want := h.guest.files["/etc/resolv.conf"], "nameserver 10.0.9.1\n"; got != want {
		t.Fatalf("resolv.conf = %q, want %q (the new host IP's DNS proxy)", got, want)
	}
	if len(h.guest.execs) != 1 {
		t.Fatalf("guest execs = %v, want one network setup", h.guest.execs)
	}
	cmd := h.guest.execs[0]
	for _, want := range []string{
		"ip addr flush dev eth0",
		"ip addr add 10.0.9.2/30 dev eth0",
		"ip route replace default via 10.0.9.1",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("network setup %q lacks %q", cmd, want)
		}
	}
	// Nothing from the snapshot's /30 reaches the guest.
	for _, s := range append([]string{cmd}, h.guest.files["/etc/resolv.conf"]) {
		if strings.Contains(s, "10.0.5.") {
			t.Fatalf("guest was configured with the snapshot's old address: %q", s)
		}
	}
	if len(h.probedIPs) != 1 || h.probedIPs[0] != "10.0.9.2" {
		t.Fatalf("agent probed at %v, want the new guest IP 10.0.9.2", h.probedIPs)
	}

	vm := b.vms["sbx-1"]
	if vm == nil {
		t.Fatal("restored VM not registered")
	}
	if len(h.guest.tokens) != 1 || h.guest.tokens[0] == "" || h.guest.tokens[0] != vm.AgentToken {
		t.Fatalf("guest got tokens %d, want one fresh token matching the host's", len(h.guest.tokens))
	}
	if h.guest.udsPath != vm.VsockPath {
		t.Fatalf("vsock client on %q, want the VM's %q", h.guest.udsPath, vm.VsockPath)
	}
	if vm.Network != h.netCfg {
		t.Fatal("restored VM does not carry the network the anti-spoof rule was pinned for")
	}
	if h.kills != 0 || len(h.cleanups) != 0 {
		t.Fatalf("successful restore killed %d / cleaned %d, want 0 / 0", h.kills, len(h.cleanups))
	}

	if inst.Config.TenantID != "tenant-a" || vm.Config.TenantID != "tenant-a" {
		t.Fatalf("tenant ID instance=%q vm=%q, want tenant-a", inst.Config.TenantID, vm.Config.TenantID)
	}
}

func TestRestoreVMSnapshot_ReaddressFailureKillsVMAndReleasesSubnet(t *testing.T) {
	cases := map[string]func(h *restoreHarness){
		"guest agent unreachable": func(h *restoreHarness) {
			h.guest.readyErr = errors.New("vsock CONNECT read: i/o timeout")
		},
		"vsock transport error": func(h *restoreHarness) {
			h.guest.execErr = errors.New("read unix vsock.sock: i/o timeout")
		},
		// On a resumed guest, File exists means the old route survived.
		"old route survives": func(h *restoreHarness) {
			h.guest.execResult = &sandbox.ExecResult{ExitCode: 2, Stderr: "RTNETLINK answers: File exists"}
		},
		"agent unreachable on new IP": func(h *restoreHarness) {
			h.probeErr = errors.New("connection refused")
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			h := newRestoreHarness(t)
			breakIt(h)
			b, inst, err := h.restore(t)
			if err == nil || !strings.Contains(err.Error(), "re-address guest") {
				t.Fatalf("err = %v, want a re-address failure", err)
			}
			if inst != nil {
				t.Fatal("failed restore returned an instance")
			}
			if h.kills != 1 {
				t.Fatalf("VM killed %d times, want 1", h.kills)
			}
			if len(h.cleanups) != 1 || h.cleanups[0] != h.netCfg {
				t.Fatalf("network cleanups = %d, want one for the restore's /30", len(h.cleanups))
			}
			if _, ok := b.vms["sbx-1"]; ok {
				t.Fatal("failed restore left the VM registered")
			}
		})
	}
}

func TestRestoreSnapshot_RefusesAnotherTenantsManifest(t *testing.T) {
	h := newRestoreHarness(t)
	m := legacyNetworkedManifest()
	m.TenantID = "tenant-b"
	_, err := RestoreSnapshot(context.Background(), manifestStore{manifest: m}, RestoreOpts{
		SandboxID: "sbx-1", TenantID: "tenant-a", FCBinary: "/nonexistent/firecracker",
		WorkDirRoot: t.TempDir(), Network: h.netCfg,
	})
	if err == nil || !strings.Contains(err.Error(), "another tenant") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if h.kills != 0 || len(h.guest.execs) != 0 {
		t.Fatal("refused restore still booted or configured a guest")
	}
}

// The store never writes a manifest without a tenant, so a blank one is a
// corrupt or misplaced manifest and must be refused like a foreign one.
func TestRestoreSnapshot_RefusesManifestWithoutTenant(t *testing.T) {
	h := newRestoreHarness(t)
	m := legacyNetworkedManifest()
	m.TenantID = ""
	_, err := RestoreSnapshot(context.Background(), manifestStore{manifest: m}, RestoreOpts{
		SandboxID: "sbx-1", TenantID: "tenant-a", FCBinary: "/nonexistent/firecracker",
		WorkDirRoot: t.TempDir(), Network: h.netCfg,
	})
	if err == nil || !strings.Contains(err.Error(), "another tenant") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if h.kills != 0 || len(h.guest.execs) != 0 {
		t.Fatal("refused restore still booted or configured a guest")
	}
}

// A network handed in for a deny-mode snapshot is the caller's to release:
// RestoreSnapshot errors and must not release it itself (a second release
// by the caller would free the /30 twice).
func TestRestoreSnapshot_DenySnapshotWithNetworkLeavesReleaseToCaller(t *testing.T) {
	h := newRestoreHarness(t)
	m := legacyNetworkedManifest()
	m.TenantID = "tenant-a"
	m.Network = nil
	_, err := RestoreSnapshot(context.Background(), manifestStore{manifest: m}, RestoreOpts{
		SandboxID: "sbx-1", TenantID: "tenant-a", FCBinary: "/nonexistent/firecracker",
		WorkDirRoot: t.TempDir(), Network: h.netCfg,
	})
	if err == nil {
		t.Fatal("want an error for a deny snapshot given a network")
	}
	if len(h.cleanups) != 0 {
		t.Fatalf("RestoreSnapshot released the network itself %d time(s); only the caller may", len(h.cleanups))
	}
	if h.kills != 0 {
		t.Fatal("nothing was booted, nothing should be killed")
	}
}
