package v1

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/hosting"
	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
)

const (
	codeTTL              = 10 * time.Minute
	codeMaxAttempts      = 5
	claimRecoveryTimeout = 30 * time.Second
)

// ─── ClaimSite ──────────────────────────────────────────────────────────

// ClaimSite starts claiming an anonymous site: it validates the claim
// token and sends a one-time code to the email. VerifyCode (with the same
// slug + token) finishes the claim. Anonymous by design; the claim token
// is the credential.
func (s *Server) ClaimSite(ctx context.Context, req *connect.Request[hostingpb.ClaimSiteRequest]) (*connect.Response[hostingpb.ClaimSiteResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !s.codeLimiter.Allow(clientIP(s, req)) || !s.globalCode.Allow() {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit exceeded"))
	}

	email, err := normalizeEmail(req.Msg.GetEmail())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	slug := strings.ToLower(strings.TrimSpace(req.Msg.GetSlug()))

	site, err := s.loadSiteBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("site not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load site: %w", err))
	}
	if err := s.checkClaimToken(site, req.Msg.GetClaimToken()); err != nil {
		return nil, err
	}

	if err := s.createAndSendCode(ctx, email, slug, clientIP(s, req)); err != nil {
		return nil, err
	}
	return connect.NewResponse(&hostingpb.ClaimSiteResponse{CodeSent: true}), nil
}

// ─── RequestCode / VerifyCode ───────────────────────────────────────────

func (s *Server) RequestCode(ctx context.Context, req *connect.Request[hostingpb.RequestCodeRequest]) (*connect.Response[hostingpb.RequestCodeResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !s.codeLimiter.Allow(clientIP(s, req)) || !s.globalCode.Allow() {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit exceeded"))
	}

	email, err := normalizeEmail(req.Msg.GetEmail())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.createAndSendCode(ctx, email, "", clientIP(s, req)); err != nil {
		return nil, err
	}
	return connect.NewResponse(&hostingpb.RequestCodeResponse{CodeSent: true}), nil
}

func (s *Server) VerifyCode(ctx context.Context, req *connect.Request[hostingpb.VerifyCodeRequest]) (*connect.Response[hostingpb.VerifyCodeResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !s.codeLimiter.Allow(clientIP(s, req)) || !s.globalCode.Allow() {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit exceeded"))
	}
	if s.provisionOwner == nil || s.issueKey == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("account provisioning is not configured on this deployment"))
	}

	email, err := normalizeEmail(req.Msg.GetEmail())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	code := strings.TrimSpace(req.Msg.GetCode())
	if code == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("code is required"))
	}

	if err := s.consumeCode(ctx, email, code); err != nil {
		return nil, err
	}

	userID, orgID, err := s.provisionOwner(ctx, email)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to provision account: %w", err))
	}

	// Finish a pending site claim when slug + token accompany the code.
	claimSlug := strings.ToLower(strings.TrimSpace(req.Msg.GetSlug()))
	if claimSlug != "" {
		if err := s.bindClaimedSite(ctx, claimSlug, req.Msg.GetClaimToken(), orgID, userID); err != nil {
			// Do not mint a key until the quota-checked claim succeeds. A
			// failed claim can then be retried without accumulating keys that
			// were never returned to the caller.
			return nil, err
		}
	}

	apiKey, err := s.issueKey(ctx, orgID, "evs.run hosting")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to issue API key; request a new sign-in code and retry: %w", err))
	}
	if claimSlug != "" {
		// Retire the claim token only after the caller's API key exists. If key
		// issuance fails, the same owner can request another code and retry the
		// idempotent claim instead of being locked out of the now-owned site.
		if err := s.retireClaimToken(ctx, claimSlug, orgID, userID); err != nil {
			slog.Warn("hosting: failed to retire completed claim token", "slug", claimSlug, "error", err)
		}
	}

	return connect.NewResponse(&hostingpb.VerifyCodeResponse{ApiKey: apiKey, OrgId: orgID}), nil
}

// ─── internals ──────────────────────────────────────────────────────────

func (s *Server) checkClaimToken(site *siteRow, token string) error {
	if site.ClaimTokenHash == nil {
		if site.TenantID != nil {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("site is already claimed"))
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New("invalid claim token"))
	}
	if token == "" ||
		subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(*site.ClaimTokenHash)) != 1 {
		return connect.NewError(connect.CodePermissionDenied, errors.New("invalid claim token"))
	}
	return nil
}

func (s *Server) createAndSendCode(ctx context.Context, email, slug, ip string) error {
	if s.sendCode == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("email delivery is not configured on this deployment"))
	}

	code, err := newNumericCode(6)
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("failed to generate code"))
	}

	var slugVal any
	if slug != "" {
		slugVal = slug
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO site_email_codes (email, code_hash, slug, ip, expires_at)
		VALUES ($1, $2, $3, NULLIF($4, '')::inet, $5)`,
		email, hashToken(code), slugVal, ip, time.Now().UTC().Add(codeTTL),
	); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to store code: %w", err))
	}

	if err := s.sendCode(ctx, email, code); err != nil {
		slog.Warn("hosting: code email failed", "error", err)
		return connect.NewError(connect.CodeInternal, errors.New("failed to send code"))
	}
	return nil
}

// consumeCode validates the newest live code for the email, counting
// attempts so codes cannot be brute forced.
func (s *Server) consumeCode(ctx context.Context, email, code string) error {
	var row struct {
		ID       string `db:"id"`
		CodeHash string `db:"code_hash"`
		Attempts int    `db:"attempts"`
	}
	err := s.db.GetContext(ctx, &row, `
		SELECT id, code_hash, attempts FROM site_email_codes
		WHERE email = $1 AND consumed_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC LIMIT 1`,
		email,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired code"))
		}
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load code: %w", err))
	}
	if row.Attempts >= codeMaxAttempts {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("too many attempts; request a new code"))
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(code)), []byte(row.CodeHash)) != 1 {
		if _, uerr := s.db.ExecContext(ctx,
			`UPDATE site_email_codes SET attempts = attempts + 1 WHERE id = $1`, row.ID,
		); uerr != nil {
			slog.Warn("hosting: attempt counter update failed", "error", uerr)
		}
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired code"))
	}
	// Atomic single-consume: the WHERE consumed_at IS NULL guard means only
	// one of N concurrent VerifyCode calls with the same code can win, so a
	// code cannot be replayed into multiple API keys.
	res, err := s.db.ExecContext(ctx,
		`UPDATE site_email_codes SET consumed_at = NOW() WHERE id = $1 AND consumed_at IS NULL`, row.ID,
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to consume code: %w", err))
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired code"))
	}
	return nil
}

// bindClaimedSite attaches an anonymous site to the freshly provisioned
// org: expiry cleared, claim token retired, manifest rewritten without the
// anonymous noindex/expiry flags.
func (s *Server) bindClaimedSite(ctx context.Context, slug, claimToken, orgID, userID string) error {
	if err := s.withModerationProjectionLock(ctx, slug, func(lockCtx context.Context) error {
		return s.bindClaimedSiteLocked(lockCtx, slug, claimToken, orgID, userID)
	}); err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return connectErr
		}
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to lock site claim: %w", err))
	}
	return nil
}

// bindClaimedSiteLocked performs the ownership transition while holding the
// same per-slug projection lock used by finalize, settings updates, delete,
// and moderation. Callers must not invoke it without that lock.
func (s *Server) bindClaimedSiteLocked(ctx context.Context, slug, claimToken, orgID, userID string) error {
	site, err := s.loadSiteBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, errors.New("site not found"))
		}
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load site: %w", err))
	}
	if site.Status == "deleting" || site.Status == "deleted" {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("site is being deleted"))
	}
	if site.TenantID != nil {
		if *site.TenantID == orgID && site.OwnerUserID != nil && *site.OwnerUserID == userID && site.ClaimTokenHash != nil {
			if err := s.checkClaimToken(site, claimToken); err != nil {
				return err
			}
			// A prior COMMIT may have succeeded while its outcome re-reads and
			// compensation failed transiently, leaving an anonymous projection over
			// an owned row. Re-project from current DB state before treating the
			// same-owner retry as complete or retiring its last recovery token.
			if err := s.repairOwnedClaimProjection(ctx, site); err != nil {
				return connect.NewError(connect.CodeInternal,
					fmt.Errorf("failed to restore claimed site projection: %w", err))
			}
			return nil
		}
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("site is already claimed"))
	}
	if err := s.checkClaimToken(site, claimToken); err != nil {
		return err
	}

	quota, quotaEnabled, err := s.resolveTenantQuota(ctx, orgID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	// Keep the transaction lifetime independent from request cancellation. The
	// individual queries still use ctx, so cancellation stops active work, but
	// database/sql cannot race its automatic rollback against failClaim's
	// synchronous rollback and manifest compensation below.
	tx, err := s.db.BeginTxx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to begin site claim: %w", err))
	}
	defer func() { _ = tx.Rollback() }()
	if quotaEnabled {
		storageBytes, err := retainedSiteStorageTx(ctx, tx, site.ID)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
		if err := enforceTenantQuotaTx(ctx, tx, orgID, quota, hosting.TenantUsage{
			Sites:        1,
			StorageBytes: storageBytes,
		}); err != nil {
			var exceeded *hosting.QuotaExceededError
			if errors.As(err, &exceeded) {
				return quotaConnectError(err)
			}
			return connect.NewError(connect.CodeInternal, err)
		}
	}

	// Rewrite the permanent manifest (no expiry, no forced noindex) BEFORE
	// committing ownership. If this fails we abort with the site still
	// anonymous, rather than reporting a successful claim while the edge
	// keeps the anonymous expiry and later serves 410 for an owned site.
	view := *site
	view.TenantID = &orgID
	view.ExpiresAt = nil
	manifestProjectionDirty := false
	failClaim := func(claimErr error) error {
		_ = tx.Rollback()
		if !manifestProjectionDirty {
			return claimErr
		}
		if restoreErr := s.restoreAuthoritativeClaimManifest(ctx, site); restoreErr != nil {
			return connect.NewError(connect.CodeInternal, errors.Join(claimErr, restoreErr))
		}
		return claimErr
	}
	if view.CurrentVersion != nil {
		// A storage PUT can return an outcome-ambiguous error after accepting
		// the body. Mark the projection dirty before attempting it so every
		// error path restores the still-anonymous authoritative manifest.
		manifestProjectionDirty = true
		if err := s.rewriteManifestWith(ctx, tx, &view); err != nil {
			return failClaim(connect.NewError(connect.CodeInternal,
				fmt.Errorf("failed to publish claimed site manifest: %w", err)))
		}
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE sites SET
			tenant_id = $2,
			owner_user_id = $3,
			claimed_at = NOW(),
			expires_at = NULL,
			updated_at = NOW()
		WHERE id = $1 AND tenant_id IS NULL`,
		site.ID, orgID, userID,
	)
	if err != nil {
		return failClaim(connect.NewError(connect.CodeInternal, fmt.Errorf("failed to claim site: %w", err)))
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return failClaim(connect.NewError(connect.CodeInternal, fmt.Errorf("failed to verify site claim: %w", err)))
	}
	if changed != 1 {
		return failClaim(connect.NewError(connect.CodeFailedPrecondition, errors.New("site was already claimed")))
	}
	if err := tx.Commit(); err != nil {
		// COMMIT errors can be outcome-ambiguous. Re-read the authoritative
		// row: if this org owns it, the permanent manifest is already correct;
		// otherwise restore the current DB projection before returning failure.
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimRecoveryTimeout)
		defer cancel()
		current, loadErr := s.loadSiteBySlug(recoveryCtx, slug)
		if loadErr == nil && current.TenantID != nil && *current.TenantID == orgID {
			// Treat an acknowledged-late commit as success.
		} else {
			commitErr := connect.NewError(connect.CodeInternal, fmt.Errorf("failed to commit site claim: %w", err))
			if manifestProjectionDirty {
				fallback := site
				if loadErr == nil {
					fallback = current
				}
				if restoreErr := s.restoreAuthoritativeClaimManifest(recoveryCtx, fallback); restoreErr != nil {
					return connect.NewError(connect.CodeInternal, errors.Join(commitErr, restoreErr))
				}
			}
			return commitErr
		}
	}

	if s.purger != nil {
		if err := s.purger.PurgeSlug(ctx, slug); err != nil {
			slog.Warn("hosting: cache purge failed", "slug", slug, "error", err)
		}
	}
	return nil
}

func (s *Server) repairOwnedClaimProjection(ctx context.Context, site *siteRow) error {
	var projectionErr error
	if site.CurrentVersion != nil {
		if err := s.rewriteManifest(ctx, site); err != nil {
			projectionErr = fmt.Errorf("rewrite owned site manifest: %w", err)
		}
	}
	var purgeErr error
	if s.purger != nil {
		if err := s.purger.PurgeSlug(ctx, site.Slug); err != nil {
			purgeErr = fmt.Errorf("purge owned site manifest: %w", err)
		}
	}
	return errors.Join(projectionErr, purgeErr)
}

func (s *Server) retireClaimToken(ctx context.Context, slug, orgID, userID string) error {
	retireCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, err := s.db.ExecContext(retireCtx, `
		UPDATE sites
		SET claim_token_hash = NULL, updated_at = NOW()
		WHERE slug = $1 AND tenant_id = $2 AND owner_user_id = $3`, slug, orgID, userID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("claimed site ownership changed before token retirement")
	}
	return nil
}

func (s *Server) restoreAuthoritativeClaimManifest(ctx context.Context, fallback *siteRow) error {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimRecoveryTimeout)
	defer cancel()

	authoritative, err := s.loadSiteBySlug(recoveryCtx, fallback.Slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			projectionErr := s.store.Delete(recoveryCtx, s.cfg.Bucket, hosting.ManifestKey(fallback.Slug))
			if projectionErr != nil {
				projectionErr = fmt.Errorf("remove orphaned claimed-site manifest: %w", projectionErr)
			}
			return errors.Join(projectionErr, s.purgeClaimRecovery(recoveryCtx, fallback.Slug))
		} else {
			// The fallback is the state read while holding the slug lock. Use it
			// when a transient re-read fails so a DB-anonymous site never keeps a
			// permanent manifest merely because compensation was needed.
			authoritative = fallback
		}
	}
	var projectionErr error
	if authoritative.CurrentVersion == nil {
		if err := s.store.Delete(recoveryCtx, s.cfg.Bucket, hosting.ManifestKey(authoritative.Slug)); err != nil {
			projectionErr = fmt.Errorf("remove unfinished claimed-site manifest: %w", err)
		}
	} else if err := s.rewriteManifest(recoveryCtx, authoritative); err != nil {
		projectionErr = fmt.Errorf("restore authoritative site manifest after failed claim: %w", err)
	}
	return errors.Join(projectionErr, s.purgeClaimRecovery(recoveryCtx, authoritative.Slug))
}

func (s *Server) purgeClaimRecovery(ctx context.Context, slug string) error {
	if s.purger == nil {
		return nil
	}
	if err := s.purger.PurgeSlug(ctx, slug); err != nil {
		return fmt.Errorf("purge edge cache after failed claim recovery: %w", err)
	}
	return nil
}

func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	at := strings.Index(email, "@")
	if len(email) < 3 || len(email) > 254 || at <= 0 || at == len(email)-1 || strings.ContainsAny(email, " \t\n") {
		return "", errors.New("valid email is required")
	}
	return email, nil
}

func newNumericCode(digits int) (string, error) {
	max := big.NewInt(1)
	for i := 0; i < digits; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}
