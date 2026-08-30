package compact

import (
	"context"
	"fmt"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Summarizer folds a slice of messages into a single short summary
// string by calling an LLM. Decoupled from any specific provider
// router — takes a gw.ChatProvider (the smallest interface that
// satisfies our needs) so the same type works in the gateway
// middleware (where the router is wired in at startup) and in the
// agent runtime's Monitor (where the engine resolves providers via
// ResolveProvider, which returns a ChatProvider).
type Summarizer struct {
	provider gw.ChatProvider
	model    string
}

// NewSummarizer constructs a Summarizer over the given provider and
// model name. The model must be one the provider can serve.
func NewSummarizer(provider gw.ChatProvider, model string) *Summarizer {
	return &Summarizer{provider: provider, model: model}
}

// truncateString shortens long values inside the transcript so the
// summarisation prompt itself doesn't blow the context window. Same
// helper that lived in internal/agents/runtime/job_queue.go;
// duplicated here intentionally to keep this package free of agent
// dependencies.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// strPtr is a one-liner used to satisfy gw.ContentPart.Text which is
// *string. Kept package-private.
func strPtr(s string) *string { return &s }

// Summarize calls the configured provider's Chat method with a
// summarisation prompt covering the supplied messages and returns the
// summary text. Errors propagate so callers can fall back (e.g. drop
// to truncation) rather than silently emitting bogus context.
func (s *Summarizer) Summarize(ctx context.Context, messages []gw.Message) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("compact: summarizer provider is nil")
	}
	if s.model == "" {
		return "", fmt.Errorf("compact: summarizer model is empty")
	}
	if len(messages) == 0 {
		return "", nil
	}

	transcript := buildTranscript(messages)

	summaryPrompt := fmt.Sprintf(`Summarize the following conversation excerpt concisely, preserving all important facts, decisions, tool results, and context needed for continuing the conversation. Focus on what was done, what was decided, and any important outputs.

Conversation:
%s

Summary:`, transcript)

	req := gw.ChatCompletionRequest{
		Model: s.model,
		Messages: []gw.Message{
			{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(summaryPrompt)}},
			},
		},
		Sampling: gw.SamplingParams{
			Temperature: 0.3,
			MaxTokens:   1000,
		},
	}

	resp, err := s.provider.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("compact: summarisation LLM call failed: %w", err)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.Content) == 0 {
		return "", fmt.Errorf("compact: summarisation produced no output")
	}
	if resp.Choices[0].Message.Content[0].Text == nil {
		return "", fmt.Errorf("compact: summarisation produced no text output")
	}
	return *resp.Choices[0].Message.Content[0].Text, nil
}

// buildTranscript renders a list of messages into a flat string the
// summariser model can read. Tool calls and tool results get their own
// lines so the summary preserves them as discrete events.
func buildTranscript(messages []gw.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		role := string(msg.Role)
		for _, part := range msg.Content {
			if part.Text != nil {
				b.WriteString(fmt.Sprintf("[%s]: %s\n", role, *part.Text))
			}
		}
		for _, tc := range msg.ToolCalls {
			b.WriteString(fmt.Sprintf(
				"[tool_call %s]: %s(%s)\n",
				tc.ID, tc.Function.Name, truncateString(tc.Function.Arguments, 200),
			))
		}
		if msg.ToolCallID != "" {
			for _, part := range msg.Content {
				if part.Text != nil {
					b.WriteString(fmt.Sprintf(
						"[tool_result %s]: %s\n",
						msg.ToolCallID, truncateString(*part.Text, 500),
					))
				}
			}
		}
	}
	return b.String()
}
