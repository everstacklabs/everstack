package middleware

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/api/common"
	httputil "github.com/everstacklabs/everstack/internal/api/http"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/auth/m2m"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/edition"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	licpkg "github.com/everstacklabs/everstack/internal/license"
	querypkg "github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/query/handlers/license"
	"github.com/everstacklabs/everstack/internal/services/trial"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	licenseconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
	"github.com/everstacklabs/everstack/pkg/tenant"
	"github.com/jmoiron/sqlx"
)

// LicenseState holds minimal fields for fast request checks
type LicenseState struct {
	Active       bool
	Status       string
	Tier         string
	IsPaid       bool
	ExpiresAt    *time.Time
	TrialExpires *time.Time
	FetchedAt    time.Time
	TenantId     string // Organization/tenant ID
	InstanceId   string // Gateway instance ID
	// SandboxBillingEnabled is independent from the platform plan fee. Free
	// tenants receive it only after completing usage billing setup.
	SandboxBillingEnabled bool
}

// IsSuspended returns true if the license is in "suspended" status (e.g., usage limits exceeded).
// Suspended licenses should still allow admin dashboard access; only AI gateway requests are blocked.
func (s *LicenseState) IsSuspended() bool {
	return s != nil && s.Status == "suspended"
}

// FeatureRelease represents feature metadata from the license service
type FeatureRelease struct {
	Name        string
	Description string
	Status      string   // 'development', 'beta', 'released', 'deprecated'
	Categories  []string // ['gateway', 'dashboard', 'api']
}

// FeaturesCallback is called when available features are updated
type FeaturesCallback func(features map[string]*FeatureRelease)

// SpendLimitConfigCallback is called when spend limit config is extracted from a license JWT.
// Parameters: amount, action ("block"|"warn"|"notify"), enabled.
type SpendLimitConfigCallback func(amount float64, action string, enabled bool)

// storedJWTPayload is the JSON shape stored in system.instances.signed_payload.
type storedJWTPayload struct {
	LicenseJWT       string `json:"license_jwt,omitempty"`
	LicensePublicKey string `json:"license_public_key,omitempty"`
}

// Circuit breaker constants for cold-start synchronous refresh path.
const (
	circuitHalfOpenAfter = 30 * time.Second // try again after 30s
	coldStartTimeout     = 3 * time.Second  // don't block requests for 10s on cold start
)

// LicenseGraceDuration is how long an expired license keeps its full plan
// entitlements before the instance degrades to CE limits (D6 in
// docs/design/editions-and-billing.md). Reads are never blocked in any state.
const LicenseGraceDuration = 14 * 24 * time.Hour

type LicenseEnforcer struct {
	sys                *cqrs.System
	db                 *sqlx.DB // Direct DB access for JWT persistence
	policy             *apilic.Policy
	licenseServiceURL  string
	deviceFingerprint  string // Device fingerprint for refresh validation
	mu                 sync.RWMutex
	cached             *LicenseState
	enabled            bool
	dryRun             bool
	trialManager       *trial.Manager
	featuresCallback   FeaturesCallback         // Callback when features are updated
	spendLimitCallback SpendLimitConfigCallback // Callback when spend limit config from JWT
	m2mProvider        m2m.TokenProvider        // New M2M provider for authentication

	// JWT-based enforcement
	verifier            *licpkg.Verifier // Ed25519 JWT verifier (nil = fallback to old behavior)
	pinned              bool             // verifier came from EVS_LICENSE_PUBLIC_KEY and cannot be replaced
	cachedJWT           string           // Raw JWT string for persistence
	cachedClaims        *licpkg.Claims   // Parsed claims from cachedJWT
	refreshing          atomic.Bool      // CAS guard for single-flight lazy refresh
	nearExpiryThreshold time.Duration    // Default 24h — triggers lazy refresh

	// Offline license file (air-gapped installs). Verified exclusively
	// against the compiled-in vendor keyring — never against a key shipped
	// beside the file (that would let operators self-sign licenses).
	licenseFilePath  string
	licenseFileMtime time.Time

	// Circuit breaker for cold-start synchronous refresh
	circuitOpen     atomic.Bool  // true = open (skip remote calls)
	circuitOpenedAt atomic.Int64 // unix timestamp (seconds) when circuit opened
}

func NewLicenseEnforcer(sys *cqrs.System, policy ...*apilic.Policy) *LicenseEnforcer {
	p := apilic.NewDefaultPolicy()
	if len(policy) > 0 && policy[0] != nil {
		p = policy[0]
	}
	return &LicenseEnforcer{
		sys:                 sys,
		policy:              p,
		enabled:             true,
		nearExpiryThreshold: 24 * time.Hour,
	}
}

// SetLicenseServiceURL sets the URL for remote license validation
func (l *LicenseEnforcer) SetLicenseServiceURL(url string) {
	l.licenseServiceURL = url
}

// SetDeviceFingerprint sets the device fingerprint for refresh validation
func (l *LicenseEnforcer) SetDeviceFingerprint(fingerprint string) {
	l.deviceFingerprint = fingerprint
}

// SetM2MProvider sets the M2M token provider for authenticated calls
func (l *LicenseEnforcer) SetM2MProvider(provider m2m.TokenProvider) {
	l.m2mProvider = provider
}

// SetVerifier sets the Ed25519 JWT verifier for local license verification.
// A pinned verifier (from EVS_LICENSE_PUBLIC_KEY) is the trust anchor and is
// never overwritten by a later, untrusted (DB- or activation-supplied) key.
func (l *LicenseEnforcer) SetVerifier(v *licpkg.Verifier) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pinned {
		return
	}
	l.verifier = v
}

// getVerifier returns the current JWT verifier under the lock.
func (l *LicenseEnforcer) getVerifier() *licpkg.Verifier {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.verifier
}

// ensureVerifierFromKey lazily initializes the verifier from a DB-supplied
// public key only when no configured key is pinned.
func (l *LicenseEnforcer) ensureVerifierFromKey(publicKey string) *licpkg.Verifier {
	l.mu.RLock()
	v, pinned := l.verifier, l.pinned
	l.mu.RUnlock()

	if pinned {
		if publicKey != "" {
			logger.Debug("license_enforcer: ignoring DB-stored license public key; verifier is pinned")
		}
		return v
	}
	if v != nil || publicKey == "" {
		return v
	}

	nv, err := licpkg.NewVerifier(publicKey)
	if err != nil {
		logger.WithError(err).Warn("license_enforcer: failed to create verifier from stored public key")
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pinned {
		return l.verifier
	}
	if l.verifier == nil {
		l.verifier = nv
		logger.Warn("license_enforcer: initialized JWT verifier from DB-stored public key (no EVS_LICENSE_PUBLIC_KEY pinned; license is not tamper-resistant)")
	}
	return l.verifier
}

// IsKeyPinned reports whether the license public key is pinned from config.
func (l *LicenseEnforcer) IsKeyPinned() bool {
	return l != nil && l.pinned
}

var (
	pinnedVerifierOnce sync.Once
	pinnedVerifier     *licpkg.Verifier
)

// loadPinnedVerifier returns the verifier built from EVS_LICENSE_PUBLIC_KEY,
// or nil when the configured trust anchor is absent or invalid.
func loadPinnedVerifier() *licpkg.Verifier {
	pinnedVerifierOnce.Do(func() {
		key := strings.TrimSpace(os.Getenv("EVS_LICENSE_PUBLIC_KEY"))
		if key == "" {
			return
		}
		v, err := licpkg.NewVerifier(key)
		if err != nil {
			logger.WithError(err).Error("license_enforcer: EVS_LICENSE_PUBLIC_KEY is set but invalid; refusing to pin")
			return
		}
		pinnedVerifier = v
		logger.Info("license_enforcer: pinned license public key from EVS_LICENSE_PUBLIC_KEY")
	})
	return pinnedVerifier
}

// SetDB sets the database connection for JWT persistence.
func (l *LicenseEnforcer) SetDB(db *sqlx.DB) {
	l.db = db
}

// Start loads the JWT from the database on startup and launches the
// background refresh loop.
func (l *LicenseEnforcer) Start(done <-chan struct{}) {
	if l == nil || l.sys == nil {
		return
	}
	if v := loadPinnedVerifier(); v != nil {
		l.mu.Lock()
		l.verifier = v
		l.pinned = true
		l.mu.Unlock()
	}
	// Offline license file wins at startup (air-gapped installs have no
	// license service to refresh from).
	l.reloadLicenseFile()
	// Try loading JWT from DB (signed_payload column).
	// This also initializes the verifier from the stored public key if needed.
	if l.getCached() == nil {
		l.loadJWTFromDB()
	}
	// If no JWT available, do one legacy refresh to populate cache
	if l.getCached() == nil {
		_ = l.refresh(context.Background())
	}
	go l.refreshLoop(done)
}

// SetLicenseFile configures the offline license file path (EVS_LICENSE_FILE).
// The file contains a single vendor-signed license JWT and is verified only
// against the compiled-in vendor keyring. It is re-checked on every refresh
// tick, so replacing the file (renewal) takes effect without a restart.
func (l *LicenseEnforcer) SetLicenseFile(path string) {
	l.licenseFilePath = path
	if path != "" && !licpkg.HasVendorKeyring() {
		logger.Warn("license_enforcer: EVS_LICENSE_FILE is set but this build carries no vendor keys; offline license files are not supported by this build (activate online instead)")
	}
}

// reloadLicenseFile loads the offline license file if configured and changed
// since the last load. Expired-but-authentic tokens are accepted; the grace
// state machine downstream decides what they entitle.
func (l *LicenseEnforcer) reloadLicenseFile() {
	if l.licenseFilePath == "" {
		return
	}
	fi, err := os.Stat(l.licenseFilePath)
	if err != nil {
		if !l.licenseFileMtime.IsZero() {
			logger.WithError(err).Warn("license_enforcer: offline license file no longer readable, keeping cached state")
		}
		return
	}
	if fi.ModTime().Equal(l.licenseFileMtime) {
		return
	}
	raw, err := os.ReadFile(l.licenseFilePath)
	if err != nil {
		logger.WithError(err).Warn("license_enforcer: failed to read offline license file")
		return
	}
	token := strings.TrimSpace(string(raw))
	claims, err := licpkg.VerifyWithVendorKeyring(token)
	if err != nil {
		logger.WithError(err).Warn("license_enforcer: offline license file failed verification against the vendor keyring")
		return
	}
	l.licenseFileMtime = fi.ModTime()
	l.SetCachedJWT(token, claims)
	// Persist so restarts (and the grace window) survive even if the file
	// is later removed.
	go l.storeJWTToDB(token)
	logger.WithFields("tier", claims.Tier, "expires", claims.ExpiresAt).Info("license_enforcer: loaded offline license file")
}

// refreshLoop keeps the cached license state fresh autonomously so renewal
// self-heals (grace -> renewed, degraded -> renewed) without an admin having
// to trigger a manual refresh. The request path never blocks on refresh.
//
// Cost control: refresh() exits before any network call when the instance has
// no stored credentials (never activated) or no license service URL is
// configured, so an unlicensed CE instance does a cheap local DB check per
// tick and nothing else — no phone-home for never-activated instances.
func (l *LicenseEnforcer) refreshLoop(done <-chan struct{}) {
	const baseInterval = time.Hour
	timer := time.NewTimer(nextRefreshDelay(baseInterval))
	defer timer.Stop()
	for {
		select {
		case <-done:
			return
		case <-timer.C:
		}
		// Offline license file: cheap mtime check, hot-reloads renewals.
		l.reloadLicenseFile()
		if l.licenseServiceURL != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = l.refresh(ctx)
			cancel()
		}
		timer.Reset(nextRefreshDelay(baseInterval))
	}
}

// nextRefreshDelay adds up to 10 minutes of jitter so fleets don't
// thundering-herd the license service.
func nextRefreshDelay(base time.Duration) time.Duration {
	return base + time.Duration(rand.Int63n(int64(10*time.Minute)))
}

// WithLicenseEnforcement enforces that the current instance has an active, valid license.
func (l *LicenseEnforcer) WithLicenseEnforcement(next http.Handler) http.Handler {
	if l == nil || l.sys == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.enabled {
			next.ServeHTTP(w, r)
			return
		}
		// Managed/shared tenant requests do not use local license activation.
		// They are provisioned by control plane and should bypass local enforcer.
		if tc := tenant.ConfigFromContext(r.Context()); tc != nil {
			next.ServeHTTP(w, r)
			return
		}
		if contextkeys.IsTenantAuthenticated(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		// Bypass enforcement via policy
		if l.policy != nil && l.policy.ShouldBypassPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		st := l.getCached()
		// Fallback: if cache is empty (e.g., activated after startup), try
		// loading the persisted JWT. Deliberately NO synchronous remote
		// refresh on the request path: a permanently-unlicensed CE instance
		// must not pay a per-request refresh tax (editions-and-billing.md D9).
		if st == nil && l.getVerifier() != nil {
			l.loadJWTFromDB()
			st = l.getCached()
		}

		// Trigger lazy refresh if JWT is near expiry but not yet expired
		l.mu.RLock()
		claims := l.cachedClaims
		l.mu.RUnlock()
		if claims != nil && claims.NearExpiry(l.nearExpiryThreshold) && !claims.IsExpired() {
			go l.lazyRefresh()
		}

		now := time.Now().UTC()

		// Helper function to check if the request is a write operation
		isWriteOperation := func(method string) bool {
			// Whitelist activation-related endpoints to allow them even when license is disabled
			if strings.Contains(r.URL.Path, "ActivateGatewayInstance") || strings.Contains(r.URL.Path, "GenerateActivationToken") {
				return false
			}
			return method == http.MethodPost || method == http.MethodPatch ||
				method == http.MethodPut || method == http.MethodDelete
		}

		// Suspended licenses (usage limits exceeded): allow ALL admin UI / HTTP operations.
		// AI gateway requests are blocked separately by SpendLimitInterceptor.
		if st != nil && st.IsSuspended() {
			w.Header().Set("X-Everstack-License-Status", "suspended")
			next.ServeHTTP(w, r)
			return
		}

		// Explicitly disabled/revoked licenses keep a harder posture: reads
		// and data export stay available (never brick), writes are blocked.
		// NOTE: the license service does not emit these statuses yet (it
		// produces active/inactive/expired/suspended/cancelled); this is the
		// forward hook for a real vendor kill switch. Cancelled subscriptions
		// deliberately degrade to CE like any inactive license — cancelling
		// must never be punished harder than never having paid.
		if st != nil && (st.Status == "disabled" || st.Status == "revoked") {
			if isWriteOperation(r.Method) {
				if l.dryRun {
					logger.Warn("license enforcement (dry-run): license disabled, blocking write operation")
					next.ServeHTTP(w, r)
					return
				}
				writePaymentRequired(w, http.StatusForbidden, "license disabled; contact support or renew")
				return
			}
			w.Header().Set("X-Everstack-License-Status", "disabled")
			next.ServeHTTP(w, r)
			return
		}

		if st == nil || !st.Active {
			// Trial mode (optional elevated preview): meter AI-gateway paths
			// while active; on expiry fall through to CE, never to a wall.
			if l.trialManager != nil && l.trialManager.IsActive() && !l.trialManager.IsExpired() {
				// Only meter requests that match metered paths (AI gateway endpoints)
				// All other endpoints (admin UI, internal services) are allowed without counting
				if l.policy != nil && l.policy.ShouldMeterRequest(r.URL.Path) {
					if err := l.trialManager.RecordRequest(r.Context()); err != nil {
						writeTrialLimitExceeded(w, err)
						return
					}
				}
				// Add trial mode header for UI awareness
				w.Header().Set("X-Everstack-Mode", "trial")
				next.ServeHTTP(w, r)
				return
			}

			// Unlicensed is a terminal, non-blocking state: the instance runs
			// as Community Edition. CE creation limits and feature gates do
			// the gating; the request itself always proceeds (D9).
			w.Header().Set("X-Everstack-License-Status", "unlicensed")
			next.ServeHTTP(w, r)
			return
		}

		// Expired licenses follow the grace state machine (D6): full
		// entitlements for the 14-day grace window, then degrade to CE.
		// Neither state blocks the request here — after grace the monitor
		// reports the license inactive, so CE limits apply at the
		// enforcement callsites.
		if st.ExpiresAt != nil && st.ExpiresAt.Before(now) {
			if now.Before(st.ExpiresAt.Add(LicenseGraceDuration)) {
				w.Header().Set("X-Everstack-License-Status", "grace")
			} else {
				w.Header().Set("X-Everstack-License-Status", "degraded-ce")
			}
			next.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// InGracePeriod reports whether the cached license is expired but still within
// the grace window (full entitlements retained).
func (l *LicenseEnforcer) InGracePeriod() bool {
	st := l.getCached()
	if st == nil || st.ExpiresAt == nil {
		return false
	}
	now := time.Now().UTC()
	return st.ExpiresAt.Before(now) && now.Before(st.ExpiresAt.Add(LicenseGraceDuration))
}

func (l *LicenseEnforcer) getCached() *LicenseState {
	l.mu.RLock()
	st := l.cached
	l.mu.RUnlock()
	return st
}

func (l *LicenseEnforcer) GetCached() *LicenseState {
	return l.getCached()
}

// SetCachedState directly sets the cached license state.
// Use this when you already have the license state (e.g., from activation response)
// to avoid an additional RPC call to the license service.
func (l *LicenseEnforcer) SetCachedState(state *LicenseState) {
	if state == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state.FetchedAt = time.Now().UTC()
	l.cached = state
	logger.WithFields(
		"tier", state.Tier,
		"is_paid", state.IsPaid,
		"active", state.Active,
	).Info("license_enforcer: cached state updated directly")
}

// SetCachedJWT stores a verified JWT and updates the cached license state from its claims.
func (l *LicenseEnforcer) SetCachedJWT(jwtString string, claims *licpkg.Claims) {
	if claims == nil {
		return
	}
	l.mu.Lock()
	l.cachedJWT = jwtString
	l.cachedClaims = claims
	st := claimsToLicenseState(claims)
	l.cached = st
	// Capture callback under lock
	spendCb := l.spendLimitCallback
	l.mu.Unlock()

	logger.WithFields(
		"tier", st.Tier,
		"active", st.Active,
	).Info("license_enforcer: cached state updated from JWT")

	// Push spend limit config from JWT to Monitor (if present)
	if spendCb != nil && (claims.SpendLimitEnabled || claims.SpendLimitAmount > 0) {
		spendCb(claims.SpendLimitAmount, claims.SpendLimitAction, claims.SpendLimitEnabled)
	}
}

// claimsToLicenseState converts JWT claims to LicenseState.
func claimsToLicenseState(c *licpkg.Claims) *LicenseState {
	isPaid := c.Tier != "free"

	var expiresAt *time.Time
	var trialExpires *time.Time
	if c.ExpiresAt != nil {
		t := c.ExpiresAt.Time
		expiresAt = &t
		if !isPaid {
			trialExpires = &t
		}
	}

	return &LicenseState{
		Active:                c.Status == "active",
		Status:                c.Status,
		Tier:                  c.Tier,
		IsPaid:                isPaid,
		ExpiresAt:             expiresAt,
		TrialExpires:          trialExpires,
		FetchedAt:             time.Now().UTC(),
		TenantId:              c.TenantID,
		InstanceId:            c.Subject,
		SandboxBillingEnabled: c.SandboxBillingEnabled,
	}
}

// SetFeaturesCallback sets a callback that will be invoked when available features are updated
func (l *LicenseEnforcer) SetFeaturesCallback(callback FeaturesCallback) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.featuresCallback = callback
}

// SetSpendLimitConfigCallback sets a callback invoked when spend limit config is extracted from JWT.
func (l *LicenseEnforcer) SetSpendLimitConfigCallback(callback SpendLimitConfigCallback) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.spendLimitCallback = callback
}

// RefreshNow forces an immediate refresh of the cached license state.
func (l *LicenseEnforcer) RefreshNow() { _ = l.refresh(context.Background()) }

// lazyRefresh performs a single-flight async refresh of the license JWT.
// Only one goroutine refreshes at a time via atomic CAS.
func (l *LicenseEnforcer) lazyRefresh() {
	if !l.refreshing.CompareAndSwap(false, true) {
		return // Another goroutine is already refreshing
	}
	defer l.refreshing.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st := l.fetchWithJWT(ctx)
	if st == nil {
		logger.Warn("license_enforcer: lazy refresh failed, continuing with existing JWT")
		return
	}

	l.mu.Lock()
	st.FetchedAt = time.Now().UTC()
	l.cached = st
	l.mu.Unlock()
}

// loadJWTFromDB reads signed_payload from system.instances, initializes the verifier
// from the stored public key if needed, and verifies the JWT.
func (l *LicenseEnforcer) loadJWTFromDB() {
	if l.sys == nil || l.sys.QueryBus == nil {
		return
	}

	// Use the existing query to get signed_payload
	credsQuery := license.GetInstanceCredentials{}
	resp, err := l.sys.QueryBus.Execute(context.Background(), credsQuery)
	if err != nil || resp == nil {
		return
	}

	var credsAny interface{} = resp
	if qr, ok := resp.(*querypkg.Response); ok && qr != nil {
		credsAny = qr.Data
	}

	creds, ok := credsAny.(*license.InstanceCredentials)
	if !ok || creds == nil {
		return
	}

	// Parse signed_payload JSON to extract license_jwt and public key
	if len(creds.SignedPayload) == 0 {
		return
	}

	var payload storedJWTPayload
	if err := json.Unmarshal(creds.SignedPayload, &payload); err != nil || payload.LicenseJWT == "" {
		return
	}

	// A pinned configuration key always wins over a DB-supplied key.
	verifier := l.ensureVerifierFromKey(payload.LicensePublicKey)
	if verifier == nil {
		return
	}

	// Verify the JWT signature
	claims, err := verifier.Verify(payload.LicenseJWT)
	if err != nil {
		// An authentic-but-expired token is still load-bearing: it anchors
		// the grace window across restarts (grace runs from the token's own
		// exp claim, so a restart one day after expiry must not lose the
		// last-known-good entitlements). Signature/issuer/audience are still
		// fully verified; only expiry is relaxed.
		expiredClaims, expErr := verifier.VerifyAllowExpired(payload.LicenseJWT)
		if expErr != nil {
			logger.WithError(err).Warn("license_enforcer: stored JWT failed verification")
			return
		}
		l.SetCachedJWT(payload.LicenseJWT, expiredClaims)
		logger.Info("license_enforcer: loaded authentic expired JWT from database (grace/degraded handling applies)")
		return
	}

	l.SetCachedJWT(payload.LicenseJWT, claims)
	logger.Info("license_enforcer: loaded and verified JWT from database")
}

// storeJWTToDB writes the JWT to system.instances.signed_payload, preserving the public key.
func (l *LicenseEnforcer) storeJWTToDB(jwtString string) {
	if l.db == nil {
		return
	}

	// Read existing signed_payload to preserve the public key
	var existingPayloadStr string
	_ = l.db.QueryRow(
		`SELECT signed_payload FROM system.instances WHERE instance_status = 'active' ORDER BY updated_at DESC LIMIT 1`,
	).Scan(&existingPayloadStr)

	var existing storedJWTPayload
	if existingPayloadStr != "" {
		_ = json.Unmarshal([]byte(existingPayloadStr), &existing)
	}

	payload := storedJWTPayload{
		LicenseJWT:       jwtString,
		LicensePublicKey: existing.LicensePublicKey, // Preserve existing public key
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		logger.WithError(err).Warn("license_enforcer: failed to marshal JWT payload")
		return
	}

	_, err = l.db.Exec(
		`UPDATE system.instances SET signed_payload = $1, updated_at = NOW() WHERE instance_status = 'active'`,
		payloadJSON,
	)
	if err != nil {
		logger.WithError(err).Warn("license_enforcer: failed to store JWT in database")
	} else {
		logger.Debug("license_enforcer: stored JWT in database")
	}
}

// fetchWithJWT calls the license service and processes the JWT from the response.
func (l *LicenseEnforcer) fetchWithJWT(ctx context.Context) *LicenseState {
	st, jwtString := l.fetchFromService(ctx)
	if st == nil {
		return nil
	}

	// If we got a JWT, verify and store it.
	if verifier := l.getVerifier(); jwtString != "" && verifier != nil {
		claims, err := verifier.Verify(jwtString)
		if err != nil {
			logger.WithError(err).Warn("license_enforcer: JWT from refresh failed verification, using proto state")
		} else {
			l.mu.Lock()
			l.cachedJWT = jwtString
			l.cachedClaims = claims
			l.mu.Unlock()
			l.storeJWTToDB(jwtString)
		}
	}

	return st
}

func (l *LicenseEnforcer) refresh(ctx context.Context) error {
	st, jwtString := l.fetchFromService(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()

	if st != nil {
		st.FetchedAt = time.Now().UTC()
		l.cached = st

		// If we got a JWT, verify and cache it
		if jwtString != "" && l.verifier != nil {
			claims, err := l.verifier.Verify(jwtString)
			if err != nil {
				logger.WithError(err).Warn("license_enforcer: JWT from refresh failed verification")
			} else {
				l.cachedJWT = jwtString
				l.cachedClaims = claims
				go l.storeJWTToDB(jwtString)
			}
		}
	} else {
		// Failed to fetch - keep existing cache for resilience
		if l.cached != nil {
			logger.Warnf("license_enforcer: failed to fetch fresh license state, continuing with cached state from %s (tier: %s, active: %v)",
				l.cached.FetchedAt.Format(time.RFC3339), l.cached.Tier, l.cached.Active)
		} else {
			if l.trialManager != nil && l.trialManager.IsActive() {
				logger.Debug("license_enforcer: no license state available, trial mode is active")
			} else {
				logger.Warn("license_enforcer: no license state available and trial mode not active")
			}
		}
	}
	return nil
}

// fetchFromService calls the License Service /refresh endpoint and returns the license state
// and the JWT string (if present in the response). Returns (nil, "") on failure.
func (l *LicenseEnforcer) fetchFromService(ctx context.Context) (*LicenseState, string) {
	if l.sys == nil || l.sys.QueryBus == nil {
		return nil, ""
	}

	// Get instance credentials (instance_id + refresh_token) from local DB
	credsQuery := license.GetInstanceCredentials{}
	resp, err := l.sys.QueryBus.Execute(ctx, credsQuery)
	if err != nil || resp == nil {
		logger.Warn("license_enforcer.fetch: failed to query instance credentials from DB")
		return nil, ""
	}

	// Unwrap query response
	var credsAny interface{} = resp
	if qr, ok := resp.(*querypkg.Response); ok && qr != nil {
		credsAny = qr.Data
	}

	creds, ok := credsAny.(*license.InstanceCredentials)
	if !ok || creds == nil || creds.InstanceID == "" || creds.RefreshToken == "" {
		logger.Debug("license_enforcer.fetch: instance credentials incomplete or missing (instance_id or refresh_token empty)")
		return nil, ""
	}

	// Call License Service /refresh endpoint remotely to get fresh license state
	if l.licenseServiceURL == "" {
		logger.Warn("license_enforcer.fetch: license service URL not configured")
		return nil, ""
	}

	// Use M2M authenticated HTTP client
	var httpClient *http.Client
	if l.m2mProvider != nil {
		httpClient = m2m.NewHTTPClient(l.m2mProvider, 10*time.Second)
		logger.Debug("license_enforcer.fetch: using new M2M provider")
	} else if len(creds.SigningKey) > 0 {
		httpClient = httputil.M2MHTTPClient(creds.InstanceID, creds.SigningKey, 10*time.Second)
		logger.Debug("license_enforcer.fetch: using legacy M2M (instance signing key)")
	} else {
		httpClient = &http.Client{Timeout: 10 * time.Second}
		logger.Warn("license_enforcer.fetch: no M2M credentials available, request may fail")
	}
	licenseClient := licenseconnect.NewInstanceServiceClient(httpClient, l.licenseServiceURL)

	refreshReq := connect.NewRequest(&licv1.RefreshInstanceRequest{
		InstanceId:            creds.InstanceID,
		RefreshToken:          creds.RefreshToken,
		DeviceFingerprintHash: l.deviceFingerprint,
	})

	refreshResp, err := licenseClient.RefreshInstance(ctx, refreshReq)
	if err != nil {
		logger.WithError(err).Warn("license_enforcer.fetch: failed to refresh license from service")
		return nil, ""
	}

	if refreshResp.Msg == nil || refreshResp.Msg.GetLicenseState() == nil {
		logger.Warn("license_enforcer.fetch: empty license state from service")
		return nil, ""
	}

	licState := refreshResp.Msg.GetLicenseState()
	jwtString := refreshResp.Msg.GetLicenseJwt()

	// Convert protobuf enum to tier string
	tier := "free"
	switch licState.PlanTier {
	case licv1.LicenseType_LICENSE_TYPE_FREE:
		tier = "free"
	case licv1.LicenseType_LICENSE_TYPE_BASIC:
		tier = "basic"
	case licv1.LicenseType_LICENSE_TYPE_PRO:
		tier = "pro"
	case licv1.LicenseType_LICENSE_TYPE_ENTERPRISE:
		tier = "enterprise"
	}

	logger.Infof("license_enforcer.fetch: refreshed license state - instance_id=%s plan_tier_enum=%d tier_string=%s status=%d",
		creds.InstanceID, int32(licState.PlanTier), tier, int32(licState.Status))

	isPaid := tier != "free"

	// Parse expiration
	var expiresAt *time.Time
	var trialExpires *time.Time
	if licState.ExpiresAt != nil {
		t := licState.ExpiresAt.AsTime()
		expiresAt = &t
		if !isPaid {
			trialExpires = &t
		}
	}

	// Map status enum to string
	status := "active"
	switch licState.Status {
	case licv1.LicenseStatus_LICENSE_STATUS_ACTIVE:
		status = "active"
	case licv1.LicenseStatus_LICENSE_STATUS_INACTIVE:
		status = "inactive"
	case licv1.LicenseStatus_LICENSE_STATUS_EXPIRED:
		status = "expired"
	case licv1.LicenseStatus_LICENSE_STATUS_SUSPENDED:
		status = "suspended"
	case licv1.LicenseStatus_LICENSE_STATUS_CANCELLED:
		status = "cancelled"
	}

	// Notify callback with available features if present
	if protoFeatures := refreshResp.Msg.GetAvailableFeatures(); len(protoFeatures) > 0 {
		l.mu.RLock()
		callback := l.featuresCallback
		l.mu.RUnlock()

		if callback != nil {
			features := make(map[string]*FeatureRelease, len(protoFeatures))
			for key, pf := range protoFeatures {
				features[key] = &FeatureRelease{
					Name:        pf.GetName(),
					Description: pf.GetDescription(),
					Status:      pf.GetStatus(),
					Categories:  pf.GetCategories(),
				}
			}
			callback(features)
		}
	}

	return &LicenseState{
		Active:                status == "active",
		Status:                status,
		Tier:                  tier,
		IsPaid:                isPaid,
		ExpiresAt:             expiresAt,
		TrialExpires:          trialExpires,
		TenantId:              licState.TenantId,
		InstanceId:            licState.InstanceId,
		SandboxBillingEnabled: licState.GetSandboxBillingEnabled(),
	}, jwtString
}

// Configuration helpers

// SetEnabled toggles enforcement. In shipped builds (CE/EE) enforcement can
// only be strengthened: config cannot switch it off, or the entire edition
// model is opt-out via YAML (editions-and-billing.md, D10). Dev builds keep
// the switch for local work.
func (l *LicenseEnforcer) SetEnabled(enabled bool) {
	if !enabled && !edition.IsDev() {
		logger.Warn("license_enforcer: ignoring config attempt to disable license enforcement (only dev builds may disable; see docs/design/editions-and-billing.md D10)")
		return
	}
	l.enabled = enabled
}

func (l *LicenseEnforcer) IsEnabled() bool    { return l.enabled }
func (l *LicenseEnforcer) SetDryRun(dry bool) { l.dryRun = dry }

// SetCacheTTL is a no-op kept for backwards compatibility. The enforcer now uses
// JWT-based lazy refresh instead of periodic polling.
func (l *LicenseEnforcer) SetCacheTTL(ttl time.Duration) {}

// SetTrialManager sets the trial manager for anonymous trial mode fallback
func (l *LicenseEnforcer) SetTrialManager(tm *trial.Manager) {
	l.trialManager = tm
}

// GetTrialManager returns the trial manager (for status endpoints)
func (l *LicenseEnforcer) GetTrialManager() *trial.Manager {
	return l.trialManager
}

// isCircuitOpen returns true if the circuit breaker is open and the half-open
// interval has not yet elapsed.
func (l *LicenseEnforcer) isCircuitOpen() bool {
	if !l.circuitOpen.Load() {
		return false
	}
	openedAt := time.Unix(l.circuitOpenedAt.Load(), 0)
	if time.Since(openedAt) >= circuitHalfOpenAfter {
		// Half-open: allow one attempt
		return false
	}
	return true
}

// openCircuit marks the circuit as open.
func (l *LicenseEnforcer) openCircuit() {
	l.circuitOpenedAt.Store(time.Now().Unix())
	l.circuitOpen.Store(true)
}

// closeCircuit marks the circuit as closed (healthy).
func (l *LicenseEnforcer) closeCircuit() {
	l.circuitOpen.Store(false)
}

// IsInTrialMode returns true if the gateway is operating in trial mode
func (l *LicenseEnforcer) IsInTrialMode() bool {
	if l.trialManager == nil {
		return false
	}
	st := l.getCached()
	// Trial mode is active when no license and trial manager is available and not expired
	return (st == nil || !st.Active) && l.trialManager.IsActive() && !l.trialManager.IsExpired()
}

// writePaymentRequired writes a consistent JSON error envelope.
func writePaymentRequired(w http.ResponseWriter, status int, message string) {
	w.Header().Set(common.ContentType, "application/json")
	w.WriteHeader(status)
	payload := map[string]any{
		"error": map[string]any{
			"type":    "error",
			"message": message,
			"code":    status,
		},
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// writeTrialLimitExceeded writes a response for trial limit exceeded
func writeTrialLimitExceeded(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Everstack-Mode", "trial")
	w.WriteHeader(http.StatusTooManyRequests)

	message := "Trial limit exceeded"
	limitType := "unknown"

	switch err {
	case trial.ErrDailyLimitReached:
		message = "Daily request limit reached. Try again tomorrow or activate a license."
		limitType = "daily"
	case trial.ErrRPMLimitReached:
		message = "Rate limit exceeded. Please slow down or activate a license."
		limitType = "rpm"
	}

	payload := map[string]any{
		"error": map[string]any{
			"type":       "trial_limit_exceeded",
			"limit_type": limitType,
			"message":    message,
			"code":       http.StatusTooManyRequests,
		},
	}
	_ = json.NewEncoder(w).Encode(payload)
}
