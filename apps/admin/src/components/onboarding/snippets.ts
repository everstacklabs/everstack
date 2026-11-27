// The one source of truth for the quickstart code the launch center shows,
// on the wizard's test step and again on the completion screen. Kept free of
// React so the exact text can be unit tested.
//
// Gateway path: the OpenAI SDKs pointed at this instance's /v1, authenticated
// with an issued key as a Bearer token (the gateway accepts either that or
// x-evs-api-key). Agent path: the sessions API (open a session, run a turn),
// which takes the same platform key in x-evs-api-key. Never the deploy invoke
// endpoint: that one takes a deployment key, so a platform key there is a 401.
//
// The key never sits inline in a command: every snippet reads it from the
// EVERSTACK_API_KEY environment variable and shows the export that sets it,
// so a real key (shown once, right after it was generated) is one copyable
// line and the code itself can be pasted anywhere.
//
// Lines are kept under ~72 characters so the panel never scrolls sideways;
// the export line with a real key is the one exception and wraps as a unit.

import type { OnboardingPath } from '@/stores/onboarding-store'

export type SnippetLang = 'curl' | 'python' | 'node' | 'go'
export type LangSnippets = Record<SnippetLang, string>

export interface SnippetTab {
  value: SnippetLang
  label: string
  // Shiki grammar.
  language: string
  filename: string
}

export const SNIPPET_TABS: readonly SnippetTab[] = [
  { value: 'curl', label: 'cURL', language: 'bash', filename: 'request.sh' },
  { value: 'python', label: 'Python', language: 'python', filename: 'main.py' },
  { value: 'node', label: 'Node', language: 'typescript', filename: 'main.ts' },
  { value: 'go', label: 'Go', language: 'go', filename: 'main.go' },
]

// Stand-ins for values the console cannot show (a key after it was shown
// once, an agent not created yet). Plain words, never angle brackets, so a
// copied snippet stays valid shell and code and fails with a clear auth or
// not-found error instead of a syntax one.
export const KEY_PLACEHOLDER = 'your-everstack-api-key'
export const KEY_ENV = 'EVERSTACK_API_KEY'
export const AGENT_PLACEHOLDER = 'your-agent-id'
export const TEST_PROMPT = 'Say hello from Everstack'

export interface SnippetInput {
  path: OnboardingPath
  // This instance's gateway origin, no trailing slash.
  base: string
  // Gateway model reference, e.g. "@anthropic/claude-haiku-4-5", or the bare
  // trial model. Always a real model: see quickstartModel.
  model: string
  agentId?: string | null
  // The real key, inlined only while it is held in memory right after it was
  // generated. Null keeps the placeholder.
  apiKey?: string | null
}

export function buildSnippets({
  path,
  base,
  model,
  agentId,
  apiKey,
}: SnippetInput): LangSnippets {
  const key = apiKey || KEY_PLACEHOLDER
  const m = model
  // The shell line that sets the key; shown as a comment in code samples.
  const exportKey = `export ${KEY_ENV}="${key}"`

  if (path === 'gateway') {
    return {
      curl: `${exportKey}

curl ${base}/v1/chat/completions \\
  -H "Authorization: Bearer $${KEY_ENV}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${m}",
    "messages": [
      { "role": "user", "content": "${TEST_PROMPT}" }
    ]
  }'`,
      python: `import os
from openai import OpenAI

# ${exportKey}
client = OpenAI(
    api_key=os.environ["${KEY_ENV}"],
    base_url="${base}/v1",
)

res = client.chat.completions.create(
    model="${m}",
    messages=[{"role": "user", "content": "${TEST_PROMPT}"}],
)
print(res.choices[0].message.content)`,
      node: `import OpenAI from "openai"

// ${exportKey}
const client = new OpenAI({
  apiKey: process.env.${KEY_ENV},
  baseURL: "${base}/v1",
})

const res = await client.chat.completions.create({
  model: "${m}",
  messages: [{ role: "user", content: "${TEST_PROMPT}" }],
})

console.log(res.choices[0].message.content)`,
      go: `package main

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	// ${exportKey}
	client := openai.NewClient(
		option.WithAPIKey(os.Getenv("${KEY_ENV}")),
		option.WithBaseURL("${base}/v1"),
	)

	params := openai.ChatCompletionNewParams{
		Model: "${m}",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("${TEST_PROMPT}"),
		},
	}
	res, err := client.Chat.Completions.New(context.Background(), params)
	if err != nil {
		panic(err)
	}

	fmt.Println(res.Choices[0].Message.Content)
}`,
    }
  }

  // The sessions API: open a session with the agent, then run a turn on it.
  // It takes the platform API key in the x-evs-api-key header. (Deploy invoke
  // is a different surface: it takes a deployment key, not this one.)
  const id = agentId || AGENT_PLACEHOLDER
  return {
    curl: `${exportKey}

# 1. Open a session with your agent
SESSION_ID=$(curl -s -X POST ${base}/v1/agents/sessions \\
  -H "x-evs-api-key: $${KEY_ENV}" \\
  -H "Content-Type: application/json" \\
  -d '{ "agent_id": "${id}" }' \\
  | grep -o '"id": *"[^"]*"' | head -1 | cut -d'"' -f4)

# 2. Send it a message
curl -X POST \\
  ${base}/v1/agents/sessions/$SESSION_ID/turns \\
  -H "x-evs-api-key: $${KEY_ENV}" \\
  -H "Content-Type: application/json" \\
  -d '{ "user_input": "${TEST_PROMPT}" }'`,
    python: `import os
from everstack import Everstack

# ${exportKey}
client = Everstack(
    api_key=os.environ["${KEY_ENV}"],
    base_url="${base}",
)

created = client.agents.sessions.create(agent_id="${id}")

res = client.agents.sessions.run_turn(
    created["session"]["id"],
    user_input="${TEST_PROMPT}",
)
turn = res["turn"]
print(turn.get("assistant_output") or turn.get("error"))`,
    node: `import Everstack from "@everstack/node"

// ${exportKey}
const client = new Everstack({
  apiKey: process.env.${KEY_ENV},
  baseUrl: "${base}",
})

// Opens a session with the agent and runs one turn on it.
const res = await client.agents.run(
  "${id}",
  "${TEST_PROMPT}",
)

console.log(res.turn.assistant_output || res.turn.error)`,
    go: `package main

import (
	"context"
	"fmt"
	"os"

	everstack "github.com/everstacklabs/everstack-go"
)

func main() {
	// ${exportKey}
	client := everstack.NewClient(
		os.Getenv("${KEY_ENV}"),
		everstack.WithBaseURL("${base}"),
	)
	ctx := context.Background()

	created, err := client.Agents.Sessions.Create(ctx, map[string]any{
		"agent_id": "${id}",
	})
	if err != nil {
		panic(err)
	}
	session := created["session"].(map[string]any)

	res, err := client.Agents.Sessions.RunTurn(
		ctx,
		session["id"].(string),
		map[string]any{"user_input": "${TEST_PROMPT}"},
	)
	if err != nil {
		panic(err)
	}

	turn := res["turn"].(map[string]any)
	if reply, ok := turn["assistant_output"]; ok {
		fmt.Println(reply)
	} else {
		fmt.Println("turn failed:", turn["error"])
	}
}`,
  }
}
