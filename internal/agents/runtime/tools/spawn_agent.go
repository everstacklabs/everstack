package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/projectruntime"
	"github.com/everstacklabs/everstack/internal/agents/revision"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agentskills "github.com/everstacklabs/everstack/internal/agents/skills"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// SpawnConfig controls sub-agent spawning behavior.
type SpawnConfig struct {
	Enabled           bool          `json:"enabled"`
	Async             bool          `json:"async"` // When true, spawn_agent returns immediately
	MaxDepth          int           `json:"maxDepth"`
	MaxTotalSpawns    int           `json:"maxTotalSpawns"`
	MaxConcurrentJobs int           `json:"maxConcurrentJobs"` // For async mode
	ChildTimeout      time.Duration `json:"childTimeout"`
	TotalTokenBudget  int           `json:"totalTokenBudget"`
	AllowedAgents     []string      `json:"allowedAgents"` // empty = allow all
}

// DefaultSpawnConfig returns the default spawn configuration.
func DefaultSpawnConfig() SpawnConfig {
	return SpawnConfig{
		Enabled:           false,
		Async:             false,
		MaxDepth:          3,
		MaxTotalSpawns:    10,
		MaxConcurrentJobs: 5,
		ChildTimeout:      2 * time.Minute,
		TotalTokenBudget:  100000,
	}
}

// ParseSpawnConfig extracts the spawn configuration from the agent config map.
func ParseSpawnConfig(config map[string]interface{}) SpawnConfig {
	cfg := DefaultSpawnConfig()
	if config == nil {
		return cfg
	}
	spawnRaw, ok := config["spawn"]
	if !ok {
		return cfg
	}
	spawnMap, ok := spawnRaw.(map[string]interface{})
	if !ok {
		return cfg
	}

	if enabled, ok := spawnMap["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if maxDepth, ok := spawnMap["maxDepth"].(float64); ok && maxDepth >= 1 && maxDepth <= 10 {
		cfg.MaxDepth = int(maxDepth)
	}
	if maxSpawns, ok := spawnMap["maxTotalSpawns"].(float64); ok && maxSpawns >= 1 && maxSpawns <= 100 {
		cfg.MaxTotalSpawns = int(maxSpawns)
	}
	if timeout, ok := spawnMap["childTimeout"].(float64); ok && timeout >= 30 && timeout <= 600 {
		cfg.ChildTimeout = time.Duration(timeout) * time.Second
	}
	if budget, ok := spawnMap["totalTokenBudget"].(float64); ok && budget >= 1000 {
		cfg.TotalTokenBudget = int(budget)
	}
	if async, ok := spawnMap["async"].(bool); ok {
		cfg.Async = async
	}
	if maxConcurrent, ok := spawnMap["maxConcurrentJobs"].(float64); ok && maxConcurrent >= 1 && maxConcurrent <= 50 {
		cfg.MaxConcurrentJobs = int(maxConcurrent)
	}
	if allowed, ok := spawnMap["allowedAgents"].([]interface{}); ok {
		for _, a := range allowed {
			if s, ok := a.(string); ok {
				cfg.AllowedAgents = append(cfg.AllowedAgents, s)
			}
		}
	}

	return cfg
}

// SpawnTracker tracks spawn tree state across parent and child agents.
type SpawnTracker struct {
	TreeID       string
	ParentNodeID *string // DB node ID of the parent spawn node (nil for root)
	CurrentDepth int
	SpawnCount   *atomic.Int32
	TokensUsed   *atomic.Int64
	Config       SpawnConfig
}

// NewSpawnTracker creates a new spawn tracker for the root agent.
func NewSpawnTracker(treeID string, config SpawnConfig) *SpawnTracker {
	return &SpawnTracker{
		TreeID:       treeID,
		CurrentDepth: 0,
		SpawnCount:   &atomic.Int32{},
		TokensUsed:   &atomic.Int64{},
		Config:       config,
	}
}

// CanSpawn checks if a new child agent can be spawned.
func (t *SpawnTracker) CanSpawn() error {
	if !t.Config.Enabled {
		return fmt.Errorf("sub-agent spawning is disabled")
	}
	if t.CurrentDepth >= t.Config.MaxDepth {
		return fmt.Errorf("maximum spawn depth reached (%d)", t.Config.MaxDepth)
	}
	if int(t.SpawnCount.Load()) >= t.Config.MaxTotalSpawns {
		return fmt.Errorf("maximum total spawns reached (%d)", t.Config.MaxTotalSpawns)
	}
	if t.Config.TotalTokenBudget > 0 && int(t.TokensUsed.Load()) >= t.Config.TotalTokenBudget {
		return fmt.Errorf("total token budget exhausted (%d)", t.Config.TotalTokenBudget)
	}
	return nil
}

// ChildTracker returns a new tracker for a child spawn with the given parent node ID.
func (t *SpawnTracker) ChildTracker(parentNodeID string) *SpawnTracker {
	return &SpawnTracker{
		TreeID:       t.TreeID,
		ParentNodeID: &parentNodeID,
		CurrentDepth: t.CurrentDepth + 1,
		SpawnCount:   t.SpawnCount, // shared pointer to atomic
		TokensUsed:   t.TokensUsed, // shared pointer to atomic
		Config:       t.Config,
	}
}

// SpawnAgentHandler handles the spawn_agent synthetic tool call.
// Implements SyntheticToolHandler for use with ToolInterceptor.
type SpawnAgentHandler struct {
	ServerCtx          context.Context
	Registry           *gw.Registry
	Router             *gw.Router
	ToolLoop           *toolloop.LoopManager
	Tracker            *SpawnTracker
	ParentEmitter      *agentrt.Emitter
	ParentInput        *agentrt.LoopInput // set at execution time
	DB                 *sqlx.DB           // Database for persisting spawn tree nodes (may be nil)
	TaskPermissionMode string
	ParentMode         string

	// EphemeralAgents maps role names to planned agent definitions.
	// Set by the planner when planning_mode is on.
	EphemeralAgents map[string]*agentrt.PlannedAgent

	// JobQueue for async spawn mode (nil = sync mode).
	JobQueue agentrt.JobQueue

	// BranchStore persists full conversation traces for child branches (may be nil).
	BranchStore *agentrt.BranchStore

	// ParentSandboxCtx provides deployment-level sandbox infrastructure to
	// spawned agents. Persisted child definitions always receive a fresh context
	// built from their own policy. Only ad hoc and planned children share it.
	ParentSandboxCtx *SandboxSessionContext

	// RevisionStore and ProjectRuntime rebind project-local functions when a
	// linked child agent is selected. They prevent a subagent from inheriting
	// the parent agent's source revision.
	RevisionStore  revision.Store
	ProjectRuntime projectruntime.Runner
	SandboxManager *sandbox.SandboxManager
	BrowserPool    *browserpool.Pool
}

// selectedChildOwnsRuntimeHandler reports whether a persisted child agent must
// receive a handler built from its own definition instead of inheriting the
// parent's bound runtime state. These handlers all carry agent-specific source,
// sandbox, browser, or skill policy.
func selectedChildOwnsRuntimeHandler(name string, handler SyntheticToolHandler) bool {
	if _, ok := handler.(*ProjectFunctionHandler); ok {
		return true
	}
	return strings.HasPrefix(name, "sandbox_") ||
		strings.HasPrefix(name, "browser_") ||
		name == "use_skill"
}

func withoutSelectedChildRuntimeTools(tools []string) []string {
	filtered := make([]string, 0, len(tools))
	for _, name := range tools {
		if strings.HasPrefix(name, "sandbox_") || strings.HasPrefix(name, "browser_") || name == "use_skill" {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func disabledRuntimeToolSet(config map[string]interface{}) map[string]struct{} {
	disabled := make(map[string]struct{})
	if config == nil {
		return disabled
	}
	switch raw := config["disabled_runtime_tools"].(type) {
	case []interface{}:
		for _, item := range raw {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				disabled[strings.TrimSpace(name)] = struct{}{}
			}
		}
	case []string:
		for _, name := range raw {
			if strings.TrimSpace(name) != "" {
				disabled[strings.TrimSpace(name)] = struct{}{}
			}
		}
	}
	return disabled
}

// AgentRuntimeConfig reconstructs the runtime policy used by normal
// agent sessions. Persistent-agent sandbox fields are projected into dedicated
// columns, so reading Config JSON alone would miss the child's actual resource
// and network limits.
func AgentRuntimeConfig(agent *agentsquery.AgentDefinitionReadModel) map[string]interface{} {
	config := make(map[string]interface{})
	if agent == nil {
		return config
	}
	if len(agent.Config) > 0 {
		_ = json.Unmarshal(agent.Config, &config)
	}
	if config == nil {
		config = make(map[string]interface{})
	}
	sandboxConfig := make(map[string]interface{})
	if existing, ok := config["sandbox"].(map[string]interface{}); ok {
		for key, value := range existing {
			sandboxConfig[key] = value
		}
	}
	// The dedicated sandbox columns are authoritative only for persistent
	// agents. CreateAgent currently projects proto defaults into those columns
	// for ordinary agents, so overlaying them would erase project-level policy
	// and could turn an explicit network deny into the default allow mode.
	if !strings.EqualFold(strings.TrimSpace(agent.LifecycleMode), "persistent") {
		return config
	}
	sandboxConfig["enabled"] = true
	sandboxConfig["persistent"] = true
	if agent.SandboxImage.Valid && strings.TrimSpace(agent.SandboxImage.String) != "" {
		sandboxConfig["image"] = agent.SandboxImage.String
	}
	if agent.SandboxCPULimit.Valid && agent.SandboxCPULimit.Float64 > 0 {
		sandboxConfig["cpu_limit"] = agent.SandboxCPULimit.Float64
	}
	if agent.SandboxMemoryMB.Valid && agent.SandboxMemoryMB.Int32 > 0 {
		sandboxConfig["memory_mb"] = float64(agent.SandboxMemoryMB.Int32)
	}
	if agent.SandboxDiskMB.Valid && agent.SandboxDiskMB.Int32 > 0 {
		sandboxConfig["disk_mb"] = float64(agent.SandboxDiskMB.Int32)
	}
	if agent.SandboxTimeoutSeconds.Valid && agent.SandboxTimeoutSeconds.Int32 > 0 {
		sandboxConfig["timeout_seconds"] = float64(agent.SandboxTimeoutSeconds.Int32)
	}
	if agent.SandboxNetworkMode.Valid && strings.TrimSpace(agent.SandboxNetworkMode.String) != "" {
		sandboxConfig["network_mode"] = agent.SandboxNetworkMode.String
	}
	if len(agent.SandboxAllowedHosts) > 0 {
		hosts := make([]interface{}, len(agent.SandboxAllowedHosts))
		for i, host := range agent.SandboxAllowedHosts {
			hosts[i] = host
		}
		sandboxConfig["allowed_hosts"] = hosts
	}
	if len(agent.SandboxEnvVars) > 0 {
		var envVars map[string]interface{}
		if json.Unmarshal(agent.SandboxEnvVars, &envVars) == nil && len(envVars) > 0 {
			sandboxConfig["env_vars"] = envVars
		}
	}
	if agent.SandboxSSHEnabled.Valid {
		sandboxConfig["ssh_enabled"] = agent.SandboxSSHEnabled.Bool
	}
	if agent.SandboxGitRepoURL.Valid && strings.TrimSpace(agent.SandboxGitRepoURL.String) != "" {
		sandboxConfig["git_repo_url"] = agent.SandboxGitRepoURL.String
	}
	if agent.SandboxGitBranch.Valid && strings.TrimSpace(agent.SandboxGitBranch.String) != "" {
		sandboxConfig["git_branch"] = agent.SandboxGitBranch.String
	}
	if len(sandboxConfig) > 0 {
		config["sandbox"] = sandboxConfig
	}
	return config
}

func selectedChildSandboxPolicy(
	config map[string]interface{},
	agentID string,
	persistent bool,
	hasProjectFunctions bool,
) (sandbox.SandboxConfig, sandbox.BrowserConfig, bool, error) {
	sandboxConfig := sandbox.ParseSandboxConfig(config)
	browserConfig := sandbox.ParseBrowserConfig(config)
	if persistent {
		sandboxConfig.Enabled = true
		sandboxConfig.Persistent = true
		sandboxConfig.AgentID = agentID
	}
	if hasProjectFunctions && !sandboxConfig.Enabled {
		return sandboxConfig, browserConfig, false, fmt.Errorf("project functions require the child sandbox to be enabled")
	}
	if browserConfig.Enabled && !sandboxConfig.Enabled {
		return sandboxConfig, browserConfig, false, fmt.Errorf("browser automation requires the child sandbox to be enabled")
	}
	return sandboxConfig, browserConfig, sandboxConfig.Enabled, nil
}

// Name returns the tool name for SyntheticToolHandler.
func (h *SpawnAgentHandler) Name() string { return "spawn_agent" }

// Definition returns the tool definition for SyntheticToolHandler.
func (h *SpawnAgentHandler) Definition() gw.ToolDefinition {
	return SpawnAgentToolDefinition()
}

// SpawnAgentToolDefinition returns the tool definition for spawn_agent.
func SpawnAgentToolDefinition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "spawn_agent",
			Description: "Spawn a sub-agent to handle a specific subtask. The sub-agent runs independently and returns its result. Use this to delegate complex subtasks that benefit from focused attention.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task": map[string]interface{}{
						"type":        "string",
						"description": "A clear description of the task for the sub-agent to accomplish.",
					},
					"agent_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional: ID of a specific agent definition to use. If omitted, the sub-agent uses the same configuration as the parent.",
					},
					"context": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Additional context or data to provide to the sub-agent.",
					},
					"approved": map[string]interface{}{
						"type":        "boolean",
						"description": "Set true only when the user has explicitly approved delegation for this task (used with task_permission_mode=ask).",
					},
				},
				"required": []string{"task"},
			},
		},
	}
}

// ExecuteWithParent runs the spawn_agent tool call with explicit parent input.
// This is the original Execute signature used by direct callers.
func (h *SpawnAgentHandler) ExecuteWithParent(ctx context.Context, args map[string]interface{}, parentInput *agentrt.LoopInput) (string, error) {
	h.ParentInput = parentInput
	return h.Execute(ctx, args)
}

// Execute runs the spawn_agent tool call (SyntheticToolHandler interface).
func (h *SpawnAgentHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	parentInput := h.ParentInput
	if parentInput == nil {
		return "", fmt.Errorf("spawn_agent: parent input not set")
	}
	return h.execute(ctx, args, parentInput)
}

// execute is the internal implementation of the spawn_agent tool.
func (h *SpawnAgentHandler) execute(ctx context.Context, args map[string]interface{}, parentInput *agentrt.LoopInput) (string, error) {
	task, _ := args["task"].(string)
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	additionalContext, _ := args["context"].(string)

	parentMode := strings.TrimSpace(h.ParentMode)
	if parentMode == "" {
		parentMode = strings.TrimSpace(parentInput.AgentMode)
	}

	switch strings.ToLower(strings.TrimSpace(h.TaskPermissionMode)) {
	case "deny":
		if h.ParentEmitter != nil {
			h.ParentEmitter.Emit(agentrt.Event{
				Type:      agentrt.EventPolicyDecision,
				SessionID: parentInput.SessionID,
				Timestamp: time.Now(),
				Data: map[string]interface{}{
					"policy":               "task_permission_mode",
					"value":                "deny",
					"decision":             "blocked",
					"parent_mode":          parentMode,
					"requested_agent_id":   strings.TrimSpace(fmt.Sprintf("%v", args["agent_id"])),
					"requested_task_short": truncateStr(task, 200),
				},
			})
		}
		return "Task delegation is denied by policy (task_permission_mode=deny).", nil
	case "ask":
		explicitApproval := hasExplicitDelegationApproval(parentInput.UserInput) ||
			hasExplicitDelegationApproval(additionalContext) ||
			parseApprovedArg(args["approved"])

		if h.ParentEmitter != nil {
			h.ParentEmitter.Emit(agentrt.Event{
				Type:      agentrt.EventPolicyDecision,
				SessionID: parentInput.SessionID,
				Timestamp: time.Now(),
				Data: map[string]interface{}{
					"policy":               "task_permission_mode",
					"value":                "ask",
					"decision":             ternary(explicitApproval, "allowed_with_user_approval", "requires_user_approval"),
					"parent_mode":          parentMode,
					"requested_agent_id":   strings.TrimSpace(fmt.Sprintf("%v", args["agent_id"])),
					"requested_task_short": truncateStr(task, 200),
					"approved":             explicitApproval,
				},
			})
		}
		if !explicitApproval {
			return "Task delegation requires user approval (task_permission_mode=ask). Briefly inform the user what subtask you plan to delegate and why, then retry this tool call with approved=true.", nil
		}
	}
	if h.ParentEmitter != nil {
		h.ParentEmitter.Emit(agentrt.Event{
			Type:      agentrt.EventPolicyDecision,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"policy":               "task_permission_mode",
				"value":                "always",
				"decision":             "allowed",
				"parent_mode":          parentMode,
				"requested_agent_id":   strings.TrimSpace(fmt.Sprintf("%v", args["agent_id"])),
				"requested_task_short": truncateStr(task, 200),
			},
		})
	}

	// Check spawn limits
	if err := h.Tracker.CanSpawn(); err != nil {
		return fmt.Sprintf("Cannot spawn sub-agent: %s. Please handle this task directly.", err.Error()), nil
	}

	agentID, _ := args["agent_id"].(string)

	// Check if agent_id matches an ephemeral agent role (from planner)
	var ephemeralAgent *agentrt.PlannedAgent
	if agentID != "" && h.EphemeralAgents != nil {
		if ea, ok := h.EphemeralAgents[agentID]; ok {
			ephemeralAgent = ea
			// Ephemeral agents are always allowed
		}
	}

	// Check allowed agents authorization (skip for ephemeral agents)
	if ephemeralAgent == nil && agentID != "" && len(h.Tracker.Config.AllowedAgents) > 0 {
		allowed := false
		for _, a := range h.Tracker.Config.AllowedAgents {
			if a == agentID {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Sprintf("Agent %s is not in the allowed agents list for spawning.", agentID), nil
		}
	}

	h.Tracker.SpawnCount.Add(1)

	// Check for async mode: if JobQueue is available and config says async,
	// enqueue the job and return immediately.
	if h.JobQueue != nil && h.Tracker.Config.Async {
		return h.executeAsync(ctx, args, parentInput, task, agentID, additionalContext, ephemeralAgent)
	}

	// Persist spawn node to database
	startedAt := time.Now()
	nodeID := uuid.New().String()
	if h.DB != nil {
		_, err := h.DB.ExecContext(ctx, `
			INSERT INTO agent_spawn_trees
				(id, tree_id, parent_node_id, agent_id, depth, status, task, tenant_id)
			VALUES ($1, $2::uuid, $3, $4, $5, 'running', $6, $7::uuid)
		`, nodeID, h.Tracker.TreeID, nilIfEmpty(h.Tracker.ParentNodeID), nilIfEmptyStr(agentID),
			h.Tracker.CurrentDepth+1, truncateStr(task, 2000), parentInput.TenantID)
		if err != nil {
			logger.WithFields("error", err.Error(), "node_id", nodeID).Warn("spawn: failed to insert spawn tree node")
		}
	}

	// Load child agent config
	var childModel, childSystemPrompt string
	var childTools []string
	var childSampling gw.SamplingParams
	childMode := "subagent"
	childTaskPermissionMode := "ask"
	childWorkingDirectory := strings.TrimSpace(parentInput.WorkingDirectory)
	childMaxSteps := int32(0)
	childAgentID := strings.TrimSpace(agentID)
	var childAgentConfig map[string]interface{}
	var selectedChildAgent *agentsquery.AgentDefinitionReadModel

	if ephemeralAgent != nil {
		// Use ephemeral agent definition from planner
		childModel = ephemeralAgent.Model
		if childModel == "" {
			childModel = parentInput.Model
		}
		childSystemPrompt = ephemeralAgent.SystemPrompt
		childTools = ephemeralAgent.Tools
		childMode = "subagent"
		childTaskPermissionMode = "allow"
		logger.WithFields("role", ephemeralAgent.Role, "task", truncateStr(task, 100)).
			Info("spawn: using ephemeral agent from plan")
	} else if agentID != "" {
		// Load specific agent definition; fall back to parent config if not found.
		agent, err := h.loadAgent(ctx, agentID)
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Failed to load agent %s: %s", agentID, err.Error()), nil
		}
		if agent != nil && strings.ToLower(strings.TrimSpace(agent.Mode)) != "subagent" {
			return fmt.Sprintf("Agent %s is mode=%q and cannot be delegated to. Set mode=subagent to allow delegation.", agentID, agent.Mode), nil
		}

		if agent != nil {
			// Use the resolved agent definition.
			selectedChildAgent = agent
			childAgentID = agent.ID
			childModel = agent.Model
			if m := strings.ToLower(strings.TrimSpace(agent.Mode)); m != "" {
				childMode = m
			}
			if tm := strings.ToLower(strings.TrimSpace(agent.TaskPermissionMode)); tm != "" {
				childTaskPermissionMode = tm
			}
			if agent.SystemPrompt.Valid {
				childSystemPrompt = agent.SystemPrompt.String
			}
			if agent.WorkingDirectory.Valid {
				childWorkingDirectory = strings.TrimSpace(agent.WorkingDirectory.String)
			}
			if agent.MaxSteps.Valid && agent.MaxSteps.Int32 > 0 {
				childMaxSteps = agent.MaxSteps.Int32
			}
			childTools = agent.Tools
			childAgentConfig = AgentRuntimeConfig(agent)
			if childWorkingDirectory != "" {
				childAgentConfig["working_directory"] = childWorkingDirectory
			}
			if temp, ok := childAgentConfig["temperature"].(float64); ok {
				childSampling.Temperature = temp
			}
			if maxTok, ok := childAgentConfig["max_tokens"].(float64); ok {
				childSampling.MaxTokens = int(maxTok)
			}
		} else {
			// Agent not found — fall back to parent config so the spawn
			// proceeds rather than failing. The LLM often invents agent
			// names; this keeps the task moving.
			logger.WithFields("requested_agent", agentID).
				Warn("spawn: agent not found, falling back to parent config")
			childModel = parentInput.Model
			childSampling = parentInput.Sampling
			if m := strings.ToLower(strings.TrimSpace(parentInput.AgentMode)); m != "" {
				childMode = m
			}
			if tm := strings.ToLower(strings.TrimSpace(parentInput.TaskPermissionMode)); tm != "" {
				childTaskPermissionMode = tm
			}
			if parentInput.MaxSteps > 0 {
				childMaxSteps = parentInput.MaxSteps
			}
			childAgentID = strings.TrimSpace(parentInput.AgentID)

			// Inherit parent tools but strip spawn_agent to prevent
			// recursive delegation chains — the child should do the work.
			for _, t := range parentInput.Tools {
				if t != "spawn_agent" && t != "parallel_tasks" && t != "check_job" {
					childTools = append(childTools, t)
				}
			}
			// Strip spawn guidance from system prompt so the child
			// focuses on executing rather than delegating.
			childSystemPrompt = stripSpawnGuidance(parentInput.SystemPrompt)
		}
	} else {
		// No agent_id provided — use parent's config.
		childModel = parentInput.Model
		childSystemPrompt = parentInput.SystemPrompt
		childTools = parentInput.Tools
		childSampling = parentInput.Sampling
		if m := strings.ToLower(strings.TrimSpace(parentInput.AgentMode)); m != "" {
			childMode = m
		}
		if tm := strings.ToLower(strings.TrimSpace(parentInput.TaskPermissionMode)); tm != "" {
			childTaskPermissionMode = tm
		}
		if parentInput.MaxSteps > 0 {
			childMaxSteps = parentInput.MaxSteps
		}
		childAgentID = strings.TrimSpace(parentInput.AgentID)
	}
	parentAgentID := strings.TrimSpace(parentInput.AgentID)
	usesSelectedChildDefinition := selectedChildAgent != nil && childAgentID != parentAgentID
	if usesSelectedChildDefinition {
		// Runtime-bound tools are reconstructed below from the selected child's
		// own policy. Remove any stale names stored on the definition first.
		childTools = withoutSelectedChildRuntimeTools(childTools)
	}

	// Ensure the child inherits any runtime-injected synthetic tools from the parent.
	// When agent_id or ephemeral agents are used, childTools comes from DB/planner
	// and won't include tools injected at request time (e.g. web_search).
	// We merge parent's synthetic tool names that exist on the interceptor.
	if parentInterceptor, ok := parentInput.Interceptor.(*ToolInterceptor); ok {
		childToolSet := make(map[string]struct{}, len(childTools))
		for _, t := range childTools {
			childToolSet[t] = struct{}{}
		}
		for name, handler := range parentInterceptor.Handlers {
			if name == "spawn_agent" || name == "parallel_tasks" {
				continue // these get their own handlers
			}
			if usesSelectedChildDefinition && selectedChildOwnsRuntimeHandler(name, handler) {
				continue // selected children receive only their own bound runtime handlers
			}
			// Add synthetic tools from parent that are also in parent's Tools list
			// (respecting the parent's allowlist) but missing from child's tools.
			if _, exists := childToolSet[name]; !exists {
				for _, pt := range parentInput.Tools {
					if pt == name {
						childTools = append(childTools, name)
						break
					}
				}
			}
		}
	}

	// Build user input for child
	userInput := task
	if additionalContext != "" {
		userInput = fmt.Sprintf("%s\n\nAdditional context:\n%s", task, additionalContext)
	}

	// Create child runtime components
	childEngine := agentrt.NewEngine(h.Registry, h.Router, h.ToolLoop)
	childEmitter := agentrt.NewEmitter()
	// Forward child events to parent emitter with spawn metadata
	if h.ParentEmitter != nil {
		spawnDepth := h.Tracker.CurrentDepth + 1
		treeID := h.Tracker.TreeID
		taskSummary := truncateStr(task, 200)
		h.ParentEmitter.Emit(agentrt.Event{
			Type:      agentrt.EventSpawnStart,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"spawn_depth":   spawnDepth,
				"spawn_tree_id": treeID,
				"spawn_task":    taskSummary,
				"agent_id":      agentID,
			},
		})
		childEmitter.AddSink(agentrt.EventSinkFunc(func(e agentrt.Event) error {
			e.Data = ensureDataMap(e.Data)
			e.Data["spawn_depth"] = spawnDepth
			e.Data["spawn_tree_id"] = treeID
			e.Data["spawn_task"] = taskSummary
			h.ParentEmitter.Emit(e)
			return nil
		}))
	}

	childConfig := agentrt.LoopConfig{
		MaxIterations:       25,
		MaxToolCallsPerTurn: 0, // unlimited
		MaxHistoryMessages:  100,
		EnableStreaming:     false,
		TurnTimeout:         h.Tracker.Config.ChildTimeout,
	}
	if childMaxSteps > 0 {
		childConfig.MaxIterations = childMaxSteps
	} else if parentInput.MaxSteps > 0 {
		childConfig.MaxIterations = parentInput.MaxSteps
	}

	childLoop := agentrt.NewLoop(childEngine, h.ToolLoop, childEmitter, childConfig)

	childInput := &agentrt.LoopInput{
		TenantID:           parentInput.TenantID,
		AgentID:            childAgentID,
		SessionID:          fmt.Sprintf("spawn_%s_%d", h.Tracker.TreeID, h.Tracker.SpawnCount.Load()),
		Model:              childModel,
		SystemPrompt:       childSystemPrompt,
		Tools:              childTools,
		Sampling:           childSampling,
		UserInput:          userInput,
		AgentMode:          childMode,
		TaskPermissionMode: childTaskPermissionMode,
		WorkingDirectory:   childWorkingDirectory,
		MaxSteps:           childConfig.MaxIterations,
	}

	// A persisted child is a separate security principal for runtime-bound
	// handlers. Build its sandbox, browser, skills, and project functions from
	// the selected definition. Ephemeral planned children continue sharing the
	// parent's handlers so they can cooperate in the same workspace.
	childIsPersistent := false
	if selectedChildAgent != nil {
		childIsPersistent = strings.EqualFold(strings.TrimSpace(selectedChildAgent.LifecycleMode), "persistent")
	}
	var childSandboxCtx *SandboxSessionContext
	var childRevision *revision.Revision
	var childBrowserConfig sandbox.BrowserConfig
	childDisabledRuntimeTools := disabledRuntimeToolSet(childAgentConfig)
	childRuntimeToolEnabled := func(name string) bool {
		_, disabled := childDisabledRuntimeTools[name]
		return !disabled
	}
	if usesSelectedChildDefinition {
		resolvedRevision, revisionErr := h.resolveChildProjectRevision(ctx, parentInput.TenantID, childAgentID)
		if revisionErr != nil {
			return "", revisionErr
		}
		childRevision = resolvedRevision

		hasProjectFunctions := childRevision != nil && len(childRevision.Manifest.Functions) > 0
		if hasProjectFunctions {
			if err := projectruntime.ValidateFunctionSandboxPolicy(childAgentConfig); err != nil {
				return "", fmt.Errorf("child agent %s runtime policy: %w", childAgentID, err)
			}
		}
		childSandboxConfig, browserConfig, needsSandbox, policyErr := selectedChildSandboxPolicy(
			childAgentConfig, childAgentID, childIsPersistent, hasProjectFunctions,
		)
		if policyErr != nil {
			return "", fmt.Errorf("child agent %s runtime policy: %w", childAgentID, policyErr)
		}
		childBrowserConfig = browserConfig
		if needsSandbox {
			manager := h.SandboxManager
			if manager == nil && h.ParentSandboxCtx != nil {
				manager = h.ParentSandboxCtx.Manager
			}
			if manager == nil {
				return "", fmt.Errorf("child agent %s requires a sandbox but the sandbox manager is unavailable", childAgentID)
			}
			childSandboxConfig = manager.ClampToGlobalLimitsForTenant(childSandboxConfig, parentInput.TenantID)
			if !childBrowserConfig.Headless && !manager.IsBrowserHeadedEnabled(parentInput.TenantID) {
				childBrowserConfig.Headless = true
			}
			childSandboxConfig.BrowserSidecar = childBrowserConfig.ToSidecarConfig()
			childSessionID := childInput.SessionID
			persistenceMode := "ephemeral"
			if childSandboxConfig.Persistent {
				childSessionID = "trp-" + childAgentID
				persistenceMode = "persistent"
				childSandboxConfig.AgentID = childAgentID
			}
			childSandboxCtx = &SandboxSessionContext{
				Manager:                 manager,
				SessionID:               childSessionID,
				TenantID:                parentInput.TenantID,
				Config:                  childSandboxConfig,
				SessionStartedAt:        startedAt,
				ExecutionMode:           "sandbox",
				PersistenceMode:         persistenceMode,
				AllowedWorkingDirectory: childWorkingDirectory,
				Emitter:                 childEmitter,
				AgentID:                 childAgentID,
				LinkedSessionID:         childSandboxConfig.LinkedSessionID,
			}
			childInput.ExecutionMode = childSandboxCtx.ExecutionMode
			childInput.PersistenceMode = childSandboxCtx.PersistenceMode
			childInput.SandboxEnabled = true
			childInput.GitRepoConfigured = strings.TrimSpace(childSandboxConfig.GitRepoURL) != ""
			if h.ParentSandboxCtx != nil {
				// These are deployment-level routing settings, not parent agent policy.
				childSandboxCtx.PortExposureBaseDomain = h.ParentSandboxCtx.PortExposureBaseDomain
				childSandboxCtx.PortExposureTLSEnabled = h.ParentSandboxCtx.PortExposureTLSEnabled
				childSandboxCtx.PortExposureListenPort = h.ParentSandboxCtx.PortExposureListenPort
				childSandboxCtx.ChannelConfigID = h.ParentSandboxCtx.ChannelConfigID
				childSandboxCtx.ChannelRef = h.ParentSandboxCtx.ChannelRef
				childSandboxCtx.ThreadRef = h.ParentSandboxCtx.ThreadRef
			}
		}
	}

	parentInterceptor, hasParentInterceptor := parentInput.Interceptor.(*ToolInterceptor)
	if hasParentInterceptor || usesSelectedChildDefinition {
		childInterceptor := NewToolInterceptor(h.ToolLoop)
		if parentInterceptor == nil {
			parentInterceptor = NewToolInterceptor(h.ToolLoop)
		}
		for name, handler := range parentInterceptor.Handlers {
			switch {
			case name == "spawn_agent":
				// Dedicated spawn handler for the child with incremented depth.
				nestedParentSandboxCtx := h.ParentSandboxCtx
				if usesSelectedChildDefinition {
					nestedParentSandboxCtx = childSandboxCtx
				}
				childSpawnHandler := &SpawnAgentHandler{
					ServerCtx:          h.ServerCtx,
					Registry:           h.Registry,
					Router:             h.Router,
					ToolLoop:           h.ToolLoop,
					Tracker:            h.Tracker.ChildTracker(nodeID),
					ParentEmitter:      childEmitter,
					ParentInput:        childInput,
					DB:                 h.DB,
					TaskPermissionMode: childTaskPermissionMode,
					ParentMode:         childMode,
					BranchStore:        h.BranchStore,
					JobQueue:           h.JobQueue,
					RevisionStore:      h.RevisionStore,
					ProjectRuntime:     h.ProjectRuntime,
					SandboxManager:     h.SandboxManager,
					BrowserPool:        h.BrowserPool,
					ParentSandboxCtx:   nestedParentSandboxCtx,
				}
				childInterceptor.RegisterHandler(childSpawnHandler)
			case usesSelectedChildDefinition && selectedChildOwnsRuntimeHandler(name, handler):
				// Rebuilt below from the selected child's own definition.
				continue
			default:
				// Share memory, ask_user, web_search, etc. with parent.
				if _, alwaysInclude := parentInterceptor.AlwaysInclude[name]; alwaysInclude {
					childInterceptor.RegisterAlwaysInclude(handler)
				} else {
					childInterceptor.RegisterHandler(handler)
				}
			}
		}
		if usesSelectedChildDefinition && childSandboxCtx != nil {
			for _, handler := range NewSandboxHandlers(childSandboxCtx) {
				if !childRuntimeToolEnabled(handler.Name()) {
					continue
				}
				childInterceptor.RegisterHandler(handler)
				childInput.Tools = appendToolName(childInput.Tools, handler.Name())
			}
			if childBrowserConfig.Enabled {
				browserCtx := &BrowserSessionContext{
					SandboxCtx: childSandboxCtx,
					Config:     childBrowserConfig,
					Emitter:    childEmitter,
					Pool:       h.BrowserPool,
				}
				for _, handler := range NewBrowserHandlers(browserCtx) {
					if !childRuntimeToolEnabled(handler.Name()) {
						continue
					}
					childInterceptor.RegisterHandler(handler)
					childInput.Tools = appendToolName(childInput.Tools, handler.Name())
				}
			}
		}
		if usesSelectedChildDefinition {
			if err := h.registerChildProjectRevisionFunctions(
				ctx, childInterceptor, childSandboxCtx, childRevision, childAgentConfig, &childInput.Tools,
			); err != nil {
				return "", err
			}
			if childSandboxCtx != nil {
				installedNames := make(map[string]struct{})
				for _, skill := range agentskills.ParseSkillsConfig(childAgentConfig) {
					childInput.Skills = append(childInput.Skills, agentrt.SkillEntry{
						Name: skill.Name, Description: skill.Description, Content: skill.Content,
					})
					installedNames[skill.Name] = struct{}{}
				}
				for _, skill := range agentskills.ResolveBuiltinSkills(childInput.Tools) {
					if _, exists := installedNames[skill.Name]; exists {
						continue
					}
					childInput.Skills = append(childInput.Skills, agentrt.SkillEntry{
						Name: skill.Name, Description: skill.Description, Content: skill.Content,
					})
				}
				if len(childInput.Skills) > 0 {
					childSandboxCtx.SkillEntries = childInput.Skills
					if childRuntimeToolEnabled("use_skill") {
						childInterceptor.RegisterAlwaysInclude(&UseSkillHandler{
							SandboxCtx: childSandboxCtx, SessionID: childInput.SessionID,
							AvailableSkills: childInput.Skills,
						})
						childInput.Tools = appendToolName(childInput.Tools, "use_skill")
					}
				}
			}
		}
		childInput.Interceptor = childInterceptor
	}

	childState := &agentrt.LoopState{
		Messages: []gw.Message{},
	}

	// Run child with timeout
	childCtx, cancel := context.WithTimeout(ctx, h.Tracker.Config.ChildTimeout)
	defer cancel()

	logger.WithFields(
		"tree_id", h.Tracker.TreeID,
		"depth", h.Tracker.CurrentDepth+1,
		"task", truncateStr(task, 100),
		"agent_id", agentID,
		"has_interceptor", childInput.Interceptor != nil,
		"tool_count", len(childInput.Tools),
	).Debug("spawning sub-agent")

	finalState, err := childLoop.Run(childCtx, childState, childInput)
	if err != nil {
		// Update spawn node as failed
		if h.DB != nil {
			_, dbErr := h.DB.ExecContext(ctx, `
				UPDATE agent_spawn_trees
				SET status = 'failed', result = $1, completed_at = NOW()
				WHERE id = $2
			`, truncateStr(err.Error(), 2000), nodeID)
			if dbErr != nil {
				logger.WithFields("error", dbErr.Error(), "node_id", nodeID).Warn("spawn: failed to update spawn tree node on error")
			}
		}
		if h.ParentEmitter != nil {
			h.ParentEmitter.Emit(agentrt.Event{
				Type:      agentrt.EventSpawnError,
				SessionID: parentInput.SessionID,
				Timestamp: time.Now(),
				Error:     err.Error(),
				Data: map[string]interface{}{
					"spawn_depth":   h.Tracker.CurrentDepth + 1,
					"spawn_tree_id": h.Tracker.TreeID,
					"spawn_task":    truncateStr(task, 200),
				},
			})
		}
		return fmt.Sprintf("Sub-agent failed: %s", err.Error()), nil
	}

	// Track token usage
	h.Tracker.TokensUsed.Add(int64(finalState.CumulativeUsage.TotalTokens))

	// Update spawn node as completed with results and token usage
	if h.DB != nil {
		_, dbErr := h.DB.ExecContext(ctx, `
			UPDATE agent_spawn_trees
			SET status = 'completed',
				result = $1,
				prompt_tokens = $2,
				completion_tokens = $3,
				total_tokens = $4,
				completed_at = NOW()
			WHERE id = $5
		`, truncateStr(finalState.LastAssistantText, 2000),
			finalState.CumulativeUsage.PromptTokens,
			finalState.CumulativeUsage.CompletionTokens,
			finalState.CumulativeUsage.TotalTokens,
			nodeID)
		if dbErr != nil {
			logger.WithFields("error", dbErr.Error(), "node_id", nodeID).Warn("spawn: failed to update spawn tree node on completion")
		}
	}

	// Persist full branch trace
	if h.BranchStore != nil {
		var msgBytes json.RawMessage
		if b, err := json.Marshal(finalState.Messages); err == nil {
			msgBytes = b
		}
		h.BranchStore.SaveBranch(ctx, &agentrt.BranchRecord{
			ID:               nodeID,
			SessionID:        parentInput.SessionID,
			TenantID:         parentInput.TenantID,
			AgentID:          childAgentID,
			Source:           "spawn",
			Instruction:      truncateStr(task, 2000),
			Conclusion:       truncateStr(finalState.LastAssistantText, 2000),
			Status:           "completed",
			Messages:         msgBytes,
			ToolCallsCount:   int(finalState.TotalToolCalls),
			PromptTokens:     int(finalState.CumulativeUsage.PromptTokens),
			CompletionTokens: int(finalState.CumulativeUsage.CompletionTokens),
			TotalTokens:      int(finalState.CumulativeUsage.TotalTokens),
			DurationMs:       time.Since(startedAt).Milliseconds(),
		})
	}

	if h.ParentEmitter != nil {
		h.ParentEmitter.Emit(agentrt.Event{
			Type:      agentrt.EventSpawnEnd,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Usage: &agentrt.UsageDelta{
				PromptTokens:     finalState.CumulativeUsage.PromptTokens,
				CompletionTokens: finalState.CumulativeUsage.CompletionTokens,
				TotalTokens:      finalState.CumulativeUsage.TotalTokens,
			},
			Data: map[string]interface{}{
				"spawn_depth":   h.Tracker.CurrentDepth + 1,
				"spawn_tree_id": h.Tracker.TreeID,
				"spawn_task":    truncateStr(task, 200),
				"result":        truncateStr(finalState.LastAssistantText, 500),
				"iterations":    finalState.IterationCount,
			},
		})
	}

	logger.WithFields(
		"tree_id", h.Tracker.TreeID,
		"depth", h.Tracker.CurrentDepth+1,
		"tokens", finalState.CumulativeUsage.TotalTokens,
		"iterations", finalState.IterationCount,
	).Debug("sub-agent completed")

	if finalState.LastAssistantText != "" {
		return finalState.LastAssistantText, nil
	}

	return "Sub-agent completed but produced no output.", nil
}

// executeAsync enqueues a spawn as an async job and returns immediately.
func (h *SpawnAgentHandler) executeAsync(
	ctx context.Context,
	args map[string]interface{},
	parentInput *agentrt.LoopInput,
	task, agentID, additionalContext string,
	ephemeralAgent *agentrt.PlannedAgent,
) (string, error) {
	// Build the run function that will execute in the background.
	// We create a copy of the handler without async to run synchronously inside the job.
	syncHandler := &SpawnAgentHandler{
		ServerCtx:          h.ServerCtx,
		Registry:           h.Registry,
		Router:             h.Router,
		ToolLoop:           h.ToolLoop,
		Tracker:            h.Tracker,
		ParentEmitter:      h.ParentEmitter,
		ParentInput:        parentInput,
		DB:                 h.DB,
		TaskPermissionMode: h.TaskPermissionMode,
		ParentMode:         h.ParentMode,
		EphemeralAgents:    h.EphemeralAgents,
		ParentSandboxCtx:   h.ParentSandboxCtx,
		RevisionStore:      h.RevisionStore,
		ProjectRuntime:     h.ProjectRuntime,
		SandboxManager:     h.SandboxManager,
		BrowserPool:        h.BrowserPool,
		// JobQueue intentionally nil — child runs synchronously
	}
	// Temporarily disable async on the tracker config so the child runs sync
	origAsync := syncHandler.Tracker.Config.Async
	syncHandler.Tracker.Config.Async = false

	runFunc := func(jobCtx context.Context) (string, error) {
		defer func() { syncHandler.Tracker.Config.Async = origAsync }()
		return syncHandler.execute(jobCtx, args, parentInput)
	}

	handle, err := h.JobQueue.Enqueue(ctx, agentrt.JobRequest{
		SessionID: parentInput.SessionID,
		AgentID:   agentID,
		TenantID:  parentInput.TenantID,
		Job:       task,
		RunFunc:   runFunc,
	})
	if err != nil {
		return fmt.Sprintf("Failed to enqueue async job: %s", err.Error()), nil
	}

	return fmt.Sprintf("Job %s enqueued. The sub-agent is running in the background. Its result will appear automatically when completed, or you can check status with check_job(job_id=\"%s\").", handle.JobID, handle.JobID), nil
}

func (h *SpawnAgentHandler) registerChildProjectFunctions(
	ctx context.Context,
	interceptor *ToolInterceptor,
	sandboxCtx *SandboxSessionContext,
	tenantID, agentID string,
	agentConfig map[string]interface{},
	tools *[]string,
) error {
	rev, err := h.resolveChildProjectRevision(ctx, tenantID, agentID)
	if err != nil {
		return err
	}
	return h.registerChildProjectRevisionFunctions(ctx, interceptor, sandboxCtx, rev, agentConfig, tools)
}

func (h *SpawnAgentHandler) resolveChildProjectRevision(
	ctx context.Context,
	tenantID, agentID string,
) (*revision.Revision, error) {
	if h.RevisionStore == nil || agentID == "" {
		return nil, nil
	}
	rev, err := h.RevisionStore.GetActive(ctx, tenantID, agentID)
	if err != nil {
		if errors.Is(err, revision.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve child agent revision: %w", err)
	}
	return rev, nil
}

func (h *SpawnAgentHandler) registerChildProjectRevisionFunctions(
	_ context.Context,
	interceptor *ToolInterceptor,
	sandboxCtx *SandboxSessionContext,
	rev *revision.Revision,
	agentConfig map[string]interface{},
	tools *[]string,
) error {
	if interceptor == nil || rev == nil {
		return nil
	}
	if len(rev.Manifest.Functions) == 0 {
		return nil
	}
	if err := projectruntime.ValidateFunctionSandboxPolicy(agentConfig); err != nil {
		return fmt.Errorf("child agent revision %s: %w", rev.ID, err)
	}
	if h.ProjectRuntime == nil {
		return fmt.Errorf("child agent revision %s declares project functions but the project runtime is unavailable", rev.ID)
	}
	if sandboxCtx == nil {
		return fmt.Errorf("child agent revision %s declares project functions but no sandbox is available", rev.ID)
	}
	for _, projectHandler := range NewProjectFunctionHandlers(h.ProjectRuntime, rev, sandboxCtx) {
		if interceptor.IsSyntheticTool(projectHandler.Name()) {
			return fmt.Errorf("child project function %q conflicts with a runtime tool", projectHandler.Name())
		}
		interceptor.RegisterHandler(projectHandler)
		if tools != nil {
			*tools = appendToolName(*tools, projectHandler.Name())
		}
	}
	return nil
}

func appendToolName(tools []string, name string) []string {
	for _, existing := range tools {
		if existing == name {
			return tools
		}
	}
	return append(tools, name)
}

// loadAgent loads an agent definition via CQRS.
// It first tries to parse agentID as a UUID and look up by ID.
// If agentID is not a valid UUID, it falls back to a name-based lookup.
func (h *SpawnAgentHandler) loadAgent(ctx context.Context, agentID string) (*agentsquery.AgentDefinitionReadModel, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && h.ServerCtx != nil {
		sys, err = cqrs.GetSystemFromContext(h.ServerCtx)
	}
	if err != nil {
		return nil, fmt.Errorf("CQRS system not available: %w", err)
	}

	tenantID := contextkeys.GetTenantID(ctx)

	// Determine whether agentID is a UUID or a name.
	var res interface{}
	if _, parseErr := uuid.Parse(agentID); parseErr == nil {
		// Valid UUID — look up by ID.
		q := agentsquery.NewGetAgentByIDQuery(agentID, tenantID)
		res, err = sys.QueryBus.Execute(ctx, q)
	} else {
		// Not a valid UUID — try name-based lookup.
		q := agentsquery.NewGetAgentByNameQuery(agentID, tenantID)
		res, err = sys.QueryBus.Execute(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("agent %q not found", agentID)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	if data == nil {
		return nil, fmt.Errorf("agent %q not found", agentID)
	}

	agent, ok := data.(*agentsquery.AgentDefinitionReadModel)
	if !ok {
		return nil, fmt.Errorf("unexpected data type for agent: %T", data)
	}
	return agent, nil
}

// listAvailableAgents returns a formatted list of available sub-agents (mode=subagent).
// Returns an empty string if listing fails or no agents exist.
func (h *SpawnAgentHandler) listAvailableAgents(ctx context.Context) string {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && h.ServerCtx != nil {
		sys, err = cqrs.GetSystemFromContext(h.ServerCtx)
	}
	if err != nil {
		return ""
	}

	tenantID := contextkeys.GetTenantID(ctx)
	subagentMode := "subagent"
	enabled := true
	q := agentsquery.NewListAgentsQuery(tenantID, &enabled, false, &subagentMode, nil, 20, 0)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return ""
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	agents, ok := data.([]agentsquery.AgentDefinitionReadModel)
	if !ok || len(agents) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, a := range agents {
		desc := ""
		if a.Description.Valid && a.Description.String != "" {
			desc = " — " + truncateStr(a.Description.String, 80)
		}
		sb.WriteString(fmt.Sprintf("  • %s (id: %s)%s\n", a.Name, a.ID, desc))
	}
	return sb.String()
}

// stripSpawnGuidance removes the spawn/fork/delegation sections from a system
// prompt so that a fallback child agent focuses on executing the task rather
// than recursively delegating.
func stripSpawnGuidance(prompt string) string {
	// Remove known capability sections injected by augmentCapabilitiesSystemPrompt.
	sections := []string{
		"## Async Sub-Agent Spawning",
		"## Sub-Agent Spawning",
		"## Context Forking",
		"Available agents for spawning",
	}
	lines := strings.Split(prompt, "\n")
	var out []string
	skip := false
	for _, line := range lines {
		// Start skipping when we hit a known section header.
		for _, sec := range sections {
			if strings.Contains(line, sec) {
				skip = true
				break
			}
		}
		if skip {
			// Stop skipping at the next blank line or the start of a new ## section
			// that isn't one of the spawn sections.
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				skip = false
				continue
			}
			if strings.HasPrefix(trimmed, "## ") {
				isSpawnSection := false
				for _, sec := range sections {
					if strings.Contains(line, sec) {
						isSpawnSection = true
						break
					}
				}
				if !isSpawnSection {
					skip = false
					out = append(out, line)
				}
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func ensureDataMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return make(map[string]interface{})
	}
	return m
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func nilIfEmpty(s *string) interface{} {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func nilIfEmptyStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func parseApprovedArg(raw interface{}) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "y", "1", "approved":
			return true
		}
	case float64:
		return v > 0
	}
	return false
}

var (
	explicitApprovalNegationRe = regexp.MustCompile(`\b(no|not|don't|do not|cannot|can't|decline|deny|denied)\b`)
	explicitApprovalPositiveRe = regexp.MustCompile(`\b(yes|y|ok|okay|approved|approve|proceed|go ahead|run it|do it|i confirm|i approve|confirmed)\b`)
)

func hasExplicitDelegationApproval(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	if explicitApprovalNegationRe.MatchString(normalized) {
		return false
	}
	return explicitApprovalPositiveRe.MatchString(normalized)
}

func ternary[T any](condition bool, whenTrue, whenFalse T) T {
	if condition {
		return whenTrue
	}
	return whenFalse
}
