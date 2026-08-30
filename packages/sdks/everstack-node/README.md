# @everstack/node

Official Node.js SDK for the Everstack AI Gateway. Provides an OpenAI-style API with type-safe model selection across multiple AI providers.

## Installation

```bash
npm install @everstack/node
# or
pnpm add @everstack/node
# or
yarn add @everstack/node
```

## Quick Start

```typescript
import Everstack from "@everstack/node";

const client = new Everstack({
  apiKey: process.env.EVERSTACK_API_KEY!,
});

const response = await client.chat.completions.create({
  model: "@openai/gpt-4o",
  messages: [{ role: "user", content: "Hello!" }],
});

console.log(response.choices[0].message.content);
```

## Features

- **OpenAI-compatible API** - Familiar interface for chat completions and embeddings
- **Type-safe model selection** - Autocomplete for all supported models (`@provider/model`)
- **Streaming support** - Native async iterators for streaming responses
- **Multi-provider routing** - Seamlessly switch between OpenAI, Anthropic, Google, Mistral, and more
- **Fallback handling** - Automatic model fallback with detailed info
- **Memory store** - Vector-based document storage and semantic search
- **Agents API** - Agent CRUD, sessions, runtime turns, reviews, sandboxes, deployments, triggers, SSH, and GitHub integration
- **Datasets + Evaluations API** - Dataset management, items, score configs, builtin metrics, eval runs, and eval schedules
- **OpenAI SDK compatibility** - Use with the official OpenAI SDK

## Usage

### Chat Completions

```typescript
import Everstack from "@everstack/node";

const client = new Everstack({ apiKey: "pk_..." });

// Non-streaming
const response = await client.chat.completions.create({
  model: "@openai/gpt-4o",
  messages: [
    { role: "system", content: "You are a helpful assistant." },
    { role: "user", content: "What is the capital of France?" },
  ],
  temperature: 0.7,
  max_tokens: 1000,
});

console.log(response.choices[0].message.content);
```

### Streaming

```typescript
const stream = await client.chat.completions.create({
  model: "@anthropic/claude-sonnet-4-20250514",
  messages: [{ role: "user", content: "Write a haiku about coding." }],
  stream: true,
});

for await (const chunk of stream) {
  const content = chunk.choices[0]?.delta?.content;
  if (content) {
    process.stdout.write(content);
  }
}
```

### Embeddings

```typescript
const response = await client.embeddings.create({
  model: "@openai/text-embedding-3-small",
  input: "Hello, world!",
});

console.log(response.data[0].embedding);
```

### List Available Models

```typescript
const models = await client.models.list();
console.log(models.data.map((m) => m.id));
```

### Memory Store

Create collections, add documents, and run semantic queries against your vector memory store.

```typescript
// Create a collection
const coll = await client.memory.collections.create({
  name: "product-docs",
  embedding_model: "text-embedding-3-small",
  embedding_dimension: 1536,
});

// Add documents (automatically chunked and embedded)
await client.memory.collections.addDocuments("product-docs", {
  documents: [
    {
      content: "Everstack routes AI requests across providers.",
      source: "intro.md",
    },
    { content: "Authentication uses API keys.", source: "auth.md" },
  ],
});

// Semantic search
const { results } = await client.memory.collections.query("product-docs", {
  query: "how does authentication work?",
  top_k: 5,
});

for (const r of results) {
  console.log(`[${r.score.toFixed(3)}] ${r.chunk_text}`);
}

// List collections
const { collections } = await client.memory.collections.list();

// Delete a collection
await client.memory.collections.delete("product-docs");
```

### Agents

```typescript
const agent = await client.agents.definitions.create({
  name: "Support Agent",
  systemPrompt: "You are a helpful support assistant.",
});

const session = await client.agents.sessions.create({
  agentId: agent.agent?.id,
});

const turn = await client.agents.sessions.runTurn({
  sessionId: session.session?.id,
  input: "Summarize open tickets",
});

console.log(turn.outputText);
```

### Datasets and Evaluations

```typescript
const dataset = await client.datasets.create({
  name: "support-evals",
  description: "Regression set for support agent",
});

await client.datasets.items.createBatch({
  datasetId: dataset.dataset?.id,
  items: [
    { input: { user_query: "How do I reset my password?" } },
    { input: { user_query: "Where is billing history?" } },
  ],
});

const run = await client.evaluations.runs.create({
  datasetId: dataset.dataset?.id,
  name: "nightly-regression",
});

console.log(run.evalRun?.id);
```

### Emitting Custom Traces

Emit self-contained OpenTelemetry spans directly to the instance. Unlike custom
observations (append-only overlays that render on top of an existing trace), a
span started here is a first-class trace: it appears in the trace list and
renders in the detail sheet on its own. The setters map onto the exact
attributes the trace UI reads for input/output/messages.

```typescript
const span = client.traces.startSpan({
  name: "chat",
  type: "GENERATION",
  model: "@openai/gpt-4o",
});

span.setMessages([
  { role: "system", content: "You are helpful." },
  { role: "user", content: "Explain markdown tables." },
]);
span.setChoices([
  { index: 0, message: { role: "assistant", content: "| a | b |\n|---|---|" } },
]);
span.setUsage({ inputTokens: 42, outputTokens: 128 });

await span.end();
console.log(span.traceId); // hex id, for building a trace URL

// Build a span tree with startChild:
const root = client.traces.startSpan({ name: "request", kind: "SERVER" });
const gen = root.startChild({ name: "provider.chat", type: "GENERATION" });
gen.setInput("prompt text"); // plain string -> flat markdown
await gen.end("response markdown");
await root.end();
```

Spans are sent to the OTLP receiver at `{baseUrl}/v1/traces`. For a
plain-markdown payload use `setInput`/`setOutput` with a string; for a chat
conversation use `setMessages`/`setChoices` (reasoning renders when a choice's
`message` carries `reasoning_content`).

### Webhooks That Emit Traces

The `examples/webhooks-traces.ts` script starts a local webhook receiver and
emits a full trace tree for each inbound webhook. It can also create and
test-fire an Everstack agent webhook trigger.

```bash
# trace an inbound sample webhook
EVERSTACK_GATEWAY_URL=http://localhost:8089 \
EVERSTACK_API_KEY=sk-local-placeholder \
pnpm --dir packages/sdks/everstack-node run example:webhooks-traces

# keep the local webhook receiver running
WEBHOOK_SECRET=dev-secret \
pnpm --dir packages/sdks/everstack-node run example:webhooks-traces -- --listen

# also create and test-fire an agent webhook trigger
EVERSTACK_API_KEY=pk_... RUN_AGENT_TRIGGER=1 \
pnpm --dir packages/sdks/everstack-node run example:webhooks-traces
```

Useful options: `--setup-trigger`, `--run-llm`, `--keep-agent`, `--no-sample`.
For cloud or multi-tenant setups set `EVERSTACK_ORG_ID` and optionally
`EVERSTACK_USER_ID`.

### SDK Method Groups

- `client.agents.definitions.*` - agent CRUD + import/export
- `client.agents.sessions.*` - session create/get/list + run/stream/steer/cancel/complete turns
- `client.agents.sandboxes.*` - lifecycle, templates, executions, events, ports
- `client.agents.deployments.*`, `client.agents.triggers.*`, `client.agents.memories.*`, `client.agents.ssh.*`, `client.agents.integrations.github.*`
- `client.datasets.*`, `client.datasets.items.*`, `client.datasets.scoreConfigs.*`, `client.datasets.metrics.listBuiltin()`
- `client.evaluations.runs.*`, `client.evaluations.schedules.*`

## Type-Safe Models

The SDK provides full TypeScript autocomplete for all supported models:

```typescript
import Everstack, { type AllModels } from "@everstack/node";

const client = new Everstack({ apiKey: "pk_..." });

// Autocomplete shows all available models
await client.chat.completions.create({
  model: "@openai/gpt-4o", // Autocomplete: @openai/gpt-4o, @anthropic/claude-sonnet-4-20250514, etc.
  messages: [...],
});
```

### Available Model Types

```typescript
import type {
  AllModels, // All models
  OpenaiModel, // OpenAI models only
  AnthropicModel, // Anthropic models only
  GoogleModel, // Google models only
  MistralModel, // Mistral models only
} from "@everstack/node";
```

### Model Utilities

```typescript
import {
  allModels, // Array of all model IDs
  providers, // Array of provider names
  modelMetadata, // Detailed metadata for each model
  getModelMetadata, // Get metadata for a specific model
  getModelsByProvider, // Get all models for a provider
  isValidModel, // Type guard for model IDs
  parseModelId, // Parse @provider/model into parts
} from "@everstack/node";

// Check if a model is valid
if (isValidModel(userInput)) {
  await client.chat.completions.create({ model: userInput, ... });
}

// Get model metadata
const meta = getModelMetadata("@openai/gpt-4o");
console.log(meta?.maxTokens); // 128000
console.log(meta?.capabilities); // ['chat', 'function_calling', 'vision']
```

## OpenAI SDK Compatibility

You can use the official OpenAI SDK with Everstack by configuring the base URL and headers:

```typescript
import OpenAI from "openai";
import { createHeaders, EVERSTACK_GATEWAY_URL } from "@everstack/node";

const openai = new OpenAI({
  baseURL: EVERSTACK_GATEWAY_URL,
  apiKey: "ignored", // Required by OpenAI SDK but not used
  defaultHeaders: createHeaders({
    apiKey: "pk_...",
    provider: "@openai",
  }),
});

const response = await openai.chat.completions.create({
  model: "gpt-4o", // Note: Use model name without @provider/ prefix
  messages: [{ role: "user", content: "Hello!" }],
});
```

Or use the convenience function:

```typescript
import OpenAI from "openai";
import { createOpenAIConfig } from "@everstack/node";

const openai = new OpenAI(
  createOpenAIConfig({
    apiKey: "pk_...",
    provider: "@openai",
  }),
);
```

## Error Handling

The SDK provides typed errors for common failure cases:

```typescript
import Everstack, {
  APIError,
  AuthenticationError,
  RateLimitError,
  InvalidModelError,
} from "@everstack/node";

try {
  await client.chat.completions.create({ ... });
} catch (error) {
  if (error instanceof AuthenticationError) {
    console.error("Invalid API key");
  } else if (error instanceof RateLimitError) {
    console.error(`Rate limited. Retry after ${error.retryAfter}s`);
  } else if (error instanceof InvalidModelError) {
    console.error(`Model not available: ${error.model}`);
  } else if (error instanceof APIError) {
    console.error(`API error ${error.status}: ${error.message}`);
  }
}
```

## Configuration

### Client Options

```typescript
const client = new Everstack({
  // Required
  apiKey: "pk_...",

  // Optional
  baseURL: "https://gateway.everstack.ai", // Custom gateway URL
  provider: "@openai", // Default provider
  orgId: "org_...", // Organization ID
  userId: "user_...", // User ID for tracking
  headers: { "X-Custom": "value" }, // Additional headers
  timeout: 60000, // Request timeout (ms)
  maxRetries: 2, // Max retry attempts
});
```

## Gateway Configuration

For configuring the Everstack gateway itself:

```typescript
import { allModels, type GatewayConfig } from "@everstack/node/config";

const config: GatewayConfig = {
  models: [
    "@openai/gpt-4o",
    "@anthropic/claude-sonnet-4-20250514",
    {
      id: "@openai/gpt-4o-mini",
      alias: "fast",
      defaults: { temperature: 0.5 },
    },
  ],
  defaults: {
    temperature: 0.7,
    maxTokens: 1000,
  },
  fallback: {
    enabled: true,
    models: ["@openai/gpt-4o-mini"],
  },
};
```

## License

MIT
