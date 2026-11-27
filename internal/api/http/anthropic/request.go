// Package anthropic translates the Messages wire protocol onto the existing
// authenticated OpenAI chat completion handler.
package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

type object = map[string]any

type request struct {
	Model       string          `json:"model"`
	MaxTokens   *int            `json:"max_tokens"`
	Temperature *float64        `json:"temperature"`
	TopP        *float64        `json:"top_p"`
	Stop        []string        `json:"stop_sequences"`
	Stream      bool            `json:"stream"`
	System      json.RawMessage `json:"system"`
	Messages    []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Schema      json.RawMessage `json:"input_schema"`
	} `json:"tools"`
	ToolChoice *struct {
		Type            string `json:"type"`
		Name            string `json:"name"`
		DisableParallel bool   `json:"disable_parallel_tool_use"`
	} `json:"tool_choice"`
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
}
type block struct {
	IsError   bool            `json:"is_error"`
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	Source    struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

func blocks(raw json.RawMessage) ([]block, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []block{{Type: "text", Text: text}}, nil
	}
	var out []block
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &out) != nil {
		return nil, fmt.Errorf("content must be a string or array of blocks")
	}
	return out, nil
}
func textBlocks(raw json.RawMessage) (string, error) {
	bs, err := blocks(raw)
	if err != nil {
		return "", err
	}
	texts := make([]string, 0, len(bs))
	for _, b := range bs {
		if b.Type != "text" {
			return "", fmt.Errorf("unsupported text block type: %s", b.Type)
		}
		texts = append(texts, b.Text)
	}
	return strings.Join(texts, "\n\n"), nil
}
func translateRequest(raw []byte) ([]byte, bool, error) {
	var in request
	if json.Unmarshal(raw, &in) != nil {
		return nil, false, fmt.Errorf("invalid JSON request")
	}
	if in.MaxTokens == nil || *in.MaxTokens <= 0 {
		return nil, false, fmt.Errorf("max_tokens is required and must be positive")
	}
	if in.Model == "" || len(in.Messages) == 0 {
		return nil, false, fmt.Errorf("model and messages are required")
	}
	out := object{"model": in.Model, "max_tokens": *in.MaxTokens, "stream": in.Stream}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if in.Stop != nil {
		out["stop"] = in.Stop
	}
	if in.Metadata.UserID != "" {
		out["user"] = in.Metadata.UserID
	}
	messages := []object{}
	if len(in.System) > 0 && string(in.System) != "null" {
		text, err := textBlocks(in.System)
		if err != nil {
			return nil, false, err
		}
		messages = append(messages, object{"role": "system", "content": text})
	}
	for _, m := range in.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return nil, false, fmt.Errorf("unsupported message role: %s", m.Role)
		}
		bs, err := blocks(m.Content)
		if err != nil {
			return nil, false, err
		}
		parts := []object{}
		calls := []object{}
		for _, b := range bs {
			switch b.Type {
			case "text":
				parts = append(parts, object{"type": "text", "text": b.Text})
			case "image":
				url := b.Source.URL
				switch b.Source.Type {
				case "base64":
					url = "data:" + b.Source.MediaType + ";base64," + b.Source.Data
				case "url":
				default:
					return nil, false, fmt.Errorf("unsupported image source type: %s", b.Source.Type)
				}
				parts = append(parts, object{"type": "image_url", "image_url": object{"url": url}})
			case "tool_use":
				if m.Role != "assistant" {
					return nil, false, fmt.Errorf("tool_use requires assistant role")
				}
				args := b.Input
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				calls = append(calls, object{"id": b.ID, "type": "function", "function": object{"name": b.Name, "arguments": string(args)}})
			case "tool_result":
				if m.Role != "user" {
					return nil, false, fmt.Errorf("tool_result requires user role")
				}
				text, err := toolResultText(b.Content)
				if err != nil {
					return nil, false, err
				}
				if b.IsError && !strings.HasPrefix(strings.ToLower(text), "error: ") {
					text = "Error: " + text
				}
				messages = append(messages, object{"role": "tool", "tool_call_id": b.ToolUseID, "content": text})
			case "thinking", "redacted_thinking":
				if m.Role != "assistant" {
					return nil, false, fmt.Errorf("%s requires assistant role", b.Type)
				}
			case "document":
				return nil, false, fmt.Errorf("document content is not supported")
			default:
				return nil, false, fmt.Errorf("unsupported content block type: %s", b.Type)
			}
		}
		if len(parts) > 0 || len(calls) > 0 {
			msg := object{"role": m.Role, "content": parts}
			if len(calls) > 0 {
				msg["tool_calls"] = calls
			}
			messages = append(messages, msg)
		}
	}
	out["messages"] = messages
	if in.Tools != nil {
		tools := []object{}
		for _, t := range in.Tools {
			if t.Type != "" && t.Type != "custom" {
				return nil, false, fmt.Errorf("server tool type is not supported: %s", t.Type)
			}
			tools = append(tools, object{"type": "function", "function": object{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		out["tools"] = tools
	}
	if tc := in.ToolChoice; tc != nil {
		switch tc.Type {
		case "auto", "none":
			out["tool_choice"] = tc.Type
		case "any":
			out["tool_choice"] = "required"
		case "tool":
			out["tool_choice"] = object{"type": "function", "function": object{"name": tc.Name}}
		default:
			return nil, false, fmt.Errorf("unsupported tool_choice: %s", tc.Type)
		}
		if tc.DisableParallel {
			out["parallel_tool_calls"] = false
		}
	}
	b, err := json.Marshal(out)
	return b, in.Stream, err
}

// Images in tool results are rejected explicitly; user-turn images are supported.
func toolResultText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	bs, err := blocks(raw)
	if err != nil {
		return "", err
	}
	texts := []string{}
	for _, b := range bs {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "image":
			return "", fmt.Errorf("images in tool results are not supported")
		default:
			return "", fmt.Errorf("unsupported tool result block type: %s", b.Type)
		}
	}
	return strings.Join(texts, "\n"), nil
}
