package fcagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
)

// snapshotChunkBytes is the per-frame payload size used by Restore when
// pumping the local archive into the gRPC client stream. Mirrors the
// fcagent server's send size so neither side is forced to coalesce.
const snapshotChunkBytes = 1 << 20 // 1 MiB

// snapshotMaxBytes is the cap surfaced to the agent. Same value as the
// gateway-side `maxSnapshotBytes` from manager_lifecycle.execSnapshot;
// kept here so the streaming path enforces the same ceiling without
// requiring the gateway to inspect the bytes.
const snapshotMaxBytes = 1 << 30 // 1 GiB

// Snapshot satisfies sandbox.Snapshotter. Streams the in-VM archive
// from the routed agent into a local destPath file. The full archive
// never lives in gateway memory — chunks are written to disk as they
// arrive. fcagent's cgroup carries the in-flight archive on its side
// (one per VM, sized by the platform helm values).
//
// Routing follows the same pattern as Exec/WriteFile/etc.: the route
// table is consulted first, with a discovery probe as fallback when
// the gateway has just restarted and the route has been seeded but the
// connection isn't pinned yet.
func (b *FCAgentBackend) Snapshot(ctx context.Context, id string, srcPath string, destPath string) error {
	cli, _, err := b.routeFor(ctx, id)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}

	stream, err := cli.Snapshot(ctx, &fcpb.SnapshotRequest{
		SandboxId:  id,
		SourcePath: srcPath,
		MaxBytes:   snapshotMaxBytes,
	})
	if err != nil {
		return fmt.Errorf("snapshot: open stream: %w", err)
	}

	// O_CREATE|O_TRUNC|O_WRONLY: snapshot replaces any prior archive
	// at the same destPath. Caller is responsible for choosing a
	// unique destPath when concurrent snapshots could collide.
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("snapshot: open dest %s: %w", destPath, err)
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(destPath)
	}
	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			cleanup()
			return fmt.Errorf("snapshot: recv chunk: %w", recvErr)
		}
		data := chunk.GetData()
		if len(data) == 0 {
			continue
		}
		if _, writeErr := f.Write(data); writeErr != nil {
			cleanup()
			return fmt.Errorf("snapshot: write %s: %w", destPath, writeErr)
		}
	}
	if syncErr := f.Sync(); syncErr != nil {
		cleanup()
		return fmt.Errorf("snapshot: fsync %s: %w", destPath, syncErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(destPath)
		return fmt.Errorf("snapshot: close %s: %w", destPath, closeErr)
	}
	return nil
}

// Restore satisfies sandbox.Snapshotter. Reads srcPath from local disk
// and streams it to the routed agent in 1 MiB chunks. The first frame
// carries the sandbox_id + dest_path metadata; subsequent frames carry
// only data.
func (b *FCAgentBackend) Restore(ctx context.Context, id string, srcPath string, destPath string) error {
	cli, _, err := b.routeFor(ctx, id)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("restore: open src %s: %w", srcPath, err)
	}
	defer f.Close()

	stream, err := cli.Restore(ctx)
	if err != nil {
		return fmt.Errorf("restore: open stream: %w", err)
	}

	// Send the metadata header. data is empty so the server can
	// distinguish "first frame" without a special-case wire format.
	if err := stream.Send(&fcpb.RestoreChunk{
		SandboxId: id,
		DestPath:  destPath,
	}); err != nil {
		return fmt.Errorf("restore: send header: %w", err)
	}

	buf := make([]byte, snapshotChunkBytes)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			// Append-copy is required because gRPC may serialize the
			// frame asynchronously after Send returns; reusing the
			// underlying buffer for the next read corrupts the wire
			// payload otherwise.
			payload := append([]byte(nil), buf[:n]...)
			if sendErr := stream.Send(&fcpb.RestoreChunk{Data: payload}); sendErr != nil {
				return fmt.Errorf("restore: send chunk: %w", sendErr)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("restore: read %s: %w", srcPath, readErr)
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("restore: close stream: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return fmt.Errorf("restore: tar exited %d: %s",
			resp.GetExitCode(), strings.TrimSpace(resp.GetStderr()))
	}
	return nil
}
