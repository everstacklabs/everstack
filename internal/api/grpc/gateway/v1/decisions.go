package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/pkg/decision"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// resolveDecisionModel requires an exact tenant-owned provider and enabled model.
// It deliberately avoids global caches and the router's missing-provider fallback.
func resolveDecisionModel(reg *gw.Registry, ref string) (gw.Provider, string, error) {
	provider, model, ok := strings.Cut(strings.TrimPrefix(ref, "@"), "/")
	if !strings.HasPrefix(ref, "@") || !ok || provider == "" || model == "" {
		return nil, "", fmt.Errorf("model must be an explicit @provider/model reference")
	}
	p, ok := reg.Get(provider)
	if !ok || !p.SupportsModel(model) {
		return nil, "", fmt.Errorf("model is not enabled for this tenant")
	}
	return p, model, nil
}
func (s *Server) SystemOne(ctx context.Context, req *connect.Request[gatewaypb.SystemOneRequest]) (*connect.Response[gatewaypb.SystemOneResponse], error) {
	if req.Msg.State == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("state is required"))
	}
	data, err := protojson.Marshal(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid decision request"))
	}
	if len(data) > decision.MaxBodyBytes {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("decision request exceeds 4 MiB"))
	}
	var input decision.Request
	if json.Unmarshal(data, &input) != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid decision request"))
	}
	if err = input.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	reg, _, err := s.ProviderBundleForRequest(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("providers unavailable for this tenant"))
	}
	p, model, err := resolveDecisionModel(reg, input.Model)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	dp, ok := p.(decision.Provider)
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("selected provider does not support decisions"))
	}
	input.Model = model
	out, err := dp.Decide(ctx, input)
	if err != nil {
		return nil, decisionConnectError(err)
	}
	// Keep the gateway's qualified model reference so clients can replay requests.
	if out.Model == "" {
		out.Model = model
	}
	out.Model = "@" + p.Name() + "/" + out.Model
	payload, err := json.Marshal(out)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("could not encode decision response"))
	}
	result := new(gatewaypb.SystemOneResponse)
	if protojson.Unmarshal(payload, result) != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("could not encode decision response"))
	}
	return connect.NewResponse(result), nil
}
func (g *GrpcServer) SystemOne(ctx context.Context, req *gatewaypb.SystemOneRequest) (*gatewaypb.SystemOneResponse, error) {
	result, err := g.base.SystemOne(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, status.Error(codes.Code(connect.CodeOf(err)), err.Error())
	}
	return result.Msg, nil
}
func decisionConnectError(err error) error {
	code := connect.CodeUnavailable
	switch {
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	default:
		var he *decision.HTTPError
		if errors.As(err, &he) {
			switch he.StatusCode {
			case 429:
				code = connect.CodeResourceExhausted
			case 400, 422:
				code = connect.CodeInvalidArgument
			case 401, 403:
				code = connect.CodeFailedPrecondition
			}
		}
	}
	return connect.NewError(code, errors.New("decision provider could not complete the request"))
}

type modelDecisionProvider struct {
	provider decision.Provider
	model    string
}

func (p modelDecisionProvider) Decide(ctx context.Context, r decision.Request) (decision.Response, error) {
	r.Model = p.model
	return p.provider.Decide(ctx, r)
}

// selectDecisionRoute is shared by chat (including streaming) and Responses.
// YAML applies only to single-tenant deployments; cloud settings live in each
// tenant's provider configuration and never inherit the host's routing policy.
func (s *Server) selectDecisionRoute(ctx context.Context, requested string, state json.RawMessage) (string, error) {
	if requested != "auto" {
		return requested, nil
	}
	if _, _, err := s.ProviderBundleForRequest(ctx); err != nil {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("providers unavailable for this tenant"))
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("providers unavailable for this tenant"))
	}
	cfg := bundle.decisionRouting
	if !s.isSharedMode() && !cfg.Enabled && s.cfg != nil {
		cfg = s.cfg.DecisionRouting
	}
	if !cfg.Enabled {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("configure decision_routing before requesting model auto"))
	}
	if err := cfg.Validate(); err != nil {
		return "", connect.NewError(connect.CodeFailedPrecondition, err)
	}
	// Validate every target, including fallback, before spending on a decision.
	for _, target := range cfg.Targets {
		p, _, err := resolveDecisionModel(bundle.reg, target.Model)
		if err != nil {
			return "", connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if _, ok := p.(decision.Provider); ok {
			return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("decision routing targets must be LLMs"))
		}
	}
	if len(state) == 0 {
		return cfg.Fallback, nil
	}
	p, model, err := resolveDecisionModel(bundle.reg, cfg.Model)
	if err != nil {
		return "", connect.NewError(connect.CodeFailedPrecondition, err)
	}
	dp, ok := p.(decision.Provider)
	if !ok {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("routing model must support decisions"))
	}
	selected, reason, err := cfg.Select(ctx, modelDecisionProvider{dp, model}, state)
	if err != nil {
		return "", decisionConnectError(err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("decision.routing.model", cfg.Model), attribute.String("decision.routing.selected", selected), attribute.String("decision.routing.reason", reason))
	return selected, nil
}

// Decision engines consume text. Requests requiring unseen images, tools or
// stored conversation state use the configured fallback without lossy judging.
// The original request is always forwarded unchanged to the chosen LLM.
func decisionChatState(messages []gw.Message, hasTools bool) json.RawMessage {
	if hasTools {
		return nil
	}
	for _, m := range messages {
		if len(m.ToolCalls) > 0 || m.ToolCallID != "" {
			return nil
		}
		for _, part := range m.Content {
			if part.Type != "text" || part.ProviderJSON != nil {
				return nil
			}
		}
	}
	state, err := json.Marshal(messages)
	if err != nil || len(state) > decision.MaxBodyBytes {
		return nil
	}
	return state
}
