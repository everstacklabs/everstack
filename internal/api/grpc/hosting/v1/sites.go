package v1

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/everstacklabs/everstack/internal/hosting"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
)

const (
	presignExpiry = 15 * time.Minute
	maxFiles      = 2000
	maxFileBytes  = int64(100 << 20) // 100 MiB per file
	maxTotalBytes = int64(250 << 20) // 250 MiB per site
	anonSiteTTL   = 24 * time.Hour
	deleteTimeout = 30 * time.Second

	uniqueViolation = "23505"
)

var errSiteDeletedDuringActivation = errors.New("site was deleted while the publish was pending")

type siteRow struct {
	ID                   string     `db:"id"`
	Slug                 string     `db:"slug"`
	TenantID             *string    `db:"tenant_id"`
	OwnerUserID          *string    `db:"owner_user_id"`
	Status               string     `db:"status"`
	SPAFallback          bool       `db:"spa_fallback"`
	Access               string     `db:"access"`
	CurrentVersion       *int32     `db:"current_version"`
	ManifestKey          *string    `db:"manifest_key"`
	TotalBytes           int64      `db:"total_bytes"`
	FileCount            int32      `db:"file_count"`
	ClaimTokenHash       *string    `db:"claim_token_hash"`
	ClaimedAt            *time.Time `db:"claimed_at"`
	KillSwitch           bool       `db:"kill_switch"`
	ModerationGeneration int64      `db:"moderation_generation"`
	ExpiresAt            *time.Time `db:"expires_at"`
	CreatedAt            time.Time  `db:"created_at"`
	LastPublishedAt      *time.Time `db:"last_published_at"`
}

type siteFileRow struct {
	Path        string  `db:"path"`
	R2Key       string  `db:"r2_key"`
	ContentType string  `db:"content_type"`
	SizeBytes   int64   `db:"size_bytes"`
	SHA256      *string `db:"sha256"`
}

const siteColumns = `id, slug, tenant_id, owner_user_id, status, spa_fallback, access,
	current_version, manifest_key, total_bytes, file_count, claim_token_hash,
	claimed_at, kill_switch, moderation_generation, expires_at, created_at, last_published_at`

// ─── PublishSite ────────────────────────────────────────────────────────

func (s *Server) PublishSite(ctx context.Context, req *connect.Request[hostingpb.PublishSiteRequest]) (*connect.Response[hostingpb.PublishSiteResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	tenantID := s.tenantID(ctx)
	ip := clientIP(s, req)
	if tenantID == "" && !s.cfg.AllowAnonymous {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("anonymous publishing is disabled; authenticate with an API key"))
	}
	if tenantID == "" && (!s.publishLimiter.Allow(ip) || !s.globalPublish.Allow()) {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("anonymous publish rate limit exceeded; retry shortly or authenticate with an API key"))
	}

	files, totalBytes, err := validateManifest(req.Msg.GetFiles())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	access := accessToString(req.Msg.GetAccess())

	finalizeToken, err := hosting.NewToken()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to generate token"))
	}

	slug, version, err := s.resolveAndRegister(ctx, tenantID, ip, req.Msg.GetSlug(), access, req.Msg.GetSpaFallback(), files, totalBytes, hashToken(finalizeToken))
	if err != nil {
		return nil, err
	}

	uploads := make([]*hostingpb.FileUpload, 0, len(files))
	for _, f := range files {
		key := hosting.ObjectKey(slug, version, f.path)
		url, headers, err := s.store.PutPresignedURL(ctx, s.cfg.Bucket, key, f.contentType, f.sizeBytes, presignExpiry)
		if err != nil {
			publishErr := fmt.Errorf("failed to presign %s: %w", f.path, err)
			if cleanupErr := s.cleanupFailedPublish(ctx, slug, version); cleanupErr != nil {
				publishErr = errors.Join(publishErr, fmt.Errorf("release failed publish reservation: %w", cleanupErr))
			}
			return nil, connect.NewError(connect.CodeInternal, publishErr)
		}
		uploads = append(uploads, &hostingpb.FileUpload{Path: f.path, UploadUrl: url, Headers: headers})
	}

	return connect.NewResponse(&hostingpb.PublishSiteResponse{
		Slug:          slug,
		Version:       version,
		Uploads:       uploads,
		FinalizeToken: finalizeToken,
	}), nil
}

type manifestFile struct {
	path        string
	sizeBytes   int64
	contentType string
	sha256      string
}

// resolveAndRegister resolves the target site for a publish (explicit slug:
// new, or a republish of a site the caller's tenant owns; anonymous callers
// can never target an existing slug — the claim flow is the upgrade path)
// and registers the pending version. Retries absorb generated-slug
// collisions and concurrent version races. Returns connect errors.
func (s *Server) resolveAndRegister(ctx context.Context, tenantID, ip, requestedSlug, access string, spa bool, files []manifestFile, totalBytes int64, finalizeTokenHash string) (string, int32, error) {
	slug := strings.ToLower(strings.TrimSpace(requestedSlug))
	var site *siteRow
	if slug != "" {
		if !hosting.ValidSlug(slug) {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid or reserved slug"))
		}
		existing, err := s.loadSiteBySlug(ctx, slug)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", 0, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to look up slug: %w", err))
		}
		if existing != nil {
			if existing.Status == "deleted" || existing.Status == "deleting" {
				return "", 0, connect.NewError(connect.CodeAlreadyExists, errors.New("slug is retained after deletion and cannot be reused"))
			}
			if tenantID == "" || existing.TenantID == nil || *existing.TenantID != tenantID {
				return "", 0, connect.NewError(connect.CodeAlreadyExists, errors.New("slug already in use"))
			}
			site = existing
		}
	}

	var version int32
	var err error
	for attempt := 0; ; attempt++ {
		if site == nil && slug == "" {
			slug, err = hosting.GenerateSlug()
			if err != nil {
				return "", 0, connect.NewError(connect.CodeInternal, errors.New("failed to generate slug"))
			}
			if hosting.IsReservedSlug(slug) {
				continue
			}
		}
		version, err = s.insertPublish(ctx, site, slug, tenantID, ip, access, spa, files, totalBytes, finalizeTokenHash)
		if err == nil {
			return slug, version, nil
		}
		var quotaExceeded *hosting.QuotaExceededError
		if errors.As(err, &quotaExceeded) {
			return "", 0, quotaConnectError(err)
		}
		if isUniqueViolation(err) && attempt < 5 {
			if site == nil && requestedSlug == "" {
				slug = "" // regenerate
			}
			continue
		}
		if isUniqueViolation(err) {
			return "", 0, connect.NewError(connect.CodeAlreadyExists, errors.New("slug already in use"))
		}
		return "", 0, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to register publish: %w", err))
	}
}

func validateManifest(entries []*hostingpb.FileManifestEntry) ([]manifestFile, int64, error) {
	if len(entries) == 0 {
		return nil, 0, errors.New("files is required")
	}
	if len(entries) > maxFiles {
		return nil, 0, fmt.Errorf("too many files (max %d)", maxFiles)
	}

	seen := make(map[string]struct{}, len(entries))
	files := make([]manifestFile, 0, len(entries))
	var total int64
	for _, e := range entries {
		p, err := normalizeSitePath(e.GetPath())
		if err != nil {
			return nil, 0, err
		}
		if _, dup := seen[p]; dup {
			return nil, 0, fmt.Errorf("duplicate path %q", p)
		}
		seen[p] = struct{}{}

		size := e.GetSizeBytes()
		if size <= 0 {
			return nil, 0, fmt.Errorf("size_bytes required for %q", p)
		}
		if size > maxFileBytes {
			return nil, 0, fmt.Errorf("%q exceeds the per-file limit (%d bytes)", p, maxFileBytes)
		}
		total += size
		if total > maxTotalBytes {
			return nil, 0, fmt.Errorf("site exceeds the total size limit (%d bytes)", maxTotalBytes)
		}

		sum := strings.ToLower(strings.TrimSpace(e.GetSha256()))
		if sum != "" && len(sum) != 64 {
			return nil, 0, fmt.Errorf("invalid sha256 for %q", p)
		}

		files = append(files, manifestFile{
			path:        p,
			sizeBytes:   size,
			contentType: contentTypeFor(p, e.GetContentType()),
			sha256:      sum,
		})
	}
	return files, total, nil
}

// normalizeSitePath cleans a site-relative path and rejects anything that
// could escape the site prefix in R2 or smuggle odd keys.
func normalizeSitePath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", errors.New("file path is required")
	}
	if strings.Contains(p, "\\") || strings.Contains(p, "//") {
		return "", fmt.Errorf("invalid path %q", raw)
	}
	cleaned := path.Clean(p)
	if cleaned != p || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("invalid path %q", raw)
	}
	if len(cleaned) > 1024 {
		return "", fmt.Errorf("path too long: %q", raw)
	}
	return cleaned, nil
}

func contentTypeFor(p, provided string) string {
	if provided != "" {
		return provided
	}
	if byExt := mime.TypeByExtension(filepath.Ext(p)); byExt != "" {
		return byExt
	}
	return "application/octet-stream"
}

// insertPublish creates (or reuses) the site row and registers the pending
// version + files in one transaction. Returns the new version number.
func (s *Server) insertPublish(ctx context.Context, site *siteRow, slug, tenantID, ip, access string, spa bool, files []manifestFile, totalBytes int64, finalizeTokenHash string) (int32, error) {
	quota, quotaEnabled, err := s.resolveTenantQuota(ctx, tenantID)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if quotaEnabled {
		requested := hosting.TenantUsage{StorageBytes: totalBytes}
		if site == nil {
			requested.Sites = 1
		}
		if err := enforceTenantQuotaTx(ctx, tx, tenantID, quota, requested); err != nil {
			return 0, err
		}
	}

	var siteID string
	var version int32
	if site == nil {
		var tenant, owner any
		if tenantID != "" {
			tenant = tenantID
			if uid := contextkeys.GetUserID(ctx); uid != "" {
				owner = uid
			}
		}
		err = tx.QueryRowContext(ctx, `
			INSERT INTO sites (slug, tenant_id, owner_user_id, spa_fallback, access, created_ip)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::inet)
			RETURNING id`,
			slug, tenant, owner, spa, access, ip,
		).Scan(&siteID)
		if err != nil {
			return 0, err
		}
		version = 1
	} else {
		// A republish must NOT touch the live site's settings; the intended
		// spa/access ride on the pending version and are applied only when
		// this version is finalized (activateVersion). An abandoned publish
		// therefore leaves the live site unchanged.
		siteID = site.ID
		err = tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(version), 0) + 1 FROM site_versions WHERE site_id = $1`,
			siteID,
		).Scan(&version)
		if err != nil {
			return 0, err
		}
	}

	var versionID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO site_versions (site_id, version, status, spa_fallback, access, total_bytes, file_count, finalize_token_hash, created_ip)
		VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, NULLIF($8, '')::inet)
		RETURNING id`,
		siteID, version, spa, access, totalBytes, len(files), finalizeTokenHash, ip,
	).Scan(&versionID)
	if err != nil {
		return 0, err
	}

	for _, f := range files {
		var sum any
		if f.sha256 != "" {
			sum = f.sha256
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO site_files (version_id, path, r2_key, content_type, size_bytes, sha256)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			versionID, f.path, hosting.ObjectKey(slug, version, f.path), f.contentType, f.sizeBytes, sum,
		); err != nil {
			return 0, err
		}
	}

	return version, tx.Commit()
}

// ─── FinalizeSite ───────────────────────────────────────────────────────

func (s *Server) FinalizeSite(ctx context.Context, req *connect.Request[hostingpb.FinalizeSiteRequest]) (*connect.Response[hostingpb.FinalizeSiteResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(req.Msg.GetSlug()))
	version := req.Msg.GetVersion()
	if slug == "" || version <= 0 || req.Msg.GetFinalizeToken() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("slug, version and finalize_token are required"))
	}

	tenantID := s.tenantID(ctx)
	if tenantID == "" && (!s.publishLimiter.Allow(clientIP(s, req)) || !s.globalPublish.Allow()) {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit exceeded"))
	}

	site, err := s.loadSiteBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("site not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load site: %w", err))
	}
	if site.Status == "deleted" || site.Status == "deleting" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("site not found"))
	}

	// The finalize token authenticates the publish itself. For claimed
	// sites, additionally require the owning tenant so a leaked token
	// cannot mutate an owned site.
	if site.TenantID != nil && (tenantID == "" || *site.TenantID != tenantID) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("not the site owner"))
	}

	var versionID, tokenHash, status string
	err = s.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(finalize_token_hash, ''), status FROM site_versions WHERE site_id = $1 AND version = $2`,
		site.ID, version,
	).Scan(&versionID, &tokenHash, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("version not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load version: %w", err))
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(req.Msg.GetFinalizeToken())), []byte(tokenHash)) != 1 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid finalize token"))
	}
	if status == "finalized" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("version already finalized"))
	}

	var files []siteFileRow
	if err := s.db.SelectContext(ctx, &files,
		`SELECT path, r2_key, content_type, size_bytes, sha256 FROM site_files WHERE version_id = $1 ORDER BY path`,
		versionID,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load files: %w", err))
	}

	// Verify every registered object actually landed in R2 before the
	// pointer swap. Bounded retry absorbs eventual-consistency lag.
	var missing []string
	for i := range files {
		if err := s.verifyUploaded(ctx, &files[i]); err != nil {
			missing = append(missing, files[i].Path)
			if len(missing) >= 10 {
				break
			}
		}
	}
	if len(missing) > 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("uploads missing or incomplete for: %s (re-upload and retry finalize)", strings.Join(missing, ", ")))
	}

	expiresAt, claimToken, err := s.activateVersion(ctx, site, versionID, version, files)
	if err != nil {
		repairErr := s.repairFailedActivation(ctx, slug)
		if errors.Is(err, errSiteDeletedDuringActivation) {
			// Presigned URLs can remain valid for 15 minutes. Keep this pending
			// reservation counted until the one-hour reaper safely removes any
			// uploads that arrive after deletion.
			if repairErr != nil {
				return nil, connect.NewError(connect.CodeInternal,
					errors.Join(errSiteDeletedDuringActivation, repairErr))
			}
			return nil, connect.NewError(connect.CodeNotFound, errSiteDeletedDuringActivation)
		}
		activationErr := fmt.Errorf("activation failed; retry finalize: %w", err)
		if repairErr != nil {
			activationErr = errors.Join(activationErr,
				fmt.Errorf("repair failed activation projection: %w", repairErr))
		}
		return nil, activationErr
	}

	resp := &hostingpb.FinalizeSiteResponse{
		Slug:    slug,
		Version: version,
		Url:     s.siteURL(slug),
	}
	if expiresAt != nil {
		resp.ExpiresAt = timestamppb.New(*expiresAt)
	}
	if claimToken != "" {
		resp.ClaimUrl = fmt.Sprintf("%s?slug=%s&token=%s", s.cfg.ClaimBaseURL, slug, claimToken)
	}
	return connect.NewResponse(resp), nil
}

// activateVersion makes a fully uploaded version live: writes the manifest
// pointer (single-PUT swap), flips DB state, mints the claim token on the
// first anonymous finalize, and purges the edge cache. Shared by
// FinalizeSite (presigned path) and PublishDirect (in-process path).
//
// A per-slug advisory lock serializes concurrent finalizers and moderation
// projections across replicas. Together with the version and moderation
// generations, it keeps delayed writes from regressing what the edge serves.
// The manifest carries the version's own spa/access settings (stored at
// publish time), so a republish cannot change the live site until it finalizes.
func (s *Server) activateVersion(ctx context.Context, site *siteRow, versionID string, version int32, files []siteFileRow) (*time.Time, string, error) {
	return s.activateVersionTx(ctx, site, versionID, version, files, true)
}

// activateVersionLocked is used while the caller holds the per-slug session
// lock. It must not try to take the same advisory lock from its separate DB
// transaction connection or it would deadlock against its caller.
func (s *Server) activateVersionLocked(ctx context.Context, site *siteRow, versionID string, version int32, files []siteFileRow) (*time.Time, string, error) {
	return s.activateVersionTx(ctx, site, versionID, version, files, false)
}

func (s *Server) activateVersionTx(ctx context.Context, site *siteRow, versionID string, version int32, files []siteFileRow, acquireSlugLock bool) (*time.Time, string, error) {
	slug := site.Slug
	anonymous := site.TenantID == nil

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("finalize failed: %w", err))
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize activation for this site across replicas unless the direct
	// publish path already holds the equivalent session lock.
	if acquireSlugLock {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, slug); err != nil {
			return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("finalize failed: %w", err))
		}
	}

	// Re-read live state under the lock. The version's spa/access are the
	// source of truth for the manifest.
	var curVersion *int32
	var spa bool
	var access string
	var existingExpiry *time.Time
	var existingClaimHash *string
	var desiredStatus string
	var moderationGeneration int64
	if err := tx.QueryRowContext(ctx, `
		SELECT s.current_version, v.spa_fallback, v.access, s.expires_at, s.claim_token_hash,
			s.status, s.moderation_generation
		FROM site_versions v JOIN sites s ON s.id = v.site_id
		WHERE v.id = $1`, versionID,
	).Scan(&curVersion, &spa, &access, &existingExpiry, &existingClaimHash, &desiredStatus, &moderationGeneration); err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("finalize failed: %w", err))
	}
	if desiredStatus == "deleted" || desiredStatus == "deleting" {
		return nil, "", errSiteDeletedDuringActivation
	}

	// Stale finalize: a newer version is already live. Skip the manifest
	// PUT and DB bump so the edge is never regressed; report the current
	// live expiry so the caller still gets a coherent response.
	if curVersion != nil && version <= *curVersion {
		return existingExpiry, "", nil
	}

	var expiresAt *time.Time
	if anonymous {
		if existingExpiry != nil {
			expiresAt = existingExpiry
		} else {
			t := time.Now().UTC().Add(anonSiteTTL)
			expiresAt = &t
		}
	}

	view := *site
	view.SPAFallback = spa
	view.Access = access
	view.Status = desiredStatus
	view.ModerationGeneration = moderationGeneration
	manifest := s.buildManifest(&view, version, files, anonymous, expiresAt)
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, errors.New("failed to encode manifest"))
	}
	manifestKey := hosting.ManifestKey(slug)
	if _, err := s.store.Put(ctx, s.cfg.Bucket, manifestKey, "application/json", strings.NewReader(string(body))); err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("failed to write manifest: %w", err))
	}

	// First finalize of an anonymous site mints its claim token.
	claimToken := ""
	if anonymous && existingClaimHash == nil {
		claimToken, err = hosting.NewToken()
		if err != nil {
			return nil, "", connect.NewError(connect.CodeInternal, errors.New("failed to generate claim token"))
		}
	}

	var totalBytes int64
	for _, f := range files {
		totalBytes += f.SizeBytes
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE site_versions SET status = 'finalized', manifest_key = $2, finalized_at = NOW() WHERE id = $1`,
		versionID, manifestKey,
	); err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("finalize failed: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sites SET
			current_version = $2,
			manifest_key = $3,
			spa_fallback = $4,
			access = $5,
			total_bytes = $6,
			file_count = $7,
			last_published_at = NOW(),
			updated_at = NOW(),
			expires_at = CASE WHEN tenant_id IS NULL THEN $8 ELSE expires_at END,
			claim_token_hash = COALESCE(claim_token_hash, NULLIF($9, ''))
		WHERE id = $1`,
		site.ID, version, manifestKey, spa, access, totalBytes, len(files), expiresAt, hashTokenOrEmpty(claimToken),
	); err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("finalize failed: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("finalize failed: %w", err))
	}

	if err := s.purger.PurgeSlug(ctx, slug); err != nil {
		slog.Warn("hosting: cache purge failed", "slug", slug, "error", err)
	}
	return expiresAt, claimToken, nil
}

func (s *Server) verifyUploaded(ctx context.Context, f *siteFileRow) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
		size, _, err := s.store.Head(ctx, s.cfg.Bucket, f.R2Key)
		if err != nil {
			lastErr = err
			continue
		}
		if size != f.SizeBytes {
			lastErr = fmt.Errorf("size mismatch: uploaded %d, declared %d", size, f.SizeBytes)
			continue
		}
		return nil
	}
	return lastErr
}

func (s *Server) buildManifest(site *siteRow, version int32, files []siteFileRow, anonymous bool, expiresAt *time.Time) hosting.Manifest {
	m := hosting.Manifest{
		SiteID:               site.ID,
		Slug:                 site.Slug,
		Version:              version,
		Status:               site.Status,
		ModerationGeneration: site.ModerationGeneration,
		SPAFallback:          site.SPAFallback,
		Access:               site.Access,
		NoIndex:              anonymous || site.Access == "noindex",
		ExpiresAt:            expiresAt,
		Files:                make(map[string]hosting.ManifestEntry, len(files)),
	}
	if m.Status == "" {
		m.Status = "active"
	}
	if !anonymous {
		m.ExpiresAt = site.ExpiresAt
	}
	for _, f := range files {
		cacheControl := "public, max-age=3600"
		if strings.HasSuffix(f.Path, ".html") || f.Path == "index.html" {
			cacheControl = "public, max-age=60"
		}
		sum := ""
		if f.SHA256 != nil {
			sum = *f.SHA256
		}
		m.Files["/"+f.Path] = hosting.ManifestEntry{
			Key:          f.R2Key,
			ContentType:  f.ContentType,
			SizeBytes:    f.SizeBytes,
			SHA256:       sum,
			CacheControl: cacheControl,
		}
	}
	return m
}

// ─── Authenticated CRUD ─────────────────────────────────────────────────

func (s *Server) GetSite(ctx context.Context, req *connect.Request[hostingpb.GetSiteRequest]) (*connect.Response[hostingpb.GetSiteResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	site, err := s.requireOwnedSite(ctx, req.Msg.GetSlug())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&hostingpb.GetSiteResponse{Site: s.toProto(site)}), nil
}

func (s *Server) ListSites(ctx context.Context, req *connect.Request[hostingpb.ListSitesRequest]) (*connect.Response[hostingpb.ListSitesResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	tenantID := s.tenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	pageSize := int(req.Msg.GetPageSize())
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	offset := 0
	if tok := req.Msg.GetPageToken(); tok != "" {
		if n, err := strconv.Atoi(tok); err == nil && n > 0 {
			offset = n
		}
	}

	var rows []siteRow
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT `+siteColumns+` FROM sites WHERE tenant_id = $1 AND status NOT IN ('deleted', 'deleting') ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		tenantID, pageSize+1, offset,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list sites: %w", err))
	}

	next := ""
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		next = strconv.Itoa(offset + pageSize)
	}
	out := make([]*hostingpb.Site, 0, len(rows))
	for i := range rows {
		out = append(out, s.toProto(&rows[i]))
	}
	return connect.NewResponse(&hostingpb.ListSitesResponse{Sites: out, NextPageToken: next}), nil
}

func (s *Server) UpdateSite(ctx context.Context, req *connect.Request[hostingpb.UpdateSiteRequest]) (*connect.Response[hostingpb.UpdateSiteResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	ownedSite, err := s.requireOwnedSite(ctx, req.Msg.GetSlug())
	if err != nil {
		return nil, err
	}

	var site *siteRow
	manifestRewritten := false
	if err := s.withModerationProjectionLock(ctx, ownedSite.Slug, func(lockCtx context.Context) error {
		// Re-read after acquiring the cross-replica lock. A concurrent delete or
		// moderation decision may have changed the row since the ownership check.
		current, err := s.requireOwnedSite(lockCtx, ownedSite.Slug)
		if err != nil {
			return err
		}
		if req.Msg.SpaFallback != nil {
			current.SPAFallback = req.Msg.GetSpaFallback()
		}
		if req.Msg.Access != nil && req.Msg.GetAccess() != hostingpb.SiteAccess_SITE_ACCESS_UNSPECIFIED {
			current.Access = accessToString(req.Msg.GetAccess())
		}

		if _, err := s.db.ExecContext(lockCtx,
			`UPDATE sites SET spa_fallback = $2, access = $3, updated_at = NOW() WHERE id = $1 AND status NOT IN ('deleted', 'deleting')`,
			current.ID, current.SPAFallback, current.Access,
		); err != nil {
			return fmt.Errorf("update site settings: %w", err)
		}

		// Settings live in the manifest. Serialize this PUT with finalize,
		// claim, delete, and moderation so it cannot regress their projection.
		if current.CurrentVersion != nil {
			if err := s.rewriteManifest(lockCtx, current); err != nil {
				slog.Warn("hosting: manifest rewrite failed", "slug", current.Slug, "error", err)
			} else {
				manifestRewritten = true
			}
		}
		site = current
		return nil
	}); err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update site: %w", err))
	}
	if manifestRewritten {
		if err := s.purger.PurgeSlug(ctx, site.Slug); err != nil {
			slog.Warn("hosting: cache purge failed", "slug", site.Slug, "error", err)
		}
	}

	return connect.NewResponse(&hostingpb.UpdateSiteResponse{Site: s.toProto(site)}), nil
}

func (s *Server) DeleteSite(ctx context.Context, req *connect.Request[hostingpb.DeleteSiteRequest]) (*connect.Response[hostingpb.DeleteSiteResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	site, err := s.requireOwnedSiteForDelete(ctx, req.Msg.GetSlug())
	if err != nil {
		return nil, err
	}
	if err := s.withModerationProjectionLock(ctx, site.Slug, func(lockCtx context.Context) error {
		return s.deleteOwnedSite(lockCtx, site)
	}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete site: %w", err))
	}
	return connect.NewResponse(&hostingpb.DeleteSiteResponse{}), nil
}

func (s *Server) deleteOwnedSite(ctx context.Context, site *siteRow) error {
	// Persist deletion intent before touching R2. Every failure after this point
	// leaves a quota-counted desired non-serving state that the background
	// reconciler and operator takedown path can retry independently.
	continueDeletion, err := s.transitionSiteToDeleting(ctx, site)
	if err != nil {
		return err
	}
	if !continueDeletion {
		return nil
	}
	// Once the durable transition is committed, finish the edge/object work
	// independently of request cancellation. The projection lock remains held
	// until this bounded cleanup attempt returns.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deleteTimeout)
	defer cancel()

	// Project deleting as disabled before removing the serving pointer. If the
	// manifest DELETE fails, the surviving manifest is still non-serving. If
	// this PUT fails, still attempt DELETE; only a failure of both operations can
	// leave the previous manifest until reconciliation or the independent KV
	// takedown path succeeds.
	manifestKey := hosting.ManifestKey(site.Slug)
	var disableProjectionErr error
	if site.CurrentVersion != nil {
		if err := s.rewriteManifest(cleanupCtx, site); err != nil {
			disableProjectionErr = fmt.Errorf("project deleting site as disabled: %w", err)
		} else if s.purger != nil {
			if err := s.purger.PurgeSlug(cleanupCtx, site.Slug); err != nil {
				slog.Warn("hosting: cache purge failed after disabling site for deletion", "slug", site.Slug, "error", err)
			}
		}
	}
	if err := s.store.Delete(cleanupCtx, s.cfg.Bucket, manifestKey); err != nil {
		return errors.Join(disableProjectionErr,
			fmt.Errorf("remove site manifest; deletion queued for retry: %w", err))
	}
	if disableProjectionErr != nil {
		// Manifest removal is the stronger non-serving projection, so a failed
		// preparatory rewrite no longer blocks object cleanup.
		slog.Warn("hosting: disabled manifest projection failed before successful removal", "slug", site.Slug, "error", disableProjectionErr)
	}
	if s.purger != nil {
		if err := s.purger.PurgeSlug(cleanupCtx, site.Slug); err != nil {
			slog.Warn("hosting: cache purge failed during deletion", "slug", site.Slug, "error", err)
		}
	}

	// Content cleanup must finish before the tombstone releases this site's
	// quota. A failed list or object delete leaves the durable deleting row for
	// retry, so bytes that may still exist in R2 remain counted.
	prefix := fmt.Sprintf("sites/%s/", site.Slug)
	objects, err := s.store.List(cleanupCtx, s.cfg.Bucket, prefix)
	if err != nil {
		return fmt.Errorf("list site objects; quota retained for retry: %w", err)
	}
	var objectCleanupErr error
	for _, obj := range objects {
		if obj.Key == manifestKey {
			continue
		}
		if err := s.store.Delete(cleanupCtx, s.cfg.Bucket, obj.Key); err != nil {
			objectCleanupErr = errors.Join(objectCleanupErr, fmt.Errorf("delete %q: %w", obj.Key, err))
		}
	}
	if objectCleanupErr != nil {
		return fmt.Errorf("remove site objects; quota retained for retry: %w", objectCleanupErr)
	}

	tx, err := s.db.BeginTxx(cleanupCtx, nil)
	if err != nil {
		return fmt.Errorf("begin tombstone: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(cleanupCtx, `
		UPDATE sites
		SET status = 'deleted', manifest_key = NULL, claim_token_hash = NULL, updated_at = NOW()
		WHERE id = $1`, site.ID); err != nil {
		return fmt.Errorf("tombstone site: %w", err)
	}
	// Finalized versions old enough that every presigned URL has expired can
	// release quota now. Recent finalized versions and all pending versions
	// remain counted until the one-hour reaper cleans them, preventing a late
	// PUT through a still-valid URL from becoming unaccounted R2 storage.
	if _, err := tx.ExecContext(cleanupCtx, `
		UPDATE site_versions
		SET status = 'deleted'
		WHERE site_id = $1
			AND status = 'finalized'
			AND created_at < NOW() - INTERVAL '1 hour'`, site.ID); err != nil {
		return fmt.Errorf("release deleted finalized-version quota: %w", err)
	}
	if _, err := tx.ExecContext(cleanupCtx, `
		UPDATE site_moderation_actions
		SET status = 'superseded',
			last_error = 'superseded because the site was deleted',
			lease_token = NULL,
			lease_expires_at = NULL,
			updated_at = NOW()
		WHERE site_id = $1 AND status = 'pending'`, site.ID); err != nil {
		return fmt.Errorf("close site moderation actions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tombstone: %w", err)
	}

	return nil
}

// transitionSiteToDeleting atomically advances the moderation generation on
// the first transition and supersedes decisions created for the old active
// generation. A previously applied restore KV record is then older than the
// deleting manifest and cannot make it serve. Retries preserve the generation
// so a takedown queued while cleanup is failing remains independently usable.
func (s *Server) transitionSiteToDeleting(ctx context.Context, site *siteRow) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin site deletion transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Refresh the full projection state after acquiring the slug lock. The
	// caller loaded its row before that lock; a finalize may have activated a
	// first version in between, and deletion must disable that newer manifest.
	var current siteRow
	if err := tx.GetContext(ctx, &current, `
		SELECT `+siteColumns+`
		FROM sites
		WHERE id = $1
		FOR UPDATE`, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lock site deletion transition: %w", err)
	}
	if current.Status == "deleted" {
		return false, nil
	}

	if current.Status != "deleting" {
		if err := tx.QueryRowxContext(ctx, `
			UPDATE sites
			SET status = 'deleting',
				claim_token_hash = NULL,
				moderation_generation = moderation_generation + 1,
				updated_at = NOW()
			WHERE id = $1
			RETURNING moderation_generation`, site.ID).Scan(&current.ModerationGeneration); err != nil {
			return false, fmt.Errorf("mark site deleting: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE site_moderation_actions
			SET status = 'superseded',
				last_error = 'superseded because site deletion started',
				lease_token = NULL,
				lease_expires_at = NULL,
				updated_at = NOW()
			WHERE site_id = $1 AND status = 'pending'`, site.ID); err != nil {
			return false, fmt.Errorf("supersede moderation before deletion: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE sites
		SET claim_token_hash = NULL, updated_at = NOW()
		WHERE id = $1`, site.ID); err != nil {
		return false, fmt.Errorf("refresh queued site deletion: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit site deletion transition: %w", err)
	}
	current.Status = "deleting"
	current.ClaimTokenHash = nil
	*site = current
	return true, nil
}

// ─── shared helpers ─────────────────────────────────────────────────────

func (s *Server) ready() error {
	if s.db == nil || s.store == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("hosting is not configured on this deployment"))
	}
	return nil
}

func (s *Server) loadSiteBySlug(ctx context.Context, slug string) (*siteRow, error) {
	var row siteRow
	if err := s.db.GetContext(ctx, &row,
		`SELECT `+siteColumns+` FROM sites WHERE slug = $1`, slug,
	); err != nil {
		return nil, err
	}
	return &row, nil
}

// requireOwnedSite loads a site and enforces tenant ownership. All
// authenticated CRUD goes through here; anonymous callers are rejected
// before any query runs.
func (s *Server) requireOwnedSite(ctx context.Context, rawSlug string) (*siteRow, error) {
	return s.requireOwnedSiteWithStatus(ctx, rawSlug, false)
}

// requireOwnedSiteForDelete includes a durable deleting row so callers can
// explicitly retry cleanup while every other tenant mutation treats it as
// gone. The background reconciler uses the same idempotent delete path.
func (s *Server) requireOwnedSiteForDelete(ctx context.Context, rawSlug string) (*siteRow, error) {
	return s.requireOwnedSiteWithStatus(ctx, rawSlug, true)
}

func (s *Server) requireOwnedSiteWithStatus(ctx context.Context, rawSlug string, includeDeleting bool) (*siteRow, error) {
	tenantID := s.tenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	slug := strings.ToLower(strings.TrimSpace(rawSlug))
	if slug == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("slug is required"))
	}
	var row siteRow
	statusPredicate := "status NOT IN ('deleted', 'deleting')"
	if includeDeleting {
		statusPredicate = "status <> 'deleted'"
	}
	err := s.db.GetContext(ctx, &row,
		`SELECT `+siteColumns+` FROM sites WHERE slug = $1 AND tenant_id = $2 AND `+statusPredicate, slug, tenantID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("site not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load site: %w", err))
	}
	return &row, nil
}

func (s *Server) rewriteManifest(ctx context.Context, site *siteRow) error {
	return s.rewriteManifestWith(ctx, s.db, site)
}

func (s *Server) rewriteManifestWith(ctx context.Context, queryer sqlx.QueryerContext, site *siteRow) error {
	var files []siteFileRow
	if err := sqlx.SelectContext(ctx, queryer, &files, `
		SELECT f.path, f.r2_key, f.content_type, f.size_bytes, f.sha256
		FROM site_files f
		JOIN site_versions v ON v.id = f.version_id
		WHERE v.site_id = $1 AND v.version = $2`,
		site.ID, *site.CurrentVersion,
	); err != nil {
		return err
	}
	manifest := s.buildManifest(site, *site.CurrentVersion, files, site.TenantID == nil, site.ExpiresAt)
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	_, err = s.store.Put(ctx, s.cfg.Bucket, hosting.ManifestKey(site.Slug), "application/json", strings.NewReader(string(body)))
	return err
}

func (s *Server) toProto(site *siteRow) *hostingpb.Site {
	out := &hostingpb.Site{
		Id:          site.ID,
		Slug:        site.Slug,
		Status:      statusToProto(site),
		Access:      accessToProto(site.Access),
		SpaFallback: site.SPAFallback,
		TotalBytes:  site.TotalBytes,
		FileCount:   site.FileCount,
		Url:         s.siteURL(site.Slug),
		Claimed:     site.TenantID != nil,
		CreatedAt:   timestamppb.New(site.CreatedAt),
	}
	if site.CurrentVersion != nil {
		out.CurrentVersion = *site.CurrentVersion
	}
	if site.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*site.ExpiresAt)
	}
	if site.LastPublishedAt != nil {
		out.LastPublishedAt = timestamppb.New(*site.LastPublishedAt)
	}
	return out
}

func statusToProto(site *siteRow) hostingpb.SiteStatus {
	switch {
	case site.KillSwitch || site.Status == "disabled" || site.Status == "deleting":
		return hostingpb.SiteStatus_SITE_STATUS_DISABLED
	case site.ExpiresAt != nil && site.ExpiresAt.Before(time.Now()):
		return hostingpb.SiteStatus_SITE_STATUS_EXPIRED
	default:
		return hostingpb.SiteStatus_SITE_STATUS_ACTIVE
	}
}

func accessToString(a hostingpb.SiteAccess) string {
	if a == hostingpb.SiteAccess_SITE_ACCESS_NOINDEX {
		return "noindex"
	}
	return "public"
}

func accessToProto(a string) hostingpb.SiteAccess {
	if a == "noindex" {
		return hostingpb.SiteAccess_SITE_ACCESS_NOINDEX
	}
	return hostingpb.SiteAccess_SITE_ACCESS_PUBLIC
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func hashTokenOrEmpty(token string) string {
	if token == "" {
		return ""
	}
	return hashToken(token)
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code) == uniqueViolation
	}
	return strings.Contains(err.Error(), "duplicate key value")
}
