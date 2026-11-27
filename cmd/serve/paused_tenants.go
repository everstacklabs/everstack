package serve

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	authdomain "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
	"github.com/everstacklabs/everstack/internal/lib/tenantstatus"
)

// Paused managed instances (docs/design/instance-over-limit.md).
//
// Managed tenant hosts are served by this binary, not by the services
// process that holds the controlplane's TenantMiddleware, and the tenant's
// work runs here too: triggers, schedules, channel connectors, sandboxes.
// pausedTenants is the one status source all of them consult; it is nil
// (every tenant active) outside managed Cloud.
var pausedTenants *tenantstatus.Checker

// pausedTenantSweepInterval bounds how long a paused instance keeps
// sandboxes, persistent agents and channel connectors after the pause.
const pausedTenantSweepInterval = 30 * time.Second

// pausedTenantHostGuard refuses requests to a paused instance's host before
// any auth or routing. A no-op when pausedTenants is nil.
func pausedTenantHostGuard(next http.Handler) http.Handler {
	cloudURL := strings.TrimSpace(os.Getenv("EVS_CLOUD_URL"))
	if cloudURL == "" {
		cloudURL = "https://app.everstack.ai"
	}
	// Read pausedTenants per request: listenAndServe may wrap the router
	// before startAPI has installed the checker.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantstatus.HostGuard(pausedTenants, cloudURL, next).ServeHTTP(w, r)
	})
}

// pausedTenantViewerCheck reports whether a request to a paused tenant's
// host carries a live session of a member of that tenant's organization.
// Only such a caller is shown the org's billing page in the refusal; it
// names the org slug, which an anonymous caller must not learn from a
// hostname (EVS-324 item 7). Same cookies and signed-out rule as
// cookieSessionAuthMiddleware; any failure means "not verified".
func pausedTenantViewerCheck(dbFn func() *sqlx.DB) func(*http.Request, tenantstatus.Tenant) bool {
	return func(r *http.Request, t tenantstatus.Tenant) bool {
		if strings.TrimSpace(t.OrganizationID) == "" {
			return false
		}
		if _, err := r.Cookie(authdomain.InstanceSignedOutCookie); err == nil {
			return false
		}
		tokens := readSessionTokens(r)
		if len(tokens) == 0 {
			return false
		}
		db := dbFn()
		if db == nil {
			return false
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		for _, token := range tokens {
			// sessionlive also refuses a mirror row whose Better Auth
			// session is gone; any error (including an unreachable store)
			// means "not verified".
			session, err := sessionlive.Lookup(ctx, db, token)
			if err != nil {
				continue
			}
			var one int
			if err := db.GetContext(ctx, &one,
				`SELECT 1 FROM everstack.organization_members
				 WHERE user_id = $1::uuid AND organization_id = $2::uuid
				 LIMIT 1`, session.UserID, t.OrganizationID); err == nil {
				return true
			}
		}
		return false
	}
}

// pausedTenantWorkloads is what the sweep stops for a paused tenant, and
// brings back once it is resumed; the sandbox manager implements it.
// ResumeTenantAgents refuses a paused tenant, so the billing reporter
// cannot un-park agents the sweep parked.
type pausedTenantWorkloads interface {
	StopTenantSandboxes(ctx context.Context, tenantID string) (int, error)
	SuspendTenantAgents(ctx context.Context, tenantID string) (int, error)
	ResumeTenantAgents(ctx context.Context, tenantID string) (int, error)
}

// pausedTenantConnectors is the channel manager's per-tenant stop and start.
type pausedTenantConnectors interface {
	StopTenantConnectors(ctx context.Context, tenantID string) int
	StartTenantConnectors(ctx context.Context, tenantID string) int
}

// pausedTenantSweeper remembers which tenants it saw paused, so a tenant
// that leaves the list gets its parked agents and channel connectors back
// (sandboxes are recreated on demand).
type pausedTenantSweeper struct {
	checker    *tenantstatus.Checker
	workloads  pausedTenantWorkloads
	connectors pausedTenantConnectors
	paused     map[string]struct{}
}

func newPausedTenantSweeper(checker *tenantstatus.Checker, workloads pausedTenantWorkloads, connectors pausedTenantConnectors) *pausedTenantSweeper {
	return &pausedTenantSweeper{checker: checker, workloads: workloads, connectors: connectors, paused: map[string]struct{}{}}
}

// sweep stops everything a paused tenant still runs on this replica and
// restores tenants resumed since the last sweep. Idempotent: a tenant with
// nothing left to stop costs one list.
func (s *pausedTenantSweeper) sweep(ctx context.Context) {
	// Judge from a fresh read: a replica's snapshot can lag the controlplane
	// by a TTL, and the restore calls below consult the same snapshot.
	if err := s.checker.Refresh(ctx); err != nil {
		logger.WithError(err).Warn("paused tenants: status reload failed; retrying next sweep")
		return
	}
	ids, err := s.checker.ListPaused(ctx)
	if err != nil {
		logger.WithError(err).Warn("paused tenants: list failed")
		return
	}
	// Deleted managed instances stop exactly like paused ones, and since
	// Checker.Active never reports them active they are never restored.
	ids = mergeSortedIDs(ids, tenantlive.DeletedIDs())
	now := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		now[id] = struct{}{}
		var sandboxes, agents, channels int
		if s.workloads != nil {
			if n, err := s.workloads.StopTenantSandboxes(ctx, id); err != nil {
				logger.WithFields("tenant_id", id).WithError(err).Warn("paused tenants: stop sandboxes failed")
			} else {
				sandboxes = n
			}
			if n, err := s.workloads.SuspendTenantAgents(ctx, id); err != nil {
				logger.WithFields("tenant_id", id).WithError(err).Warn("paused tenants: suspend agents failed")
			} else {
				agents = n
			}
		}
		if s.connectors != nil {
			channels = s.connectors.StopTenantConnectors(ctx, id)
		}
		if sandboxes+agents+channels > 0 {
			logger.WithFields("tenant_id", id, "sandboxes", sandboxes, "agents", agents, "channels", channels).
				Info("paused tenants: stopped workloads of a paused instance")
		}
	}
	for id := range s.paused {
		if _, still := now[id]; still {
			continue
		}
		if !s.restore(ctx, id) {
			now[id] = struct{}{} // keep it: retried next sweep
		}
	}
	s.paused = now
}

// restore brings back a resumed tenant's parked agents and channel
// connectors. It reports whether the restore ran; false keeps the tenant on
// the sweep's list so the next sweep retries.
func (s *pausedTenantSweeper) restore(ctx context.Context, id string) bool {
	if !s.checker.Active(ctx, id) {
		return false
	}
	agents, channels := 0, 0
	if s.workloads != nil {
		n, err := s.workloads.ResumeTenantAgents(ctx, id)
		if err != nil {
			logger.WithFields("tenant_id", id).WithError(err).Warn("paused tenants: resume agents failed; retrying next sweep")
			return false
		}
		agents = n
	}
	if s.connectors != nil {
		channels = s.connectors.StartTenantConnectors(ctx, id)
	}
	logger.WithFields("tenant_id", id, "agents", agents, "channels", channels).
		Info("paused tenants: instance resumed; agents and channels restored")
	return true
}

// startPausedTenantSweep runs the sweep now and every interval.
func startPausedTenantSweep(ctx context.Context, checker *tenantstatus.Checker, workloads pausedTenantWorkloads, connectors pausedTenantConnectors) {
	if checker == nil {
		return
	}
	s := newPausedTenantSweeper(checker, workloads, connectors)
	go func() {
		s.sweep(ctx)
		ticker := time.NewTicker(pausedTenantSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweep(ctx)
			}
		}
	}()
}

// mergeSortedIDs returns the sorted union of a and b.
func mergeSortedIDs(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, id := range list {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}
