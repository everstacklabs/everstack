package channels

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// pausedChannelStore embeds a nil ChannelStore: any call the test does not
// expect (session routing, metering) panics.
type pausedChannelStore struct {
	ChannelStore
	configs []*ChannelConfigRecord
}

func (s *pausedChannelStore) ListEnabledChannelConfigs(context.Context) ([]*ChannelConfigRecord, error) {
	return s.configs, nil
}

// blockingConnector connects until its context is cancelled or it is stopped.
type blockingConnector struct {
	stopped chan struct{}
	once    sync.Once
}

func newBlockingConnector() *blockingConnector {
	return &blockingConnector{stopped: make(chan struct{})}
}

func (c *blockingConnector) Start(ctx context.Context) error {
	select {
	case <-ctx.Done():
	case <-c.stopped:
	}
	return ctx.Err()
}

func (c *blockingConnector) Stop(context.Context) error {
	c.once.Do(func() { close(c.stopped) })
	return nil
}

func (c *blockingConnector) Send(context.Context, string, string, OutboundMessage) (string, error) {
	return "", nil
}
func (c *blockingConnector) SendTyping(context.Context, string) error { return nil }
func (c *blockingConnector) EditMessage(context.Context, string, string, OutboundMessage) error {
	return nil
}
func (c *blockingConnector) Status() ConnectorStatus { return StatusConnected }
func (c *blockingConnector) Platform() Platform      { return PlatformSlack }

type pauseSwitch struct {
	mu     sync.Mutex
	paused map[string]bool
}

func (p *pauseSwitch) set(tenantID string, paused bool) {
	p.mu.Lock()
	p.paused[tenantID] = paused
	p.mu.Unlock()
}

func (p *pauseSwitch) active(_ context.Context, tenantID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.paused[tenantID]
}

func newPausedFixture(t *testing.T) (*ChannelManager, *pauseSwitch, *int32) {
	t.Helper()
	store := &pausedChannelStore{configs: []*ChannelConfigRecord{
		{ID: "cfg-a1", TenantID: "inst-a", Platform: string(PlatformSlack), Name: "a1", Enabled: true},
		{ID: "cfg-a2", TenantID: "inst-a", Platform: string(PlatformSlack), Name: "a2", Enabled: true},
		{ID: "cfg-b1", TenantID: "inst-b", Platform: string(PlatformSlack), Name: "b1", Enabled: true},
		{ID: "cfg-a3", TenantID: "inst-a", Platform: string(PlatformSlack), Name: "a3", Enabled: true, InstanceAffinity: "other-node"},
	}}
	m := NewChannelManager(store, nil, nil, "node-1", nil, nil, nil)
	var created int32
	m.RegisterFactory(PlatformSlack, func(ConnectorConfig, MessageHandler) (Connector, error) {
		atomic.AddInt32(&created, 1)
		return newBlockingConnector(), nil
	})
	sw := &pauseSwitch{paused: map[string]bool{}}
	m.SetTenantActiveFn(sw.active)
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, sw, &created
}

func running(m *ChannelManager) map[string]bool {
	out := map[string]bool{}
	for id := range m.GetAllStatuses() {
		out[id] = true
	}
	return out
}

func TestStartSkipsPausedTenantConnectors(t *testing.T) {
	m, sw, created := newPausedFixture(t)
	sw.set("inst-a", true)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := running(m)
	if len(got) != 1 || !got["cfg-b1"] {
		t.Fatalf("running connectors = %v, want only cfg-b1", got)
	}
	if n := atomic.LoadInt32(created); n != 1 {
		t.Fatalf("connectors created = %d, want 1 (a paused tenant must not dial out)", n)
	}

	// ReloadConfig-style start of a paused tenant's config is not an error.
	if err := m.startConnector(&ChannelConfigRecord{ID: "cfg-a1", TenantID: "inst-a", Platform: string(PlatformSlack)}); err != nil {
		t.Fatalf("startConnector() for paused tenant = %v, want nil", err)
	}
	if running(m)["cfg-a1"] {
		t.Fatal("paused tenant's connector was started")
	}
}

func TestStopAndStartTenantConnectors(t *testing.T) {
	m, sw, _ := newPausedFixture(t)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := running(m); len(got) != 3 {
		t.Fatalf("running connectors = %v, want 3", got)
	}

	sw.set("inst-a", true)
	if n := m.StopTenantConnectors(context.Background(), "inst-a"); n != 2 {
		t.Fatalf("StopTenantConnectors() = %d, want 2", n)
	}
	if got := running(m); len(got) != 1 || !got["cfg-b1"] {
		t.Fatalf("after stop, running = %v, want only cfg-b1", got)
	}
	if n := m.StopTenantConnectors(context.Background(), "inst-a"); n != 0 {
		t.Fatalf("second StopTenantConnectors() = %d, want 0", n)
	}

	// Still paused: the resume sweep must not bring it back.
	if n := m.StartTenantConnectors(context.Background(), "inst-a"); n != 0 {
		t.Fatalf("StartTenantConnectors() while paused = %d, want 0", n)
	}

	sw.set("inst-a", false)
	if n := m.StartTenantConnectors(context.Background(), "inst-a"); n != 2 {
		t.Fatalf("StartTenantConnectors() after resume = %d, want 2 (affinity-mismatched cfg-a3 excluded)", n)
	}
	if n := m.StartTenantConnectors(context.Background(), "inst-a"); n != 0 {
		t.Fatalf("repeat StartTenantConnectors() = %d, want 0 (idempotent)", n)
	}
	if got := running(m); len(got) != 3 || got["cfg-a3"] {
		t.Fatalf("after resume, running = %v", got)
	}
}

func TestHandleMessageDropsPausedTenant(t *testing.T) {
	m, sw, _ := newPausedFixture(t)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The connector is already running when the pause lands; it does not
	// re-check, so handleMessage must. Reaching metering or session routing
	// would call the nil embedded store and panic.
	sw.set("inst-a", true)
	err := m.handleMessage(context.Background(), InboundMessage{
		Platform:        PlatformSlack,
		ChannelConfigID: "cfg-a1",
		TenantID:        "inst-a",
		PlatformUserID:  "U1",
		Text:            "hello",
	})
	if err != nil {
		t.Fatalf("handleMessage() = %v, want nil (silent drop)", err)
	}

	// The trusted tenant is the stored config's, not the message field.
	err = m.handleMessage(context.Background(), InboundMessage{
		Platform:        PlatformSlack,
		ChannelConfigID: "cfg-a1",
		TenantID:        "inst-b",
		Text:            "spoofed tenant",
	})
	if err != nil {
		t.Fatalf("handleMessage() with spoofed tenant = %v, want nil (dropped)", err)
	}
}
