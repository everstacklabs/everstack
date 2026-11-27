package v1

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	api_keycmd "github.com/everstacklabs/everstack/internal/commands/handlers/api_key"
	"github.com/everstacklabs/everstack/internal/cqrs"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	apikey "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	api_keypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1"
)

var (
	svcAcctPrefix = apikeylib.PlatformKeyPrefix + "svcacct-"
	usrAcctPrefix = apikeylib.PlatformKeyPrefix + "usracct-"
	apiVersion    = "api01"
)

func (s *Server) CreateApiKey(ctx context.Context, req *connect.Request[api_keypb.CreateApiKeyRequest]) (*connect.Response[api_keypb.CreateApiKeyResponse], error) {
	key, err := s.createKey(ctx, req.Msg.GetName(), req.Msg.GetType(), req.Msg.GetUserId(), nil)
	if err != nil {
		return nil, err
	}
	plaintext := key.Plaintext
	resp := &api_keypb.CreateApiKeyResponse{ApiKey: &api_keypb.ApiKey{Id: key.ID, Name: req.Msg.GetName(), Type: req.Msg.GetType()}}
	// Return one-time plaintext in body; DB stores only hash
	resp.ApiKey.Hash = plaintext
	out := connect.NewResponse(resp)
	out.Header().Set("Cache-Control", "no-store")
	out.Header().Set("Pragma", "no-cache")
	return out, nil
}

func (s *Server) DeleteApiKey(ctx context.Context, req *connect.Request[api_keypb.DeleteApiKeyRequest]) (*connect.Response[api_keypb.DeleteApiKeyResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	orgID := contextkeys.GetTenantID(ctx)
	if orgID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	res, err := sys.QueryBus.Execute(ctx, apikey.NewGetApiKeyByIDQuery(req.Msg.GetId(), orgID, ""))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if qr, ok := res.(*query.Response); ok {
		res = qr.Data
	}
	if rm, ok := res.(apikey.APIKeyReadModel); ok && rm.Surfaces != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("this key belongs to a bot; remove the bot instead"))
	}
	if err := s.revokeKey(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&api_keypb.DeleteApiKeyResponse{Success: true, Message: "revocation dispatched"}), nil
}

func (s *Server) GetApiKey(ctx context.Context, req *connect.Request[api_keypb.GetApiKeyRequest]) (*connect.Response[api_keypb.GetApiKeyResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	orgID := contextkeys.GetTenantID(ctx)
	if orgID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	q := apikey.NewGetApiKeyByIDQuery(req.Msg.GetId(), orgID, "")
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	qr, ok := res.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid query response"))
	}
	if qr.Data == nil {
		return connect.NewResponse(&api_keypb.GetApiKeyResponse{}), nil
	}
	rm, ok := qr.Data.(apikey.APIKeyReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}
	sensitiveID := ""
	if rm.SensitiveID != "" {
		sensitiveID = rm.SensitiveID
	}
	pb := &api_keypb.ApiKey{Id: rm.ID, Name: rm.Name, Hash: rm.Hash, Type: api_keypb.ApiKeyType(api_keypb.ApiKeyType_value[rm.Type]), SensitiveId: sensitiveID, CreatedAt: utils.ParseTimestamp(rm.CreatedAt), UpdatedAt: utils.ParseTimestamp(rm.UpdatedAt)}
	return connect.NewResponse(&api_keypb.GetApiKeyResponse{ApiKey: pb}), nil
}

func (g *GrpcServer) GetApiKey(ctx context.Context, req *api_keypb.GetApiKeyRequest) (*api_keypb.GetApiKeyResponse, error) {
	cReq := &connect.Request[api_keypb.GetApiKeyRequest]{Msg: req}
	resp, err := g.base.GetApiKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (s *Server) ListApiKeys(ctx context.Context, req *connect.Request[api_keypb.ListApiKeysRequest]) (*connect.Response[api_keypb.ListApiKeysResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	// Convert the request parameters
	userID := req.Msg.GetUserId()
	// org id comes from context only. The previous code fell back to
	// req.Msg.GetOrgId() when context was empty, which let any caller
	// list another tenant's API keys by setting `org_id` in the request
	// body — the same body-trust pattern that produced the 2026-05-06
	// cross-tenant leak. EVS_ORG_ID survives as a self-hosted single-tenant
	// override only when context is empty.
	orgID := contextkeys.GetTenantID(ctx)
	if orgID == "" {
		orgID = os.Getenv("EVS_ORG_ID")
	}
	if orgID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	// Instance-level isolation: each managed instance only sees its own keys
	instanceID := os.Getenv("EVS_INSTANCE_ID")
	var typeStr string
	if req.Msg.GetType() != api_keypb.ApiKeyType_API_KEY_TYPE_UNSPECIFIED {
		typeStr = req.Msg.GetType().String()
	}

	q := apikey.NewListApiKeysQuery(userID, orgID, instanceID, typeStr, "")
	q.ExcludeSurfaceLimited = true
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Handle the response
	var apiKeys []*api_keypb.ApiKey
	if res != nil {
		// Check if it's a query.Response wrapper
		if qr, ok := res.(*query.Response); ok {
			if qr.Data != nil {
				if apiKeyList, ok := qr.Data.([]apikey.APIKeyReadModel); ok {
					for _, rm := range apiKeyList {
						if rm.Surfaces != nil {
							continue
						}
						sensitiveID := ""
						if rm.SensitiveID != "" {
							sensitiveID = rm.SensitiveID
						}
						pb := &api_keypb.ApiKey{
							Id:          rm.ID,
							Name:        rm.Name,
							Hash:        rm.Hash,
							Type:        api_keypb.ApiKeyType(api_keypb.ApiKeyType_value[rm.Type]),
							SensitiveId: sensitiveID,
							CreatedAt:   utils.ParseTimestamp(rm.CreatedAt),
							UpdatedAt:   utils.ParseTimestamp(rm.UpdatedAt),
						}
						apiKeys = append(apiKeys, pb)
					}
				}
			}
		} else if apiKeyList, ok := res.([]apikey.APIKeyReadModel); ok {
			// Direct list (fallback)
			for _, rm := range apiKeyList {
				if rm.Surfaces != nil {
					continue
				}
				sensitiveID := ""
				if rm.SensitiveID != "" {
					sensitiveID = rm.SensitiveID
				}
				pb := &api_keypb.ApiKey{
					Id:          rm.ID,
					Name:        rm.Name,
					Hash:        rm.Hash,
					Type:        api_keypb.ApiKeyType(api_keypb.ApiKeyType_value[rm.Type]),
					SensitiveId: sensitiveID,
					CreatedAt:   utils.ParseTimestamp(rm.CreatedAt),
					UpdatedAt:   utils.ParseTimestamp(rm.UpdatedAt),
				}
				apiKeys = append(apiKeys, pb)
			}
		}
	}

	return connect.NewResponse(&api_keypb.ListApiKeysResponse{ApiKeys: apiKeys}), nil
}

func (s *Server) RegenerateApiKey(ctx context.Context, req *connect.Request[api_keypb.RegenerateApiKeyRequest]) (*connect.Response[api_keypb.RegenerateApiKeyResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("RegenerateApiKey not implemented"))
}

func (s *Server) UpdateApiKey(ctx context.Context, req *connect.Request[api_keypb.UpdateApiKeyRequest]) (*connect.Response[api_keypb.UpdateApiKeyResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("UpdateApiKey not implemented"))
}

// keyRegistrationTimeout bounds each billing call on the key paths.
const keyRegistrationTimeout = 3 * time.Second

// keyRegistrationRetryBackoff is the pause before the single retry of a
// billing call that failed transiently. A variable so tests can shorten it.
var keyRegistrationRetryBackoff = 250 * time.Millisecond

// registerWithBilling reports a created key to billing, synchronously so a
// spend limit set right after creation finds it. The tenant is the one this
// handler just stored on the key, from the authenticated context, never from
// the request body. A transient failure is retried once; a failure that
// remains does not undo the key: it is logged, and billing then refuses a
// limit on the key until it has made a request (billing's traffic fallback),
// so ownership is never assumed.
func (s *Server) registerWithBilling(ctx context.Context, tenantID, keyID string) {
	if s == nil || s.registrar == nil {
		return
	}
	if err := callBillingWithRetry(ctx, func(c context.Context) error {
		return s.registrar.RegisterAPIKey(c, tenantID, keyID)
	}); err != nil {
		logger.WithFields("tenant_id", tenantID, "api_key_id", keyID, "error", err.Error()).
			Warn("api key: registration with billing failed; a spend limit on this key is refused until it makes a request")
	}
}

// unregisterWithBilling tells billing a deleted key is gone so its ownership
// record is removed. The tenant is the authenticated caller's, the same one
// the revocation was scoped to; billing removes only the row owned by the
// organization it resolves to. Best effort: a failure is logged and the
// delete still succeeds. A stale row is harmless, because a key's spend
// limit only ever applies to traffic from the key's own organization.
func (s *Server) unregisterWithBilling(ctx context.Context, tenantID, keyID string) {
	if s == nil || s.registrar == nil || tenantID == "" || keyID == "" {
		return
	}
	if err := callBillingWithRetry(ctx, func(c context.Context) error {
		return s.registrar.UnregisterAPIKey(c, tenantID, keyID)
	}); err != nil {
		logger.WithFields("tenant_id", tenantID, "api_key_id", keyID, "error", err.Error()).
			Warn("api key: unregistering the deleted key with billing failed; its ownership record stays")
	}
}

// callBillingWithRetry runs call with a bounded deadline, detached from the
// caller's cancellation, and retries it once after a short backoff when it
// failed transiently. A permanent refusal (bad input, permission, conflict,
// unknown tenant) is returned without a retry.
func callBillingWithRetry(ctx context.Context, call func(context.Context) error) error {
	base := context.WithoutCancel(ctx)
	attempt := func() error {
		c, cancel := context.WithTimeout(base, keyRegistrationTimeout)
		defer cancel()
		return call(c)
	}
	err := attempt()
	if err == nil || !billingErrorTransient(err) {
		return err
	}
	time.Sleep(keyRegistrationRetryBackoff)
	return attempt()
}

// billingErrorTransient reports whether a billing call failure may succeed
// on a retry: billing unreachable, overloaded or timed out, or an internal
// failure on its side. Every code that is a verdict on the request itself
// (InvalidArgument, PermissionDenied, Unauthenticated, AlreadyExists,
// FailedPrecondition, NotFound, Unimplemented, ...) is permanent.
func billingErrorTransient(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeUnavailable,
		connect.CodeDeadlineExceeded,
		connect.CodeResourceExhausted,
		connect.CodeAborted,
		connect.CodeInternal,
		connect.CodeUnknown:
		return true
	}
	return false
}

// createdKey is the one-time credential returned by the shared creation path.
type createdKey struct{ ID, Plaintext, Hint string }

// createKey is shared by the public RPC and the trusted bot service. Surfaces
// never come from a public protobuf request.
func (s *Server) createKey(ctx context.Context, name string, keyType api_keypb.ApiKeyType, userID string, surfaces []string) (createdKey, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return createdKey{}, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	orgID := contextkeys.GetTenantID(ctx)
	if orgID == "" {
		orgID = os.Getenv("EVS_ORG_ID")
	}
	if orgID == "" {
		return createdKey{}, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return createdKey{}, err
	}
	acct := svcAcctPrefix
	if keyType == api_keypb.ApiKeyType_API_KEY_TYPE_USER {
		acct = usrAcctPrefix
	}
	plaintext := fmt.Sprintf("%s%s-%s", acct, apiVersion, base64.RawURLEncoding.EncodeToString(buf))
	cmd := api_keycmd.NewCreateApiKeyCommand(name, keyType.String(), plaintext, userID, orgID, os.Getenv("EVS_INSTANCE_ID"))
	cmd.Surfaces = surfaces
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		// A dispatch can fail after persistence. Revoke any partially created key.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		cleanupErr := s.revokeKey(cleanup, cmd.ID)
		return createdKey{}, errors.Join(err, cleanupErr)
	}
	s.registerWithBilling(ctx, orgID, cmd.ID)
	return createdKey{ID: cmd.ID, Plaintext: plaintext, Hint: utils.MaskApiKey(plaintext)}, nil
}

// CreateBotKey is trusted server-side code, not a public RPC.
func (s *Server) CreateBotKey(ctx context.Context, name string, surfaces []string) (id, plaintext, hint string, err error) {
	if contextkeys.GetTenantID(ctx) == "" || surfaces == nil {
		return "", "", "", errors.New("bot tenant and surfaces required")
	}
	key, err := s.createKey(ctx, "bot:"+name, api_keypb.ApiKeyType_API_KEY_TYPE_ORG, contextkeys.GetAuthenticatedUserID(ctx), surfaces)
	return key.ID, key.Plaintext, key.Hint, err
}

func (s *Server) RevokeBotKey(ctx context.Context, id string) error { return s.revokeKey(ctx, id) }

func (s *Server) revokeKey(ctx context.Context, id string) error {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	orgID := contextkeys.GetTenantID(ctx)
	if orgID == "" {
		return connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	if err := sys.CommandBus.Dispatch(ctx, api_keycmd.NewRevokeApiKeyCommand(id, orgID, "", "")); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	s.unregisterWithBilling(ctx, orgID, id)
	return nil
}
