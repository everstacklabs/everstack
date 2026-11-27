package sandbox

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// RootfsReferenceReporter is implemented by backends whose hosts keep
// rootfs images on local disk (the firecracker-agent backend). The
// manager tells them which images are still referenced so the host-side
// rootfs GC never deletes an image a sandbox can still boot or wake into.
type RootfsReferenceReporter interface {
	// unknown marks a set the gateway cannot vouch for; the agent then
	// keeps every image.
	ReportRootfsReferences(ctx context.Context, reporter string, images []string, unknown bool) error
}

// DefaultRootfsReporterName is the reporter name gateways use unless
// EVS_SANDBOX_ROOTFS_REPORTER overrides it. It must be in the agents'
// FC_ROOTFS_GC_REPORTERS allowlist.
const DefaultRootfsReporterName = "everstack-gateway"

// RootfsReporterID returns "<name>/<instance>": the allowlisted name
// plus the replica's instance (pod name), which keeps replicas' reports
// separate on the agent.
func RootfsReporterID(instance string) string {
	name := strings.TrimSpace(os.Getenv("EVS_SANDBOX_ROOTFS_REPORTER"))
	if name == "" {
		name = DefaultRootfsReporterName
	}
	return name + "/" + instance
}

// rootfsReferenceInterval is how often the referenced set is sent. The
// agent trusts a report for 30 minutes, so a few missed sends are fine.
const rootfsReferenceInterval = 5 * time.Minute

// rootfsReferencedImagesQuery lists the image of every sandbox row that
// is not terminal. Sleeping, stopped, archived, failed and error rows
// all stay referenced: each can still be revived, and a revive boots
// `<tag>.ext4` again. Legacy rows without lifecycle_state fall back to
// status.
const rootfsReferencedImagesQuery = `
		SELECT DISTINCT image FROM sandbox_instances
		WHERE image <> ''
		  AND COALESCE(NULLIF(lifecycle_state, ''), status, '') NOT IN ('terminated', 'deleted')`

// StartRootfsReferenceReporter sends the referenced image set to the
// backend now and every rootfsReferenceInterval until ctx ends. It is a
// no-op when the backend has no host-side rootfs store or there is no
// DB. executorImage is the configured OpenAI Agents executor image,
// referenced by configuration rather than by rows; when it is empty every
// report is marked unknown.
func (m *SandboxManager) StartRootfsReferenceReporter(ctx context.Context, reporter, executorImage string) {
	if _, ok := m.backend.(RootfsReferenceReporter); !ok || m.db == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(rootfsReferenceInterval)
		defer ticker.Stop()
		for {
			if err := m.reportRootfsReferences(ctx, reporter, executorImage); err != nil {
				logger.WithFields("error", err.Error()).
					Warn("sandbox_manager: rootfs reference report failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	logger.WithFields("reporter", reporter, "interval", rootfsReferenceInterval.String()).
		Info("sandbox_manager: rootfs reference reporter started")
}

// reportRootfsReferences runs one query and send. When the query fails
// nothing is sent: a partial set would let the GC delete an image that
// is still needed, while silence makes it fail closed. An unset executor
// image is sent as unknown, never as a set that looks complete.
func (m *SandboxManager) reportRootfsReferences(ctx context.Context, reporter, executorImage string) error {
	rep, ok := m.backend.(RootfsReferenceReporter)
	if !ok || m.db == nil {
		return nil
	}
	qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var rows []string
	if err := m.db.SelectContext(qCtx, &rows, rootfsReferencedImagesQuery); err != nil {
		return fmt.Errorf("query referenced images: %w", err)
	}

	seen := map[string]struct{}{}
	images := make([]string, 0, len(rows)+1)
	executorImage = strings.TrimSpace(executorImage)
	unknown := executorImage == ""
	for _, img := range append(rows, executorImage) {
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}
		if _, dup := seen[img]; dup {
			continue
		}
		seen[img] = struct{}{}
		images = append(images, img)
	}
	sort.Strings(images)

	sendCtx, cancelSend := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSend()
	return rep.ReportRootfsReferences(sendCtx, reporter, images, unknown)
}
