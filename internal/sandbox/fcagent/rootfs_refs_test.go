package fcagent

import (
	"context"
	"sync"
	"testing"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type rootfsRefsAgent struct {
	fcpb.UnimplementedFirecrackerAgentServer
	mu   sync.Mutex
	reqs []*fcpb.ReportRootfsReferencesRequest
	err  error
}

func (a *rootfsRefsAgent) ReportRootfsReferences(_ context.Context, req *fcpb.ReportRootfsReferencesRequest) (*fcpb.Empty, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reqs = append(a.reqs, req)
	if a.err != nil {
		return nil, a.err
	}
	return &fcpb.Empty{}, nil
}

func TestReportRootfsReferencesReachesEveryAgent(t *testing.T) {
	agent := &rootfsRefsAgent{}
	b := startFakeAgent(t, agent)

	images := []string{"everstack/openai-codex-firecracker-rootfs:openai-codex-0.154.0-r3"}
	if err := b.ReportRootfsReferences(context.Background(), "everstack-gateway/gw-pod-1", images, true); err != nil {
		t.Fatalf("ReportRootfsReferences: %v", err)
	}
	if len(agent.reqs) != 1 {
		t.Fatalf("agent got %d reports, want 1", len(agent.reqs))
	}
	got := agent.reqs[0]
	if got.GetReporter() != "everstack-gateway/gw-pod-1" || len(got.GetImages()) != 1 || got.GetImages()[0] != images[0] || !got.GetUnknown() {
		t.Fatalf("report = %+v", got)
	}
}

func TestReportRootfsReferencesToleratesAgentsWithoutTheRPC(t *testing.T) {
	// An agent older than the rootfs GC answers Unimplemented. That is
	// the expected mixed-version state during a rollout, not an error.
	b := startFakeAgent(t, &rootfsRefsAgent{err: status.Error(codes.Unimplemented, "unknown method")})
	if err := b.ReportRootfsReferences(context.Background(), "gw", nil, false); err != nil {
		t.Fatalf("err = %v, want nil for Unimplemented", err)
	}
}

func TestReportRootfsReferencesSurfacesRealFailures(t *testing.T) {
	b := startFakeAgent(t, &rootfsRefsAgent{err: status.Error(codes.Internal, "boom")})
	if err := b.ReportRootfsReferences(context.Background(), "gw", nil, false); err == nil {
		t.Fatal("expected error")
	}
}
