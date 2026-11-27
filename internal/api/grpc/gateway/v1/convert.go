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
	var logprobs *bool
	var topLogprobs *int
	if sampling != nil {
		logprobs = sampling.Logprobs
		if sampling.TopLogprobs != nil {
			value := int(sampling.GetTopLogprobs())
			topLogprobs = &value
		}
	}
	out := gw.ChatCompletionRequest{
		Model:             in.GetModel(),
		ParallelToolCalls: in.ParallelToolCalls,
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
			Logprobs:              logprobs,
			TopLogprobs:           topLogprobs,
			LogitBias:             sampling.GetLogitBias(),
		},
		Stream:         in.GetStream(),
		Metadata:       convertMetadata(in.GetMetadata()),
		ResponseFormat: convertResponseFormat(in.GetResponseFormat()),
		ServiceTier:    in.GetServiceTier(),
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
				part.ImageDetail = p.GetImageDetail()
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
		msg.Name = m.GetName()
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
				Strict:      t.GetFunction().Strict,
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

// convertResponseFormat reads the OpenAI response_format object
// ({"type":"json_object"} or {"type":"json_schema","json_schema":{name,
// description, schema, strict}}) into the gateway shape. An absent or
// typeless value is nil, meaning plain text.
func convertResponseFormat(pbStruct *structpb.Struct) *gw.ChatResponseFormat {
	if pbStruct == nil {
		return nil
	}
	raw := pbStruct.AsMap()
	typ, _ := raw["type"].(string)
	if typ == "" {
		return nil
	}
	out := &gw.ChatResponseFormat{Type: typ}
	if js, ok := raw["json_schema"].(map[string]interface{}); ok {
		schema := &gw.ChatJSONSchema{}
		schema.Name, _ = js["name"].(string)
		schema.Description, _ = js["description"].(string)
		schema.Schema, _ = js["schema"].(map[string]interface{})
		if strict, ok := js["strict"].(bool); ok {
			schema.Strict = &strict
		}
		out.JSONSchema = schema
	}
	return out
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
		Usage:   toProtoUsage(&resp.Usage),
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
	// Group repeated deltas for the same choice without mixing distinct choices.
	// This preserves historical single-choice frames (for example Gemini text
	// parts) while retaining the real index for multi-choice streams.
	choices := make([]*gatewaypb.Choice, 0, len(ch.Choices))
	positions := map[int]int{}
	for _, d := range ch.Choices {
		position, ok := positions[d.Index]
		if !ok {
			position = len(choices)
			positions[d.Index] = position
			choices = append(choices, &gatewaypb.Choice{Index: int32(d.Index), Message: toProtoMessage(gw.NewMessage(gw.RoleAssistant))})
		}
		choice := choices[position]
		msg := toProtoMessage(d.Delta)
		choice.Message.Content = append(choice.Message.Content, msg.Content...)
		choice.Message.ToolCalls = append(choice.Message.ToolCalls, msg.ToolCalls...)
		if choice.FinishReason == "" {
			choice.FinishReason = d.FinishReason
		}
	}
	// Preserve the historical usage-only single-choice terminator.
	if len(choices) == 0 {
		choices = append(choices, &gatewaypb.Choice{Message: toProtoMessage(gw.NewMessage(gw.RoleAssistant))})
	}

	resp := &gatewaypb.ChatCompletionResponse{
		Id:      ch.ID,
		Created: ch.Created.Unix(),
		Model:   ch.Model,
		Choices: choices,
	}

	// Carry token usage through. The non-streaming path already does this (see
	// toProtoResponse), but the streaming path did not, and the
	// OpenAI-compatible surface reconstructs its response from these chunks --
	// so a client on /openai/v1 received usage of zero for every request and
	// could not attribute cost at all. The counts were already being read here
	// for internal cost metrics; they were simply never returned to the caller.
	if ch.Usage != nil {
		resp.Usage = toProtoUsage(ch.Usage)
	}

	return resp
}

// toProtoUsage converts usage to the OpenAI-shaped proto, including the
// prompt and completion breakdowns when the provider reported them. cached
// tokens follow OpenAI's meaning (prompt tokens read from cache), and
// reasoning tokens are a subset of completion_tokens, never extra.
func toProtoUsage(u *gw.Usage) *gatewaypb.Usage {
	out := &gatewaypb.Usage{
		PromptTokens:     int32(u.PromptTokens),
		CompletionTokens: int32(u.CompletionTokens),
		TotalTokens:      int32(u.TotalTokens),
	}
	if d := u.PromptDetails; d != nil {
		if cached := u.CacheReadCount(); cached > 0 || d.AudioTokens > 0 {
			out.PromptTokensDetails = &gatewaypb.UsageTokenDetails{
				CachedTokens: int32(cached),
				AudioTokens:  int32(d.AudioTokens),
			}
		}
	}
	if d := u.CompletionDetails; d != nil && (d.ReasoningTokens > 0 || d.AudioTokens > 0) {
		out.CompletionTokensDetails = &gatewaypb.UsageTokenDetails{
			ReasoningTokens: int32(d.ReasoningTokens),
			AudioTokens:     int32(d.AudioTokens),
		}
	}
	return out
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
