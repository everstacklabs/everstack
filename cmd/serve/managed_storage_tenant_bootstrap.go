package serve

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
)

const managedStorageTenantBootstrapTimeout = 3 * time.Second

// managedStorageTenantBootstrap creates the system-managed default before an
// authenticated cloud tenant reaches the gateway. Successful tenants are
// cached for the lifetime of the process; the storage service still repairs
// the record on its own connection-facing paths.
type managedStorageTenantBootstrap struct {
	defaults storagepkg.ManagedDefaultEnsurer
	ready    sync.Map
	mu       sync.Mutex
	inFlight map[string]*managedStorageTenantBootstrapAttempt
}

type managedStorageTenantBootstrapAttempt struct {
	done chan struct{}
	err  error
}

func newManagedStorageTenantBootstrap(defaults storagepkg.ManagedDefaultEnsurer) *managedStorageTenantBootstrap {
	return &managedStorageTenantBootstrap{
		defaults: defaults,
		inFlight: make(map[string]*managedStorageTenantBootstrapAttempt),
	}
}

func (m *managedStorageTenantBootstrap) Wrap(next http.Handler) http.Handler {
	if next == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m == nil || m.defaults == nil || !contextkeys.IsTenantAuthenticated(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		tenantID := strings.TrimSpace(contextkeys.GetTenantID(r.Context()))
		if tenantID == "" {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := m.ready.Load(tenantID); ok {
			next.ServeHTTP(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), managedStorageTenantBootstrapTimeout)
		defer cancel()
		if err := m.ensure(ctx, tenantID); err != nil {
			logger.WithError(err).SetFields("tenant_id", tenantID).
				Warn("managed storage: tenant default provisioning failed")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Everstack Storage is temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		m.ready.Store(tenantID, struct{}{})
		next.ServeHTTP(w, r)
	})
}

func (m *managedStorageTenantBootstrap) ensure(ctx context.Context, tenantID string) error {
	if _, ok := m.ready.Load(tenantID); ok {
		return nil
	}

	m.mu.Lock()
	if _, ok := m.ready.Load(tenantID); ok {
		m.mu.Unlock()
		return nil
	}
	if attempt, ok := m.inFlight[tenantID]; ok {
		m.mu.Unlock()
		select {
		case <-attempt.done:
			return attempt.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	attempt := &managedStorageTenantBootstrapAttempt{done: make(chan struct{})}
	m.inFlight[tenantID] = attempt
	m.mu.Unlock()

	_, err := m.defaults.EnsureDefault(ctx, tenantID)
	if err == nil {
		m.ready.Store(tenantID, struct{}{})
	}
	m.mu.Lock()
	attempt.err = err
	delete(m.inFlight, tenantID)
	close(attempt.done)
	m.mu.Unlock()
	return err
}
