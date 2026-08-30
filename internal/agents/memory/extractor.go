package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
)

// extractionResult is the JSON structure returned by the extraction LLM call.
type extractionResult struct {
	Facts        []extractedFact        `json:"facts"`
	Instructions []extractedInstruction `json:"instructions"`
}

type extractedFact struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Scope      string  `json:"scope"` // "user", "agent", or "global". Defaults to "agent".
}

type extractedInstruction struct {
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
	Scope      string  `json:"scope"` // "user", "agent", or "global". Defaults to "agent".
}

// Extractor uses an LLM to extract facts and instructions from conversation turns.
type Extractor struct {
	store  Store
	router *gw.Router
	model  string // model to use for extraction (should be fast/cheap)
}

// NewExtractor creates a new memory extractor.
func NewExtractor(store Store, router *gw.Router, model string) *Extractor {
	return &Extractor{
		store:  store,
		router: router,
		model:  model,
	}
}

// inferScope returns a valid MemoryScope, defaulting to agent if empty/unknown.
func inferScope(raw string) MemoryScope {
	switch MemoryScope(raw) {
	case MemoryScopeUser:
		return MemoryScopeUser
	case MemoryScopeGlobal:
		return MemoryScopeGlobal
	default:
		return MemoryScopeAgent
	}
}

// Extract analyzes a conversation turn and persists extracted facts/instructions.
// userID is optional — when provided, user-scoped memories will be associated with this user.
func (e *Extractor) Extract(ctx context.Context, agentID, tenantID, sessionID, userInput, assistantOutput string, turnNumber int32, userID *string) error {
	if strings.TrimSpace(userInput) == "" {
		return nil
	}

	ctx, span := telemetry.StartMemorySpan(ctx, "extract", agentID)
	defer span.End()
	span.SetAttributes(attribute.Int(attrs.AgentTurnNumber, int(turnNumber)))

	// Build the extraction prompt
	userMsg := strings.ReplaceAll(extractionUserTemplate, "{{.UserInput}}", userInput)
	userMsg = strings.ReplaceAll(userMsg, "{{.AssistantOutput}}", assistantOutput)

	resp, err := e.callLLM(ctx, extractionPrompt, userMsg)
	if err != nil {
		telemetry.RecordError(span, err)
		return fmt.Errorf("extraction LLM call: %w", err)
	}

	// Parse response
	var result extractionResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		logger.WithFields(
			"agent_id", agentID,
			"response", resp,
			"error", err.Error(),
		).Warn("memory extractor: failed to parse LLM response")
		return nil // non-fatal — extraction is best-effort
	}

	// Persist extracted facts
	sessionIDPtr := &sessionID
	turnNumberPtr := &turnNumber
	var memories []*AgentMemory

	for _, f := range result.Facts {
		if f.Key == "" || f.Value == "" {
			continue
		}
		if f.Confidence < 0.3 {
			continue
		}

		scope := inferScope(f.Scope)

		// Check if fact already exists — supersede if so
		existing, err := e.store.FindByFactKey(ctx, tenantID, agentID, f.Key)
		if err != nil {
			logger.WithFields("agent_id", agentID, "fact_key", f.Key, "error", err.Error()).
				Warn("memory extractor: failed to check existing fact")
		}

		m := &AgentMemory{
			TenantID:         tenantID,
			AgentID:          agentID,
			Scope:            scope,
			MemoryType:       MemoryTypeFact,
			Content:          f.Value,
			FactKey:          &f.Key,
			Confidence:       f.Confidence,
			Source:           MemorySourceAutoExtracted,
			SourceSessionID:  sessionIDPtr,
			SourceTurnNumber: turnNumberPtr,
			IsActive:         true,
		}

		// Assign userID for user-scoped memories
		if scope == MemoryScopeUser && userID != nil {
			m.UserID = userID
		}

		memories = append(memories, m)

		// If existing fact found, we'll supersede it after saving the new one
		if existing != nil {
			// Save now to get ID, then supersede
			if err := e.store.Save(ctx, m); err != nil {
				logger.WithFields("agent_id", agentID, "error", err.Error()).
					Warn("memory extractor: failed to save fact")
				continue
			}
			if err := e.store.Supersede(ctx, tenantID, existing.ID, m.ID); err != nil {
				logger.WithFields("agent_id", agentID, "error", err.Error()).
					Warn("memory extractor: failed to supersede old fact")
			}
			// Remove from batch since we already saved
			memories = memories[:len(memories)-1]
		}
	}

	for _, i := range result.Instructions {
		if i.Content == "" || i.Confidence < 0.3 {
			continue
		}

		// Deduplicate: skip if an identical active instruction already exists
		existing, err := e.store.FindByContent(ctx, tenantID, agentID, MemoryTypeInstruction, i.Content)
		if err != nil {
			logger.WithFields("agent_id", agentID, "error", err.Error()).
				Warn("memory extractor: failed to check existing instruction")
		}
		if existing != nil {
			continue
		}

		scope := inferScope(i.Scope)

		m := &AgentMemory{
			TenantID:         tenantID,
			AgentID:          agentID,
			Scope:            scope,
			MemoryType:       MemoryTypeInstruction,
			Content:          i.Content,
			Confidence:       i.Confidence,
			Source:           MemorySourceAutoExtracted,
			SourceSessionID:  sessionIDPtr,
			SourceTurnNumber: turnNumberPtr,
			IsActive:         true,
		}

		if scope == MemoryScopeUser && userID != nil {
			m.UserID = userID
		}

		memories = append(memories, m)
	}

	if len(memories) > 0 {
		if err := e.store.SaveBatch(ctx, memories); err != nil {
			return fmt.Errorf("save extracted memories: %w", err)
		}
	}

	logger.WithFields(
		"agent_id", agentID,
		"session_id", sessionID,
		"turn", turnNumber,
		"facts", len(result.Facts),
		"instructions", len(result.Instructions),
	).Info("memory extractor: extracted memories from turn")

	return nil
}

// Summarize creates a session summary from the full conversation.
// It skips trivial sessions (fewer than 2 user messages) and upserts
// so that only one active summary exists per session.
func (e *Extractor) Summarize(ctx context.Context, agentID, tenantID, sessionID string, messages []gw.Message) error {
	// Build conversation text from messages, counting user messages
	var sb strings.Builder
	userMsgCount := 0
	for _, msg := range messages {
		if msg.Role == gw.RoleSystem {
			continue // skip system prompt
		}
		role := string(msg.Role)
		for _, part := range msg.Content {
			if part.Text != nil {
				sb.WriteString(fmt.Sprintf("[%s]: %s\n", role, *part.Text))
				if msg.Role == gw.RoleUser {
					userMsgCount++
				}
			}
		}
	}
	conversation := sb.String()
	if strings.TrimSpace(conversation) == "" {
		return nil
	}

	// Skip trivial sessions with fewer than 2 user messages
	if userMsgCount < 2 {
		return nil
	}

	// Truncate if too long (keep last 8000 chars)
	if len(conversation) > 8000 {
		conversation = conversation[len(conversation)-8000:]
	}

	resp, err := e.callLLM(ctx, summarizationPrompt, conversation)
	if err != nil {
		return fmt.Errorf("summarization LLM call: %w", err)
	}

	summary := strings.TrimSpace(resp)
	if summary == "" {
		return nil
	}

	sessionIDPtr := &sessionID
	m := &AgentMemory{
		TenantID:        tenantID,
		AgentID:         agentID,
		Scope:           MemoryScopeAgent,
		MemoryType:      MemoryTypeSessionSummary,
		Content:         summary,
		Confidence:      1.0,
		Source:          MemorySourceAutoExtracted,
		SourceSessionID: sessionIDPtr,
		IsActive:        true,
	}

	if err := e.store.UpsertSummary(ctx, m); err != nil {
		return fmt.Errorf("save session summary: %w", err)
	}

	logger.WithFields(
		"agent_id", agentID,
		"session_id", sessionID,
	).Info("memory extractor: upserted session summary")

	return nil
}

// callLLM makes a non-streaming chat completion call to the configured model.
func (e *Extractor) callLLM(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	provider, route, err := e.router.Resolve(e.model)
	if err != nil {
		return "", fmt.Errorf("resolve model %s: %w", e.model, err)
	}

	model := e.model
	if route.ModelName != "" {
		model = route.ModelName
	}

	resp, err := provider.Chat(ctx, gw.ChatCompletionRequest{
		Model: model,
		Messages: []gw.Message{
			{
				Role:    gw.RoleSystem,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(systemPrompt)}},
			},
			{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(userMessage)}},
			},
		},
		Sampling: gw.SamplingParams{
			MaxTokens: 1024,
		},
	})
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty response from model")
	}

	// Extract text from first choice
	for _, part := range resp.Choices[0].Message.Content {
		if part.Text != nil {
			return *part.Text, nil
		}
	}
	return "", fmt.Errorf("no text in response")
}

func strPtr(s string) *string { return &s }
