package gateway

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
)

// Gateway-specific commands for LLM operations

// ChatCompletionCommand represents a chat completion request.
type ChatCompletionCommand struct {
	commands.BaseCommand
	Model           string                 `json:"model"`
	Provider        string                 `json:"provider"`
	Messages        []ChatMessage          `json:"messages"`
	Stream          bool                   `json:"stream"`
	Temperature     *float64               `json:"temperature,omitempty"`
	MaxTokens       *int                   `json:"max_tokens,omitempty"`
	RequestMetadata map[string]interface{} `json:"request_metadata,omitempty"`
}

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role    string                 `json:"role"`
	Content []MessageContent       `json:"content"`
	Name    string                 `json:"name,omitempty"`
	Extra   map[string]interface{} `json:"extra,omitempty"`
}

// MessageContent represents content within a chat message.
type MessageContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
}

func NewChatCompletionCommand(model, provider string, messages []ChatMessage, stream bool, userID, apiKey, traceID string) *ChatCompletionCommand {
	return &ChatCompletionCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			APIKey:    apiKey,
			TraceID:   traceID,
		},
		Model:    model,
		Provider: provider,
		Messages: messages,
		Stream:   stream,
	}
}

func (c ChatCompletionCommand) AggregateID() string { return c.ID }
func (c ChatCompletionCommand) CommandType() string { return "ChatCompletion" }

func (c ChatCompletionCommand) Validate() error {
	if len(c.Messages) == 0 {
		return fmt.Errorf("messages cannot be empty")
	}
	if c.Model == "" {
		return fmt.Errorf("model cannot be empty")
	}
	return nil
}

// ProcessEmbeddingCommand represents an embedding generation request.
type ProcessEmbeddingCommand struct {
	commands.BaseCommand
	Model           string                 `json:"model"`
	Input           []string               `json:"input"`
	EncodingFormat  string                 `json:"encoding_format,omitempty"`
	Dimensions      *int                   `json:"dimensions,omitempty"`
	RequestMetadata map[string]interface{} `json:"request_metadata,omitempty"`
}

func NewProcessEmbeddingCommand(model string, input []string, userID, apiKey, traceID string) *ProcessEmbeddingCommand {
	return &ProcessEmbeddingCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			APIKey:    apiKey,
			TraceID:   traceID,
		},
		Model: model,
		Input: input,
	}
}

func (c ProcessEmbeddingCommand) AggregateID() string { return c.ID }
func (c ProcessEmbeddingCommand) CommandType() string { return "Embeddings" }

func (c ProcessEmbeddingCommand) Validate() error {
	if len(c.Input) == 0 {
		return fmt.Errorf("input cannot be empty")
	}
	if c.Model == "" {
		return fmt.Errorf("model cannot be empty")
	}
	return nil
}

// ConfigureModelCommand represents a model configuration change.
type ConfigureModelCommand struct {
	commands.BaseCommand
	Provider string                 `json:"provider"`
	ModelID  string                 `json:"model_id"`
	Alias    string                 `json:"alias"`
	Config   map[string]interface{} `json:"config"`
	Enabled  bool                   `json:"enabled"`
}

func NewConfigureModelCommand(provider, modelID, alias string, config map[string]interface{}, enabled bool, userID, traceID string) *ConfigureModelCommand {
	return &ConfigureModelCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		Provider: provider,
		ModelID:  modelID,
		Alias:    alias,
		Config:   config,
		Enabled:  enabled,
	}
}

func (c ConfigureModelCommand) AggregateID() string { return c.Provider + ":" + c.ModelID }
func (c ConfigureModelCommand) CommandType() string { return "ConfigureModel" }

func (c ConfigureModelCommand) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("provider cannot be empty")
	}
	if c.ModelID == "" {
		return fmt.Errorf("model_id cannot be empty")
	}
	if c.Alias == "" {
		return fmt.Errorf("alias cannot be empty")
	}
	return nil
}

// UpdateLoadBalancerCommand represents a load balancer configuration change.
type UpdateLoadBalancerCommand struct {
	commands.BaseCommand
	Strategy  string             `json:"strategy"`
	KeySource string             `json:"key_source"`
	Weights   map[string]float64 `json:"weights,omitempty"`
	Enabled   bool               `json:"enabled"`
}

func NewUpdateLoadBalancerCommand(strategy, keySource string, weights map[string]float64, enabled bool, userID, traceID string) *UpdateLoadBalancerCommand {
	return &UpdateLoadBalancerCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		Strategy:  strategy,
		KeySource: keySource,
		Weights:   weights,
		Enabled:   enabled,
	}
}

func (c UpdateLoadBalancerCommand) AggregateID() string { return "load_balancer" }
func (c UpdateLoadBalancerCommand) CommandType() string { return "UpdateLoadBalancer" }

func (c UpdateLoadBalancerCommand) Validate() error {
	validStrategies := map[string]bool{
		"priority":      true,
		"round_robin":   true,
		"weighted_hash": true,
		"parallel":      true,
	}
	if !validStrategies[c.Strategy] {
		return fmt.Errorf("invalid strategy: %s", c.Strategy)
	}

	validKeySources := map[string]bool{
		"api_key":     true,
		"user_id":     true,
		"ip":          true,
		"correlation": true,
	}
	if !validKeySources[c.KeySource] {
		return fmt.Errorf("invalid key_source: %s", c.KeySource)
	}

	return nil
}

// ToJSON serializes a command to JSON.
func ToJSON(cmd commands.Command) ([]byte, error) {
	return json.Marshal(cmd)
}

// FromJSON deserializes a command from JSON based on command type.
func FromJSON(commandType string, data []byte) (commands.Command, error) {
	switch commandType {
	case "ChatCompletion":
		var cmd ChatCompletionCommand
		if err := json.Unmarshal(data, &cmd); err != nil {
			return nil, err
		}
		return cmd, nil
	case "Embeddings":
		var cmd ProcessEmbeddingCommand
		if err := json.Unmarshal(data, &cmd); err != nil {
			return nil, err
		}
		return cmd, nil
	case "ConfigureModel":
		var cmd ConfigureModelCommand
		if err := json.Unmarshal(data, &cmd); err != nil {
			return nil, err
		}
		return cmd, nil
	case "UpdateLoadBalancer":
		var cmd UpdateLoadBalancerCommand
		if err := json.Unmarshal(data, &cmd); err != nil {
			return nil, err
		}
		return cmd, nil
	default:
		return nil, fmt.Errorf("unknown command type: %s", commandType)
	}
}
