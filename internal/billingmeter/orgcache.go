package billingmeter

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// orgCache memoizes tenant->billable-org resolution. ResolveOrg costs up to two
// sequential Postgres queries, and it would otherwise run on every provider
// attempt. Resolution is a pure function of the tenant id: an organization is
// its own org, and a managed instance's organization_id is written once at
// provisioning and never re-parented (the only UPDATEs touch status, health,
// container and soft-delete columns), so a TTL bounds the only staleness that
// exists (a tenant appearing or being deleted). Negative results (tenant maps
// to no org, err == nil) are cached briefly too: unmapped tenants must not pay
// two doomed queries per attempt forever. Errors are never cached.
type orgCache struct {
	mu       sync.RWMutex
	byTenant map[string]orgEntry
}

type orgEntry struct {
	org     string
	ok      bool
	expires time.Time
}

const (
	orgPositiveTTL = 5 * time.Minute
	orgNegativeTTL = time.Minute
)

// lookup returns the cached mapping for exactly this tenant id and whether it
// was present and fresh. The key is the tenant id and nothing else, so one
// tenant can never observe another tenant's org.
func (c *orgCache) lookup(tenantID string) (org string, ok, hit bool) {
	if c == nil {
		return "", false, false
	}
	now := time.Now()
	c.mu.RLock()
	e, found := c.byTenant[tenantID]
	c.mu.RUnlock()
	if !found || !now.Before(e.expires) {
		return "", false, false
	}
	return e.org, e.ok, true
}

// store records a successful resolution (positive or "no org"). Callers must
// not store errors: a failed lookup retries on the next request rather than
// pinning a false negative.
func (c *orgCache) store(tenantID, org string, ok bool) {
	if c == nil {
		return
	}
	ttl := orgNegativeTTL
	if ok {
		ttl = orgPositiveTTL
	}
	c.mu.Lock()
	if c.byTenant == nil {
		c.byTenant = make(map[string]orgEntry)
	}
	c.byTenant[tenantID] = orgEntry{org: org, ok: ok, expires: time.Now().Add(ttl)}
	c.mu.Unlock()
}

// resolveOrgFunc is ResolveOrg's signature, indirected so tests can count how
// many times Postgres would be hit without a database.
type resolveOrgFunc func(ctx context.Context, db *sqlx.DB, tenantID string) (string, bool, error)

// resolve returns the billable org for tenantID, consulting the cache first.
// Errors are returned (and never cached) so the caller can decide whether an
// unresolvable org is a silent skip or a loud one.
func (c *orgCache) resolve(ctx context.Context, db *sqlx.DB, tenantID string, fn resolveOrgFunc) (string, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", false, nil
	}
	if org, ok, hit := c.lookup(tenantID); hit {
		return org, ok, nil
	}
	org, ok, err := fn(ctx, db, tenantID)
	if err != nil {
		return "", false, err
	}
	ok = ok && org != ""
	c.store(tenantID, org, ok)
	return org, ok, nil
}
