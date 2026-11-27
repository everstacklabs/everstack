package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/firecracker"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentServer implements the generated FirecrackerAgentServer gRPC interface.
// It wraps the in-process FirecrackerBackend and exposes it over gRPC.
type AgentServer struct {
	fcpb.UnimplementedFirecrackerAgentServer
	backend  *firecracker.FirecrackerBackend
	funcEnvs *FunctionEnvManager
	nodeName string

	// rootfsRefs collects the gateways' referenced-image reports for the
	// rootfs GC. Created with the server so reports received before the
	// GC starts are not lost.
	rootfsRefs *firecracker.RootfsReferences
}

// NewAgentServer creates a new agent server wrapping the Firecracker backend.
//
// backend may be nil at registration time — main.go registers the
// gRPC service up front so the listener is open for kubelet probes,
// then attaches the backend via SetBackend once preflight passes.
// Until then the notReadyUnaryInterceptor / notReadyStreamInterceptor
// reject FirecrackerAgent calls with Unavailable, so handler code
// never sees a nil backend.
func NewAgentServer(backend *firecracker.FirecrackerBackend, nodeName string) *AgentServer {
	return &AgentServer{
		backend:  backend,
		nodeName: nodeName,
		// No reporter is trusted until SetRootfsReporters runs, so the GC
		// keeps every tag image by default.
		rootfsRefs: firecracker.NewRootfsReferences(firecracker.DefaultRootfsReferenceMaxAge, nil),
	}
}

// SetRootfsReporters replaces the reference store with one that records
// reports only from the given reporter names (FC_ROOTFS_GC_REPORTERS).
// Call before the server starts serving.
func (s *AgentServer) SetRootfsReporters(names []string) {
	s.rootfsRefs = firecracker.NewRootfsReferences(firecracker.DefaultRootfsReferenceMaxAge, names)
}

// RootfsReferences returns the store the rootfs GC reads gateway reports
// from.
func (s *AgentServer) RootfsReferences() *firecracker.RootfsReferences {
	return s.rootfsRefs
}

// maxRootfsReferenceImages bounds one report. A gateway sends DISTINCT
// image references, which number in the tens; the cap only stops a
// misbehaving caller from growing agent memory without limit.
const maxRootfsReferenceImages = 4096

// ReportRootfsReferences records which rootfs images the calling
// gateway still references. See RootfsReferences for how reports from
// several gateways combine and expire.
func (s *AgentServer) ReportRootfsReferences(_ context.Context, req *fcpb.ReportRootfsReferencesRequest) (*fcpb.Empty, error) {
	if len(req.GetImages()) > maxRootfsReferenceImages {
		return nil, status.Errorf(codes.InvalidArgument,
			"report names %d images, limit %d", len(req.GetImages()), maxRootfsReferenceImages)
	}
	// Caller authentication belongs to the transport; this only keeps an
	// unexpected reporter's view out of the GC, and tells it so.
	if !s.rootfsRefs.Report(req.GetReporter(), req.GetImages(), req.GetUnknown()) {
		return nil, status.Errorf(codes.PermissionDenied,
			"reporter %q is not in FC_ROOTFS_GC_REPORTERS", firecracker.RootfsReporterName(req.GetReporter()))
	}
	return &fcpb.Empty{}, nil
}

// SetBackend installs the real backend after preflight succeeds.
// Safe under the readiness gate: the atomic.Bool flip in readiness
// happens-after this assignment, and handlers only run when that
// flag is true, so the field write is observed.
func (s *AgentServer) SetBackend(b *firecracker.FirecrackerBackend) {
	s.backend = b
}

// SetFunctionEnvManager installs the isolated-function runtime after the
// backend is ready. Until set, InvokeFunction returns Unavailable — the
// readiness gate keeps callers away before this point anyway.
func (s *AgentServer) SetFunctionEnvManager(m *FunctionEnvManager) {
	s.funcEnvs = m
}

func (s *AgentServer) CreateSandbox(ctx context.Context, req *fcpb.CreateSandboxRequest) (*fcpb.SandboxInstance, error) {
	if req == nil || req.Config == nil {
		return nil, fmt.Errorf("create sandbox: request or config is nil")
	}
	inst, err := s.backend.Create(ctx, req.Id, configFromProto(req.Config))
	if err != nil {
		log.Printf("firecracker-agent: create failed id=%s: %v", req.Id, err)
		return nil, fmt.Errorf("node %s: create failed: %w", s.nodeName, err)
	}
	return s.instanceToProto(inst), nil
}

func (s *AgentServer) Exec(ctx context.Context, req *fcpb.ExecRequest) (*fcpb.ExecResult, error) {
	if req == nil {
		return nil, fmt.Errorf("exec: request is nil")
	}
	result, err := s.backend.Exec(ctx, req.SandboxId, sandbox.ExecRequest{
		Command: req.Command,
		WorkDir: req.WorkDir,
		Env:     req.Env,
		Timeout: time.Duration(req.TimeoutMs) * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	return &fcpb.ExecResult{
		ExitCode:   int32(result.ExitCode),
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		DurationMs: result.DurationMs,
		TimedOut:   result.TimedOut,
	}, nil
}

func (s *AgentServer) WriteFile(ctx context.Context, req *fcpb.WriteFileRequest) (*fcpb.Empty, error) {
	if req == nil {
		return nil, fmt.Errorf("write file: request is nil")
	}
	if err := s.backend.WriteFile(ctx, req.SandboxId, req.Path, req.Content); err != nil {
		return nil, err
	}
	return &fcpb.Empty{}, nil
}

func (s *AgentServer) ReadFile(ctx context.Context, req *fcpb.ReadFileRequest) (*fcpb.ReadFileResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("read file: request is nil")
	}
	data, err := s.backend.ReadFile(ctx, req.SandboxId, req.Path)
	if err != nil {
		return nil, err
	}
	return &fcpb.ReadFileResponse{Content: data, Size: int64(len(data))}, nil
}

func (s *AgentServer) ListFiles(ctx context.Context, req *fcpb.ListFilesRequest) (*fcpb.ListFilesResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("list files: request is nil")
	}
	files, err := s.backend.ListFiles(ctx, req.SandboxId, req.Path)
	if err != nil {
		return nil, err
	}
	out := make([]*fcpb.FileInfo, 0, len(files))
	for _, f := range files {
		out = append(out, &fcpb.FileInfo{
			Name:  f.Name,
			Path:  f.Path,
			Size:  f.Size,
			IsDir: f.IsDir,
		})
	}
	return &fcpb.ListFilesResponse{Files: out}, nil
}

func (s *AgentServer) DestroySandbox(ctx context.Context, req *fcpb.DestroySandboxRequest) (*fcpb.Empty, error) {
	if req == nil {
		return nil, fmt.Errorf("destroy sandbox: request is nil")
	}
	if err := s.backend.Destroy(ctx, req.Id); err != nil {
		return nil, err
	}
	return &fcpb.Empty{}, nil
}

func (s *AgentServer) Status(ctx context.Context, req *fcpb.StatusRequest) (*fcpb.SandboxInstance, error) {
	if req == nil {
		return nil, fmt.Errorf("status: request is nil")
	}
	inst, err := s.backend.Status(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return s.instanceToProto(inst), nil
}

func (s *AgentServer) Stats(ctx context.Context, req *fcpb.StatsRequest) (*fcpb.SandboxStats, error) {
	if req == nil {
		return nil, fmt.Errorf("stats: request is nil")
	}
	st, err := s.backend.Stats(ctx, req.SandboxId)
	if err != nil {
		return nil, err
	}
	return &fcpb.SandboxStats{
		CpuPercent:     st.CPUPercent,
		MemoryUsage:    st.MemoryUsage,
		MemoryLimit:    st.MemoryLimit,
		MemoryPercent:  st.MemoryPercent,
		NetworkRxBytes: st.NetworkRxBytes,
		NetworkTxBytes: st.NetworkTxBytes,
		BlockRead:      st.BlockRead,
		BlockWrite:     st.BlockWrite,
		Pids:           int32(st.PIDs),
		TimestampUnix:  st.Timestamp.Unix(),
	}, nil
}

func (s *AgentServer) ExposePort(ctx context.Context, req *fcpb.ExposePortRequest) (*fcpb.ExposePortResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("expose_port: request is nil")
	}
	hostPort, err := s.backend.ExposePort(ctx, req.SandboxId, int(req.Port), req.Protocol)
	if err != nil {
		return nil, err
	}
	return &fcpb.ExposePortResponse{HostPort: int32(hostPort)}, nil
}

func (s *AgentServer) UnexposePort(ctx context.Context, req *fcpb.UnexposePortRequest) (*fcpb.Empty, error) {
	if req == nil {
		return nil, fmt.Errorf("unexpose_port: request is nil")
	}
	if err := s.backend.UnexposePort(ctx, req.SandboxId, int(req.Port)); err != nil {
		return nil, err
	}
	return &fcpb.Empty{}, nil
}

func (s *AgentServer) ListSandboxes(ctx context.Context, _ *fcpb.Empty) (*fcpb.ListSandboxesResponse, error) {
	instances, err := s.backend.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*fcpb.SandboxInstance, 0, len(instances))
	for _, inst := range instances {
		out = append(out, s.instanceToProto(inst))
	}
	return &fcpb.ListSandboxesResponse{Instances: out}, nil
}

func (s *AgentServer) NodeInfo(_ context.Context, _ *fcpb.Empty) (*fcpb.NodeInfoResponse, error) {
	return &fcpb.NodeInfoResponse{
		NodeName:      s.nodeName,
		Backend:       s.backend.Name(),
		TimestampUnix: time.Now().Unix(),
	}, nil
}

// GetNodeHealth returns the agent's self-assessed pressure signal.
// Polled by the gateway's LoadBalancer to gate placement: when
// healthy=false this node gets skipped for new sandbox creates
// until the next poll cycle. Cheap to compute — pure reads from
// /proc + statfs + rlimit, no I/O on the hot path.
//
// Thresholds are evaluated host-side so the gateway stays thin and
// per-host overrides (e.g. small-disk dev nodes) live with the
// node config. Override via FCAGENT_HEALTH_DISK_PCT,
// FCAGENT_HEALTH_MEM_PCT, FCAGENT_HEALTH_CPU_PCT,
// FCAGENT_HEALTH_FD_PCT env vars.
func (s *AgentServer) GetNodeHealth(_ context.Context, _ *fcpb.Empty) (*fcpb.NodeHealth, error) {
	return collectHostHealth(s.backend, defaultPressureThresholds()), nil
}

// Logs streams sandbox log bytes to the caller.
func (s *AgentServer) Logs(req *fcpb.LogsRequest, stream grpc.ServerStreamingServer[fcpb.LogChunk]) error {
	if req == nil {
		return fmt.Errorf("logs: request is nil")
	}
	opts := sandbox.LogsOptions{
		Follow:     req.Follow,
		Tail:       int(req.Tail),
		Timestamps: req.Timestamps,
	}
	if req.SinceUnix > 0 {
		opts.Since = time.Unix(req.SinceUnix, 0)
	}
	rc, err := s.backend.Logs(stream.Context(), req.SandboxId, opts)
	if err != nil {
		return err
	}
	defer rc.Close()
	buf := make([]byte, 32*1024)
	for {
		n, readErr := rc.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&fcpb.LogChunk{Data: append([]byte(nil), buf[:n]...)}); sendErr != nil {
				return sendErr
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

// Shell proxies a bidirectional interactive shell session. Honors
// init.SessionId for reattach when the underlying backend implements
// PersistentShellBackend; falls back to the legacy "new shell each
// call" behavior for backends that don't.
func (s *AgentServer) Shell(stream grpc.BidiStreamingServer[fcpb.ShellClientMessage, fcpb.ShellServerMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("shell: recv init: %w", err)
	}
	init := first.GetInit()
	if init == nil {
		return fmt.Errorf("shell: first message must be init")
	}

	// s.backend is the concrete *FirecrackerBackend, which always
	// implements PersistentShellBackend — no interface assertion
	// needed here.
	sess, err := s.backend.ShellWithSession(stream.Context(), init.SandboxId, init.SessionId, init.Command)
	if err != nil {
		// Translate ErrSessionGone into the typed "session_terminated"
		// exit frame so the gateway can present a clean message to
		// the user rather than a stack-trace-shaped string.
		if errors.Is(err, firecracker.ErrSessionGone) {
			_ = stream.Send(&fcpb.ShellServerMessage{
				Msg: &fcpb.ShellServerMessage_Exit{Exit: &fcpb.ShellExit{
					Code:              -1,
					Error:             "session_gone",
					SessionTerminated: true,
				}},
			})
			return nil
		}
		return err
	}
	defer sess.Conn.Close()

	// Send the session metadata as the first server message so the
	// gateway / client knows which session_id it landed on. Older
	// clients ignore unknown oneofs gracefully.
	if sess.ShellSessionID != "" {
		if sendErr := stream.Send(&fcpb.ShellServerMessage{
			Msg: &fcpb.ShellServerMessage_Session{Session: &fcpb.ShellSession{
				SessionId:  sess.ShellSessionID,
				Reattached: sess.Reattached,
				Transport:  sess.Transport,
			}},
		}); sendErr != nil {
			return fmt.Errorf("shell: send session metadata: %w", sendErr)
		}
	}

	// server → client: forward stdout bytes
	errCh := make(chan error, 2)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := sess.Conn.Read(buf)
			if n > 0 {
				if sendErr := stream.Send(&fcpb.ShellServerMessage{
					Msg: &fcpb.ShellServerMessage_Stdout{
						Stdout: &fcpb.ShellStdout{Data: append([]byte(nil), buf[:n]...)},
					},
				}); sendErr != nil {
					errCh <- sendErr
					return
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					errCh <- readErr
					return
				}
				errCh <- nil
				return
			}
		}
	}()

	// client → server: stdin, resize, close
	go func() {
		for {
			msg, recvErr := stream.Recv()
			if recvErr != nil {
				if errors.Is(recvErr, io.EOF) {
					errCh <- nil
					return
				}
				errCh <- recvErr
				return
			}
			switch m := msg.Msg.(type) {
			case *fcpb.ShellClientMessage_Stdin:
				if _, writeErr := sess.Conn.Write(m.Stdin.Data); writeErr != nil {
					errCh <- writeErr
					return
				}
			case *fcpb.ShellClientMessage_Resize:
				if sess.Resize != nil {
					_ = sess.Resize(uint16(m.Resize.Rows), uint16(m.Resize.Cols))
				}
			case *fcpb.ShellClientMessage_Close:
				errCh <- nil
				return
			}
		}
	}()

	// wait for either side to finish
	firstErr := <-errCh
	_ = sess.Conn.Close()
	<-errCh
	if firstErr != nil {
		_ = stream.Send(&fcpb.ShellServerMessage{
			Msg: &fcpb.ShellServerMessage_Exit{
				Exit: &fcpb.ShellExit{Code: 1, Error: firstErr.Error()},
			},
		})
		return firstErr
	}
	return stream.Send(&fcpb.ShellServerMessage{
		Msg: &fcpb.ShellServerMessage_Exit{Exit: &fcpb.ShellExit{Code: 0}},
	})
}

// ListSessions enumerates persistent shell sessions inside a sandbox.
// The response carries the guest's wall-clock time so callers can
// compute "last active 3m ago" without trusting their own clock.
func (s *AgentServer) ListSessions(ctx context.Context, req *fcpb.ListSessionsRequest) (*fcpb.ListSessionsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("list_sessions: request is nil")
	}
	infos, nowUnix, err := s.backend.ListShellSessionsWithClock(ctx, req.SandboxId)
	if err != nil {
		return nil, err
	}
	out := &fcpb.ListSessionsResponse{
		Sessions: make([]*fcpb.SessionInfo, 0, len(infos)),
		NowUnix:  nowUnix,
	}
	for _, i := range infos {
		out.Sessions = append(out.Sessions, &fcpb.SessionInfo{
			SessionId:        i.ID,
			AttachedClients:  uint32(i.AttachedClients),
			CreatedUnix:      i.CreatedUnix,
			LastActivityUnix: i.LastActivityUnix,
		})
	}
	return out, nil
}

// KillSession terminates a persistent shell session.
func (s *AgentServer) KillSession(ctx context.Context, req *fcpb.KillSessionRequest) (*fcpb.Empty, error) {
	if req == nil {
		return nil, fmt.Errorf("kill_session: request is nil")
	}
	if err := s.backend.KillShellSession(ctx, req.SandboxId, req.SessionId); err != nil {
		return nil, err
	}
	return &fcpb.Empty{}, nil
}

// --- Snapshot / Restore (streaming) ---
//
// These two RPCs replace the old "tar in VM → ReadFile entire archive
// as []byte → WriteFile to disk" path that capped the gateway at 1 GB
// per snapshot and OOMed under N concurrent stops. They still buffer
// the archive in fcagent memory (vsock has no streaming primitives),
// but the gateway sees the bytes as a stream of 1 MiB chunks and
// writes them straight to disk without ever holding the full archive.
// fcagent's cgroup is sized for one archive in flight at a time.

const (
	snapshotChunkBytes        = 1 << 20 // 1 MiB
	snapshotInVMArchivePath   = "/tmp/everstack_snapshot.tar.gz"
	restoreInVMArchivePath    = "/tmp/everstack_restore.tar.gz"
	snapshotDefaultSourcePath = "/workspace"
	snapshotDefaultDestPath   = "/workspace"
	snapshotTarTimeout        = 90 * time.Second
)

// Snapshot streams a tar.gz of source_path inside the microVM to the
// caller. Server-streaming so the gateway never holds the full archive.
func (s *AgentServer) Snapshot(req *fcpb.SnapshotRequest, stream grpc.ServerStreamingServer[fcpb.SnapshotChunk]) error {
	if req == nil {
		return fmt.Errorf("snapshot: request is nil")
	}
	if s.backend == nil {
		return fmt.Errorf("snapshot: backend not ready")
	}
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return fmt.Errorf("snapshot: sandbox_id is required")
	}
	sourcePath := req.GetSourcePath()
	if sourcePath == "" {
		sourcePath = snapshotDefaultSourcePath
	}

	ctx := stream.Context()

	// Run tar inside the VM, archiving the requested path. The
	// `stat -c %s` tail prints the archive size so we can enforce
	// max_bytes before pulling the bytes out.
	tarCmd := fmt.Sprintf(
		"tar -czf %s -C / %s && stat -c %%s %s",
		snapshotInVMArchivePath,
		sandboxRelPath(sourcePath),
		snapshotInVMArchivePath,
	)
	res, err := s.backend.Exec(ctx, sandboxID, sandbox.ExecRequest{
		Command: []string{"sh", "-lc", tarCmd},
		Timeout: snapshotTarTimeout,
	})
	if err != nil {
		return fmt.Errorf("snapshot: in-VM tar failed: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("snapshot: tar exited %d: %s", res.ExitCode, res.Stderr)
	}
	if maxBytes := req.GetMaxBytes(); maxBytes > 0 {
		// Best-effort cap: the archive is already on the VM's
		// /tmp at this point; we just refuse to ship it across.
		// Cleanup of the in-VM archive happens in the deferred
		// goroutine below regardless of whether we shipped it.
		var sz int64
		fmt.Sscanf(res.Stdout, "%d", &sz)
		if sz > maxBytes {
			s.cleanupInVMArchive(sandboxID, snapshotInVMArchivePath)
			return fmt.Errorf("snapshot: archive %d bytes exceeds cap %d", sz, maxBytes)
		}
	}

	// Pull the archive out of the VM. This is the one buffer in
	// fcagent memory; it's bounded by the per-VM workspace size and
	// fcagent's 24 GiB cgroup limit (helm value).
	data, err := s.backend.ReadFile(ctx, sandboxID, snapshotInVMArchivePath)
	if err != nil {
		s.cleanupInVMArchive(sandboxID, snapshotInVMArchivePath)
		return fmt.Errorf("snapshot: readfile archive: %w", err)
	}

	// Best-effort cleanup of the in-VM archive — runs detached so a
	// slow rm doesn't block the streaming reply.
	go s.cleanupInVMArchive(sandboxID, snapshotInVMArchivePath)

	// Stream out in chunkSize-byte frames. The gateway writes each
	// frame straight to disk and never accumulates the full archive
	// in heap.
	for offset := 0; offset < len(data); offset += snapshotChunkBytes {
		end := offset + snapshotChunkBytes
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&fcpb.SnapshotChunk{Data: data[offset:end]}); err != nil {
			return err
		}
	}
	return nil
}

// Restore extracts a streamed tar.gz into dest_path inside the microVM.
// Client-streaming so the gateway sends 1 MiB chunks straight from disk
// without buffering the archive.
func (s *AgentServer) Restore(stream grpc.ClientStreamingServer[fcpb.RestoreChunk, fcpb.RestoreResponse]) error {
	if s.backend == nil {
		return fmt.Errorf("restore: backend not ready")
	}
	var (
		sandboxID     string
		destPath      string
		buf           []byte
		bytesReceived int64
		gotFirst      bool
	)
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("restore: recv: %w", err)
		}
		if !gotFirst {
			sandboxID = chunk.GetSandboxId()
			destPath = chunk.GetDestPath()
			gotFirst = true
		}
		if len(chunk.Data) > 0 {
			buf = append(buf, chunk.Data...)
			bytesReceived += int64(len(chunk.Data))
		}
	}
	if !gotFirst {
		return fmt.Errorf("restore: empty stream")
	}
	if sandboxID == "" {
		return fmt.Errorf("restore: sandbox_id missing on first frame")
	}
	if destPath == "" {
		destPath = snapshotDefaultDestPath
	}

	ctx := stream.Context()
	if err := s.backend.WriteFile(ctx, sandboxID, restoreInVMArchivePath, buf); err != nil {
		return fmt.Errorf("restore: writefile archive into VM: %w", err)
	}
	tarCmd := fmt.Sprintf(
		"tar -xzf %s -C %s --no-same-owner --no-same-permissions && rm -f %s",
		restoreInVMArchivePath, destPath, restoreInVMArchivePath,
	)
	res, err := s.backend.Exec(ctx, sandboxID, sandbox.ExecRequest{
		Command: []string{"sh", "-lc", tarCmd},
		Timeout: snapshotTarTimeout,
	})
	if err != nil {
		return fmt.Errorf("restore: in-VM tar failed: %w", err)
	}
	return stream.SendAndClose(&fcpb.RestoreResponse{
		ExitCode:      int32(res.ExitCode),
		Stderr:        res.Stderr,
		BytesReceived: bytesReceived,
	})
}

// cleanupInVMArchive removes a temporary archive from inside the VM.
// Best-effort; failures are logged but never returned to the caller.
//
// We swallow EOF / connection-closed errors silently because the
// snapshot-on-stop flow races this goroutine against VM teardown:
// the gateway snapshots, the cleanup fires async, the gateway
// destroys the VM, and the half-finished rm hits a dead vsock. The
// archive lives in the VM's ephemeral rootfs (/tmp), so when the VM
// dies the file dies with it — same effective outcome as a
// successful rm. Surfacing the error just spams the agent log with
// one line per snapshot.
func (s *AgentServer) cleanupInVMArchive(sandboxID, path string) {
	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.backend.Exec(cleanCtx, sandboxID, sandbox.ExecRequest{
		Command: []string{"rm", "-f", path},
		Timeout: 5 * time.Second,
	})
	if err == nil {
		return
	}
	msg := err.Error()
	// Match the vsock client's error wrapping ("failed to read
	// response: EOF") and the manager's "vm not found" path —
	// both mean "VM is gone, file is gone too."
	if strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "VM not found") ||
		strings.Contains(msg, "vm not found") {
		return
	}
	log.Printf("snapshot: cleanup in-VM archive %s failed: %v", path, err)
}

// sandboxRelPath strips a single leading '/' so `tar -C /` can use the
// path as a relative archive root.
func sandboxRelPath(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}

// --- converters ---

func configFromProto(c *fcpb.SandboxConfig) sandbox.InstanceConfig {
	if c == nil {
		return sandbox.InstanceConfig{}
	}
	return sandbox.InstanceConfig{
		Image:          c.Image,
		CPULimit:       c.CpuLimit,
		MemoryMB:       c.MemoryMb,
		DiskMB:         c.DiskMb,
		TimeoutSeconds: int(c.TimeoutSeconds),
		NetworkMode:    sandbox.NetworkMode(c.NetworkMode),
		AllowedHosts:   c.AllowedHosts,
		EnvVars:        c.EnvVars,
		WorkDir:        c.WorkDir,
		TenantID:       c.TenantId,
		SessionID:      c.SessionId,
		DNSServers:     c.DnsServers,
		Name:           c.Name,
		AgentID:        c.AgentId,
		SSHEnabled:     c.SshEnabled,
	}
}

func (s *AgentServer) instanceToProto(inst *sandbox.Instance) *fcpb.SandboxInstance {
	if inst == nil {
		return nil
	}
	return &fcpb.SandboxInstance{
		Id:            inst.ID,
		ContainerId:   inst.ContainerID,
		Status:        string(inst.Status),
		Backend:       inst.Backend,
		CreatedAtUnix: inst.CreatedAt.Unix(),
		ExpiresAtUnix: inst.ExpiresAt.Unix(),
		NodeName:      s.nodeName,
		Name:          inst.Name,
		AgentHealthy:  inst.AgentHealthy,
	}
}
