package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// certReloader holds a TLS keypair on disk and re-reads it whenever the
// underlying files change. Plugged into tls.Config.GetCertificate so a
// running listener picks up cert-manager renewals without a pod
// restart.
//
// Why polling instead of fsnotify: k8s Secret volumes use the kubelet
// "atomic writer" pattern — the actual files are symlinks to a
// timestamped `..<rfc3339>.<id>` directory, and the swap happens on
// the parent dir, not the file. fsnotify watches on `tls.crt` will
// never fire. Polling mtime on the symlink resolves through and is
// boring code that just works across mount semantics (Secret,
// projected volume, ConfigMap, plain bind mount).
type certReloader struct {
	certPath string
	keyPath  string

	// current keypair; pointer-swap so GetCertificate is lock-free.
	cert atomic.Pointer[tls.Certificate]

	// last-seen mtime so the poll loop only does the LoadX509KeyPair
	// work when something actually changed.
	lastModUnix atomic.Int64
}

// newCertReloader loads the keypair once and starts a background
// goroutine that re-reads it on file change. Returns an error if the
// initial load fails — the caller should not start serving on a
// listener whose first cert load fails.
func newCertReloader(ctx context.Context, certPath, keyPath string) (*certReloader, error) {
	r := &certReloader{certPath: certPath, keyPath: keyPath}
	if err := r.reload(); err != nil {
		return nil, err
	}
	go r.watch(ctx)
	return r, nil
}

// reload reads both files, parses the keypair, and atomically swaps
// the pointer. Caller of this returns nil-or-error; callers of
// GetCertificate never see the error directly (they get the previous
// keypair until reload succeeds, which is the safe behaviour).
func (r *certReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return fmt.Errorf("cert_reloader: load %s + %s: %w", r.certPath, r.keyPath, err)
	}
	r.cert.Store(&cert)
	if info, statErr := os.Stat(r.certPath); statErr == nil {
		r.lastModUnix.Store(info.ModTime().Unix())
	}
	return nil
}

// watch polls the cert file's mtime and reloads when it changes.
// 60s cadence is a good trade between renewal latency and syscall
// noise — cert-manager renews 30 days before expiry, so being a
// minute late on the swap is meaningless.
func (r *certReloader) watch(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			info, err := os.Stat(r.certPath)
			if err != nil {
				// File temporarily missing during atomic swap is
				// possible on some volume types; log at debug and
				// retry on the next tick.
				logger.WithFields("error", err.Error(), "path", r.certPath).
					Debug("cert_reloader: stat failed; retry next tick")
				continue
			}
			modUnix := info.ModTime().Unix()
			if modUnix == r.lastModUnix.Load() {
				continue
			}
			if err := r.reload(); err != nil {
				logger.WithFields("error", err.Error()).
					Warn("cert_reloader: reload failed; keeping previous cert")
				continue
			}
			logger.WithFields("path", r.certPath).
				Info("cert_reloader: reloaded TLS keypair")
		}
	}
}

// GetCertificate is the tls.Config callback. Lock-free read.
func (r *certReloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := r.cert.Load()
	if cert == nil {
		return nil, fmt.Errorf("cert_reloader: no certificate loaded")
	}
	return cert, nil
}
