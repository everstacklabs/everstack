package aws_bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
)

type Provider struct {
	cfg    Config
	client bedrockAPI
}

func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Client != nil {
		return &Provider{cfg: cfg, client: cfg.Client}, nil
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(awsCfg, func(o *bedrockruntime.Options) {
		if strings.TrimSpace(cfg.BaseURL) != "" {
			o.BaseEndpoint = aws.String(strings.TrimRight(cfg.BaseURL, "/"))
		}
	})

	return &Provider{cfg: cfg, client: client}, nil
}

func (p *Provider) Name() string { return "aws-bedrock" }

func (p *Provider) SupportsModel(model string) bool {
	if len(p.cfg.SupportedModels) == 0 {
		return strings.Contains(strings.ToLower(model), ".") || strings.Contains(strings.ToLower(model), "arn:")
	}
	model = strings.ToLower(model)
	for _, m := range p.cfg.SupportedModels {
		if strings.ToLower(m) == model {
			return true
		}
	}
	return false
}

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	input, err := p.buildConverseInput(req)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", p.Name(),
		"model", req.Model,
		"stream", false,
		"tools", len(req.Tools),
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.client.Converse(ctx, input)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	return p.toGatewayResponse(resp, req.Model), nil
}

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	input, err := p.buildConverseStreamInput(req)
	if err != nil {
		return err
	}

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", p.Name(),
		"model", req.Model,
		"stream", true,
		"tools", len(req.Tools),
		"correlation_id", cid,
	).Info("provider stream request issued")

	resp, err := p.client.ConverseStream(ctx, input)
	if err != nil {
		return err
	}
	defer resp.GetStream().Close()

	type partialToolCall struct {
		id   string
		name string
		args strings.Builder
	}
	toolCalls := map[int32]*partialToolCall{}
	var usage *gw.Usage
	finishReason := "stop"

	for event := range resp.GetStream().Events() {
		switch ev := event.(type) {
		case *bedrocktypes.ConverseStreamOutputMemberContentBlockStart:
			idx := aws.ToInt32(ev.Value.ContentBlockIndex)
			switch start := ev.Value.Start.(type) {
			case *bedrocktypes.ContentBlockStartMemberToolUse:
				toolCalls[idx] = &partialToolCall{
					id:   aws.ToString(start.Value.ToolUseId),
					name: aws.ToString(start.Value.Name),
				}
			}
		case *bedrocktypes.ConverseStreamOutputMemberContentBlockDelta:
			idx := aws.ToInt32(ev.Value.ContentBlockIndex)
			switch delta := ev.Value.Delta.(type) {
			case *bedrocktypes.ContentBlockDeltaMemberText:
				if delta.Value == "" {
					continue
				}
				if err := onChunk(gw.ChatResponseChunk{
					ID:      fmt.Sprintf("bedrock-%d", time.Now().UnixNano()),
					Created: time.Now().UTC(),
					Model:   req.Model,
					Choices: []gw.ChoiceDelta{{
						Index: 0,
						Delta: gw.Message{Role: gw.RoleAssistant, Content: []gw.ContentPart{gw.Text(delta.Value)}},
					}},
				}); err != nil {
					return err
				}
			case *bedrocktypes.ContentBlockDeltaMemberToolUse:
				if tc := toolCalls[idx]; tc != nil && delta.Value.Input != nil {
					tc.args.WriteString(aws.ToString(delta.Value.Input))
				}
			}
		case *bedrocktypes.ConverseStreamOutputMemberContentBlockStop:
			idx := aws.ToInt32(ev.Value.ContentBlockIndex)
			if tc := toolCalls[idx]; tc != nil {
				if err := onChunk(gw.ChatResponseChunk{
					ID:      fmt.Sprintf("bedrock-%d", time.Now().UnixNano()),
					Created: time.Now().UTC(),
					Model:   req.Model,
					Choices: []gw.ChoiceDelta{{
						Index: 0,
						Delta: gw.Message{Role: gw.RoleAssistant, ToolCalls: []gw.ToolCall{{
							ID:   tc.id,
							Type: "function",
							Function: gw.ToolCallFunction{
								Name:      tc.name,
								Arguments: tc.args.String(),
							},
						}}},
						FinishReason: "tool_calls",
					}},
				}); err != nil {
					return err
				}
				delete(toolCalls, idx)
			}
		case *bedrocktypes.ConverseStreamOutputMemberMessageStop:
			finishReason = mapStopReason(ev.Value.StopReason)
		case *bedrocktypes.ConverseStreamOutputMemberMetadata:
			usage = usageFromBedrock(ev.Value.Usage)
			ratelimit.GlobalMonitor.Update(ratelimit.RateLimitInfo{Provider: p.Name(), Model: req.Model})
		}
	}
	if err := resp.GetStream().Err(); err != nil {
		return err
	}
	return onChunk(gw.ChatResponseChunk{
		ID:      fmt.Sprintf("bedrock-%d", time.Now().UnixNano()),
		Created: time.Now().UTC(),
		Model:   req.Model,
		Choices: []gw.ChoiceDelta{{
			Index:        0,
			Delta:        gw.Message{Role: gw.RoleAssistant},
			FinishReason: finishReason,
		}},
		Usage: usage,
	})
}

func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, gw.ErrNotSupported{Operation: "embed", Provider: p.Name()}
}

func (p *Provider) buildConverseInput(req gw.ChatCompletionRequest) (*bedrockruntime.ConverseInput, error) {
	messages, system, err := p.convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	return &bedrockruntime.ConverseInput{
		ModelId:         aws.String(req.Model),
		Messages:        messages,
		System:          system,
		InferenceConfig: inferenceConfig(req.Sampling),
		ToolConfig:      toolConfig(req.Tools),
		RequestMetadata: stringMap(req.Metadata),
	}, nil
}

func (p *Provider) buildConverseStreamInput(req gw.ChatCompletionRequest) (*bedrockruntime.ConverseStreamInput, error) {
	messages, system, err := p.convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	return &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(req.Model),
		Messages:        messages,
		System:          system,
		InferenceConfig: inferenceConfig(req.Sampling),
		ToolConfig:      toolConfig(req.Tools),
		RequestMetadata: stringMap(req.Metadata),
	}, nil
}

func (p *Provider) convertMessages(msgs []gw.Message) ([]bedrocktypes.Message, []bedrocktypes.SystemContentBlock, error) {
	bedrockMessages := make([]bedrocktypes.Message, 0, len(msgs))
	system := make([]bedrocktypes.SystemContentBlock, 0, 1)
	for _, msg := range msgs {
		if msg.Role == gw.RoleSystem {
			for _, part := range msg.Content {
				if part.Text != nil && *part.Text != "" {
					system = append(system, &bedrocktypes.SystemContentBlockMemberText{Value: *part.Text})
				}
			}
			continue
		}

		if msg.ToolCallID != "" {
			resultText := textFromContent(msg.Content)
			bedrockMessages = append(bedrockMessages, bedrocktypes.Message{
				Role: bedrocktypes.ConversationRoleUser,
				Content: []bedrocktypes.ContentBlock{&bedrocktypes.ContentBlockMemberToolResult{Value: bedrocktypes.ToolResultBlock{
					ToolUseId: aws.String(msg.ToolCallID),
					Content:   []bedrocktypes.ToolResultContentBlock{&bedrocktypes.ToolResultContentBlockMemberText{Value: resultText}},
				}}},
			})
			continue
		}

		content := make([]bedrocktypes.ContentBlock, 0, len(msg.Content)+len(msg.ToolCalls))
		for _, part := range msg.Content {
			if part.Text != nil && *part.Text != "" {
				content = append(content, &bedrocktypes.ContentBlockMemberText{Value: *part.Text})
			}
		}
		for _, tc := range msg.ToolCalls {
			input := map[string]interface{}{}
			if strings.TrimSpace(tc.Function.Arguments) != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					input = map[string]interface{}{"raw": tc.Function.Arguments}
				}
			}
			content = append(content, &bedrocktypes.ContentBlockMemberToolUse{Value: bedrocktypes.ToolUseBlock{
				ToolUseId: aws.String(tc.ID),
				Name:      aws.String(tc.Function.Name),
				Input:     bedrockdocument.NewLazyDocument(input),
			}})
		}
		if len(content) == 0 {
			continue
		}
		bedrockMessages = append(bedrockMessages, bedrocktypes.Message{Role: convertRole(msg.Role), Content: content})
	}
	return bedrockMessages, system, nil
}

func (p *Provider) toGatewayResponse(resp *bedrockruntime.ConverseOutput, model string) gw.ChatCompletionResponse {
	msg := gw.Message{Role: gw.RoleAssistant}
	if out, ok := resp.Output.(*bedrocktypes.ConverseOutputMemberMessage); ok {
		for _, part := range out.Value.Content {
			switch block := part.(type) {
			case *bedrocktypes.ContentBlockMemberText:
				if block.Value != "" {
					msg.Content = append(msg.Content, gw.Text(block.Value))
				}
			case *bedrocktypes.ContentBlockMemberToolUse:
				args, _ := json.Marshal(block.Value.Input)
				msg.ToolCalls = append(msg.ToolCalls, gw.ToolCall{
					ID:   aws.ToString(block.Value.ToolUseId),
					Type: "function",
					Function: gw.ToolCallFunction{
						Name:      aws.ToString(block.Value.Name),
						Arguments: string(args),
					},
				})
			}
		}
	}
	finishReason := mapStopReason(resp.StopReason)
	if len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	return gw.ChatCompletionResponse{
		ID:      fmt.Sprintf("bedrock-%d", time.Now().UnixNano()),
		Created: time.Now().UTC(),
		Model:   model,
		Choices: []gw.Choice{{Index: 0, Message: msg, FinishReason: finishReason}},
		Usage:   derefUsage(resp.Usage),
	}
}

func convertRole(role gw.MessageRole) bedrocktypes.ConversationRole {
	if role == gw.RoleAssistant {
		return bedrocktypes.ConversationRoleAssistant
	}
	return bedrocktypes.ConversationRoleUser
}

func inferenceConfig(s gw.SamplingParams) *bedrocktypes.InferenceConfiguration {
	conf := &bedrocktypes.InferenceConfiguration{}
	if s.MaxCompletionTokens > 0 {
		v := int32(s.MaxCompletionTokens)
		conf.MaxTokens = &v
	} else if s.MaxTokens > 0 {
		v := int32(s.MaxTokens)
		conf.MaxTokens = &v
	}
	if s.Temperature > 0 {
		v := float32(s.Temperature)
		conf.Temperature = &v
	}
	if s.TopP > 0 {
		v := float32(s.TopP)
		conf.TopP = &v
	}
	if len(s.Stop) > 0 {
		conf.StopSequences = s.Stop
	}
	if conf.MaxTokens == nil && conf.Temperature == nil && conf.TopP == nil && len(conf.StopSequences) == 0 {
		return nil
	}
	return conf
}

func toolConfig(tools []gw.ToolDefinition) *bedrocktypes.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}
	out := &bedrocktypes.ToolConfiguration{Tools: make([]bedrocktypes.Tool, 0, len(tools))}
	for _, tool := range tools {
		out.Tools = append(out.Tools, &bedrocktypes.ToolMemberToolSpec{Value: bedrocktypes.ToolSpecification{
			Name:        aws.String(tool.Function.Name),
			Description: aws.String(tool.Function.Description),
			InputSchema: &bedrocktypes.ToolInputSchemaMemberJson{Value: bedrockdocument.NewLazyDocument(tool.Function.Parameters)},
		}})
	}
	return out
}

func stringMap(metadata map[string]interface{}) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for k, v := range metadata {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func textFromContent(parts []gw.ContentPart) string {
	var b strings.Builder
	for _, part := range parts {
		if part.Text != nil {
			b.WriteString(*part.Text)
		}
	}
	return b.String()
}

func mapStopReason(reason bedrocktypes.StopReason) string {
	switch string(reason) {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

func usageFromBedrock(usage *bedrocktypes.TokenUsage) *gw.Usage {
	if usage == nil {
		return nil
	}
	u := derefUsage(usage)
	return &u
}

func derefUsage(usage *bedrocktypes.TokenUsage) gw.Usage {
	if usage == nil {
		return gw.Usage{}
	}
	prompt := int(aws.ToInt32(usage.InputTokens))
	completion := int(aws.ToInt32(usage.OutputTokens))
	total := int(aws.ToInt32(usage.TotalTokens))
	gwUsage := gw.Usage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: total}
	if aws.ToInt32(usage.CacheReadInputTokens) > 0 || aws.ToInt32(usage.CacheWriteInputTokens) > 0 {
		gwUsage.PromptDetails = &gw.TokenDetails{
			CachedTokens: int(aws.ToInt32(usage.CacheReadInputTokens) + aws.ToInt32(usage.CacheWriteInputTokens)),
			TextTokens:   prompt,
		}
	}
	return gwUsage
}
