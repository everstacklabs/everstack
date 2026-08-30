package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/google/uuid"
)

// AskUserHandler is a synthetic tool that lets the LLM ask the user a question
// mid-conversation and block until the user responds (or a timeout fires).
type AskUserHandler struct {
	Emitter    *agentrt.Emitter
	SessionID  string
	RequestCh  chan<- agentrt.UserInputRequest
	ResponseCh <-chan agentrt.UserInputResponse
}

func (h *AskUserHandler) Name() string { return "ask_user" }

func (h *AskUserHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "ask_user",
			Description: "Ask the user a question and wait for their response. Choose the interaction style that best fits the missing information: use text input for open-ended answers, single_select when the user should pick one option, and multi_select when the user may choose several options. Never ask clarifying questions in plain assistant text — if you need the user's answer before continuing, call ask_user instead. Do NOT use this to confirm actions, ask for permission, or seek validation; just proceed with the best approach when a reasonable default exists.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"question": map[string]interface{}{
						"type":        "string",
						"description": "The question to ask the user.",
					},
					"input_type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"text", "single_select", "multi_select"},
						"description": "How the UI should collect the answer. Use text for open-ended input, single_select for one choice, or multi_select for several choices.",
					},
					"options": map[string]interface{}{
						"type":        "array",
						"description": "Optional structured choices for the user. Best used with single_select or multi_select.",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"label": map[string]interface{}{
									"type": "string",
								},
								"value": map[string]interface{}{
									"type": "string",
								},
								"description": map[string]interface{}{
									"type": "string",
								},
							},
							"required": []string{"label"},
						},
					},
					"allow_custom_response": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether the user may provide a custom answer in addition to or instead of the listed options.",
					},
					"placeholder": map[string]interface{}{
						"type":        "string",
						"description": "Optional placeholder text for the text input field.",
					},
					"min_selections": map[string]interface{}{
						"type":        "integer",
						"description": "Optional minimum selections for multi_select.",
					},
					"max_selections": map[string]interface{}{
						"type":        "integer",
						"description": "Optional maximum selections for multi_select.",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "How long to wait for the user's response in seconds. Default: 300 (5 minutes).",
					},
				},
				"required": []string{"question"},
			},
		},
	}
}

func (h *AskUserHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	question, _ := args["question"].(string)
	question = strings.TrimSpace(question)
	if question == "" {
		return "", fmt.Errorf("question is required")
	}

	inputType := parseStringArg(args, "input_type")
	if inputType == "" {
		inputType = "text"
	}
	if inputType != "text" && inputType != "single_select" && inputType != "multi_select" {
		inputType = "text"
	}

	options := parseOptionsArg(args["options"])
	allowCustom := parseBoolArg(args, "allow_custom_response")
	placeholder := strings.TrimSpace(parseStringArg(args, "placeholder"))
	minSelections := parseIntArg(args, "min_selections")
	maxSelections := parseIntArg(args, "max_selections")
	if inputType == "text" && len(options) > 0 {
		inputType = "single_select"
	}
	if inputType == "multi_select" && minSelections <= 0 {
		minSelections = 1
	}
	if maxSelections > 0 && minSelections > maxSelections {
		minSelections = maxSelections
	}

	// Parse timeout (default 300s / 5 min)
	timeoutSec := 300
	if ts, ok := args["timeout_seconds"].(float64); ok && ts > 0 {
		timeoutSec = int(ts)
	}

	inputID := uuid.New().String()

	// Emit user_input.requested event so the frontend renders the input card
	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:        agentrt.EventUserInputRequested,
			SessionID:   h.SessionID,
			Timestamp:   time.Now(),
			UserInputID: inputID,
			Data: map[string]interface{}{
				"input_id":              inputID,
				"question":              question,
				"input_type":            inputType,
				"options":               optionsToEventData(options),
				"allow_custom_response": allowCustom,
				"placeholder":           placeholder,
				"min_selections":        minSelections,
				"max_selections":        maxSelections,
				"timeout_seconds":       timeoutSec,
			},
		})
	}

	// Send request to SessionManager for gate registration
	req := agentrt.UserInputRequest{
		InputID:             inputID,
		SessionID:           h.SessionID,
		Question:            question,
		InputType:           inputType,
		Options:             options,
		AllowCustomResponse: allowCustom,
		Placeholder:         placeholder,
		MinSelections:       minSelections,
		MaxSelections:       maxSelections,
		TimeoutSec:          timeoutSec,
	}
	select {
	case h.RequestCh <- req:
	case <-ctx.Done():
		if h.Emitter != nil {
			h.Emitter.Emit(agentrt.Event{
				Type:        agentrt.EventUserInputCancelled,
				SessionID:   h.SessionID,
				Timestamp:   time.Now(),
				UserInputID: inputID,
				Reason:      "context_cancelled",
			})
		}
		return "", ctx.Err()
	}

	// Block waiting for user response, timeout, or cancellation
	timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
	defer timer.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			if h.Emitter != nil {
				h.Emitter.Emit(agentrt.Event{
					Type:        agentrt.EventUserInputCancelled,
					SessionID:   h.SessionID,
					Timestamp:   time.Now(),
					UserInputID: inputID,
					Reason:      "context_cancelled",
				})
			}
			return "", ctx.Err()

		case resp := <-h.ResponseCh:
			if h.Emitter != nil {
				h.Emitter.Emit(agentrt.Event{
					Type:        agentrt.EventUserInputReceived,
					SessionID:   h.SessionID,
					Timestamp:   time.Now(),
					UserInputID: inputID,
					Data: map[string]interface{}{
						"response": resp.Text,
					},
				})
			}
			answer := strings.TrimSpace(resp.Text)
			if answer == "" {
				answer = "User submitted an empty response. Continue with the best reasonable default if possible."
			} else {
				answer = agentrt.WrapUserAnswer(answer)
			}
			return answer, nil

		case <-timer.C:
			if h.Emitter != nil {
				h.Emitter.Emit(agentrt.Event{
					Type:        agentrt.EventUserInputCancelled,
					SessionID:   h.SessionID,
					Timestamp:   time.Now(),
					UserInputID: inputID,
					Reason:      "timeout",
				})
			}
			return "User did not respond within the timeout period.", nil

		case <-heartbeat.C:
			if h.Emitter != nil {
				h.Emitter.Emit(agentrt.Event{
					Type:        agentrt.EventUserInputHeartbeat,
					SessionID:   h.SessionID,
					Timestamp:   time.Now(),
					UserInputID: inputID,
				})
			}
		}
	}
}

func parseStringArg(args map[string]interface{}, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func parseBoolArg(args map[string]interface{}, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func parseIntArg(args map[string]interface{}, key string) int {
	if ts, ok := args[key].(float64); ok {
		return int(ts)
	}
	return 0
}

func parseOptionsArg(raw interface{}) []agentrt.UserInputOption {
	optRaw, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	seen := make(map[string]struct{}, len(optRaw))
	options := make([]agentrt.UserInputOption, 0, len(optRaw))
	for _, item := range optRaw {
		switch v := item.(type) {
		case string:
			label := strings.TrimSpace(v)
			if label == "" {
				continue
			}
			key := strings.ToLower(label)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			options = append(options, agentrt.UserInputOption{Label: label, Value: label})
		case map[string]interface{}:
			label, _ := v["label"].(string)
			label = strings.TrimSpace(label)
			if label == "" {
				continue
			}
			value, _ := v["value"].(string)
			value = strings.TrimSpace(value)
			if value == "" {
				value = label
			}
			description, _ := v["description"].(string)
			key := strings.ToLower(value)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			options = append(options, agentrt.UserInputOption{Label: label, Value: value, Description: strings.TrimSpace(description)})
		}
	}
	return options
}

func optionsToEventData(options []agentrt.UserInputOption) []map[string]interface{} {
	if len(options) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(options))
	for _, option := range options {
		out = append(out, map[string]interface{}{
			"label":       option.Label,
			"value":       option.Value,
			"description": option.Description,
		})
	}
	return out
}
