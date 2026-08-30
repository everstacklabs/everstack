// Package authzconnect is the Policy Enforcement Point (PEP): a ConnectRPC
// interceptor that authorizes every RPC against the ReBAC engine (the PDP in
// pkg/authz). It replaces the long-standing commented-out RBAC placeholder in
// the interceptor chain (internal/api/service/registry/registry.go), so authz
// is enforced centrally for every service instead of being re-implemented (or
// forgotten) per handler.
package authzconnect

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/pkg/authz"
)

// Rule declares how to authorize a single RPC procedure.
type Rule struct {
	// Permission required to call the procedure.
	Permission authz.Permission
	// Object extracts the target object from the request message. Returning
	// ok=false marks the call as not resource-scoped (e.g. a list/create at the
	// org root); such calls require authentication but no per-object check
	// unless OrgScoped is set.
	Object func(msg any) (obj authz.Object, ok bool)
}

// Registry maps a full Connect procedure (e.g.
// "/everstack.datasets.v1.DatasetsService/DeleteDataset") to its Rule.
type Registry map[string]Rule

// Mode controls enforcement during rollout.
type Mode int

const (
	// ModeEnforce denies on a failed check (production).
	ModeEnforce Mode = iota
	// ModeShadow logs what it WOULD deny but always allows. Use to validate the
	// registry + tuple backfill against real traffic before enforcing.
	ModeShadow
)

// Logger is the minimal logging surface the interceptor needs (so the core
// package doesn't depend on a concrete logger).
type Logger interface {
	Warnf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Warnf(string, ...any) {}

// Interceptor is the PEP.
type Interceptor struct {
	engine      *authz.Engine
	rules       Registry
	userID      func(ctx context.Context) string
	tenant      func(ctx context.Context) string
	sessionRole func(ctx context.Context) string
	mode        Mode
	requireRule bool
	log         Logger
}

// Options configures the interceptor.
type Options struct {
	// UserID extracts the verified caller id from context (set upstream by the
	// authentication interceptor). Required.
	UserID func(ctx context.Context) string
	// Tenant extracts the verified tenant id from context. Used to scope every
	// engine check to the caller's tenant so the authorization graph cannot leak
	// across tenants. Optional but strongly recommended (the Postgres tuple store
	// fails closed without a tenant).
	Tenant func(ctx context.Context) string
	// SessionRole extracts the caller's org-level role (owner/admin/member/
	// viewer) in the active tenant. When set (with UserID + Tenant), the engine
	// bridges it in as instance membership so instance-local resources resolve
	// without a synced membership graph. Optional.
	SessionRole func(ctx context.Context) string
	// Mode: enforce or shadow. Defaults to ModeShadow (safe) when zero value is
	// explicitly chosen via NewInterceptor's mode arg instead.
	Mode Mode
	// RequireRule: when true, a procedure with no rule is denied (fail closed,
	// for fully-covered services). When false, unmapped procedures pass through
	// (authentication still required upstream) — useful while rolling out.
	RequireRule bool
	// Log receives shadow/deny diagnostics. Optional.
	Log Logger
}

// NewInterceptor builds the PEP.
func NewInterceptor(engine *authz.Engine, rules Registry, opts Options) *Interceptor {
	log := opts.Log
	if log == nil {
		log = nopLogger{}
	}
	return &Interceptor{
		engine:      engine,
		rules:       rules,
		userID:      opts.UserID,
		tenant:      opts.Tenant,
		sessionRole: opts.SessionRole,
		mode:        opts.Mode,
		requireRule: opts.RequireRule,
		log:         log,
	}
}

// scopeCheck returns a context scoped to the caller's tenant and, when the
// session role is known, carrying their session membership so instance-local
// resources resolve via the BridgeStore. An unknown/invalid role is omitted
// (the check then relies on persisted tuples only — fail closed for resources).
func (i *Interceptor) scopeCheck(ctx context.Context, userID string) context.Context {
	tenant := ""
	if i.tenant != nil {
		tenant = i.tenant(ctx)
		ctx = authz.ContextWithTenant(ctx, tenant)
	}
	if i.sessionRole != nil && userID != "" && tenant != "" {
		if role := authz.Role(i.sessionRole(ctx)); role.Valid() {
			ctx = authz.ContextWithSessionMembership(ctx, authz.SessionMembership{
				UserID: userID, Tenant: tenant, Role: role,
			})
		}
	}
	return ctx
}

// WrapUnary implements connect.Interceptor.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		proc := req.Spec().Procedure
		rule, hasRule := i.rules[proc]
		if !hasRule {
			if i.requireRule {
				return nil, connect.NewError(connect.CodePermissionDenied,
					fmt.Errorf("no authorization rule for %s", proc))
			}
			return next(ctx, req) // not yet covered; upstream auth still applies
		}

		userID := ""
		if i.userID != nil {
			userID = i.userID(ctx)
		}
		if userID == "" {
			// Authenticated upstream (the auth interceptors already reject truly
			// anonymous callers) but no user identity resolved — e.g. an API-key or
			// M2M caller scoped to a tenant but not a user. A user-scoped rule
			// cannot be evaluated, so deny when enforcing. In shadow mode we must
			// never block, only record what enforcement would have done.
			if i.mode == ModeShadow {
				i.log.Warnf("authz SHADOW: would deny %s (no user identity in context)", proc)
				return next(ctx, req)
			}
			return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("unauthenticated"))
		}

		obj, scoped := authz.Object{}, false
		if rule.Object != nil {
			obj, scoped = rule.Object(req.Any())
		}
		if !scoped {
			// Not resource-scoped: authentication is enough for this procedure.
			return next(ctx, req)
		}

		checkCtx := i.scopeCheck(ctx, userID)
		allowed, err := i.engine.CheckPermission(checkCtx, userID, rule.Permission, obj)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("authorization check failed: %w", err))
		}
		if !allowed {
			if i.mode == ModeShadow {
				i.log.Warnf("authz SHADOW: would deny %s for user=%s perm=%s obj=%s", proc, userID, rule.Permission, obj)
				return next(ctx, req)
			}
			return nil, connect.NewError(connect.CodePermissionDenied,
				fmt.Errorf("permission %s denied on %s", rule.Permission, obj))
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient passes through (no outbound streaming policy here).
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler authorizes streaming handlers using the same rule lookup,
// but can only enforce at stream-open (no per-message object). Procedures with a
// resource-scoped rule but no object-from-headers are allowed if authenticated.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		proc := conn.Spec().Procedure
		if _, hasRule := i.rules[proc]; !hasRule {
			return next(ctx, conn)
		}
		userID := ""
		if i.userID != nil {
			userID = i.userID(ctx)
		}
		if userID == "" {
			// Same userless-but-authenticated case as WrapUnary: never block in
			// shadow mode, deny only when enforcing.
			if i.mode == ModeShadow {
				i.log.Warnf("authz SHADOW: would deny stream %s (no user identity in context)", proc)
				return next(ctx, conn)
			}
			return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("unauthenticated"))
		}
		return next(ctx, conn)
	}
}

var _ connect.Interceptor = (*Interceptor)(nil)
