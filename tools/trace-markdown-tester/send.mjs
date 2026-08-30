#!/usr/bin/env node
// Trace markdown tester
//
// Sends hand-crafted traces to a local Everstack instance so you can eyeball how
// the trace detail sheet renders markdown. It POSTs raw OTLP/HTTP JSON (protojson
// ExportTraceServiceRequest) straight at /v1/traces -- no SDK, no deps, full
// control over span attributes so every markdown render path can be driven
// deliberately.
//
// The trace sheet renders markdown through TWO different engines:
//   - trace-overview.tsx:890 + conversation-view.tsx  -> BARE react-markdown (CommonMark only, NO gfm)
//   - tool-call-card.tsx (AgentMarkdown)              -> marked + gfm + shiki highlighting
// The same kitchen-sink string is pushed through each so you can compare.
//
// Usage:
//   EVERSTACK_API_KEY=sk-mf-... node tools/trace-markdown-tester/send.mjs [scenario]
//   scenario = all (default) | flat | conversation | reasoning | tools
//
// Env:
//   EVERSTACK_OTLP_URL  (optional)  default http://localhost:8089/v1/traces
//   EVERSTACK_API_KEY   (optional)  API key, sent as X-Api-Key. Not needed for a
//                                   local standalone instance: LocalScopeResolver
//                                   injects the default local tenant and OTLP's
//                                   WithTenantAuth falls back to it (auth.go:45).
//                                   Required only when targeting a shared/managed
//                                   gateway. Mint one at /vault/api-keys.

import { randomBytes } from 'node:crypto'

const URL = process.env.EVERSTACK_OTLP_URL || 'http://localhost:8089/v1/traces'
const API_KEY = process.env.EVERSTACK_API_KEY
const SCENARIO = (process.argv[2] || 'all').toLowerCase()

// ---------------------------------------------------------------------------
// Markdown fixtures
// ---------------------------------------------------------------------------

// Kitchen-sink: exercises CommonMark + GFM. GFM-only features (table,
// strikethrough, task list, autolink, footnote) are called out inline so you can
// see at a glance which engine dropped them.
const KITCHEN_SINK = `# Markdown kitchen sink

A paragraph with **bold**, *italic*, \`inline code\`, and a [link](https://everstack.ai).

## Lists

- unordered one
- unordered two
  - nested a
  - nested b
- unordered three

1. ordered one
2. ordered two
3. ordered three

### Task list (GFM only)

- [x] done item
- [ ] todo item
- [ ] another todo

## Blockquote

> A blockquote.
> Second line of the same quote.
>
> > Nested quote.

## Code block

\`\`\`ts
export function greet(name: string): string {
  // syntax highlighting only exists in AgentMarkdown (shiki)
  return \`hello, \${name}\`
}
\`\`\`

\`\`\`python
def fib(n: int) -> int:
    return n if n < 2 else fib(n - 1) + fib(n - 2)
\`\`\`

## Table (GFM only)

| Feature        | CommonMark | GFM |
| -------------- | :--------: | :-: |
| Tables         |     no     | yes |
| Strikethrough  |     no     | yes |
| Task lists     |     no     | yes |

## Inline extras

Strikethrough (GFM): ~~deleted text~~.

Autolink (GFM): https://everstack.ai/observability

Footnote (GFM): here is a claim.[^1]

[^1]: And the supporting footnote.

---

Horizontal rule above. Emoji: :rocket: (only some engines). Done.`

const USER_PROMPT = `Please summarise the markdown rendering rules. Use a **table** if helpful, and include a fenced code sample.`

const SYSTEM_PROMPT = `You are a docs assistant. Respond in rich markdown: headings, lists, a table, and a fenced code block.`

// ---------------------------------------------------------------------------
// OTLP JSON helpers (protojson: bytes -> base64, timestamps -> string nanos)
// ---------------------------------------------------------------------------

const traceId = () => randomBytes(16).toString('base64')
const spanId = () => randomBytes(8).toString('base64')

// use a fixed "now" so a whole trace shares a coherent window
const nowNs = () => (BigInt(Date.now()) * 1_000_000n)

const attr = (key, stringValue) => ({ key, value: { stringValue } })
const intAttr = (key, n) => ({ key, value: { intValue: String(n) } })

function span({ name, tid, sid, parent, startNs, durMs, kind = 3, attributes = [] }) {
  const start = startNs
  const end = start + BigInt(durMs) * 1_000_000n
  return {
    traceId: tid,
    spanId: sid,
    parentSpanId: parent,
    name,
    kind, // 3 = CLIENT, 2 = SERVER
    startTimeUnixNano: start.toString(),
    endTimeUnixNano: end.toString(),
    attributes,
    status: { code: 1 }, // OK
  }
}

// A trace = a SERVER root + one GENERATION child carrying the payload.
// `genAttrs` are the input/output attributes that drive the markdown panels.
function buildTrace({ label, genAttrs }) {
  const tid = traceId()
  const rootSid = spanId()
  const genSid = spanId()
  const start = nowNs()

  const root = span({
    name: `markdown-test.${label}`,
    tid,
    sid: rootSid,
    parent: '',
    startNs: start,
    durMs: 1200,
    kind: 2,
    attributes: [
      attr('service.name', 'trace-markdown-tester'),
      attr('scenario', label),
    ],
  })

  const gen = span({
    name: 'provider.chat', // starts with "provider." -> picked as primary span
    tid,
    sid: genSid,
    parent: rootSid,
    startNs: start + 50_000_000n,
    durMs: 1000,
    kind: 3,
    attributes: [
      attr('observation.type', 'GENERATION'),
      attr('gen_ai.system', 'markdown-lab'),
      attr('gen_ai.request.model', 'md-torture-1'),
      attr('llm.request.model', 'md-torture-1'),
      attr('llm.response.model', 'md-torture-1'),
      intAttr('gen_ai.usage.input_tokens', 42),
      intAttr('gen_ai.usage.output_tokens', 256),
      ...genAttrs,
    ],
  })

  return {
    resourceSpans: [
      {
        resource: {
          attributes: [attr('service.name', 'trace-markdown-tester')],
        },
        scopeSpans: [
          {
            scope: { name: 'trace-markdown-tester', version: '1.0.0' },
            spans: [root, gen],
          },
        ],
      },
    ],
  }
}

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------

// 1. FLAT: plain markdown strings on `input` / `output`.
//    Not message-shaped, not JSON object -> flat react-markdown path
//    (trace-overview.tsx:890). CommonMark only.
function scenarioFlat() {
  return buildTrace({
    label: 'flat',
    genAttrs: [
      attr('input', USER_PROMPT),
      attr('output', KITCHEN_SINK),
      attr('trace.input', USER_PROMPT),
      attr('trace.output', KITCHEN_SINK),
    ],
  })
}

// 2. CONVERSATION: OpenAI-shaped messages -> ConversationView, each message's
//    text content rendered via react-markdown (conversation-view.tsx:36).
function scenarioConversation() {
  const messages = [
    { role: 'system', content: SYSTEM_PROMPT },
    { role: 'user', content: USER_PROMPT },
  ]
  const choices = [
    {
      index: 0,
      message: { role: 'assistant', content: KITCHEN_SINK },
      finish_reason: 'stop',
    },
  ]
  return buildTrace({
    label: 'conversation',
    genAttrs: [
      attr('llm.request.messages', JSON.stringify(messages)),
      attr('llm.response.choices', JSON.stringify(choices)),
    ],
  })
}

// 3. REASONING: assistant message with reasoning_content -> ReasoningBlock
//    markdown (conversation-view.tsx:105), plus normal content markdown.
function scenarioReasoning() {
  const messages = [{ role: 'user', content: 'Explain your reasoning in markdown.' }]
  const reasoning = `## Reasoning (thinking)

Working through it step by step:

1. First I consider the **table** requirement.
2. Then the ~~strikethrough~~ (GFM).
3. Finally the code fence.

\`\`\`js
const answer = 42
\`\`\``
  const choices = [
    {
      index: 0,
      message: {
        role: 'assistant',
        content: KITCHEN_SINK,
        reasoning_content: reasoning,
      },
      finish_reason: 'stop',
    },
  ]
  return buildTrace({
    label: 'reasoning',
    genAttrs: [
      attr('llm.request.messages', JSON.stringify(messages)),
      attr('llm.response.choices', JSON.stringify(choices)),
    ],
  })
}

// 4. TOOLS: assistant emits a tool call; the tool result is markdown. Tool
//    results render through AgentMarkdown (marked + gfm + shiki) in
//    tool-call-card.tsx -- the OTHER engine, for side-by-side comparison.
function scenarioTools() {
  const toolResult = `### Search results

| rank | title            | score |
| ---- | ---------------- | ----- |
| 1    | Observability    | 0.98  |
| 2    | Trace detail     | 0.91  |

- [x] indexed
- [ ] re-ranked

\`\`\`json
{ "hits": 2, "took_ms": 12 }
\`\`\`

Note: ~~deprecated~~ endpoint replaced.`

  const messages = [
    { role: 'user', content: 'Search the docs for **observability**.' },
    {
      role: 'assistant',
      content: null,
      tool_calls: [
        {
          id: 'call_1',
          type: 'function',
          function: {
            name: 'search_docs',
            arguments: JSON.stringify({ query: 'observability', top_k: 2 }),
          },
        },
      ],
    },
    { role: 'tool', tool_call_id: 'call_1', name: 'search_docs', content: toolResult },
  ]
  const choices = [
    {
      index: 0,
      message: { role: 'assistant', content: `Here is what I found:\n\n${toolResult}` },
      finish_reason: 'stop',
    },
  ]
  return buildTrace({
    label: 'tools',
    genAttrs: [
      attr('llm.request.messages', JSON.stringify(messages)),
      attr('llm.response.choices', JSON.stringify(choices)),
    ],
  })
}

const SCENARIOS = {
  flat: scenarioFlat,
  conversation: scenarioConversation,
  reasoning: scenarioReasoning,
  tools: scenarioTools,
}

// ---------------------------------------------------------------------------
// Send
// ---------------------------------------------------------------------------

async function send(label, payload) {
  const tid = payload.resourceSpans[0].scopeSpans[0].spans[0].traceId
  // hex form of the trace id -- this is how it appears in ClickHouse / the URL
  const tidHex = Buffer.from(tid, 'base64').toString('hex')
  const headers = { 'Content-Type': 'application/json' }
  // OTLP ingest authenticates via Bearer / x-evs-api-key (its own path,
  // not the gateway's X-Api-Key). Locally the key is optional — WithTenantAuth
  // falls back to the default tenant (auth.go:45).
  if (API_KEY) {
    headers['Authorization'] = `Bearer ${API_KEY}`
    headers['x-evs-api-key'] = API_KEY
  }
  const res = await fetch(URL, {
    method: 'POST',
    headers,
    body: JSON.stringify(payload),
  })
  const text = await res.text()
  if (!res.ok) {
    console.error(`  ✗ ${label}: HTTP ${res.status} ${res.statusText}\n    ${text.slice(0, 400)}`)
    return false
  }
  console.log(`  ✓ ${label}  traceId=${tidHex}`)
  return true
}

async function main() {
  const toRun =
    SCENARIO === 'all' ? Object.keys(SCENARIOS) : SCENARIO in SCENARIOS ? [SCENARIO] : null

  if (!toRun) {
    console.error(`unknown scenario "${SCENARIO}". valid: all, ${Object.keys(SCENARIOS).join(', ')}`)
    process.exit(1)
  }

  console.log(`→ POST ${URL}`)
  let ok = true
  for (const label of toRun) {
    const payload = SCENARIOS[label]()
    ok = (await send(label, payload)) && ok
  }
  if (ok) {
    console.log('\ndone. open the admin Traces view and look for "markdown-test.<scenario>".')
  } else {
    process.exit(1)
  }
}

main().catch((err) => {
  console.error('send failed:', err.message)
  console.error('is the gateway up on', URL, '? (make dev / air serve on :8089)')
  process.exit(1)
})
