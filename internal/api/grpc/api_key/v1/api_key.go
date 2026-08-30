package v1

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"connectrpc.com/connect"
	api_keycmd "github.com/everstacklabs/everstack/internal/commands/handlers/api_key"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	apikey "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	api_keypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1"
)

var (
	svcAcctPrefix = "sk-mf-svcacct-"
	usrAcctPrefix = "sk-mf-usracct-"
	apiVersion    = "api01"
)

func (s *Server) CreateApiKey(ctx context.Context, req *connect.Request[api_keypb.CreateApiKeyRequest]) (*connect.Response[api_keypb.CreateApiKeyResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	// Generate a random plaintext key
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	raw := base64.RawURLEncoding.EncodeToString(buf)
	// Dynamic prefix
	acct := svcAcctPrefix
	if req.Msg.GetType() == api_keypb.ApiKeyType_API_KEY_TYPE_USER {
		acct = usrAcctPrefix
	}
	prefix := fmt.Sprintf("%s%s-", acct, apiVersion)
	plaintext := prefix + raw

	// Resolve org_id and instance_id for tenant isolation. Context is
	// the trust anchor; EVS_ORG_ID is the self-hosted single-tenant
	// override that ListApiKeys/DeleteApiKey already document. If both
	// are empty the request is unauthenticated against any tenant —
	// fail closed rather than minting a key that lands in an
	// unattributable row.
	orgID := contextkeys.GetTenantID(ctx)
	if orgID == "" {
		orgID = os.Getenv("EVS_ORG_ID")
	}
	if orgID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	instanceID := os.Getenv("EVS_INSTANCE_ID")

	cmd := api_keycmd.NewCreateApiKeyCommand(req.Msg.GetName(), req.Msg.GetType().String(), plaintext, req.Msg.GetUserId(), orgID, instanceID)
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &api_keypb.CreateApiKeyResponse{ApiKey: &api_keypb.ApiKey{Id: cmd.ID, Name: req.Msg.GetName(), Type: req.Msg.GetType()}}
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
	cmd := api_keycmd.NewRevokeApiKeyCommand(req.Msg.GetId(), orgID, "", "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
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
