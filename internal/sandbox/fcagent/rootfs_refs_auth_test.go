package fcagent

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// captureAgent records the authorization metadata of every
// ReportRootfsReferences call it receives.
type captureAgent struct {
	fcpb.UnimplementedFirecrackerAgentServer
	mu      sync.Mutex
	headers []string
}

func (c *captureAgent) ReportRootfsReferences(ctx context.Context, _ *fcpb.ReportRootfsReferencesRequest) (*fcpb.Empty, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	c.mu.Lock()
	c.headers = append(c.headers, md.Get("authorization")...)
	c.mu.Unlock()
	return &fcpb.Empty{}, nil
}

// The rootfs GC report fans out to every discovered agent through the
// discovery's cached connections. Those connections must carry the
// bearer token, otherwise an enforcing agent rejects the report and
// its GC loses the gateway's references.
func TestReportRootfsReferences_SendsBearerTokenToEveryAgent(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	agent := &captureAgent{}
	srv := grpc.NewServer()
	fcpb.RegisterFirecrackerAgentServer(srv, agent)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dialOpts, err := buildDialOptions(TLSConfig{}, "gc-token")
	if err != nil {
		t.Fatalf("buildDialOptions: %v", err)
	}
	dialOpts = append(dialOpts, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}))

	// Two static targets (a comma makes the list static, no DNS); both
	// land on the same in-memory listener.
	disc, err := NewDiscovery("agent-a:1,agent-b:1", 1, time.Hour, 2*time.Second, dialOpts)
	if err != nil {
		t.Fatalf("NewDiscovery: %v", err)
	}
	t.Cleanup(disc.Stop)

	b := &FCAgentBackend{discovery: disc}
	if err := b.ReportRootfsReferences(context.Background(), "gateway-test", []string{"base:v1"}, false); err != nil {
		t.Fatalf("ReportRootfsReferences: %v", err)
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.headers) != 2 {
		t.Fatalf("expected one authenticated call per target, got headers %v", agent.headers)
	}
	for _, h := range agent.headers {
		if h != "Bearer gc-token" {
			t.Fatalf("authorization = %q, want %q", h, "Bearer gc-token")
		}
	}
}
