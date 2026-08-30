package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/gorilla/mux"
	"google.golang.org/protobuf/types/known/timestamppb"

	sandboxpkg "github.com/everstacklabs/everstack/internal/sandbox"
	sandboxcp "github.com/everstacklabs/everstack/internal/sandbox/controlplane"
	sshpkg "github.com/everstacklabs/everstack/internal/ssh"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// SetSSHKeyStore sets the SSH key store for key CRUD operations.
// This can work independently of the SSH proxy.
func (s *Server) SetSSHKeyStore(keyStore *sshpkg.KeyStore) {
	s.sshKeyStore = keyStore
}

// SetSSHProxy sets the SSH proxy and connection info on the server.
func (s *Server) SetSSHProxy(proxy *sshpkg.Proxy, host string, port int) {
	s.sshProxy = proxy
	fingerprint := ""
	if proxy != nil {
		fingerprint = proxy.Fingerprint()
	}
	s.SetSSHEndpoint(host, port, fingerprint)
}

// SetSSHEndpoint sets the SSH endpoint advertised to clients. This is used by
// horizontally-scaled HTTP gateway pods that do not own a local SSH listener.
func (s *Server) SetSSHEndpoint(host string, port int, fingerprint string) {
	s.sshEndpointConfigured = host != "" && port > 0
	s.sshHost = host
	s.sshPort = port
	s.sshHostKeyFingerprint = fingerprint
}

// SetRegion records the region slug this gateway pod serves so SSH info
// responses can surface it. Empty string for region-unaware deployments.
func (s *Server) SetRegion(region string) {
	s.region = region
}

// AddSSHKey uploads a user's SSH public key.
func (s *Server) AddSSHKey(ctx context.Context, req *connect.Request[agentspb.AddSSHKeyRequest]) (*connect.Response[agentspb.AddSSHKeyResponse], error) {
	if s.sshKeyStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrSSHNotConfigured)
	}

	msg := req.Msg
	if msg.Name == "" || msg.PublicKey == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name and public_key are required"))
	}

	tenantID, err := s.resolveTenantID(ctx, msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	userID := s.resolveUserID(ctx)

	key, err := s.sshKeyStore.AddKey(ctx, userID, tenantID, msg.Name, msg.PublicKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.AddSSHKeyResponse{
		Key: sshKeyToProto(key),
	}), nil
}

// ListSSHKeys lists all SSH keys for the tenant.
// Keys are listed tenant-wide so that admins and the UI can see all keys
// regardless of which auth method (API key vs session) was used to add them.
func (s *Server) ListSSHKeys(ctx context.Context, req *connect.Request[agentspb.ListSSHKeysRequest]) (*connect.Response[agentspb.ListSSHKeysResponse], error) {
	if s.sshKeyStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrSSHNotConfigured)
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	keys, err := s.sshKeyStore.ListKeysByTenant(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var pbKeys []*agentspb.SSHKey
	for _, k := range keys {
		pbKeys = append(pbKeys, sshKeyToProto(&k))
	}

	return connect.NewResponse(&agentspb.ListSSHKeysResponse{
		Keys: pbKeys,
	}), nil
}

// DeleteSSHKey removes an SSH key.
// Deletion is tenant-scoped so that keys added via CLI (different user ID)
// can be managed from the admin UI.
func (s *Server) DeleteSSHKey(ctx context.Context, req *connect.Request[agentspb.DeleteSSHKeyRequest]) (*connect.Response[agentspb.DeleteSSHKeyResponse], error) {
	if s.sshKeyStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrSSHNotConfigured)
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	if err := s.sshKeyStore.DeleteKeyByTenant(ctx, req.Msg.KeyId, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.DeleteSSHKeyResponse{
		Success: true,
	}), nil
}

// GrantSandboxSSHAccess grants a user SSH access to a sandbox.
func (s *Server) GrantSandboxSSHAccess(ctx context.Context, req *connect.Request[agentspb.GrantSandboxSSHAccessRequest]) (*connect.Response[agentspb.GrantSandboxSSHAccessResponse], error) {
	if s.sshKeyStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrSSHNotConfigured)
	}

	msg := req.Msg
	if msg.SandboxId == "" || msg.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("sandbox_id and user_id are required"))
	}

	tenantID, err := s.resolveTenantID(ctx, msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	grantedBy := s.resolveUserID(ctx)

	if err := s.sshKeyStore.GrantAccess(ctx, msg.SandboxId, msg.UserId, tenantID, grantedBy); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.GrantSandboxSSHAccessResponse{
		Success: true,
	}), nil
}

// RevokeSandboxSSHAccess revokes SSH access.
func (s *Server) RevokeSandboxSSHAccess(ctx context.Context, req *connect.Request[agentspb.RevokeSandboxSSHAccessRequest]) (*connect.Response[agentspb.RevokeSandboxSSHAccessResponse], error) {
	if s.sshKeyStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrSSHNotConfigured)
	}

	msg := req.Msg
	tenantID, err := s.resolveTenantID(ctx, msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	if err := s.sshKeyStore.RevokeAccess(ctx, msg.SandboxId, msg.UserId, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.RevokeSandboxSSHAccessResponse{
		Success: true,
	}), nil
}

// GetSandboxSSHInfo returns SSH connection info for a sandbox.
func (s *Server) GetSandboxSSHInfo(ctx context.Context, req *connect.Request[agentspb.GetSandboxSSHInfoRequest]) (*connect.Response[agentspb.GetSandboxSSHInfoResponse], error) {
	identifier := req.Msg.SandboxId
	if identifier == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrSandboxIDRequired)
	}

	scope, err := s.resolveSandboxTenantInstanceScope(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	resolved := s.resolveSandboxInfoInScope(ctx, identifier, scope)
	if !resolved.found {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox %q not found", identifier))
	}

	base := &agentspb.GetSandboxSSHInfoResponse{
		Enabled:        false,
		SandboxId:      resolved.sandboxID,
		SessionId:      resolved.sessionID,
		Name:           resolved.name,
		Status:         resolved.status,
		LifecycleState: resolved.lifecycleState,
	}
	if !s.sshEndpointConfigured {
		base.DisabledReason = "SSH proxy is not configured"
		return connect.NewResponse(base), nil
	}
	if !resolved.running {
		base.DisabledReason = "Sandbox is not running"
		return connect.NewResponse(base), nil
	}
	if s.sshKeyStore != nil {
		userID := s.resolveUserID(ctx)
		keys, err := s.sshKeyStore.ListKeys(ctx, userID, resolved.tenantID)
		if err == nil && len(keys) == 0 {
			base.DisabledReason = "No SSH key is configured for this user"
			return connect.NewResponse(base), nil
		}
		if userID != "" {
			hasAccess, err := s.sshKeyStore.CheckAccess(ctx, resolved.sandboxID, userID, resolved.tenantID)
			if err == nil && !hasAccess {
				base.DisabledReason = "SSH access has not been granted for this sandbox"
				return connect.NewResponse(base), nil
			}
		}
	}

	return connect.NewResponse(&agentspb.GetSandboxSSHInfoResponse{
		Enabled:          true,
		Host:             s.sshHost,
		Port:             int32(s.sshPort),
		ConnectionString: formatSSHConnectionString(resolved.user, s.sshHost, s.sshPort),
		HostFingerprint:  s.sshHostKeyFingerprint,
		SandboxId:        resolved.sandboxID,
		SessionId:        resolved.sessionID,
		Name:             resolved.name,
		Status:           resolved.status,
		LifecycleState:   resolved.lifecycleState,
		Region:           s.region,
		ShortCode:        resolved.user,
	}), nil
}

// CreateSandboxSSHToken creates a short-lived SSH bearer token for a sandbox.
func (s *Server) CreateSandboxSSHToken(ctx context.Context, req *connect.Request[agentspb.CreateSandboxSSHTokenRequest]) (*connect.Response[agentspb.CreateSandboxSSHTokenResponse], error) {
	if s.sshKeyStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrSSHNotConfigured)
	}
	if !s.sshEndpointConfigured {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("SSH proxy is not configured"))
	}

	msg := req.Msg
	if msg.SandboxId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrSandboxIDRequired)
	}
	scope, err := s.resolveSandboxTenantInstanceScope(ctx, msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	resolved := s.resolveSandboxInfoInScope(ctx, msg.SandboxId, scope)
	if !resolved.found {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox %q not found", msg.SandboxId))
	}
	if !resolved.running {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("sandbox is not running"))
	}
	createdBy := s.resolveUserID(ctx)
	created, err := sandboxcp.NewSSHService(s.sshKeyStore).CreateToken(ctx, sandboxcp.CreateSSHTokenRequest{
		Scope:            scope.WithSandbox(resolved.sandboxID),
		CreatedBy:        createdBy,
		SSHUser:          resolved.user,
		SSHHost:          s.sshHost,
		SSHPort:          s.sshPort,
		ExpiresInMinutes: msg.ExpiresInMinutes,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentspb.CreateSandboxSSHTokenResponse{
		Token:            sshTokenToProto(created.Token),
		RawToken:         created.RawToken,
		ConnectionString: created.ConnectionString,
	}), nil
}

// ListSandboxSSHTokens lists active temporary SSH tokens for a sandbox.
func (s *Server) ListSandboxSSHTokens(ctx context.Context, req *connect.Request[agentspb.ListSandboxSSHTokensRequest]) (*connect.Response[agentspb.ListSandboxSSHTokensResponse], error) {
	if s.sshKeyStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrSSHNotConfigured)
	}
	msg := req.Msg
	if msg.SandboxId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrSandboxIDRequired)
	}
	scope, err := s.resolveSandboxTenantInstanceScope(ctx, msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	resolved := s.resolveSandboxInfoInScope(ctx, msg.SandboxId, scope)
	if !resolved.found {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox %q not found", msg.SandboxId))
	}
	tokens, err := sandboxcp.NewSSHService(s.sshKeyStore).ListTokens(ctx, sandboxcp.ListSSHTokensRequest{
		Scope:       scope.WithSandbox(resolved.sandboxID),
		RequestedBy: s.resolveUserID(ctx),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	pbTokens := make([]*agentspb.SandboxSSHToken, 0, len(tokens))
	for i := range tokens {
		pbTokens = append(pbTokens, sshTokenToProto(&tokens[i]))
	}
	return connect.NewResponse(&agentspb.ListSandboxSSHTokensResponse{Tokens: pbTokens}), nil
}

// RevokeSandboxSSHToken revokes a temporary SSH token for a sandbox.
func (s *Server) RevokeSandboxSSHToken(ctx context.Context, req *connect.Request[agentspb.RevokeSandboxSSHTokenRequest]) (*connect.Response[agentspb.RevokeSandboxSSHTokenResponse], error) {
	if s.sshKeyStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrSSHNotConfigured)
	}
	msg := req.Msg
	if msg.SandboxId == "" || msg.TokenId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("sandbox_id and token_id are required"))
	}
	scope, err := s.resolveSandboxTenantInstanceScope(ctx, msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	resolved := s.resolveSandboxInfoInScope(ctx, msg.SandboxId, scope)
	if !resolved.found {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox %q not found", msg.SandboxId))
	}
	if err := sandboxcp.NewSSHService(s.sshKeyStore).RevokeToken(ctx, sandboxcp.RevokeSSHTokenRequest{
		Scope:       scope.WithSandbox(resolved.sandboxID),
		TokenID:     msg.TokenId,
		RequestedBy: s.resolveUserID(ctx),
	}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentspb.RevokeSandboxSSHTokenResponse{Success: true}), nil
}

// ─── Helpers ────────────────────────────────────────────────────────────

func sshKeyToProto(k *sshpkg.UserSSHKey) *agentspb.SSHKey {
	pb := &agentspb.SSHKey{
		Id:          k.ID,
		Name:        k.Name,
		Fingerprint: k.Fingerprint,
		KeyType:     k.KeyType,
	}
	if k.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, k.CreatedAt); err == nil {
			pb.CreatedAt = timestamppb.New(t)
		}
	}
	if k.LastUsedAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *k.LastUsedAt); err == nil {
			pb.LastUsedAt = timestamppb.New(t)
		}
	}
	return pb
}

func sshTokenToProto(t *sshpkg.SandboxSSHToken) *agentspb.SandboxSSHToken {
	if t == nil {
		return nil
	}
	pb := &agentspb.SandboxSSHToken{
		Id:             t.ID,
		SandboxId:      t.SandboxID,
		TenantId:       t.TenantID,
		TokenPrefix:    t.TokenPrefix,
		CreatedBy:      t.CreatedBy,
		CreatedAt:      timestamppb.New(t.CreatedAt),
		ExpiresAt:      timestamppb.New(t.ExpiresAt),
		OrganizationId: t.OrganizationID,
		InstanceId:     t.InstanceID,
	}
	if t.RevokedAt.Valid {
		pb.RevokedAt = timestamppb.New(t.RevokedAt.Time)
	}
	if t.LastUsedAt.Valid {
		pb.LastUsedAt = timestamppb.New(t.LastUsedAt.Time)
	}
	if t.LastUsedIP.Valid {
		pb.LastUsedIp = t.LastUsedIP.String
	}
	return pb
}

func formatSSHConnectionString(user, host string, port int) string {
	if port == 22 {
		return fmt.Sprintf("ssh %s@%s", user, host)
	}
	return fmt.Sprintf("ssh %s@%s -p %d", user, host, port)
}

// HandleSandboxSSHInfoHTTP handles GET /v1/sandbox/instances/{sandbox_id}/ssh-info.
// Registered directly on the gorilla mux to bypass API key middleware.
func (s *Server) HandleSandboxSSHInfoHTTP(w http.ResponseWriter, r *http.Request) {
	identifier := mux.Vars(r)["sandbox_id"]
	if identifier == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "sandbox_id is required"})
		return
	}

	if !s.sshEndpointConfigured {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"enabled":         false,
			"disabled_reason": "SSH proxy is not configured",
		})
		return
	}

	resolved := s.resolveSandboxInfo(r.Context(), identifier)
	if !resolved.found {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("sandbox %q not found", identifier)})
		return
	}

	resp := map[string]interface{}{
		"enabled":          resolved.running,
		"disabled_reason":  "",
		"host":             s.sshHost,
		"port":             s.sshPort,
		"host_fingerprint": s.sshHostKeyFingerprint,
		"region":           s.region,
		"short_code":       resolved.user,
	}
	if resolved.running {
		resp["connection_string"] = formatSSHConnectionString(resolved.user, s.sshHost, s.sshPort)
	} else {
		resp["disabled_reason"] = "Sandbox is not running"
	}
	if resolved.sessionID != "" {
		resp["session_id"] = resolved.sessionID
	}
	if resolved.sandboxID != "" {
		resp["sandbox_id"] = resolved.sandboxID
	}
	if resolved.name != "" {
		resp["name"] = resolved.name
	}
	if resolved.image != "" {
		resp["image"] = resolved.image
	}
	if resolved.status != "" {
		resp["status"] = resolved.status
	}
	if resolved.lifecycleState != "" {
		resp["lifecycle_state"] = resolved.lifecycleState
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// resolveSSHUser returns a stable SSH username for the given sandbox identifier.
// It performs a best-effort lookup through sandbox manager, but bounds the wait
// so SSH-info endpoints can't hang behind long-held sandbox manager write locks.
// On lookup timeout/cancellation, it falls back to the caller-provided identifier.
func (s *Server) resolveSSHUser(ctx context.Context, identifier string) (string, bool) {
	if s.sandboxMgr == nil {
		return identifier, true
	}

	type lookupResult struct {
		user  string
		found bool
	}
	resultCh := make(chan lookupResult, 1)
	go func() {
		inst, ok := s.sandboxMgr.GetBySandboxIDOrName(identifier)
		if !ok || inst == nil {
			// DB fallback so post-pod-restart cache misses don't
			// degrade the SSH-info card to "unavailable".
			dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if dbInst, dbErr := s.sandboxMgr.LookupInstanceByIDFromDB(dbCtx, identifier); dbErr == nil && dbInst != nil {
				inst = dbInst
				ok = true
			}
		}
		if !ok || inst == nil {
			resultCh <- lookupResult{}
			return
		}
		// Prefer the public short_code (bitly-style, stable, ASCII-safe)
		// for the SSH username so the displayed connection string matches
		// the URL/SSH model: `ssh <short_code>@ssh.evs.run`. Fall back to
		// id/name only for legacy rows that predate the column.
		user := inst.ShortCode
		if user == "" {
			user = inst.ID
		}
		if user == "" {
			user = identifier
		}
		resultCh <- lookupResult{user: user, found: true}
	}()

	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()

	select {
	case res := <-resultCh:
		return res.user, res.found
	case <-ctx.Done():
		return identifier, true
	case <-timer.C:
		return identifier, true
	}
}

// sandboxResolved holds resolved sandbox metadata.
type sandboxResolved struct {
	user           string
	sessionID      string
	sandboxID      string
	name           string
	image          string
	status         string
	lifecycleState string
	tenantID       string
	instanceID     string
	running        bool
	found          bool
}

// resolveSandboxInfo returns sandbox metadata for the given identifier.
// Uses the same bounded-wait pattern as resolveSSHUser.
//
// Lookup order: in-memory cache first, then DB fallback. Without the
// DB fallback, every existing sandbox 404s on its SSH-info card right
// after a gateway pod restart — the in-memory map is empty until the
// restoreInstances loop finishes, and restore can take tens of
// seconds against a busy fcagent.
func (s *Server) resolveSandboxInfo(ctx context.Context, identifier string) sandboxResolved {
	if s.sandboxMgr == nil {
		return sandboxResolved{user: identifier, found: true}
	}

	resultCh := make(chan sandboxResolved, 1)
	go func() {
		inst, ok := s.sandboxMgr.GetBySandboxIDOrName(identifier)
		if !ok || inst == nil {
			// Cache miss → DB fallback. LookupInstanceByIDFromDB
			// matches id, name, OR short_code in a single index hit,
			// so it covers every identifier shape the FE might send.
			dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if dbInst, dbErr := s.sandboxMgr.LookupInstanceByIDFromDB(dbCtx, identifier); dbErr == nil && dbInst != nil {
				inst = dbInst
				ok = true
			}
		}
		if !ok || inst == nil {
			resultCh <- sandboxResolved{}
			return
		}
		// Same precedence as resolveSSHUser: short_code wins so the
		// displayed `ssh user@host` line matches the bitly-style identifier
		// the rest of the product surfaces.
		user := inst.ShortCode
		if user == "" {
			user = inst.ID
		}
		if user == "" {
			user = identifier
		}
		instanceID := inst.InstanceID
		if instanceID == "" {
			instanceID = inst.Config.TenantID
		}
		resultCh <- sandboxResolved{
			user:           user,
			sessionID:      inst.Config.SessionID,
			sandboxID:      inst.ID,
			name:           inst.Name,
			image:          inst.Config.Image,
			status:         string(inst.Status),
			lifecycleState: sandboxpkg.PublicLifecycleState(inst.LifecycleState, inst.Status),
			tenantID:       inst.Config.TenantID,
			instanceID:     instanceID,
			running:        sandboxInstanceIsRunning(inst),
			found:          true,
		}
	}()

	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()

	select {
	case res := <-resultCh:
		return res
	case <-ctx.Done():
		return sandboxResolved{user: identifier, sandboxID: identifier, running: true, found: true}
	case <-timer.C:
		return sandboxResolved{user: identifier, sandboxID: identifier, running: true, found: true}
	}
}

// resolveSandboxInfoInScope resolves sandbox metadata without ever falling back
// to an unscoped sandbox_id/name/short_code lookup. Public authenticated paths
// should use this helper so identifier collisions cannot cross tenant/instance
// boundaries.
func (s *Server) resolveSandboxInfoInScope(ctx context.Context, identifier string, scope sandboxpkg.TenantInstanceScope) sandboxResolved {
	if s.sandboxMgr == nil || !scope.HasSandboxTenant() {
		return sandboxResolved{}
	}

	resultCh := make(chan sandboxResolved, 1)
	go func() {
		inst, ok := s.sandboxMgr.GetBySandboxIDOrNameInScope(identifier, scope)
		if !ok || inst == nil {
			dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if dbInst, dbErr := s.sandboxMgr.LookupInstanceByIDFromDBInScope(dbCtx, identifier, scope); dbErr == nil && dbInst != nil {
				inst = dbInst
				ok = true
			}
		}
		if !ok || inst == nil {
			resultCh <- sandboxResolved{}
			return
		}
		resultCh <- sandboxResolvedFromInstance(identifier, inst)
	}()

	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()

	select {
	case res := <-resultCh:
		return res
	case <-ctx.Done():
		return sandboxResolved{}
	case <-timer.C:
		return sandboxResolved{}
	}
}

func sandboxResolvedFromInstance(identifier string, inst *sandboxpkg.Instance) sandboxResolved {
	user := inst.ShortCode
	if user == "" {
		user = inst.ID
	}
	if user == "" {
		user = identifier
	}
	instanceID := inst.InstanceID
	if instanceID == "" {
		instanceID = inst.Config.TenantID
	}
	return sandboxResolved{
		user:           user,
		sessionID:      inst.Config.SessionID,
		sandboxID:      inst.ID,
		name:           inst.Name,
		image:          inst.Config.Image,
		status:         string(inst.Status),
		lifecycleState: sandboxpkg.PublicLifecycleState(inst.LifecycleState, inst.Status),
		tenantID:       inst.Config.TenantID,
		instanceID:     instanceID,
		running:        sandboxInstanceIsRunning(inst),
		found:          true,
	}
}
