package kubernetes

import (
	"context"
	"io"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/logbuffer"
)

// Logs returns a stream of sandbox logs captured from exec and shell output.
// We use the application-level log buffer (same as Docker) instead of
// "kubectl logs" because PID 1 is "sleep infinity" which produces no output.
// All meaningful logs come from Exec and Shell sessions which write to the buffer.
func (b *KubernetesBackend) Logs(ctx context.Context, id string, opts sandbox.LogsOptions) (io.ReadCloser, error) {
	sl := b.getOrCreateLogs(id)

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		// Write existing entries (tail)
		entries := sl.Snapshot(opts.Tail)
		for _, e := range entries {
			if !opts.Since.IsZero() && e.Timestamp.Before(opts.Since) {
				continue
			}
			line := logbuffer.FormatEntry(e, opts.Timestamps)
			if _, err := pw.Write([]byte(line + "\n")); err != nil {
				return
			}
		}

		if !opts.Follow {
			return
		}

		// Subscribe for new entries
		ch := sl.Subscribe()
		defer sl.Unsubscribe(ch)

		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return // log buffer closed (sandbox destroyed)
				}
				line := logbuffer.FormatEntry(e, opts.Timestamps)
				if _, err := pw.Write([]byte(line + "\n")); err != nil {
					return
				}
			}
		}
	}()

	return pr, nil
}
