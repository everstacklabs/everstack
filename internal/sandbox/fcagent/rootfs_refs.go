package fcagent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const rootfsReportTimeout = 5 * time.Second

var _ sandbox.RootfsReferenceReporter = (*FCAgentBackend)(nil)

// ReportRootfsReferences sends the referenced rootfs images to every
// discovered agent, so each host's rootfs GC keeps them. Agents that
// predate the RPC answer Unimplemented and are skipped; any other
// failure is returned (joined) after every target has been tried.
// Implements sandbox.RootfsReferenceReporter.
func (b *FCAgentBackend) ReportRootfsReferences(ctx context.Context, reporter string, images []string, unknown bool) error {
	req := &fcpb.ReportRootfsReferencesRequest{Reporter: reporter, Images: images, Unknown: unknown}
	var errs []error
	for _, target := range b.discovery.Targets() {
		cli, err := b.discovery.Client(target)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", target, err))
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, rootfsReportTimeout)
		_, err = cli.ReportRootfsReferences(callCtx, req)
		cancel()
		if err != nil && status.Code(err) != codes.Unimplemented {
			errs = append(errs, fmt.Errorf("%s: %w", target, err))
		}
	}
	return errors.Join(errs...)
}
