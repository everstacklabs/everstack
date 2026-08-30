package fcagent

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeAgentServer is the smallest possible FirecrackerAgent that
// satisfies the streaming methods under test. It echoes whatever
// payload was uploaded via Restore and replays it verbatim on
// Snapshot. Other methods return Unimplemented.
type fakeAgentServer struct {
	fcpb.UnimplementedFirecrackerAgentServer

	// snapshotPayload is what the server hands back on Snapshot. Set
	// from a test helper or implicitly by Restore (latest upload).
	snapshotPayload []byte
	snapshotSendErr error // optional injected error from Send loop

	lastRestoreSandbox string
	lastRestoreDest    string
	lastRestoreBytes   []byte
	restoreExitCode    int32
	restoreStderr      string
}

func (f *fakeAgentServer) Snapshot(req *fcpb.SnapshotRequest, stream grpc.ServerStreamingServer[fcpb.SnapshotChunk]) error {
	const chunk = 1 << 20
	for off := 0; off < len(f.snapshotPayload); off += chunk {
		end := off + chunk
		if end > len(f.snapshotPayload) {
			end = len(f.snapshotPayload)
		}
		if err := stream.Send(&fcpb.SnapshotChunk{Data: f.snapshotPayload[off:end]}); err != nil {
			return err
		}
		if f.snapshotSendErr != nil {
			return f.snapshotSendErr
		}
	}
	return nil
}

func (f *fakeAgentServer) Restore(stream grpc.ClientStreamingServer[fcpb.RestoreChunk, fcpb.RestoreResponse]) error {
	var (
		buf      bytes.Buffer
		gotFirst bool
	)
	for {
		c, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if !gotFirst {
			f.lastRestoreSandbox = c.GetSandboxId()
			f.lastRestoreDest = c.GetDestPath()
			gotFirst = true
		}
		if len(c.Data) > 0 {
			buf.Write(c.Data)
		}
	}
	f.lastRestoreBytes = buf.Bytes()
	return stream.SendAndClose(&fcpb.RestoreResponse{
		ExitCode:      f.restoreExitCode,
		Stderr:        f.restoreStderr,
		BytesReceived: int64(buf.Len()),
	})
}

// startFakeAgent boots fakeAgentServer on a bufconn and returns a
// FCAgentBackend wired to dial it through Discovery.staticTargets.
// Cleanup is registered via t.Cleanup.
func startFakeAgent(t *testing.T, fake *fakeAgentServer) *FCAgentBackend {
	t.Helper()
	const target = "buf-fakeagent:9090"

	srv := grpc.NewServer()
	fcpb.RegisterFirecrackerAgentServer(srv, fake)
	lis := bufconn.Listen(1 << 20)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("fake agent server.Serve: %v", err)
		}
	}()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough://"+target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(context.Background())
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	disc := &Discovery{
		targets: []string{target},
		conns:   map[string]*grpc.ClientConn{target: conn},
	}
	b := &FCAgentBackend{
		discovery: disc,
		routes:    map[string]string{"sbx-test": target},
	}
	return b
}

// TestSnapshotRoundtrip verifies the streaming Snapshot client writes
// every byte the server sent to disk, in order, with no truncation.
// 4 MiB random payload exercises the multi-chunk send path.
func TestSnapshotRoundtrip(t *testing.T) {
	const size = 4 << 20
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}
	fake := &fakeAgentServer{snapshotPayload: payload}
	b := startFakeAgent(t, fake)

	dir := t.TempDir()
	dest := filepath.Join(dir, "snap.tar.gz")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Snapshot(ctx, "sbx-test", "/workspace", dest); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("snapshot dest does not match server payload (got %d bytes, want %d)", len(got), len(payload))
	}
}

// TestSnapshotMissingRouteIsTyped exercises the route-missing path so
// callers can distinguish "no route" from a real backend failure.
func TestSnapshotMissingRouteIsTyped(t *testing.T) {
	b := &FCAgentBackend{
		discovery: &Discovery{},
		routes:    make(map[string]string),
	}
	err := b.Snapshot(context.Background(), "sbx-not-here", "/workspace", filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Fatal("expected route-missing error")
	}
	// Wrapped, so we just check the substring.
	if !contains(err.Error(), "snapshot:") {
		t.Fatalf("expected snapshot:-prefixed error, got %v", err)
	}
}

// TestRestoreRoundtrip verifies the client streams every byte from
// disk to the server, that the first frame carries metadata, and
// that subsequent frames carry only data.
func TestRestoreRoundtrip(t *testing.T) {
	const size = 3 << 20
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "snap.tar.gz")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	fake := &fakeAgentServer{}
	b := startFakeAgent(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Restore(ctx, "sbx-test", src, "/workspace"); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if fake.lastRestoreSandbox != "sbx-test" {
		t.Errorf("server saw sandbox_id=%q, want sbx-test", fake.lastRestoreSandbox)
	}
	if fake.lastRestoreDest != "/workspace" {
		t.Errorf("server saw dest=%q, want /workspace", fake.lastRestoreDest)
	}
	if !bytes.Equal(fake.lastRestoreBytes, payload) {
		t.Errorf("server received %d bytes, want %d (or content mismatch)", len(fake.lastRestoreBytes), len(payload))
	}
}

// TestRestoreNonZeroTarExitsErrors ensures a non-zero tar exit on the
// server side surfaces as an error to the caller (and is not silently
// swallowed), so the manager's revive path doesn't think the restore
// succeeded when it didn't.
func TestRestoreNonZeroTarExitsErrors(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "snap.tar.gz")
	if err := os.WriteFile(src, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	fake := &fakeAgentServer{
		restoreExitCode: 2,
		restoreStderr:   "tar: not in gzip format",
	}
	b := startFakeAgent(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := b.Restore(ctx, "sbx-test", src, "/workspace")
	if err == nil {
		t.Fatal("expected error for non-zero tar exit code")
	}
	if !contains(err.Error(), "exited 2") {
		t.Fatalf("expected exit-code in error, got: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && (indexOf(s, sub) >= 0)))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Compile-time assertion so a refactor that drops the Snapshotter
// methods fails the build before it lands.
var _ snapshotterCheck = (*FCAgentBackend)(nil)

type snapshotterCheck interface {
	Snapshot(ctx context.Context, id, src, dest string) error
	Restore(ctx context.Context, id, src, dest string) error
}

