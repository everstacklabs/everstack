package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// PlanningMode controls whether the planner runs before agent execution.
type PlanningMode string

const (
	PlanningModeOff PlanningMode = "off"
	PlanningModeOn  PlanningMode = "on"
)

// SpawnConfig holds spawn limits adjusted dynamically by the planner.
// This mirrors tools.SpawnConfig to avoid circular imports.
type SpawnConfig struct {
	Enabled          bool          `json:"enabled"`
	MaxDepth         int           `json:"maxDepth"`
	MaxTotalSpawns   int           `json:"maxTotalSpawns"`
	ChildTimeout     time.Duration `json:"childTimeout"`
	TotalTokenBudget int           `json:"totalTokenBudget"`
	AllowedAgents    []string      `json:"allowedAgents"`
}

// SpawnPlan is the output of the task planner — a structured decomposition
// of a complex task into sub-agents with execution strategy.
type SpawnPlan struct {
	Strategy       string         `json:"strategy"`        // single, parallel, sequential, pipeline
	SubAgents      []PlannedAgent `json:"sub_agents"`
	AdjustedConfig SpawnConfig    `json:"adjusted_config"` // dynamic limits based on task
	Reasoning      string         `json:"reasoning"`       // LLM's reasoning (for observability)
}

// PlannedAgent describes a dynamically-planned sub-agent.
type PlannedAgent struct {
	Role         string   `json:"role"`          // e.g., "researcher", "coder", "reviewer"
	Task         string   `json:"task"`          // specific task description
	SystemPrompt string   `json:"system_prompt"` // generated system prompt
	Model        string   `json:"model"`         // model selection
	Tools        []string `json:"tools"`         // which tools this sub-agent needs
	DependsOn    []string `json:"depends_on"`    // advisory sequencing hints
}

// TaskPlanner uses an LLM to decompose complex tasks into spawn plans.
type TaskPlanner struct {
	registry *gw.Registry
	router   *gw.Router
}

// NewTaskPlanner creates a new TaskPlanner.
func NewTaskPlanner(registry *gw.Registry, router *gw.Router) *TaskPlanner {
	return &TaskPlanner{
		registry: registry,
		router:   router,
	}
}

// PlannerConfig holds configuration for the planner.
type PlannerConfig struct {
	PlanningMode     PlanningMode `json:"planning_mode"`
	PlannerModel     string       `json:"planner_model"`      // e.g., "gpt-4o-mini"
	MaxPlannedAgents int          `json:"max_planned_agents"` // max sub-agents to plan (default: 5)
}

// DefaultPlannerConfig returns sensible defaults.
func DefaultPlannerConfig() PlannerConfig {
	return PlannerConfig{
		PlanningMode:     PlanningModeOff,
		PlannerModel:     "gpt-4o-mini",
		MaxPlannedAgents: 5,
	}
}

// ParsePlannerConfig extracts planner config from agent config map.
func ParsePlannerConfig(config map[string]interface{}) PlannerConfig {
	pc := DefaultPlannerConfig()

	spawn, ok := config["spawn"].(map[string]interface{})
	if !ok {
		return pc
	}

	if mode, ok := spawn["planning_mode"].(string); ok {
		switch mode {
		case "on":
			pc.PlanningMode = PlanningModeOn
		default:
			pc.PlanningMode = PlanningModeOff
		}
	}

	if model, ok := spawn["planner_model"].(string); ok && model != "" {
		pc.PlannerModel = model
	}

	if max, ok := spawn["max_planned_agents"].(float64); ok && max > 0 && max <= 20 {
		pc.MaxPlannedAgents = int(max)
	}

	return pc
}

// Plan analyzes a task and produces a SpawnPlan with sub-agent decomposition.
// It makes one fast LLM call to a lightweight model (e.g., gpt-4o-mini).
func (tp *TaskPlanner) Plan(ctx context.Context, task string, availableAgents []AgentCatalogEntry, availableTools []string, config PlannerConfig, emitter *Emitter, sessionID string) (*SpawnPlan, error) {
	startTime := time.Now()

	// Emit plan.start event
	if emitter != nil {
		emitter.Emit(Event{
			Type:      EventPlanStart,
			SessionID: sessionID,
			Timestamp: startTime,
			Data:      map[string]interface{}{"task": task, "planner_model": config.PlannerModel},
		})
	}

	// Build the planner system prompt
	systemPrompt := buildPlannerSystemPrompt(availableAgents, availableTools, config.MaxPlannedAgents)

	// Make the LLM call
	plan, err := tp.callPlannerLLM(ctx, config.PlannerModel, systemPrompt, task)
	if err != nil {
		return nil, fmt.Errorf("planner LLM call: %w", err)
	}

	// Clamp sub-agents to max
	if len(plan.SubAgents) > config.MaxPlannedAgents {
		plan.SubAgents = plan.SubAgents[:config.MaxPlannedAgents]
	}

	// Adjust spawn config based on plan
	plan.AdjustedConfig = SpawnConfig{
		Enabled:          true,
		MaxDepth:         3,
		MaxTotalSpawns:   len(plan.SubAgents) + 2, // headroom
		ChildTimeout:     5 * time.Minute,
		TotalTokenBudget: 200000,
	}

	// Emit plan.complete event
	if emitter != nil {
		emitter.Emit(Event{
			Type:      EventPlanComplete,
			SessionID: sessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"strategy":    plan.Strategy,
				"sub_agents":  len(plan.SubAgents),
				"reasoning":   plan.Reasoning,
				"duration_ms": time.Since(startTime).Milliseconds(),
			},
		})
	}

	logger.WithFields(
		"session_id", sessionID,
		"strategy", plan.Strategy,
		"sub_agents", len(plan.SubAgents),
		"duration_ms", time.Since(startTime).Milliseconds(),
	).Info("planner: task decomposition complete")

	return plan, nil
}

// AgentCatalogEntry describes an available agent for the planner.
type AgentCatalogEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
	Model       string   `json:"model"`
}

func buildPlannerSystemPrompt(agents []AgentCatalogEntry, tools []string, maxAgents int) string {
	agentsJSON, _ := json.Marshal(agents)
	toolsJSON, _ := json.Marshal(tools)

	return fmt.Sprintf(`You are a task planning agent. Your job is to analyze a user's request and determine the best execution strategy.

## Available Agents
%s

## Available Tools
%s

## Instructions
1. Analyze the task complexity
2. If the task is simple (single-step, single-domain), return strategy "single" with no sub_agents
3. If the task requires multiple steps or domains, decompose into sub-agents (max %d)
4. For each sub-agent, specify: role, task, system_prompt, model, tools, depends_on
5. Choose a strategy: "single" (no decomposition), "parallel" (independent tasks), "sequential" (dependent tasks), "pipeline" (output of one feeds into next)

## Response Format
Respond with valid JSON matching this schema:
{
  "strategy": "single|parallel|sequential|pipeline",
  "sub_agents": [
    {
      "role": "string (short identifier like 'researcher', 'coder')",
      "task": "string (specific task description)",
      "system_prompt": "string (system prompt for this sub-agent)",
      "model": "string (model to use)",
      "tools": ["tool1", "tool2"],
      "depends_on": ["role_name"]
    }
  ],
  "reasoning": "string (brief explanation of your decomposition strategy)"
}`, string(agentsJSON), string(toolsJSON), maxAgents)
}

func (tp *TaskPlanner) callPlannerLLM(ctx context.Context, model, systemPrompt, task string) (*SpawnPlan, error) {
	sysText := systemPrompt
	taskText := task
	req := gw.ChatCompletionRequest{
		Model: model,
		Messages: []gw.Message{
			{Role: gw.RoleSystem, Content: []gw.ContentPart{{Type: "text", Text: &sysText}}},
			{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: &taskText}}},
		},
		Sampling: gw.SamplingParams{
			MaxTokens:   2048,
			Temperature: 0.0,
		},
		Metadata: map[string]interface{}{
			"response_format": map[string]string{"type": "json_object"},
		},
	}

	resp, err := gw.HandleChat(ctx, tp.router, req)
	if err != nil {
		return nil, fmt.Errorf("route chat: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("planner: empty response from LLM")
	}

	content := extractTextContent(resp.Choices[0].Message)
	if content == "" {
		return nil, fmt.Errorf("planner: no text content in response")
	}

	var plan SpawnPlan
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return nil, fmt.Errorf("parse plan: %w (content: %s)", err, content)
	}

	return &plan, nil
}

func extractTextContent(msg gw.Message) string {
	for _, part := range msg.Content {
		if part.Type == "text" && part.Text != nil && *part.Text != "" {
			return *part.Text
		}
	}
	return ""
}
