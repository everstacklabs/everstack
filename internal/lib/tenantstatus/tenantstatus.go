// Package tenantstatus answers "may this managed instance run work right
// now?" for the gateway process, which serves every managed tenant from one
// pool and has no controlplane in-process (docs/design/instance-over-limit.md).
//
// The source of truth is everstack.tenant_config.status on the platform
// database. A tenant whose row says anything but "active" (an over-limit
// pause, a user stop) must not serve requests, fire triggers, run schedules,
// consume channel messages or hold sandboxes. Lookups are cached for a short
// TTL so hot paths cost one primary-key read per tenant per interval.
//
// Unknown tenants are active: gateway tenant ids also include self-hosted
// tenants and organization-id aliases that have no tenant_config row. A
// lookup error is also treated as active (and logged) so a platform DB blip
// cannot take every tenant down; the next lookup after the TTL retries.
package tenantstatus

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
	"github.com/jmoiron/sqlx"
)

// DefaultTTL is how stale a cached status may be. It bounds how long a
// paused instance keeps running work on a replica after the pause.
const DefaultTTL = 30 * time.Second

// Over-limit pause reasons, mirrored from internal/controlplane (which the
// gateway cannot import).
const (
	ReasonOverLimitChoice    = "over_limit_choice"
	ReasonOverLimitAuto      = "over_limit_auto"
	ReasonExtraBillingUnpaid = "extra_billing_unpaid"
)

// IsOverLimitReason reports whether a pause came from the instance-limit flow.
func IsOverLimitReason(reason string) bool {
	switch reason {
	case ReasonOverLimitChoice, ReasonOverLimitAuto, ReasonExtraBillingUnpaid:
		return true
	}
	return false
}

// Tenant is one tenant_config row as far as pausing is concerned.
type Tenant struct {
	InstanceID         string `db:"instance_id"`
	OrganizationID     string `db:"organization_id"`
	Slug               string `db:"slug"`
	OrgSlug            string `db:"org_slug"`
	Status             string `db:"status"`
	PausedReason       string `db:"paused_reason"`
	CustomDomain       string `db:"custom_domain"`
	CustomDomainStatus string `db:"custom_domain_status"`
	// GatewayURL is the instance's canonical URL as provisioned; its host
	// is the only spelling of the managed host that resolves to the tenant.
	GatewayURL string `db:"gateway_url"`
}

// Active reports whether the tenant may serve and run work. Only a paused
// row (an over-limit pause or a user stop, which the controlplane records
// as paused) stops it; provisioning and other transitional states are left
// to the paths that already handle them.
func (t Tenant) Active() bool { return t.Status != "paused" }

// hostLabel is the managed host's first DNS label: {slug}-{last 6 hex of
// the instance id}.
func (t Tenant) hostLabel() string {
	hex := strings.ReplaceAll(strings.ToLower(t.InstanceID), "-", "")
	if len(hex) < 6 || t.Slug == "" {
		return ""
	}
	return strings.ToLower(t.Slug) + "-" + hex[len(hex)-6:]
}

// snapshot is every tenant_config row, indexed three ways. Managed Cloud
// has tens of rows, so the whole table is cheaper to hold than a
// per-key cache: no query per request, no per-host growth.
type snapshot struct {
	byID     map[string]Tenant
	byLabel  map[string]Tenant
	byDomain map[string]Tenant
	loadedAt time.Time
}

// Checker answers tenant status and host lookups from an in-memory snapshot
// of everstack.tenant_config, reloaded at most once per TTL. A nil *Checker
// reports every tenant active, so callers can install it unconditionally.
type Checker struct {
	db  *sqlx.DB
	ttl time.Duration
	now func() time.Time

	// loadMu serialises reloads; snap is read lock-free.
	loadMu sync.Mutex
	snap   atomic.Pointer[snapshot]

	touchMu  sync.Mutex
	touched  map[string]time.Time
	touchGap time.Duration

	// viewer reports whether a request comes from a verified member of the
	// tenant's organization. Only such a caller is shown the org's billing
	// page (which names the org slug) when the instance is paused. Nil
	// means nobody is verified.
	viewer atomic.Pointer[func(*http.Request, Tenant) bool]
}

// SetViewerCheck installs the member check HostGuard uses before revealing
// a paused tenant's billing page.
func (c *Checker) SetViewerCheck(fn func(*http.Request, Tenant) bool) {
	if c == nil {
		return
	}
	if fn == nil {
		c.viewer.Store(nil)
		return
	}
	c.viewer.Store(&fn)
}

func (c *Checker) verifiedViewer(r *http.Request, t Tenant) bool {
	if c == nil {
		return false
	}
	fn := c.viewer.Load()
	return fn != nil && (*fn)(r, t)
}

// pausedRecheck bounds how long a replica keeps refusing a host after the
// instance was resumed: a refused request reloads the snapshot when it is
// older than this, at most once per interval.
const pausedRecheck = 5 * time.Second

// New builds a checker over the platform database (the one that holds
// everstack.tenant_config). ttl <= 0 uses DefaultTTL.
func New(db *sqlx.DB, ttl time.Duration) *Checker {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Checker{
		db: db, ttl: ttl, now: time.Now,
		touched: map[string]time.Time{}, touchGap: time.Minute,
	}
}

// SetClock overrides time for tests.
func (c *Checker) SetClock(now func() time.Time) { c.now = now }

// Refresh reloads the snapshot now. The pause sweep calls it before acting
// so a resume is never judged against a stale snapshot.
func (c *Checker) Refresh(ctx context.Context) error {
	if c == nil || c.db == nil {
		return nil
	}
	_, err := c.load(ctx, 0)
	return err
}

// current returns a snapshot no older than maxAge, reloading if needed. On
// a reload error the previous snapshot is kept (nil when there never was
// one, which callers treat as "everyone active").
func (c *Checker) current(ctx context.Context, maxAge time.Duration) (*snapshot, error) {
	if s := c.snap.Load(); s != nil && c.now().Sub(s.loadedAt) < maxAge {
		return s, nil
	}
	return c.load(ctx, maxAge)
}

func (c *Checker) load(ctx context.Context, maxAge time.Duration) (*snapshot, error) {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	// Another caller may have reloaded while this one waited.
	if s := c.snap.Load(); s != nil && maxAge > 0 && c.now().Sub(s.loadedAt) < maxAge {
		return s, nil
	}
	qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var rows []Tenant
	err := c.db.SelectContext(qctx, &rows, `SELECT instance_id::text AS instance_id,
		COALESCE(organization_id::text, '') AS organization_id,
		COALESCE(slug, '') AS slug,
		COALESCE(org_slug, '') AS org_slug,
		COALESCE(status, '') AS status,
		COALESCE(paused_reason, '') AS paused_reason,
		COALESCE(custom_domain, '') AS custom_domain,
		COALESCE(custom_domain_status, '') AS custom_domain_status,
		COALESCE(gateway_url, '') AS gateway_url
		FROM everstack.tenant_config`)
	if err != nil {
		prev := c.snap.Load()
		if prev != nil {
			// Keep serving the last known state; retry after another TTL.
			c.snap.Store(&snapshot{byID: prev.byID, byLabel: prev.byLabel, byDomain: prev.byDomain, loadedAt: c.now()})
		}
		return prev, err
	}
	s := &snapshot{
		byID:     make(map[string]Tenant, len(rows)),
		byLabel:  make(map[string]Tenant, len(rows)),
		byDomain: map[string]Tenant{},
		loadedAt: c.now(),
	}
	for _, t := range rows {
		s.byID[t.InstanceID] = t
		if l := t.hostLabel(); l != "" {
			s.byLabel[l] = t
		}
		if t.CustomDomain != "" && t.CustomDomainStatus == "active" {
			s.byDomain[normalizeHost(t.CustomDomain)] = t
		}
	}
	c.snap.Store(s)
	return s, nil
}

// Lookup returns the tenant_config row for an instance id (found=false when
// there is none).
func (c *Checker) Lookup(ctx context.Context, instanceID string) (Tenant, bool, error) {
	instanceID = strings.TrimSpace(instanceID)
	if c == nil || c.db == nil || instanceID == "" {
		return Tenant{}, false, nil
	}
	s, err := c.current(ctx, c.ttl)
	if s == nil {
		return Tenant{}, false, err
	}
	t, ok := s.byID[instanceID]
	return t, ok, err
}

// Active reports whether tenantID may run work. Unknown tenants and a
// checker that has never loaded count as active (see the package comment).
//
// A deleted managed instance is never active, with or without a leftover
// tenant_config row: deleting it frees its plan quota, so its channels,
// triggers, schedules and agent turns must stop (tenantlive, managed
// gateways only).
func (c *Checker) Active(ctx context.Context, tenantID string) bool {
	if tenantlive.Deleted(tenantID) {
		return false
	}
	t, found, err := c.Lookup(ctx, tenantID)
	if err != nil {
		logger.WithFields("tenant_id", tenantID).WithError(err).Warn("tenantstatus: snapshot reload failed; using the last known state")
	}
	return !found || t.Active()
}

// ActiveFn adapts Active to the func type the background loops take.
func (c *Checker) ActiveFn() func(ctx context.Context, tenantID string) bool {
	return func(ctx context.Context, tenantID string) bool { return c.Active(ctx, tenantID) }
}

// ListPaused returns the instance ids whose tenant_config is paused, from a
// snapshot no older than the TTL (call Refresh first for a fresh answer).
func (c *Checker) ListPaused(ctx context.Context) ([]string, error) {
	if c == nil || c.db == nil {
		return nil, nil
	}
	s, err := c.current(ctx, c.ttl)
	if s == nil {
		return nil, err
	}
	var ids []string
	for id, t := range s.byID {
		if !t.Active() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, err
}

// ResolveHost maps a request host to its tenant: the tenant's canonical
// managed host (first label {slug}-{last 6 hex of the instance id}, the rest
// matching its gateway_url) or a verified custom domain.
// Served from memory; arbitrary hosts cost nothing.
func (c *Checker) ResolveHost(ctx context.Context, host string) (Tenant, bool, error) {
	host = normalizeHost(host)
	if c == nil || c.db == nil || host == "" {
		return Tenant{}, false, nil
	}
	s, err := c.current(ctx, c.ttl)
	if s == nil {
		return Tenant{}, false, err
	}
	t, ok := lookupHost(s, host)
	return t, ok, err
}

func lookupHost(s *snapshot, host string) (Tenant, bool) {
	if t, ok := s.byDomain[host]; ok {
		return t, true
	}
	label := host
	if i := strings.IndexByte(host, '.'); i >= 0 {
		label = host[:i]
	}
	t, ok := s.byLabel[label]
	if !ok || !HostMatchesCanonical(host, t.GatewayURL) {
		return Tenant{}, false
	}
	return t, true
}

// NormalizeHost canonicalises a Host header or URL host for comparison:
// trimmed, no port, no trailing dot, lowercase.
func NormalizeHost(host string) string { return normalizeHost(host) }

// CanonicalHost is the normalized host of a tenant's gateway URL, or ""
// when the URL is empty or has no host.
func CanonicalHost(gatewayURL string) string {
	gatewayURL = strings.TrimSpace(gatewayURL)
	if gatewayURL == "" {
		return ""
	}
	u, err := url.Parse(gatewayURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return normalizeHost(u.Host)
}

// HostMatchesCanonical reports whether a request host found by its first
// label is the tenant's full canonical host. Lookups key on the first DNS
// label only, so without this any domain routed to the gateway could name
// a tenant ("acme-a1b2c3.attacker.example"). A row that records no gateway
// URL keeps label-only matching: every provisioned row has one (the column
// is NOT NULL and the controlplane backfills it at startup), so that is a
// legacy fallback, not a path a real tenant takes. A URL that does not
// parse matches nothing.
func HostMatchesCanonical(host, gatewayURL string) bool {
	if strings.TrimSpace(gatewayURL) == "" {
		return true
	}
	canonical := CanonicalHost(gatewayURL)
	return canonical != "" && canonical == normalizeHost(host)
}

// recheckPaused reloads the snapshot when a request was refused against one
// older than pausedRecheck, so a resume is honoured within seconds instead
// of a full TTL. Returns the fresh answer.
func (c *Checker) recheckPaused(ctx context.Context, host string) (Tenant, bool) {
	s, _ := c.current(ctx, pausedRecheck)
	if s == nil {
		return Tenant{}, false
	}
	return lookupHost(s, normalizeHost(host))
}

// TouchLastUsed records an accepted request on managed_instances.last_used_at,
// at most once a minute per instance on this replica. The instance-limit
// engine orders by it when a grace period expires. Asynchronous: a lost
// touch only shifts the ordering by a minute. Only called for requests the
// gateway accepted (authenticated), so anonymous traffic cannot steer which
// instance survives auto-pause.
func (c *Checker) TouchLastUsed(ctx context.Context, instanceID string) {
	if c == nil || c.db == nil || instanceID == "" {
		return
	}
	now := c.now()
	c.touchMu.Lock()
	if last, ok := c.touched[instanceID]; ok && now.Sub(last) < c.touchGap {
		c.touchMu.Unlock()
		return
	}
	c.touched[instanceID] = now
	c.touchMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := c.db.ExecContext(ctx,
			`UPDATE everstack.managed_instances SET last_used_at = $1
			 WHERE id::text = $2 AND (last_used_at IS NULL OR last_used_at < $1)`, now, instanceID); err != nil {
			logger.WithFields("instance_id", instanceID).WithError(err).Debug("tenantstatus: last_used_at touch failed")
		}
	}()
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// ---- the paused response ----------------------------------------------

// ErrorBody is the JSON every request to a paused instance gets. The shape
// follows the gateway's other JSON errors ({"error":{...}}) so OpenAI-style
// clients surface the message, and carries the billing page.
type ErrorBody struct {
	Error PausedError `json:"error"`
}

// PausedError is the error object inside ErrorBody.
type PausedError struct {
	Type       string `json:"type"`
	Code       string `json:"code"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	BillingURL string `json:"billing_url,omitempty"`
}

// BillingURL is the cloud billing page for an org ("" without an app URL).
func BillingURL(cloudAppURL, orgSlug string) string {
	if strings.TrimSpace(cloudAppURL) == "" || orgSlug == "" {
		return ""
	}
	return strings.TrimRight(cloudAppURL, "/") + "/" + orgSlug + "/settings/billing"
}

// WritePaused answers a request for a paused instance: 403 with a JSON body
// when it is over the plan's instance limit, 503 for an explicit user stop.
// Browser navigations to an over-limit instance go to the billing page.
func WritePaused(w http.ResponseWriter, r *http.Request, apiRequest bool, reason, billingURL string) {
	overLimit := IsOverLimitReason(reason)
	body := ErrorBody{Error: PausedError{Type: "instance_paused", Code: "instance_paused", BillingURL: billingURL}}
	status := http.StatusServiceUnavailable
	if overLimit {
		status = http.StatusForbidden
		body.Error.Reason = "over_plan_instance_limit"
		body.Error.Message = "This instance is paused because your organization is over its plan's instance limit. Resume it, pause another instance, upgrade your plan, or pay for extra instances"
		if billingURL != "" {
			body.Error.Message += " at " + billingURL
		}
		body.Error.Message += "."
	} else {
		body.Error.Reason = "stopped"
		body.Error.Message = "This instance is stopped. Start it from the Everstack cloud console"
		if billingURL != "" {
			body.Error.Message += " (" + billingURL + ")"
		}
		body.Error.Message += "."
	}
	if !apiRequest && billingURL != "" && overLimit {
		http.Redirect(w, r, billingURL, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-EVS-Instance-Paused", body.Error.Reason)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// IsAPIRequest reports whether r is an API call rather than a browser
// navigation (Connect RPC, REST prefixes, JSON or gRPC content types).
func IsAPIRequest(r *http.Request) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, "/everstack.") || strings.HasPrefix(p, "/v1/") || strings.HasPrefix(p, "/api/") {
		return true
	}
	ct := r.Header.Get("Content-Type")
	return strings.Contains(ct, "application/json") || strings.Contains(ct, "connect+proto") || strings.Contains(ct, "grpc")
}

// HostGuard refuses every request whose host resolves to a tenant that is
// not active, before any auth or routing. Hosts that are not tenant hosts
// pass untouched. Liveness paths (/health*, /__*) always pass so probes keep
// working. A refusal re-checks a snapshot older than a few seconds first, so
// a resumed instance is not refused for a whole TTL.
//
// Accepted API requests record last use once they have succeeded (2xx), so
// only authenticated traffic counts toward which instance survives an
// auto-pause.
func HostGuard(c *Checker, cloudAppURL string, next http.Handler) http.Handler {
	if c == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; strings.HasPrefix(p, "/health") || strings.HasPrefix(p, "/__") {
			next.ServeHTTP(w, r)
			return
		}
		t, found, err := c.ResolveHost(r.Context(), r.Host)
		if err != nil {
			logger.WithFields("host", r.Host).WithError(err).Warn("tenantstatus: snapshot reload failed; using the last known state")
		}
		if found && !t.Active() {
			t, found = c.recheckPaused(r.Context(), r.Host)
		}
		if found && !t.Active() {
			// The billing page names the org slug: only a verified member
			// of the tenant's org sees it (EVS-324 item 7).
			billing := ""
			if c.verifiedViewer(r, t) {
				billing = BillingURL(cloudAppURL, t.OrgSlug)
			}
			WritePaused(w, r, IsAPIRequest(r), t.PausedReason, billing)
			return
		}
		if !found {
			next.ServeHTTP(w, r)
			return
		}
		// Name the Host's tenant for the credential validators, so a key of
		// another tenant is refused here instead of running (and being
		// counted as this tenant's use) on this host. Routing evidence
		// only; see contextkeys.WithHostSelectedTenant.
		r = r.WithContext(contextkeys.WithHostSelectedTenant(r.Context(), contextkeys.RequestInstanceScope{
			InstanceID:     t.InstanceID,
			OrganizationID: t.OrganizationID,
		}))
		ServeAndRecordUse(w, r, next, func() { c.TouchLastUsed(r.Context(), t.InstanceID) })
	})
}

// ServeAndRecordUse serves r and calls record only when it was an API
// request that succeeded (2xx): authenticated use, which is what decides
// which instances survive an auto-pause.
func ServeAndRecordUse(w http.ResponseWriter, r *http.Request, next http.Handler, record func()) {
	if !countsAsUse(r) {
		next.ServeHTTP(w, r)
		return
	}
	rec := &statusRecorder{ResponseWriter: w}
	next.ServeHTTP(rec, r)
	if rec.status >= 200 && rec.status < 300 {
		record()
	}
}

// countsAsUse limits last-use recording to API paths; the console's static
// assets answer 200 without auth.
func countsAsUse(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/everstack.") || strings.HasPrefix(p, "/v1/") || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/openai/")
}

// statusRecorder captures the response status without hiding the
// streaming, hijacking or http.ResponseController capabilities of w.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
