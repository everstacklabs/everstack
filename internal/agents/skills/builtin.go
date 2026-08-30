package skills

// BuiltinSkill represents a default skill that is auto-injected when an agent
// has the corresponding tools enabled.
type BuiltinSkill struct {
	Name         string
	Description  string
	Content      string
	RequiresAny  []string // At least one of these tools must be enabled
	RequiresAll  []string // All of these tools must be enabled (empty = no requirement)
}

// BuiltinSkills returns all built-in skills that ship with the platform.
// These provide structured instructions for common tool-use patterns.
func BuiltinSkills() []BuiltinSkill {
	return []BuiltinSkill{
		{
			Name:        "workflow-builder",
			Description: "Instructions for creating Studio workflows with proper node types, layout, and edge connections.",
			RequiresAny: []string{"create_workflow"},
			Content: `# Workflow Builder

## Assignment

You can create Studio workflows using the create_workflow tool. Follow these instructions when the user asks to build a workflow, pipeline, or automation.

## Process

1. Clarify the user's goal — what should the workflow do end-to-end?
2. Identify the appropriate node types for each step
3. Design the graph: nodes, edges, and layout
4. Call create_workflow with the complete configuration

## Node Types

### Input/Output
- **start**: Entry point (handles: out). Every workflow must have exactly one.
- **response**: Final output (handles: in). Every workflow must have at least one.

### AI Nodes
- **provider**: LLM provider call — GPT-4, Claude, etc. (handles: in, out). Config: model, temperature, max_tokens.
- **agent**: Run a deployed agent as a step (handles: in, out). Config: agentId.

### Middleware
- **auth**: API key or JWT authentication (handles: in, out)
- **rateLimiter**: Rate limiting (handles: in, out). Config: requests_per_minute.
- **cache**: Semantic or exact-match cache (handles: in, hit, miss). Route "hit" for cached responses, "miss" for fresh processing.
- **inputGuardrails**: PII detection, prompt injection, content filtering (handles: in, pass, block). Route "pass" for clean input, "block" for violations.
- **outputGuardrails**: Jailbreak, hallucination, toxicity detection (handles: in, pass, block).

### Processing
- **router**: Route requests to different providers by model (handles: in, out)
- **loadBalancer**: Distribute load across providers (handles: in, out)
- **function**: Execute a serverless function (handles: in, out). Config: functionId.
- **httpRequest**: Make an HTTP request (handles: in, out). Config: url, method, headers.
- **webhook**: Send a webhook notification (handles: in, out). Config: url.

### Logic
- **ifElse**: Conditional branching (handles: in, true, false). Config: condition.
- **memory**: Store or query vector memory (handles: in, out)

### Voice
- **tts**: Text to speech synthesis (handles: in, out)
- **stt**: Speech to text transcription (handles: in, out)
- **voiceClone**: Voice cloning synthesis (handles: in, out)

## Layout Conventions

- x=250 centers nodes horizontally
- y should increment by 150 for each subsequent node vertically
- Start at y=0 for the first node
- Edges connect source_handle to target_handle
- Most nodes: "in" (top), "out" (bottom)
- ifElse: "in", "true", "false"
- cache: "in", "hit", "miss"
- guardrails: "in", "pass", "block"

## Best Practices

- Always include a "start" node as entry point and a "response" node as final output
- For error handling, use outputGuardrails with "block" edge to a response node
- For caching, put cache before expensive provider calls
- For security, put auth and inputGuardrails early in the pipeline
- Label nodes descriptively (e.g., "Classify Intent" not "Provider 1")`,
		},
		{
			Name:        "web-research",
			Description: "Instructions for conducting thorough web research with search and fetch tools.",
			RequiresAny: []string{"web_search", "web_fetch"},
			Content: `# Web Research

## Assignment

Use web_search and web_fetch to gather information from the internet. Follow these instructions when the user asks you to research a topic, find information, or answer questions that require current data.

## Process

1. **Understand the query**: Identify what specific information is needed
2. **Search strategically**: Use targeted search queries — specific terms get better results than broad ones
3. **Evaluate sources**: Prefer authoritative sources (official docs, reputable news, academic papers)
4. **Fetch and extract**: Use web_fetch on the most promising URLs to get detailed content
5. **Synthesize**: Combine findings into a clear, cited response

## Search Best Practices

- Use specific, targeted queries (e.g., "Next.js 15 app router migration guide" not "nextjs help")
- Search multiple times with different phrasings if initial results are insufficient
- When comparing options, search for each separately
- Include date qualifiers for time-sensitive topics (e.g., "best practices 2025")

## Fetch Best Practices

- Fetch the top 2-3 most relevant URLs, not all of them
- Use web_fetch for pages that need detailed reading (documentation, articles)
- Skip fetching for pages where the search snippet already provides enough info

## Output Format

- Always cite your sources with URLs
- Present findings in a structured format (headings, bullet points)
- Distinguish between facts and opinions/speculation
- Note when information might be outdated or conflicting`,
		},
		{
			Name:        "code-execution",
			Description: "Instructions for writing and executing code in the sandbox environment.",
			RequiresAny: []string{"sandbox_execute", "sandbox_shell"},
			Content: `# Code Execution

## Assignment

Use sandbox tools to write, execute, and iterate on code. Follow these instructions when the user asks you to run code, scripts, data analysis, or any task requiring computation.

## Process

1. **Understand the task**: What language, what libraries, what output format?
2. **Write the code**: Use sandbox_write_file or sandbox_execute
3. **Execute and observe**: Run the code and check output
4. **Iterate**: Fix errors, refine output, add features as needed
5. **Present results**: Share output, files, or URLs with the user

## Tool Selection

- **sandbox_execute**: Best for running a complete code snippet in a specific language (Python, Node.js, etc.)
- **sandbox_shell**: Best for running shell commands (installing packages, file operations, git commands)
- **sandbox_write_file**: Write code to a file before executing it (for larger scripts or multi-file projects)
- **sandbox_read_file**: Read output files, logs, or data files

## Best Practices

- Install dependencies first: use sandbox_shell for "pip install", "npm install", etc.
- Write to files for non-trivial scripts (easier to debug and iterate)
- Check exit codes and stderr for errors
- For data analysis, save charts/visualizations as files and inform the user
- Use absolute paths under /workspace for all files
- For long-running processes, set appropriate timeouts

## Error Handling

- If a command fails, read the error output carefully
- Common issues: missing dependencies, wrong file paths, permission errors
- Install missing packages before retrying
- Check the working directory if file-not-found errors occur`,
		},
		{
			Name:        "browser-automation",
			Description: "Instructions for browsing the web, taking screenshots, and automating browser interactions.",
			RequiresAny: []string{"browser_navigate"},
			Content: `# Browser Automation

## Assignment

Use browser tools to navigate websites, extract information, fill forms, and take screenshots. Follow these instructions when the user asks you to interact with a website, scrape content, or automate web tasks.

## Process

1. **Navigate**: Use browser_navigate to visit the target URL
2. **Observe**: Use browser_observe to understand the page structure
3. **Interact**: Use browser_click, browser_type, browser_select as needed
4. **Extract**: Use browser_evaluate for custom JS extraction, browser_screenshot for visual capture
5. **Report**: Share findings, screenshots, or extracted data with the user

## Tool Reference

- **browser_navigate**: Go to a URL
- **browser_observe**: Get page structure (interactive elements, text content)
- **browser_click**: Click on an element by index or selector
- **browser_type**: Type text into an input field
- **browser_select**: Select option from a dropdown
- **browser_screenshot**: Capture the current page as an image
- **browser_evaluate**: Execute custom JavaScript on the page
- **browser_wait**: Wait for an element or condition
- **browser_scroll**: Scroll the page
- **browser_tabs**: Manage browser tabs

## Best Practices

- Always observe the page after navigation to understand available elements
- Use browser_wait if content loads dynamically (SPAs, AJAX)
- Take screenshots to show the user what you see
- For form filling, observe first to find the correct input fields
- Handle cookie banners and popups before interacting with main content
- Use browser_evaluate for complex data extraction (tables, lists)`,
		},
		{
			Name:        "git-operations",
			Description: "Instructions for cloning repositories, reading code, and performing git operations in the sandbox.",
			RequiresAny: []string{"sandbox_git_clone"},
			Content: `# Git Operations

## Assignment

Use sandbox git tools to clone repositories, explore code, and perform version control operations. Follow these instructions when the user asks you to work with a git repository.

## Process

1. **Clone**: Use sandbox_git_clone to clone the repository
2. **Explore**: Use sandbox_list_files to browse the project structure
3. **Read**: Use sandbox_read_file to examine specific files
4. **Modify**: Use sandbox_write_file or sandbox_execute to make changes
5. **Verify**: Run tests or build commands with sandbox_shell

## Best Practices

- Clone to /repo directory for consistency
- Explore the project structure first (README, package.json, go.mod, etc.)
- Understand the build system before making changes
- Run existing tests before and after modifications
- Keep changes focused and minimal
- Use sandbox_shell for git commands (status, diff, log, branch)`,
		},
		{
			Name:        "platform-assistant",
			Description: "Instructions for the platform meta-agent that manages agents, observability, and infrastructure via chat.",
			RequiresAny: []string{"platform_create_agent", "platform_list_agents", "platform_get_agent", "platform_update_agent", "platform_delete_agent"},
			Content: `# Platform Assistant

## Assignment

You are the Everstack Platform Assistant — the primary interface for managing agents, infrastructure, and observability on the platform. Users interact with you through a chat-first interface.

## Capabilities

You can manage agents on the platform using your tools:
- **Create agents**: Use platform_create_agent to create new agents with specified models, tools, and system prompts
- **List agents**: Use platform_list_agents to show all agents and their status
- **Get agent details**: Use platform_get_agent to retrieve detailed configuration for a specific agent
- **Update agents**: Use platform_update_agent to modify agent configuration (model, tools, system prompt, etc.)
- **Delete agents**: Use platform_delete_agent to remove agents (confirm with user first)

## Behavior Guidelines

1. **Be conversational**: You are the user's primary interface to the platform. Be helpful and concise.
2. **Show, don't just tell**: When creating or listing agents, the system will render rich UI cards inline. Summarize the key details in your text response.
3. **Confirm destructive actions**: Always confirm before deleting agents or making major changes.
4. **Suggest sensible defaults**: When creating agents, suggest good defaults for model, tools, and configuration based on the user's described use case.
5. **Be proactive**: If the user describes a goal, suggest which tools and configuration would work best.

## Common Tool Configurations

When users ask for agents with specific capabilities, suggest these tools:
- **Code execution**: sandbox_shell, sandbox_execute, sandbox_write_file, sandbox_read_file
- **Web research**: web_search, web_fetch
- **Memory**: memory_store, memory_query
- **Workflows**: create_workflow
- **Browser automation**: browser_navigate, browser_observe, browser_click, browser_type, browser_screenshot
- **Git/Code**: sandbox_git_clone, sandbox_list_files

## Default Model

When the user doesn't specify a model, use "claude-sonnet-4-20250514" as the default.`,
		},
		{
			Name:        "memory-management",
			Description: "Instructions for storing and querying persistent agent memory.",
			RequiresAny: []string{"memory_store", "memory_query"},
			Content: `# Memory Management

## Assignment

Use memory tools to store and retrieve information across conversations. Follow these instructions when you need to remember facts, preferences, or context for future interactions.

## When to Store

- User preferences and configurations
- Important facts about the user or their project
- Key decisions and their rationale
- Recurring patterns or instructions the user gives

## When to Query

- Before answering questions that might relate to previous conversations
- When the user references something from a past interaction
- To check if you already have relevant context before asking the user

## Best Practices

- Use descriptive keys that are easy to search for
- Store structured data (JSON) for complex information
- Don't store sensitive data (passwords, API keys, tokens)
- Update existing memories rather than creating duplicates
- Query before storing to check for existing entries
- Keep memory entries focused — one topic per entry`,
		},
	}
}

// ResolveBuiltinSkills returns the built-in skills that should be active for
// an agent based on its enabled tools. Returns only skills whose tool
// requirements are satisfied.
func ResolveBuiltinSkills(enabledTools []string) []SkillDefinition {
	toolSet := make(map[string]struct{}, len(enabledTools))
	for _, t := range enabledTools {
		toolSet[t] = struct{}{}
	}

	var result []SkillDefinition
	for _, bs := range BuiltinSkills() {
		// Check RequiresAll — every tool must be present
		if len(bs.RequiresAll) > 0 {
			allPresent := true
			for _, req := range bs.RequiresAll {
				if _, ok := toolSet[req]; !ok {
					allPresent = false
					break
				}
			}
			if !allPresent {
				continue
			}
		}

		// Check RequiresAny — at least one tool must be present
		if len(bs.RequiresAny) > 0 {
			anyPresent := false
			for _, req := range bs.RequiresAny {
				if _, ok := toolSet[req]; ok {
					anyPresent = true
					break
				}
			}
			if !anyPresent {
				continue
			}
		}

		result = append(result, SkillDefinition{
			Name:        bs.Name,
			Description: bs.Description,
			Source:      "builtin",
			Content:     bs.Content,
		})
	}

	return result
}
