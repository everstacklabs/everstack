package memory

// extractionPrompt is the system prompt for the extraction LLM call.
const extractionPrompt = `You are a memory extraction system. Analyze the conversation turn below and extract structured facts and instructions.

Rules:
- Facts are objective information stated by the user (name, preferences, project details, technical context).
- Instructions are directives the user gives about how the assistant should behave.
- Each fact must have a dot-notation key (e.g. "user.name", "project.language", "preference.editor").
- Confidence is 0.0 to 1.0. Explicit statements get 1.0, inferred information gets 0.5-0.8.
- Do NOT extract trivial or transient information (greetings, acknowledgments, etc).
- Do NOT extract information the assistant stated — only extract user-provided information.
- If there is nothing meaningful to extract, return empty arrays.

Scope rules — assign a scope to each extracted item:
- "user": Personal facts about the user (name, preferences, personal history, role, timezone)
- "agent": Domain/project knowledge relevant to this agent (project.language, deployment.target, codebase patterns)
- "global": Organization-wide facts that apply across all agents (company.name, company.stack, team conventions)

When in doubt, default to "agent".

Respond ONLY with valid JSON:
{
  "facts": [
    {"key": "user.name", "value": "Arnab", "confidence": 1.0, "scope": "user"},
    {"key": "project.language", "value": "Go + TypeScript", "confidence": 0.9, "scope": "agent"}
  ],
  "instructions": [
    {"content": "Always use pnpm for package management", "confidence": 0.9, "scope": "agent"}
  ]
}`

// extractionUserTemplate is the user message template for extraction.
// Placeholders: {{.UserInput}}, {{.AssistantOutput}}
const extractionUserTemplate = `User said:
{{.UserInput}}

Assistant responded:
{{.AssistantOutput}}`

// summarizationPrompt is the system prompt for session summarization.
const summarizationPrompt = `You are a session summarization system. Given a conversation between a user and an AI assistant, produce a concise summary that captures:

1. What the user wanted to accomplish
2. Key decisions made
3. Important outcomes or artifacts produced
4. Any unresolved issues

Keep the summary to 2-4 sentences. Focus on information that would be useful context for future sessions.
Respond with just the summary text, no JSON or formatting.`

// consolidationPrompt is the system prompt for memory consolidation.
const consolidationPrompt = `You are a memory consolidation system. Given a list of related facts, merge them into a single authoritative fact.

Rules:
- Keep the most recent and specific information.
- If facts conflict, prefer the one with higher confidence.
- Produce a single merged fact with an appropriate confidence score.

Respond ONLY with valid JSON:
{
  "key": "the.fact.key",
  "value": "the merged value",
  "confidence": 0.95
}`
