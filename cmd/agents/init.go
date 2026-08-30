package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [name]",
		Short: "Scaffold a new agent project directory",
		Long: `Scaffold a directory-based agent project.

Creates <name>/ with agent.yaml, instructions.md, an example project function
and an example skill. Deploy it with:

  cd <name> && evs deploy`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "my-agent"
			if len(args) == 1 {
				name = args[0]
			}
			if _, err := os.Stat(name); err == nil {
				return fmt.Errorf("directory %s already exists", name)
			}

			files := map[string]string{
				"agent.yaml":            initAgentYAML(name),
				"instructions.md":       initInstructions,
				"src/get_time.ts":       initTool,
				"skills/style/SKILL.md": initSkill,
			}
			for rel, content := range files {
				path := filepath.Join(name, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					return err
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created agent project %s/\n\n", name)
			fmt.Fprintf(out, "  %s/agent.yaml        agent config (model, tools, triggers)\n", name)
			fmt.Fprintf(out, "  %s/instructions.md   system prompt\n", name)
			fmt.Fprintf(out, "  %s/src/              project code executed in the agent sandbox\n", name)
			fmt.Fprintf(out, "  %s/skills/           markdown playbooks, loaded on demand\n", name)
			fmt.Fprintf(out, "  %s/subagents/        optional nested agent projects\n\n", name)
			fmt.Fprintf(out, "Next: cd %s && evs deploy\n", name)
			return nil
		},
	}
}

func initAgentYAML(name string) string {
	return `name: ` + strconv.Quote(name) + `
description: Describe what this agent does
model: claude-sonnet-5

# The system prompt lives in a plain markdown file.
instructions: ./instructions.md

limits:
  max_turns: 25
  max_tool_calls_per_turn: 10

permissions:
  task_mode: ask # ask | always | deny (subagent delegation approval)

# Model parameters merged into the agent config.
config:
  temperature: 0.3

# Builtin runtime tools.
tools:
  - web_search

# Project-local TypeScript, JavaScript, or Python exports. Every call runs
# from this immutable source revision inside the agent's sandbox.
functions:
  get_time:
    file: ./src/get_time.ts
    export: getTime
    description: Return the current server time
    parameters:
      type: object
      properties:
        echo:
          type: string

# Additional source files or directories bundled with the immutable revision.
# Function entrypoints, agent.yaml, instructions, and SKILL.md files are
# included automatically.
files:
  - ./src

# Markdown playbooks loaded lazily via use_skill.
skills:
  - ./skills/style

# Nested agent projects. Each directory is a full project of its own
# (agent.yaml + instructions.md, and optionally tools/ and skills/) and
# deploys as a subagent linked to this one. Subagents may not nest further.
# subagents:
#   - ./subagents/risk-reviewer

# Uncomment to run on a schedule or expose a webhook.
# triggers:
#   - type: cron
#     name: daily-digest
#     schedule: "0 9 * * *"
#     input: "Write the daily digest."
#   - type: webhook
#     name: inbound
`
}

const initInstructions = `# Role

You are a helpful agent. Explain what you can do when greeted.

# Guidelines

- Prefer tools over guessing.
- Say when you do not have enough context to answer safely.
`

const initTool = `// Project functions can import other files in this directory. The named
// export receives the tool-call arguments and its return value becomes the
// tool result.
export async function getTime(args: { echo?: string }) {
  return { now: new Date().toISOString(), echo: args?.echo ?? null };
}
`

const initSkill = `---
name: style
description: House style for user-facing answers.
---

# House style

- Lead with the answer, then the reasoning.
- Keep answers under six sentences unless asked for detail.
`
