package docker

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/egress"
)

func TestStartEgressControlFailsClosedForWhitelist(t *testing.T) {
	t.Parallel()

	b := &DockerBackend{}
	err := b.startEgressControl(context.Background(), "sbx-a", sandbox.InstanceConfig{NetworkMode: sandbox.NetworkWhitelist})
	if err == nil {
		t.Fatal("expected missing egress controller to fail")
	}
}

func TestStartEgressControlSkipsUnrestrictedModes(t *testing.T) {
	t.Parallel()

	b := &DockerBackend{}
	for _, mode := range []sandbox.NetworkMode{sandbox.NetworkAllow, sandbox.NetworkDeny} {
		if err := b.startEgressControl(context.Background(), "sbx-a", sandbox.InstanceConfig{NetworkMode: mode}); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
}

func TestStartEgressControlPropagatesSidecarFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	b := &DockerBackend{egress: fakeEgressController{err: wantErr}}
	err := b.startEgressControl(context.Background(), "sbx-a", sandbox.InstanceConfig{NetworkMode: sandbox.NetworkWhitelist})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}

func TestStartEgressControlPassesWhitelistConfig(t *testing.T) {
	t.Parallel()

	fake := &recordingEgressController{}
	b := &DockerBackend{egress: fake}
	cfg := sandbox.InstanceConfig{
		NetworkMode:  sandbox.NetworkWhitelist,
		AllowedHosts: []string{"example.com"},
		DNSServers:   []string{"1.1.1.1:53"},
	}
	if err := b.startEgressControl(context.Background(), "sbx-a", cfg); err != nil {
		t.Fatalf("startEgressControl: %v", err)
	}
	if fake.sandboxID != "sbx-a" || fake.cfg.Mode != egress.EgressWhitelist || fake.cfg.AllowedHosts[0] != "example.com" || fake.cfg.DNSServers[0] != "1.1.1.1:53" {
		t.Fatalf("unexpected egress call: id=%q cfg=%+v", fake.sandboxID, fake.cfg)
	}
}

type fakeEgressController struct {
	err error
}

func (f fakeEgressController) Start(context.Context, string, egress.EgressConfig) error {
	return f.err
}

func (f fakeEgressController) Stop(context.Context, string) error {
	return nil
}

type recordingEgressController struct {
	sandboxID string
	cfg       egress.EgressConfig
}

func (r *recordingEgressController) Start(_ context.Context, sandboxID string, cfg egress.EgressConfig) error {
	r.sandboxID = sandboxID
	r.cfg = cfg
	return nil
}

func (r *recordingEgressController) Stop(context.Context, string) error {
	return nil
}
