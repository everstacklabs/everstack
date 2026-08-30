package registry

import (
	"context"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/authzresource"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/authz"
	"github.com/everstacklabs/everstack/pkg/authz/authzconnect"
	orgv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/org/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/org/v1/orgconnect"
	"github.com/jmoiron/sqlx"
)

// authzResourceTypes are the concrete resource type names registered in the
// authorization model in addition to org/workspace/instance. Extend as more
// resource types get per-resource grants.
func authzResourceTypes() []string {
	return authz.DefaultResourceTypes()
}

// orgObj builds an organization object resolver from a request's
// GetOrganizationId accessor.
func orgObj[T interface{ GetOrganizationId() string }]() func(any) (authz.Object, bool) {
	return func(msg any) (authz.Object, bool) {
		m, ok := msg.(T)
		if !ok || m.GetOrganizationId() == "" {
			return authz.Object{}, false
		}
		return authz.Org(m.GetOrganizationId()), true
	}
}

// wsObj builds a workspace object resolver from a request's GetWorkspaceId.
func wsObj[T interface{ GetWorkspaceId() string }]() func(any) (authz.Object, bool) {
	return func(msg any) (authz.Object, bool) {
		m, ok := msg.(T)
		if !ok || m.GetWorkspaceId() == "" {
			return authz.Object{}, false
		}
		return authz.Workspace(m.GetWorkspaceId()), true
	}
}

// AuthzRegistry maps Connect procedures to (permission, resource) rules for the
// central enforcement point. This is the single, auditable place coverage lives.
//
// The OrganizationService rules below are fully enforceable today: the engine's
// org/workspace tuples are seeded by authz.BackfillFromCloudSchema, so a Check
// resolves against real membership. Resource services (datasets, agents,
// prompts, ...) are added as their per-resource parent/grant tuples start being
// written on create; until then they are intentionally absent (unmapped
// procedures pass through, so reads/writes are unaffected during rollout).
func AuthzRegistry() authzconnect.Registry {
	return authzconnect.Registry{
		// --- Organization management (org owners/admins) ---
		orgconnect.OrganizationServiceUpdateOrganizationProcedure: {
			Permission: authz.PermOrgManageMembers, Object: orgObj[*orgv1.UpdateOrganizationRequest](),
		},
		orgconnect.OrganizationServiceDeleteOrganizationProcedure: {
			Permission: authz.PermOrgDelete, Object: orgObj[*orgv1.DeleteOrganizationRequest](),
		},
		orgconnect.OrganizationServiceInviteMemberProcedure: {
			Permission: authz.PermOrgManageMembers, Object: orgObj[*orgv1.InviteMemberRequest](),
		},
		orgconnect.OrganizationServiceUpdateMemberRoleProcedure: {
			Permission: authz.PermOrgManageMembers, Object: orgObj[*orgv1.UpdateMemberRoleRequest](),
		},
		orgconnect.OrganizationServiceRemoveMemberProcedure: {
			Permission: authz.PermOrgManageMembers, Object: orgObj[*orgv1.RemoveMemberRequest](),
		},
		// --- Workspace management ---
		orgconnect.OrganizationServiceCreateWorkspaceProcedure: {
			Permission: authz.PermOrgManageWorkspaces, Object: orgObj[*orgv1.CreateWorkspaceRequest](),
		},
		orgconnect.OrganizationServiceUpdateWorkspaceProcedure: {
			Permission: authz.PermWorkspaceManage, Object: wsObj[*orgv1.UpdateWorkspaceRequest](),
		},
		orgconnect.OrganizationServiceDeleteWorkspaceProcedure: {
			Permission: authz.PermWorkspaceManage, Object: wsObj[*orgv1.DeleteWorkspaceRequest](),
		},
		orgconnect.OrganizationServiceAddWorkspaceMemberProcedure: {
			Permission: authz.PermWorkspaceManage, Object: wsObj[*orgv1.AddWorkspaceMemberRequest](),
		},
		orgconnect.OrganizationServiceUpdateWorkspaceMemberRoleProcedure: {
			Permission: authz.PermWorkspaceManage, Object: wsObj[*orgv1.UpdateWorkspaceMemberRoleRequest](),
		},
		orgconnect.OrganizationServiceRemoveWorkspaceMemberProcedure: {
			Permission: authz.PermWorkspaceManage, Object: wsObj[*orgv1.RemoveWorkspaceMemberRequest](),
		},
	}
}

type authzLogger struct{}

func (authzLogger) Warnf(format string, args ...any) { logger.Warnf(format, args...) }

// BuildAuthzInterceptor constructs the central ReBAC enforcement point from a DB
// handle and environment flags, or returns nil to leave authorization to the
// existing per-handler checks during rollout.
//
//	EVS_AUTHZ_ENABLED=true  -> build the interceptor (default: disabled/nil)
//	EVS_AUTHZ_ENFORCE=true  -> deny on failed checks (default: shadow/log-only)
//
// The engine is backed by the relation_tuples table; populate it with
// authz.BackfillFromCloudSchema before turning on enforcement.
func BuildAuthzInterceptor(db *sqlx.DB) connect.Interceptor {
	if v := os.Getenv("EVS_AUTHZ_ENABLED"); v != "true" && v != "1" {
		return nil
	}
	if db == nil {
		logger.Warn("authz: EVS_AUTHZ_ENABLED set but no DB available; enforcement point disabled")
		return nil
	}

	store := authz.NewPostgresStore(db, "relation_tuples")
	// Resource lifecycle recorders write parent tuples on create/delete through
	// the same store, so newly-created resources are immediately resolvable.
	authzresource.SetStore(store)
	// The engine reads through a BridgeStore so a caller's session role (set by
	// the PEP) is treated as instance membership — instance-local resources
	// resolve without a synced org/workspace membership graph. The backfill below
	// writes to the raw store. With no session membership in context the bridge is
	// a pass-through.
	engine := authz.NewEngine(authz.NewBridgeStore(store), authz.EverstackSchema().WithResourceTypes(authzResourceTypes()...))

	// Self-populate the tuple store from existing org/workspace memberships so
	// the engine reflects current access without a separate manual backfill job.
	// Idempotent (ON CONFLICT DO NOTHING), best-effort, off the startup path.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		stats, err := authz.BackfillFromCloudSchema(ctx, db, store)
		if err != nil {
			logger.WithError(err).Warn("authz: tuple backfill failed; engine will still serve already-written tuples")
			return
		}
		logger.WithFields(
			"org_memberships", stats.OrgMemberships,
			"workspace_parents", stats.WorkspaceParents,
			"workspace_memberships", stats.WorkspaceMemberships,
			"instance_parents", stats.InstanceParents,
		).Info("authz: tuple backfill complete")
	}()

	mode := authzconnect.ModeShadow
	if v := os.Getenv("EVS_AUTHZ_ENFORCE"); v == "true" || v == "1" {
		mode = authzconnect.ModeEnforce
	}
	logger.WithFields("enforce", mode == authzconnect.ModeEnforce).
		Info("authz: central ReBAC enforcement point enabled")

	return authzconnect.NewInterceptor(engine, AuthzRegistry(), authzconnect.Options{
		UserID: func(ctx context.Context) string {
			if uid := contextkeys.GetUserID(ctx); uid != "" {
				return uid
			}
			return contextkeys.CloudUserIDFromContext(ctx)
		},
		Tenant:      func(ctx context.Context) string { return contextkeys.GetTenantID(ctx) },
		SessionRole: func(ctx context.Context) string { return contextkeys.GetUserRole(ctx) },
		Mode:        mode,
		RequireRule: false, // unmapped procedures pass through during rollout
		Log:         authzLogger{},
	})
}
