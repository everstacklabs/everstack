package serve

import (
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

// Negative cache for Host -> tenant_config resolution (EVS-324 item 7).
//
// resolveInstanceFromHost runs for every request that carries a session
// cookie or bearer token, and its predicate (slug || '-' || right(...)) is
// not index-backed. Without a negative cache a flood of requests with
// made-up Host headers turns into a table scan each. Misses are remembered
// for a short TTL, so a tenant provisioned right after a miss becomes
// routable within seconds. Only a definite "no such row" is cached; errors
// such as timeouts always go back to the DB.
//
// A missing tenant_config table is remembered separately, per database (see
// missingTenantConfigTable below).
//
// Positive answers are deliberately not cached here: status and membership
// can change, and the existing lookup is the source of truth for them.
const (
	hostNegativeCacheTTL        = 15 * time.Second
	hostNegativeCacheMaxEntries = 4096
)

// hostNegativeCacheNow is swapped by tests.
var hostNegativeCacheNow = time.Now

type hostNegativeKey struct {
	db    *sqlx.DB
	label string
}

type hostNegativeCache struct {
	mu      sync.Mutex
	entries map[hostNegativeKey]time.Time // key -> expiry
}

var unknownHostCache = &hostNegativeCache{entries: make(map[hostNegativeKey]time.Time)}

// known reports whether label was recently confirmed not to exist.
func (c *hostNegativeCache) known(db *sqlx.DB, label string) bool {
	key := hostNegativeKey{db: db, label: label}
	c.mu.Lock()
	defer c.mu.Unlock()
	expiry, ok := c.entries[key]
	if !ok {
		return false
	}
	if hostNegativeCacheNow().After(expiry) {
		delete(c.entries, key)
		return false
	}
	return true
}

// remember records a confirmed miss. The map is bounded: when full, expired
// entries are swept, and if it is still full it is reset. Losing entries
// only costs a DB lookup.
func (c *hostNegativeCache) remember(db *sqlx.DB, label string) {
	now := hostNegativeCacheNow()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= hostNegativeCacheMaxEntries {
		for k, expiry := range c.entries {
			if now.After(expiry) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= hostNegativeCacheMaxEntries {
			c.entries = make(map[hostNegativeKey]time.Time)
		}
	}
	c.entries[hostNegativeKey{db: db, label: label}] = now.Add(hostNegativeCacheTTL)
}

// missingTenantConfigTable remembers, per database, that it has no
// tenant_config table.
//
// A self-hosted gateway never has one: the table belongs to the cloud
// platform database. There the lookup in resolveInstanceFromHost cannot
// succeed for any host, yet it used to go to Postgres for every request that
// carried a bearer token, which is every OpenAI-style API call, and again for
// the same call on its way from the REST gateway to the Connect handlers.
// Under load those lookups held more connections than Postgres accepts, and
// unrelated queries, API-key validation among them, started to fail.
//
// The entry is set only when Postgres itself reports the relation (or its
// schema) undefined for the fixed lookup statement. The Host header is a
// bound parameter of that statement, so a caller cannot cause or prevent it.
// It lives in its own map, so no host label can collide with it, and it
// expires on the same short TTL as a host miss: a table created later is
// picked up within seconds. While it is set, the answer is the one the
// failing query gave: no instance scope.
var missingTenantConfigTable = &hostNegativeCache{entries: make(map[hostNegativeKey]time.Time)}

// errNoTenantConfigTable is the answer while a database is remembered as
// having no tenant_config table. Like the Postgres error it stands in for, it
// is not sql.ErrNoRows: "this host is not a tenant" and "this database has no
// tenants" stay distinguishable to callers.
var errNoTenantConfigTable = errors.New("tenant_config does not exist in this database")

// isUndefinedRelation reports whether err is Postgres saying that a table
// (42P01) or its schema (3F000) does not exist.
func isUndefinedRelation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "42P01" || pgErr.Code == "3F000")
}
