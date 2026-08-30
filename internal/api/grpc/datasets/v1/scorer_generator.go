package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
)

// GenerateScorer asks Claude (via the gateway) to draft an LLM-judge
// prompt for the user's evaluation intent. Returns a structured suggestion
// the caller can edit and save via CreateScoreConfig.
//
// Direct attack on Braintrust's "Generate scorer" wedge. Langfuse has no
// equivalent. Meta-prompt approach below: we ask Claude to itself emit
// the inner prompt as JSON, parsed and surfaced through the response.
func (s *EvalServer) GenerateScorer(
	ctx context.Context,
	req *connect.Request[datasetspb.GenerateScorerRequest],
) (*connect.Response[datasetspb.GenerateScorerResponse], error) {
	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("tenant not resolved"))
	}
	intent := strings.TrimSpace(req.Msg.GetIntent())
	if intent == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("intent is required"))
	}
	dataType := strings.ToLower(req.Msg.GetDataType())
	if dataType == "" {
		dataType = "numeric"
	}

	metaPrompt := buildScorerMetaPrompt(intent, dataType, req.Msg.GetExamples())

	// Talk to the gateway exactly like eval_runner does — same auth
	// pattern, same tenant headers.
	content, err := generateChatCompletionContent(ctx, tenantID, map[string]interface{}{
		"model": defaultScorerModel(),
		"messages": []map[string]interface{}{
			{"role": "system", "content": scorerSystemPrompt()},
			{"role": "user", "content": metaPrompt},
		},
		"stream": false,
		"response_format": map[string]interface{}{
			"type": "json_object",
		},
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	suggestion, err := parseScorerSuggestion(content)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("parse model output: %w", err))
	}

	// Pin the requested data_type if Claude didn't echo it back.
	if suggestion.DataType == "" {
		suggestion.DataType = dataType
	}

	return connect.NewResponse(&datasetspb.GenerateScorerResponse{
		SuggestedName:       suggestion.SuggestedName,
		Prompt:              suggestion.Prompt,
		DataType:            suggestion.DataType,
		SuggestedCategories: suggestion.SuggestedCategories,
		Notes:               suggestion.Notes,
	}), nil
}

type scorerSuggestion struct {
	SuggestedName       string   `json:"suggested_name"`
	Prompt              string   `json:"prompt"`
	DataType            string   `json:"data_type"`
	SuggestedCategories []string `json:"suggested_categories"`
	Notes               string   `json:"notes"`
}

func parseScorerSuggestion(content string) (scorerSuggestion, error) {
	content = stripJSONCodeFence(content)

	var out scorerSuggestion
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return out, err
	}
	if out.Prompt == "" {
		return out, fmt.Errorf("model response missing prompt field")
	}
	return out, nil
}

func extractScorerContent(respBody []byte) string {
	var respMap map[string]interface{}
	if err := json.Unmarshal(respBody, &respMap); err != nil {
		return ""
	}
	choices, _ := respMap["choices"].([]interface{})
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]interface{})
	if choice == nil {
		return ""
	}
	msg, _ := choice["message"].(map[string]interface{})
	if msg == nil {
		return ""
	}
	if s, ok := msg["content"].(string); ok {
		return s
	}
	return ""
}

func scorerSystemPrompt() string {
	return `You are an expert evaluation-prompt engineer. The user wants to evaluate LLM outputs and will describe what they want to measure. Reply ONLY with a JSON object (no markdown fences, no preamble) with these fields:

  "suggested_name": short slug identifying the scorer (e.g. "politeness", "factuality"). Lowercase, snake_case, no spaces.
  "prompt":         The LLM-judge prompt template. Must use placeholders {input}, {output}, {expected_output}, {context} where appropriate. Must instruct the model to reply with JSON {"score": <value>, "reason": "..."} matching the data_type.
  "data_type":      One of "numeric" (0..1 float), "categorical" (one of suggested_categories), or "boolean" (true|false).
  "suggested_categories": REQUIRED if data_type is "categorical", else [].
  "notes":          Anything ambiguous about the user's intent, the examples, or rubric edge cases. Empty string if none.

Write the prompt so a competent LLM can grade outputs deterministically. Make the scoring rubric explicit (what earns each score). Cite the user's examples in your reasoning if they help.`
}

func buildScorerMetaPrompt(intent, dataType string, examples []*datasetspb.GenerateScorerExample) string {
	var b strings.Builder
	b.WriteString("Evaluation intent:\n")
	b.WriteString(intent)
	b.WriteString("\n\nTarget data_type: ")
	b.WriteString(dataType)
	b.WriteString("\n")
	if len(examples) > 0 {
		b.WriteString("\nExamples (input / output / desired label):\n")
		for i, ex := range examples {
			b.WriteString(fmt.Sprintf("\nExample %d:\n  input:  %s\n  output: %s\n  label:  %s\n", i+1, ex.GetInput(), ex.GetOutput(), ex.GetLabel()))
		}
	} else {
		b.WriteString("\n(No examples supplied — base the rubric on the intent alone.)\n")
	}
	b.WriteString("\nReturn the JSON object now.")
	return b.String()
}

func scorerGenGatewayURL() string {
	if v := os.Getenv("MF_EVAL_RUNNER_GATEWAY_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:8080"
}

func defaultScorerModel() string {
	if v := os.Getenv("MF_SCORER_GEN_MODEL"); v != "" {
		return v
	}
	// Claude 4 family default — meta-prompting is reasoning-heavy.
	return "@anthropic/claude-sonnet-4-6"
}
