package registry

import (
	"connectrpc.com/connect"
	grpcserver "github.com/everstacklabs/everstack/internal/api/grpc/server"
	"github.com/everstacklabs/everstack/internal/api/grpc/server/middleware"
	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/database"
	licensemonitor "github.com/everstacklabs/everstack/internal/services/license_monitor"
	"github.com/jmoiron/sqlx"
)

type Registry struct {
	DBConn          *database.Conn
	Writer          database.Writer
	LE              *httpmw.LicenseEnforcer
	LP              *apilic.Policy
	Monitor         *licensemonitor.Monitor // License monitor for spend limit enforcement
	cliDeviceTokens *deviceauth.TokenManager
	cliAuthDB       *sqlx.DB

	// Managed reports whether this gateway serves other people's tenants. Its
	// only consumer is LocalTenantInterceptor, which must inject nothing on a
	// managed gateway: a single local tenant id is meaningless there, and any
	// request that reaches a handler without one would otherwise be attributed
	// to it. Named for what it gates rather than for SharedDB, because a
	// gateway can be managed without a pre-opened shared pool — see
	// isManagedGateway in cmd/serve.
	Managed bool

	// Authz is the central ReBAC enforcement point. It is nil unless authz is
	// enabled at startup, and runs after authentication has populated the
	// caller's user and tenant context.
	Authz connect.Interceptor
}

// New returns a registry with the default license policy. Authentication is
// the API-key/session interceptor built in BuildInterceptors; there is no
// separate M2M gate. The former M2M interceptor only checked that credential
// headers were present, and its service match never fired because
// MethodPrefix() has no leading slash (EVS-324 item 15). Procedures that
// billing and license call on the gateway authenticate their own payloads.
func New() *Registry {
	return &Registry{
		LP: apilic.NewDefaultPolicy(),
	}
}

// SetDatabase wires a database connection and writer into the registry for later use.
func (r *Registry) SetDatabase(conn *database.Conn, writer database.Writer) {
	r.DBConn = conn
	r.Writer = writer
}

// SetLicense attaches a shared license enforcer and policy.
func (r *Registry) SetLicense(enforcer *httpmw.LicenseEnforcer, policy *apilic.Policy) {
	r.LE = enforcer
	if policy != nil {
		r.LP = policy
	}
}

// SetLicenseMonitor attaches a license monitor for spend limit enforcement.
func (r *Registry) SetLicenseMonitor(monitor *licensemonitor.Monitor) {
	r.Monitor = monitor
}

// SetManagedMode records whether this gateway serves other people's tenants.
// See Registry.Managed.
func (r *Registry) SetManagedMode(managed bool) {
	r.Managed = managed
}

// SetCLIDeviceTokenManager enables signed human CLI sessions on the bounded
// management RPC surface enforced by APIKeyConnectInterceptor.
func (r *Registry) SetCLIDeviceTokenManager(manager *deviceauth.TokenManager) {
	r.cliDeviceTokens = manager
}

// SetCLIAuthorizationDB wires the platform database used to authorize human
// CLI sessions on managed gateways. In single-database deployments this can be
// the same pool as DBConn.RW.
func (r *Registry) SetCLIAuthorizationDB(db *sqlx.DB) {
	r.cliAuthDB = db
}

// BuildInterceptors returns the interceptor chain for a service based on policy.
// Chain order: apiKey → localTenant → license → spend_limit → callDuration → activity → validation → llmActivity → authz
func (r *Registry) BuildInterceptors(service grpcserver.ConnectServer) []connect.Interceptor {
	// Use DB-backed session interceptor for self-hosted (validates session cookies
	// directly against the sessions table), or standard policy-only interceptor
	var apiKeyInterceptor *middleware.APIKeyConnectInterceptor
	if r.DBConn != nil && r.DBConn.RW != nil {
		apiKeyInterceptor = middleware.NewAPIKeyInterceptorWithSessionDB(false, r.LP, r.DBConn.RW)
	} else {
		apiKeyInterceptor = middleware.NewAPIKeyInterceptorWithPolicy(false, r.LP)
	}
	if r.cliDeviceTokens != nil {
		apiKeyInterceptor.SetCLIDeviceTokenManager(r.cliDeviceTokens)
	}
	if r.cliAuthDB != nil {
		apiKeyInterceptor.SetCLIAuthorizationDB(r.cliAuthDB)
	}
	db := dbFromConn(r.DBConn)
	base := []connect.Interceptor{
		apiKeyInterceptor,
		middleware.NewLocalTenantInterceptor(db, r.Managed),
		middleware.NewLicenseConnectInterceptor(r.LE, r.LP),
	}

	// Add spend limit interceptor if monitor is available
	if r.Monitor != nil {
		base = append(base, middleware.NewSpendLimitInterceptor(r.Monitor, r.LP))
	}

	base = append(base,
		middleware.CallDurationHandler(),
		middleware.ActivityInterceptor(),
		middleware.ValidationHandler(),
		middleware.LLMActivityInterceptor(),
	)

	if r.Authz != nil {
		base = append(base, r.Authz)
	}

	return base
}

// SetAuthzInterceptor wires the central authorization interceptor. Passing nil
// leaves the existing per-handler authorization behavior unchanged.
func (r *Registry) SetAuthzInterceptor(i connect.Interceptor) {
	r.Authz = i
}

func dbFromConn(conn *database.Conn) *sqlx.DB {
	if conn == nil {
		return nil
	}
	return conn.RW
}
