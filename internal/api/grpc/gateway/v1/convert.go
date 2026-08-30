package v1

import (
	"encoding/json"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func toGatewayRequest(in *gatewaypb.ChatCompletionRequest) gw.ChatCompletionRequest {
	sampling := in.GetSampling()
	var reasoningBudget *int
	var reasoningEnabled *bool
	if sampling != nil && sampling.ReasoningBudgetTokens != nil {
		value := int(sampling.GetReasoningBudgetTokens())
		reasoningBudget = &value
	}
	if sampling != nil {
		reasoningEnabled = sampling.ReasoningEnabled
	}
	var topK *int
	if sampling != nil && sampling.TopK != nil {
		value := int(sampling.GetTopK())
		topK = &value
	}
	var seed *int64
	if sampling != nil && sampling.Seed != nil {
		value := sampling.GetSeed()
		seed = &value
	}
	out := gw.ChatCompletionRequest{
		Model: in.GetModel(),
		Sampling: gw.SamplingParams{
			Temperature:           float64(sampling.GetTemperature()),
			TemperatureConfigured: sampling != nil && sampling.Temperature != nil,
			TopP:                  float64(sampling.GetTopP()),
			TopPConfigured:        sampling != nil && sampling.TopP != nil,
			MaxTokens:             int(sampling.GetMaxTokens()),
			MaxCompletionTokens:   int(sampling.GetMaxCompletionTokens()),
			Stop:                  sampling.GetStop(),
			FrequencyPenalty:      float64(sampling.GetFrequencyPenalty()),
			FrequencyConfigured:   sampling != nil && sampling.FrequencyPenalty != nil,
			PresencePenalty:       float64(sampling.GetPresencePenalty()),
			PresenceConfigured:    sampling != nil && sampling.PresencePenalty != nil,
			ReasoningEffort:       sampling.GetReasoningEffort(),
			ReasoningBudget:       reasoningBudget,
			ReasoningEnabled:      reasoningEnabled,
			TopK:                  topK,
			Seed:                  seed,
			Verbosity:             sampling.GetVerbosity(),
		},
		Stream:   in.GetStream(),
		Metadata: convertMetadata(in.GetMetadata()),
	}
	for _, m := range in.GetMessages() {
		parts := make([]gw.ContentPart, 0, len(m.GetContent()))
		for _, p := range m.GetContent() {
			// Be tolerant of missing type by inferring from populated oneof field
			t := p.GetType()
			if t == "" {
				if p.GetText() != "" {
					t = "text"
				} else if p.GetImageUrl() != "" {
					t = "image_url"
				}
			}
			part := gw.ContentPart{Type: t}
			switch t {
			case "text":
				txt := p.GetText()
				part.Text = &txt
			case "image_url":
				u := p.GetImageUrl()
				part.ImageURL = &u
			}
			if raw := p.GetProviderJson(); raw != "" {
				providerJSON := json.RawMessage(raw)
				if json.Valid(providerJSON) {
					part.ProviderJSON = &providerJSON
				}
			}
			if part.Text != nil || part.ImageURL != nil || part.ProviderJSON != nil {
				parts = append(parts, part)
			}
		}
		msg := gw.NewMessage(mapRoleFromProto(m.GetRole()), parts...)
		// Convert tool calls if present
		for _, tc := range m.GetToolCalls() {
			msg.ToolCalls = append(msg.ToolCalls, gw.ToolCall{
				ID:   tc.GetId(),
				Type: tc.GetType(),
				Function: gw.ToolCallFunction{
					Name:      tc.GetFunction().GetName(),
					Arguments: tc.GetFunction().GetArguments(),
				},
			})
		}
		// Convert tool_call_id if present
		if tcID := m.GetToolCallId(); tcID != "" {
			msg.ToolCallID = tcID
		}
		out.Messages = append(out.Messages, msg)
	}

	// Convert tools
	for _, t := range in.GetTools() {
		tool := gw.ToolDefinition{
			Type: t.GetType(),
			Function: gw.ToolFunctionDef{
				Name:        t.GetFunction().GetName(),
				Description: t.GetFunction().GetDescription(),
			},
		}
		// Convert parameters from Struct to map
		if params := t.GetFunction().GetParameters(); params != nil {
			tool.Function.Parameters = params.AsMap()
		}
		out.Tools = append(out.Tools, tool)
	}

	// Convert tool_choice
	if tc := in.GetToolChoice(); tc != nil {
		switch choice := tc.GetChoice().(type) {
		case *gatewaypb.ToolChoice_Mode:
			// String mode: "auto", "none", "required"
			out.ToolChoice = choice.Mode
		case *gatewaypb.ToolChoice_SpecificTool:
			// Specific tool selection
			out.ToolChoice = map[string]interface{}{
				"type": choice.SpecificTool.GetType(),
				"function": map[string]interface{}{
					"name": choice.SpecificTool.GetFunction().GetName(),
				},
			}
		}
	}

	return out
}

func mapRoleFromProto(r gatewaypb.Role) gw.MessageRole {
	switch r {
	case gatewaypb.Role_ROLE_SYSTEM:
		return gw.RoleSystem
	case gatewaypb.Role_ROLE_USER:
		return gw.RoleUser
	case gatewaypb.Role_ROLE_ASSISTANT:
		return gw.RoleAssistant
	case gatewaypb.Role_ROLE_FUNCTION:
		return gw.RoleFunction
	case gatewaypb.Role_ROLE_TOOL:
		return gw.RoleTool
	default:
		return gw.RoleUser
	}
}

// convertMetadata converts google.protobuf.Struct to map[string]interface{}
func convertMetadata(pbStruct *structpb.Struct) map[string]interface{} {
	if pbStruct == nil {
		return nil
	}
	return pbStruct.AsMap()
}

func toProtoResponse(resp gw.ChatCompletionResponse) *gatewaypb.ChatCompletionResponse {
	out := &gatewaypb.ChatCompletionResponse{
		Id:      resp.ID,
		Created: resp.Created.Unix(),
		Model:   resp.Model,
		Usage:   &gatewaypb.Usage{PromptTokens: int32(resp.Usage.PromptTokens), CompletionTokens: int32(resp.Usage.CompletionTokens), TotalTokens: int32(resp.Usage.TotalTokens)},
	}
	for _, c := range resp.Choices {
		out.Choices = append(out.Choices, &gatewaypb.Choice{
			Index:        int32(c.Index),
			Message:      toProtoMessage(c.Message),
			FinishReason: c.FinishReason,
		})
	}
	return out
}

// attachFallbackInfo adds fallback metadata to a response
func attachFallbackInfo(resp *gatewaypb.ChatCompletionResponse, requestedModel, actualModel, reason string, attempts int32) *gatewaypb.ChatCompletionResponse {
	resp.FallbackInfo = &gatewaypb.FallbackInfo{
		FallbackUsed:     true,
		RequestedModel:   requestedModel,
		ActualModel:      actualModel,
		FallbackReason:   reason,
		FallbackAttempts: attempts,
	}
	return resp
}

func toProtoChunk(chunk gw.ChatResponseChunk) *gatewaypb.ChatResponseChunk {
	out := &gatewaypb.ChatResponseChunk{
		Id:      chunk.ID,
		Created: chunk.Created.Unix(),
		Model:   chunk.Model,
	}
	for _, d := range chunk.Choices {
		out.Choices = append(out.Choices, &gatewaypb.ChoiceDelta{
			Index:        int32(d.Index),
			Delta:        toProtoMessage(d.Delta),
			FinishReason: d.FinishReason,
		})
	}
	return out
}

// toProtoResponseFromChunk converts a streaming chunk into a minimal
// ChatCompletionResponse by concatenating text deltas into a single message.
func toProtoResponseFromChunk(ch gw.ChatResponseChunk) *gatewaypb.ChatCompletionResponse {
	var parts []gw.ContentPart
	for _, d := range ch.Choices {
		parts = append(parts, d.Delta.Content...)
	}

	msg := toProtoMessage(gw.NewMessage(gw.RoleAssistant, parts...))

	choice := &gatewaypb.Choice{
		Index:   0,
		Message: msg,
		// We could propagate a finish_reason from the first delta if present,
		// but many providers only set it on final chunk.
	}

	return &gatewaypb.ChatCompletionResponse{
		Id:      ch.ID,
		Created: ch.Created.Unix(),
		Model:   ch.Model,
		Choices: []*gatewaypb.Choice{choice},
	}
}

func toProtoMessage(m gw.Message) *gatewaypb.Message {
	pm := &gatewaypb.Message{Role: mapRoleToProto(m.Role)}
	for _, p := range m.Content {
		cp := &gatewaypb.ContentPart{Type: p.Type}
		switch p.Type {
		case "text":
			if p.Text != nil {
				cp.Data = &gatewaypb.ContentPart_Text{Text: *p.Text}
			}
		case "image_url":
			if p.ImageURL != nil {
				cp.Data = &gatewaypb.ContentPart_ImageUrl{ImageUrl: *p.ImageURL}
			}
		}
		if p.ProviderJSON != nil && json.Valid(*p.ProviderJSON) {
			cp.ProviderJson = string(*p.ProviderJSON)
		}
		pm.Content = append(pm.Content, cp)
	}
	// Convert tool calls if present
	for _, tc := range m.ToolCalls {
		pm.ToolCalls = append(pm.ToolCalls, &gatewaypb.ToolCall{
			Id:   tc.ID,
			Type: tc.Type,
			Function: &gatewaypb.ToolCallFunction{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	// Convert tool_call_id if present
	if m.ToolCallID != "" {
		pm.ToolCallId = m.ToolCallID
	}
	return pm
}

func mapRoleToProto(r gw.MessageRole) gatewaypb.Role {
	switch r {
	case gw.RoleSystem:
		return gatewaypb.Role_ROLE_SYSTEM
	case gw.RoleUser:
		return gatewaypb.Role_ROLE_USER
	case gw.RoleAssistant:
		return gatewaypb.Role_ROLE_ASSISTANT
	case gw.RoleFunction:
		return gatewaypb.Role_ROLE_FUNCTION
	case gw.RoleTool:
		return gatewaypb.Role_ROLE_TOOL
	default:
		return gatewaypb.Role_ROLE_USER
	}
}
