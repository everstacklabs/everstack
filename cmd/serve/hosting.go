package serve

// evs.run hosting control plane wiring.
//
// The hosting service itself (internal/api/grpc/hosting/v1) and its supporting
// libraries have been on master for some time, fully implemented and never
// constructed by anything — so SitesService was defined, generated and compiled,
// but no route was ever registered and /v1/sites answered 404 in every
// environment. This file is the missing construction, extracted from the
// evs-run-hosting line.
//
// It lives apart from start_api.go deliberately: the wiring is self-contained,
// start_api.go is already past five thousand lines, and keeping it separate
// makes the hosting surface reviewable on its own.

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	hostingsvc "github.com/everstacklabs/everstack/internal/api/grpc/hosting/v1"
	authdomain "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	authrepo "github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	apikeycmd "github.com/everstacklabs/everstack/internal/commands/handlers/api_key"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	hostinglib "github.com/everstacklabs/everstack/internal/hosting"
	hostingmoderation "github.com/everstacklabs/everstack/internal/hosting/moderation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	s3pkg "github.com/everstacklabs/everstack/internal/storage/s3"
)

// buildHostingServer constructs the evs.run hosting control plane and reports
// whether hosting is actually enabled (EVS_HOSTING_R2_* configured). It is
// built before the inbound MCP provider so publish_site/list_sites can be
// exposed there. When disabled it still returns a server that registers but
// answers Unavailable, so self-hosted deployments are unaffected.
func buildHostingServer(ctx context.Context, initRes *database.InitResult, embeddedDefaults *EmbeddedDefaults) (*hostingsvc.Server, bool) {
	hostingCfg := hostingsvc.Config{
		Bucket:         os.Getenv("EVS_HOSTING_R2_BUCKET"),
		BaseDomain:     os.Getenv("EVS_HOSTING_BASE_DOMAIN"),
		ClaimBaseURL:   os.Getenv("EVS_HOSTING_CLAIM_BASE_URL"),
		AllowAnonymous: os.Getenv("EVS_HOSTING_ALLOW_ANONYMOUS") == "true",
		ProxyToken:     os.Getenv("EVS_HOSTING_PROXY_TOKEN"),
	}
	var hostingDB *sqlx.DB
	if initRes != nil && initRes.Primary != nil {
		hostingDB = initRes.Primary.RW
	}
	endpoint := os.Getenv("EVS_HOSTING_R2_ENDPOINT")
	if endpoint == "" || hostingCfg.Bucket == "" || hostingDB == nil {
		return hostingsvc.CreateServerWithDeps(ctx, nil, nil, hostingCfg), false
	}

	hostingStore, err := s3pkg.New(ctx, s3pkg.Config{
		Endpoint:        endpoint,
		Region:          os.Getenv("EVS_HOSTING_R2_REGION"), // "auto" for R2
		Bucket:          hostingCfg.Bucket,
		AccessKeyID:     os.Getenv("EVS_HOSTING_R2_ACCESS_KEY"),
		SecretAccessKey: os.Getenv("EVS_HOSTING_R2_SECRET_KEY"),
		// Path-style addressing for local MinIO (and any S3 endpoint
		// without wildcard-subdomain buckets). R2 works either way.
		ForcePathStyle: os.Getenv("EVS_HOSTING_R2_FORCE_PATH_STYLE") == "true",
	})
	if err != nil {
		logger.WithError(err).Warn("hosting: failed to construct R2 client; hosting disabled")
		return hostingsvc.CreateServerWithDeps(ctx, nil, nil, hostingCfg), false
	}
	hostingServer := hostingsvc.CreateServerWithDeps(ctx, hostingDB, hostingStore, hostingCfg)

	baseDomain := hostingCfg.BaseDomain
	if baseDomain == "" {
		baseDomain = "evs.run"
	}
	if zoneID, apiToken := os.Getenv("EVS_HOSTING_CF_ZONE_ID"), os.Getenv("EVS_HOSTING_CF_API_TOKEN"); zoneID != "" && apiToken != "" {
		hostingServer.SetPurger(&hostinglib.CloudflarePurger{ZoneID: zoneID, APIToken: apiToken, BaseDomain: baseDomain})
	}

	moderationStore := hostingmoderation.NewPostgresStore(hostingDB)
	accelerator := hostingmoderation.EdgeEnforcer(hostingmoderation.UnavailableEdgeEnforcer{
		Reason: "Cloudflare KV enforcement is not configured",
	})
	edgeConfigured := false
	if accountID, namespaceID, apiToken := os.Getenv("EVS_HOSTING_CF_ACCOUNT_ID"), os.Getenv("EVS_HOSTING_CF_KV_NAMESPACE_ID"), os.Getenv("EVS_HOSTING_CF_KV_API_TOKEN"); accountID != "" && namespaceID != "" && apiToken != "" {
		cloudflareEdge, err := hostingmoderation.NewCloudflareKVEnforcer(hostingmoderation.CloudflareKVConfig{
			AccountID: accountID, NamespaceID: namespaceID, APIToken: apiToken,
		})
		if err != nil {
			logger.WithError(err).Warn("hosting: invalid Cloudflare KV moderation configuration")
		} else {
			accelerator = cloudflareEdge
			edgeConfigured = true
		}
	}
	edgeEnforcer := hostingmoderation.CoordinatedEdgeEnforcer{
		Authoritative: hostingServer.ModerationManifestEnforcer(),
		Accelerator:   accelerator,
	}
	moderationController := hostingmoderation.NewController(moderationStore, edgeEnforcer)
	hostingServer.SetEdgeEnforcementConfigured(edgeConfigured)
	if edgeConfigured {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if attempted, err := moderationController.ReconcilePending(ctx, 50); err != nil {
						logger.WithError(err).Warn("hosting: moderation reconciliation failed")
					} else if attempted > 0 {
						logger.WithFields("attempted", attempted).Info("hosting: reconciled pending moderation actions")
					}
				}
			}
		}()
	}
	go func() {
		reap := func() {
			if completed, err := hostingServer.ReconcileDeletingSites(ctx, 100); err != nil {
				logger.WithError(err).Warn("hosting: site deletion reconciliation failed")
			} else if completed > 0 {
				logger.WithFields("completed", completed).Info("hosting: completed queued site deletions")
			}
			if cleaned, err := hostingServer.ReapStalePending(ctx, 100); err != nil {
				logger.WithError(err).Warn("hosting: stale upload cleanup failed")
			} else if cleaned > 0 {
				logger.WithFields("cleaned", cleaned).Info("hosting: released stale storage reservations")
			}
		}
		reap()
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reap()
			}
		}
	}()

	if embeddedDefaults != nil && embeddedDefaults.EmailCodeSender != nil {
		hostingServer.SetCodeSender(embeddedDefaults.EmailCodeSender)
	}

	// Claim flow: find-or-create the user + org for a verified email,
	// mirroring the self-hosted Register orchestration
	// (internal/auth/selfhosted/service/self_hosted_auth.go).
	hostingUserRepo := authrepo.NewUserRepository(hostingDB)
	hostingOrgRepo := authrepo.NewOrganizationRepository(hostingDB)
	hostingServer.SetOwnerProvisioner(func(ctx context.Context, emailAddr string) (string, string, error) {
		user, err := hostingUserRepo.GetByEmail(ctx, emailAddr)
		if err != nil {
			return "", "", fmt.Errorf("user lookup failed: %w", err)
		}
		if user == nil {
			user = &authdomain.User{
				ID:        uuid.New(),
				Email:     emailAddr,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			if err := hostingUserRepo.Create(ctx, user); err != nil {
				return "", "", fmt.Errorf("user create failed: %w", err)
			}
		}
		orgs, err := hostingOrgRepo.ListByUserID(ctx, user.ID)
		if err != nil {
			return "", "", fmt.Errorf("org lookup failed: %w", err)
		}
		// Reuse only an org this user OWNS. Reusing an org where they are
		// merely a member/viewer would mint a tenant-wide key for an
		// organization they do not control (privilege escalation via email
		// verification). Members with no owned org get a fresh personal org.
		for _, o := range orgs {
			if o.Role == "owner" {
				return user.ID.String(), o.ID.String(), nil
			}
		}
		orgName := emailAddr
		if at := strings.Index(emailAddr, "@"); at > 0 {
			orgName = emailAddr[:at]
		}
		slug, err := hostingOrgRepo.GenerateUniqueSlug(ctx, orgName)
		if err != nil {
			return "", "", fmt.Errorf("slug generation failed: %w", err)
		}
		org, err := hostingOrgRepo.CreateSimple(ctx, slug, orgName)
		if err != nil {
			return "", "", fmt.Errorf("org create failed: %w", err)
		}
		if err := hostingOrgRepo.AddMemberSimple(ctx, org.ID, user.ID, "owner"); err != nil {
			return "", "", fmt.Errorf("membership create failed: %w", err)
		}
		ws := &authdomain.Workspace{
			OrganizationID: org.ID,
			Slug:           "default",
			Name:           "Default",
			Environment:    authdomain.EnvProduction,
		}
		if err := hostingOrgRepo.CreateWorkspace(ctx, ws); err != nil {
			logger.WithError(err).Warn("hosting: default workspace create failed")
		}
		return user.ID.String(), org.ID.String(), nil
	})

	// Key issuance goes through the standard api_key command stack so the
	// minted key validates via the normal interceptor path (full-string
	// HMAC; prefixes are not parsed anywhere).
	hostingServer.SetKeyIssuer(func(ctx context.Context, orgID, name string) (string, error) {
		sys, err := cqrs.GetSystemFromContext(ctx)
		if err != nil {
			return "", fmt.Errorf("CQRS system not available: %w", err)
		}
		buf := make([]byte, 32)
		if _, err := cryptorand.Read(buf); err != nil {
			return "", err
		}
		plaintext := "evs_live_" + base64.RawURLEncoding.EncodeToString(buf)
		cmd := apikeycmd.NewCreateApiKeyCommand(name, "API_KEY_TYPE_SERVICE", plaintext, "", orgID, os.Getenv("EVS_INSTANCE_ID"))
		if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
			return "", err
		}
		return plaintext, nil
	})

	logger.WithFields("bucket", hostingCfg.Bucket, "base_domain", baseDomain).Info("hosting: evs.run control plane enabled")
	return hostingServer, true
}
