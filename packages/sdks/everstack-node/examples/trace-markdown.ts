#!/usr/bin/env tsx
/**
 * Emit custom traces via the SDK to exercise how the trace detail sheet renders
 * markdown. Sends four self-contained traces through `client.traces.startSpan`,
 * each driving a different render path.
 *
 *   EVERSTACK_GATEWAY_URL=http://localhost:8089 \
 *   EVERSTACK_API_KEY=sk-... \
 *   pnpm tsx examples/trace-markdown.ts
 *
 * Against a local standalone instance no real key is needed — the OTLP receiver
 * falls back to the default local tenant — but the SDK requires a non-empty
 * apiKey, so a placeholder is used when unset.
 */

import { Everstack } from "../src/index.js";

const KITCHEN_SINK = `# Markdown kitchen sink

Paragraph with **bold**, *italic*, \`inline code\`, and a [link](https://everstack.ai).

## Lists
- one
- two
  - nested
1. first
2. second

### Task list (GFM only)
- [x] done
- [ ] todo

## Blockquote
> quoted line
> > nested quote

## Code
\`\`\`ts
export const greet = (n: string) => \`hello \${n}\`
\`\`\`

## Table (GFM only)
| feature | commonmark | gfm |
| ------- | :--------: | :-: |
| tables  |     no     | yes |

Strikethrough (GFM): ~~gone~~. Autolink: https://everstack.ai/observability

---
Done.`;

const USER_PROMPT = "Summarise the markdown rules. Use a **table** and a code fence.";

async function main() {
  const baseUrl = process.env.EVERSTACK_GATEWAY_URL ?? "http://localhost:8089";
  const apiKey = process.env.EVERSTACK_API_KEY ?? "sk-local-placeholder";
  const client = new Everstack({ apiKey, baseUrl });

  console.log(`→ emitting to ${baseUrl}/v1/traces`);

  // 1. flat: plain-string input/output -> flat markdown panel.
  {
    const span = client.traces.startSpan({ name: "markdown-sdk.flat", type: "GENERATION", model: "md-torture-1" });
    span.setInput(USER_PROMPT);
    span.setOutput(KITCHEN_SINK);
    await span.end();
    console.log(`  ✓ flat          traceId=${span.traceId}`);
  }

  // 2. conversation: message-shaped input/output -> structured conversation view.
  {
    const span = client.traces.startSpan({ name: "markdown-sdk.conversation", type: "GENERATION", model: "md-torture-1" });
    span.setMessages([
      { role: "system", content: "Respond in rich markdown with a table and a code fence." },
      { role: "user", content: USER_PROMPT },
    ]);
    span.setChoices([
      { index: 0, message: { role: "assistant", content: KITCHEN_SINK }, finish_reason: "stop" },
    ]);
    span.setUsage({ inputTokens: 42, outputTokens: 256, totalTokens: 298 });
    await span.end();
    console.log(`  ✓ conversation  traceId=${span.traceId}`);
  }

  // 3. reasoning: reasoning_content nested in the choice -> reasoning block.
  {
    const span = client.traces.startSpan({ name: "markdown-sdk.reasoning", type: "GENERATION", model: "md-torture-1" });
    span.setMessages([{ role: "user", content: "Explain your reasoning in markdown." }]);
    span.setChoices([
      {
        index: 0,
        message: {
          role: "assistant",
          content: KITCHEN_SINK,
          reasoning_content: "## Reasoning\n\n1. Consider the **table**.\n2. Then ~~strikethrough~~.\n\n```js\nconst answer = 42\n```",
        },
        finish_reason: "stop",
      },
    ]);
    await span.end();
    console.log(`  ✓ reasoning     traceId=${span.traceId}`);
  }

  // 4. tree: a root span with a GENERATION child (tool-style result markdown).
  {
    const root = client.traces.startSpan({ name: "markdown-sdk.tree", kind: "SERVER" });
    const child = root.startChild({ name: "provider.chat", type: "GENERATION", model: "md-torture-1" });
    child.setMessages([{ role: "user", content: "Search the docs for **observability**." }]);
    child.setChoices([
      { index: 0, message: { role: "assistant", content: `Here is what I found:\n\n${KITCHEN_SINK}` } },
    ]);
    await child.end();
    await root.end();
    console.log(`  ✓ tree          traceId=${root.traceId}`);
  }

  console.log("\ndone. open /observability/traces and look for \"markdown-sdk.<scenario>\".");
}

main().catch((err) => {
  console.error("failed:", err.message);
  process.exit(1);
});
