package v1

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	promptscmd "github.com/everstacklabs/everstack/internal/commands/handlers/prompts"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	promptsquery "github.com/everstacklabs/everstack/internal/query/handlers/prompts"
	promptspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/prompts/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// getSys retrieves the CQRS system from either request or server context.
func (s *PromptServer) getSys(ctx context.Context) (*cqrs.System, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	return sys, nil
}

// tenantFromContext returns the tenant id set by the auth middleware. The
// prompts API deliberately has no tenant field on any request message — the
// authenticated context is the only accepted source (tenant-isolation rule).
func tenantFromContext(ctx context.Context) (string, error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("missing tenant context"))
	}
	return tenantID, nil
}

func ensureTenantSchema(ctx context.Context, tenantID string) context.Context {
	if database.TenantSchemaFromContext(ctx) == "" && tenantID != "" {
		return database.WithTenantSchema(ctx, tenantID)
	}
	return ctx
}

// unwrapQueryData unwraps a *query.Response into its Data payload.
func unwrapQueryData(res interface{}) interface{} {
	if resp, ok := res.(*query.Response); ok {
		return resp.Data
	}
	return res
}

// getPromptReadModel fetches a prompt by id or name, nil when absent.
func (s *PromptServer) getPromptReadModel(ctx context.Context, sys *cqrs.System, tenantID, id, name string) (*promptsquery.PromptReadModel, error) {
	res, err := sys.QueryBus.Execute(ctx, promptsquery.NewGetPromptQuery(id, name, tenantID))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, nil
	}
	data := unwrapQueryData(res)
	if data == nil {
		return nil, nil
	}
	rm, ok := data.(*promptsquery.PromptReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}
	return rm, nil
}

// ===== Prompt RPCs =====

func (s *PromptServer) CreatePrompt(ctx context.Context, req *connect.Request[promptspb.CreatePromptRequest]) (*connect.Response[promptspb.CreatePromptResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ctx = ensureTenantSchema(ctx, tenantID)
	userID := contextkeys.GetUserID(ctx)

	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	// Surface duplicate names synchronously — the projection is async, so a
	// constraint violation there would be invisible to the caller.
	existing, err := s.getPromptReadModel(ctx, sys, tenantID, "", req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("a prompt with this name already exists"))
	}

	var config map[string]interface{}
	if req.Msg.GetConfig() != nil {
		config = req.Msg.GetConfig().AsMap()
	}

	cmd := promptscmd.NewCreatePromptCommand(
		tenantID,
		req.Msg.GetName(),
		req.Msg.GetDescription(),
		userID,
		"",
		req.Msg.GetTags(),
		messagesFromProto(req.Msg.GetMessages()),
		config,
		req.Msg.GetCommitMessage(),
	)
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var latestVersion int32
	if len(cmd.Messages) > 0 {
		latestVersion = 1
	}
	return connect.NewResponse(&promptspb.CreatePromptResponse{
		Prompt: &promptspb.Prompt{
			Id:            cmd.ID,
			TenantId:      tenantID,
			Name:          req.Msg.GetName(),
			Description:   req.Msg.GetDescription(),
			Tags:          req.Msg.GetTags(),
			LatestVersion: latestVersion,
			VersionCount:  latestVersion,
		},
	}), nil
}

func (s *PromptServer) GetPrompt(ctx context.Context, req *connect.Request[promptspb.GetPromptRequest]) (*connect.Response[promptspb.GetPromptResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetId() == "" && req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id or name is required"))
	}

	rm, err := s.getPromptReadModel(ctx, sys, tenantID, req.Msg.GetId(), req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	if rm == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("prompt not found"))
	}
	return connect.NewResponse(&promptspb.GetPromptResponse{Prompt: promptToProto(rm)}), nil
}

func (s *PromptServer) ListPrompts(ctx context.Context, req *connect.Request[promptspb.ListPromptsRequest]) (*connect.Response[promptspb.ListPromptsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}

	q := promptsquery.NewListPromptsQuery(tenantID, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var prompts []*promptspb.Prompt
	if res != nil {
		if list, ok := unwrapQueryData(res).([]promptsquery.PromptReadModel); ok {
			for i := range list {
				prompts = append(prompts, promptToProto(&list[i]))
			}
		}
	}
	return connect.NewResponse(&promptspb.ListPromptsResponse{Prompts: prompts}), nil
}

func (s *PromptServer) UpdatePrompt(ctx context.Context, req *connect.Request[promptspb.UpdatePromptRequest]) (*connect.Response[promptspb.UpdatePromptResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ctx = ensureTenantSchema(ctx, tenantID)
	userID := contextkeys.GetUserID(ctx)

	cmd := promptscmd.NewUpdatePromptCommand(req.Msg.GetId(), tenantID, userID, "")
	if req.Msg.Name != nil {
		cmd.Name = req.Msg.Name
	}
	if req.Msg.Description != nil {
		cmd.Description = req.Msg.Description
	}
	if req.Msg.GetSetTags() {
		tags := req.Msg.GetTags()
		if tags == nil {
			tags = []string{}
		}
		cmd.Tags = &tags
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&promptspb.UpdatePromptResponse{
		Prompt: &promptspb.Prompt{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *PromptServer) DeletePrompt(ctx context.Context, req *connect.Request[promptspb.DeletePromptRequest]) (*connect.Response[promptspb.DeletePromptResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ctx = ensureTenantSchema(ctx, tenantID)
	userID := contextkeys.GetUserID(ctx)

	cmd := promptscmd.NewDeletePromptCommand(req.Msg.GetId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&promptspb.DeletePromptResponse{
		Success: true,
		Message: "prompt deletion dispatched",
	}), nil
}

// ===== Version RPCs =====

func (s *PromptServer) CreatePromptVersion(ctx context.Context, req *connect.Request[promptspb.CreatePromptVersionRequest]) (*connect.Response[promptspb.CreatePromptVersionResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ctx = ensureTenantSchema(ctx, tenantID)
	userID := contextkeys.GetUserID(ctx)

	if len(req.Msg.GetMessages()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("messages are required"))
	}

	prompt, err := s.getPromptReadModel(ctx, sys, tenantID, req.Msg.GetPromptId(), "")
	if err != nil {
		return nil, err
	}
	if prompt == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("prompt not found"))
	}
	nextVersion := int(prompt.LatestVersion) + 1

	var config map[string]interface{}
	if req.Msg.GetConfig() != nil {
		config = req.Msg.GetConfig().AsMap()
	}

	cmd := promptscmd.NewCreatePromptVersionCommand(
		req.Msg.GetPromptId(),
		tenantID,
		userID,
		"",
		nextVersion,
		messagesFromProto(req.Msg.GetMessages()),
		config,
		req.Msg.GetCommitMessage(),
		req.Msg.GetLabels(),
	)
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&promptspb.CreatePromptVersionResponse{
		Version: &promptspb.PromptVersion{
			Id:            cmd.ID,
			PromptId:      req.Msg.GetPromptId(),
			TenantId:      tenantID,
			Version:       int32(nextVersion),
			Messages:      req.Msg.GetMessages(),
			Config:        req.Msg.GetConfig(),
			Labels:        req.Msg.GetLabels(),
			CommitMessage: req.Msg.GetCommitMessage(),
			CreatedBy:     userID,
		},
	}), nil
}

func (s *PromptServer) ListPromptVersions(ctx context.Context, req *connect.Request[promptspb.ListPromptVersionsRequest]) (*connect.Response[promptspb.ListPromptVersionsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}

	q := promptsquery.NewListPromptVersionsQuery(tenantID, req.Msg.GetPromptId(), int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var versions []*promptspb.PromptVersion
	if res != nil {
		if list, ok := unwrapQueryData(res).([]promptsquery.PromptVersionReadModel); ok {
			for i := range list {
				versions = append(versions, promptVersionToProto(&list[i]))
			}
		}
	}
	return connect.NewResponse(&promptspb.ListPromptVersionsResponse{Versions: versions}), nil
}

func (s *PromptServer) GetPromptVersion(ctx context.Context, req *connect.Request[promptspb.GetPromptVersionRequest]) (*connect.Response[promptspb.GetPromptVersionResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}

	var version *int
	if req.Msg.Version != nil {
		v := int(req.Msg.GetVersion())
		version = &v
	}
	var label *string
	if req.Msg.Label != nil {
		l := req.Msg.GetLabel()
		label = &l
	}

	rm, err := s.getPromptVersionReadModel(ctx, sys, tenantID, req.Msg.GetPromptId(), version, label)
	if err != nil {
		return nil, err
	}
	if rm == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("prompt version not found"))
	}
	return connect.NewResponse(&promptspb.GetPromptVersionResponse{Version: promptVersionToProto(rm)}), nil
}

func (s *PromptServer) SetPromptLabels(ctx context.Context, req *connect.Request[promptspb.SetPromptLabelsRequest]) (*connect.Response[promptspb.SetPromptLabelsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := tenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ctx = ensureTenantSchema(ctx, tenantID)
	userID := contextkeys.GetUserID(ctx)

	v := int(req.Msg.GetVersion())
	rm, err := s.getPromptVersionReadModel(ctx, sys, tenantID, req.Msg.GetPromptId(), &v, nil)
	if err != nil {
		return nil, err
	}
	if rm == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("prompt version not found"))
	}

	cmd := promptscmd.NewSetPromptLabelsCommand(req.Msg.GetPromptId(), tenantID, userID, "", v, req.Msg.GetLabels())
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	out := promptVersionToProto(rm)
	out.Labels = req.Msg.GetLabels()
	return connect.NewResponse(&promptspb.SetPromptLabelsResponse{Version: out}), nil
}

func (s *PromptServer) getPromptVersionReadModel(ctx context.Context, sys *cqrs.System, tenantID, promptID string, version *int, label *string) (*promptsquery.PromptVersionReadModel, error) {
	res, err := sys.QueryBus.Execute(ctx, promptsquery.NewGetPromptVersionQuery(tenantID, promptID, version, label))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, nil
	}
	data := unwrapQueryData(res)
	if data == nil {
		return nil, nil
	}
	rm, ok := data.(*promptsquery.PromptVersionReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}
	return rm, nil
}

// ===== Converters =====

func messagesFromProto(msgs []*promptspb.PromptMessage) []promptscmd.PromptMessagePayload {
	out := make([]promptscmd.PromptMessagePayload, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, promptscmd.PromptMessagePayload{
			Role:    m.GetRole(),
			Content: m.GetContent(),
		})
	}
	return out
}

func promptToProto(rm *promptsquery.PromptReadModel) *promptspb.Prompt {
	p := &promptspb.Prompt{
		Id:            rm.ID,
		TenantId:      rm.TenantID,
		Name:          rm.Name,
		Description:   rm.Description,
		LatestVersion: rm.LatestVersion,
		VersionCount:  rm.VersionCount,
		CreatedAt:     utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:     utils.ParseTimestamp(rm.UpdatedAt),
	}
	if len(rm.Tags) > 0 {
		var tags []string
		if err := json.Unmarshal(rm.Tags, &tags); err == nil {
			p.Tags = tags
		}
	}
	if len(rm.Labels) > 0 {
		var labels map[string]int32
		if err := json.Unmarshal(rm.Labels, &labels); err == nil {
			p.Labels = labels
		}
	}
	if rm.ArchivedAt.Valid {
		p.ArchivedAt = utils.ParseTimestamp(rm.ArchivedAt.String)
	}
	return p
}

func promptVersionToProto(rm *promptsquery.PromptVersionReadModel) *promptspb.PromptVersion {
	v := &promptspb.PromptVersion{
		Id:            rm.ID,
		PromptId:      rm.PromptID,
		TenantId:      rm.TenantID,
		Version:       rm.Version,
		CommitMessage: rm.CommitMessage,
		CreatedBy:     rm.CreatedBy,
		CreatedAt:     utils.ParseTimestamp(rm.CreatedAt),
	}
	if len(rm.Messages) > 0 {
		var msgs []promptscmd.PromptMessagePayload
		if err := json.Unmarshal(rm.Messages, &msgs); err == nil {
			for _, m := range msgs {
				v.Messages = append(v.Messages, &promptspb.PromptMessage{Role: m.Role, Content: m.Content})
			}
		}
	}
	if len(rm.Config) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.Config, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				v.Config = s
			}
		}
	}
	if len(rm.Labels) > 0 {
		var labels []string
		if err := json.Unmarshal(rm.Labels, &labels); err == nil {
			v.Labels = labels
		}
	}
	return v
}
