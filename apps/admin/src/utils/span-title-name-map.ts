// Map technical span names to human-readable titles

import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { capitalize } from '@everstack/utils/functions/capitalize'
import { getProviderName } from './extract-provider-name'
import {
  getAttr,
  categoryFromObservationType,
  categoryFromSpanName,
  importanceForCategory,
  type SpanDisplayConfig,
} from './traces-common'

// Docker API patterns for function execution
const DOCKER_PATTERNS: Array<{ pattern: RegExp; name: string }> = [
  {
    pattern: /^(POST|GET)\s+\/v[\d.]+\/containers\/create/,
    name: 'Create Container',
  },
  {
    pattern: /^(POST|GET)\s+\/v[\d.]+\/containers\/[a-f0-9]+\/start/,
    name: 'Start Container',
  },
  {
    pattern: /^(POST|GET)\s+\/v[\d.]+\/containers\/[a-f0-9]+\/wait/,
    name: 'Wait for Completion',
  },
  {
    pattern: /^GET\s+\/v[\d.]+\/containers\/[a-f0-9]+\/logs/,
    name: 'Get Logs',
  },
  {
    pattern: /^DELETE\s+\/v[\d.]+\/containers\/[a-f0-9]+/,
    name: 'Remove Container',
  },
  {
    pattern: /^GET\s+\/v[\d.]+\/images\/(.+)\/json/,
    name: 'Check Runtime Image',
  },
  { pattern: /^POST\s+\/v[\d.]+\/images\/create/, name: 'Pull Image' },
  {
    pattern: /^GET\s+\/v[\d.]+\/containers\/[a-f0-9]+\/json/,
    name: 'Inspect Container',
  },
]

const mappings: Record<string, string> = {
  // Gateway operations
  'gateway.chat.completion': 'Chat Completion',
  'gateway.request.normalize': 'Request Normalization',
  'gateway.model.resolution': 'Model Resolution',
  'gateway.response.process': 'Response Processing',
  'gateway.cache.lookup': 'Cache Lookup',
  'gateway.cache.store': 'Cache Store',
  'gateway.ratelimit.check': 'Rate Limit Check',
  'gateway.validation.request': 'Request Validation',
  'gateway.validation.response': 'Response Validation',

  // Cache operations
  'cache.lookup': 'Cache Lookup',
  'cache.store': 'Cache Store',
  'cache.async.store': 'Async Cache Store',
  'cache.invalidate': 'Cache Invalidate',

  // Provider API calls
  'provider.openai.chat': 'OpenAI API Call',
  'provider.anthropic.chat': 'Anthropic API Call',
  'provider.google.chat': 'Google API Call',
  'provider.cohere.chat': 'Cohere API Call',
  'provider.mistral.chat': 'Mistral API Call',
  'provider.groq.chat': 'Groq API Call',
  'provider.azure.chat': 'Azure OpenAI API Call',
  'provider.azure-openai.chat': 'Azure OpenAI API Call',
  'provider.bedrock.chat': 'AWS Bedrock API Call',
  'provider.aws-bedrock.chat': 'AWS Bedrock API Call',
  'provider.vertex-ai.chat': 'Vertex AI API Call',
  'provider.together.chat': 'Together API Call',
  'provider.deepseek.chat': 'DeepSeek API Call',
  'provider.fireworks.chat': 'Fireworks API Call',
  'provider.xai.chat': 'xAI API Call',
  'provider.perplexity.chat': 'Perplexity API Call',
  'provider.cerebras.chat': 'Cerebras API Call',
  'provider.nvidia-nim.chat': 'NVIDIA NIM API Call',

  // Function execution
  'function.execution': 'Function Execution',
  'function.execution.isolated': 'Isolated Function Execution',
  'function.invoke': 'Function Invoke',
  'function.result': 'Function Result',

  // Tool loop
  'tool_loop.iteration': 'Tool Loop Iteration',
  'tool_loop.start': 'Tool Loop Start',
  'tool_loop.end': 'Tool Loop End',
}

// Acronyms that should render uppercase rather than title-cased, so a span name
// like "claude_code.llm_request" reads "Claude Code LLM Request".
const ACRONYMS = new Set([
  'llm', 'api', 'mcp', 'a2a', 'id', 'url', 'http', 'https', 'ai', 'sql', 'json',
  'ttft', 'rag', 'sdk', 'cli', 'io', 'fs', 'vm', 'gpu', 'cpu',
])

// humanize turns a technical span name into a readable title: it splits on dots,
// underscores, spaces and dashes, title-cases each word, and uppercases known
// acronyms. This is the generic fallback for span names we have no explicit
// mapping for (e.g. external SDK telemetry like Claude Code).
export function humanizeSpanName(spanName: string): string {
  return spanName
    .split(/[._\s-]+/)
    .filter(Boolean)
    .map((w) => (ACRONYMS.has(w.toLowerCase()) ? w.toUpperCase() : capitalize(w)))
    .join(' ')
}

export function getSpanDisplayName(spanName: string): string {
  // Check exact match first
  if (mappings[spanName]) return mappings[spanName]

  // External coding-agent telemetry (Claude Code, etc.): keep the agent prefix
  // but humanize the operation.
  if (spanName.toLowerCase().startsWith('claude_code')) {
    const rest = spanName.slice('claude_code'.length).replace(/^[._\s-]+/, '')
    return rest ? `Claude Code: ${humanizeSpanName(rest)}` : 'Claude Code'
  }

  // Check Docker API patterns
  for (const { pattern, name } of DOCKER_PATTERNS) {
    if (pattern.test(spanName)) {
      return name
    }
  }

  // Pattern matching for provider calls
  if (spanName.startsWith('provider.')) {
    const provider = spanName.split('.')[1]
    return `${capitalize(provider)} API Call`
  }

  // Pattern matching for gateway operations
  if (spanName.startsWith('gateway.')) {
    const operation = spanName.split('.').slice(1).join(' ')
    return operation.split('.').map(capitalize).join(' ')
  }

  // Pattern matching for cache operations
  if (spanName.startsWith('cache.')) {
    const operation = spanName.split('.').slice(1).join(' ')
    return `Cache ${operation.split('.').map(capitalize).join(' ')}`
  }

  // Pattern matching for function operations
  if (spanName.startsWith('function.')) {
    const operation = spanName.split('.').slice(1).join(' ')
    return `Function ${operation.split('.').map(capitalize).join(' ')}`
  }

  // Pattern matching for tool loop operations
  if (spanName.startsWith('tool_loop.')) {
    const operation = spanName.split('.').slice(1).join(' ')
    return `Tool Loop ${operation.split('_').map(capitalize).join(' ')}`
  }

  // Semantic taxonomy span names (M1 emitter) — give the new namespaces readable
  // titles instead of falling through to the generic dotted-name formatter.
  if (spanName.startsWith('sandbox.fs.')) {
    return `Sandbox FS: ${capitalize(spanName.slice('sandbox.fs.'.length))}`
  }
  if (spanName.startsWith('sandbox.exec.')) {
    return `Sandbox Exec: ${spanName.slice('sandbox.exec.'.length)}`
  }
  if (spanName.startsWith('browser.')) {
    return `Browser: ${capitalize(spanName.slice('browser.'.length))}`
  }
  if (spanName.startsWith('memory.')) {
    return `Memory ${capitalize(spanName.slice('memory.'.length))}`
  }
  if (spanName === 'workflow.run') return 'Workflow Run'
  if (spanName.startsWith('workflow.node.')) {
    return `Workflow Node: ${capitalize(spanName.slice('workflow.node.'.length))}`
  }
  if (spanName === 'mcp.tool.call') return 'MCP Tool Call'
  if (spanName === 'a2a.call') return 'A2A Call'
  if (spanName === 'scorer.pipeline') return 'Scorer Pipeline'
  if (spanName.startsWith('scorer.')) {
    return `Scorer: ${spanName.slice('scorer.'.length).split('_').map(capitalize).join(' ')}`
  }
  if (spanName.startsWith('vector.')) {
    return `Vector ${capitalize(spanName.slice('vector.'.length))}`
  }
  if (spanName === 'embedding.embed') return 'Embedding'
  if (spanName === 'harness.run') return 'Harness Run'
  if (spanName.startsWith('integration.')) {
    return `Integration: ${capitalize(spanName.slice('integration.'.length))}`
  }

  // Fallback: render the raw span name VERBATIM. Everything above this line is an
  // Everstack-origin name (our gateway / agent / semantic-taxonomy namespaces),
  // which we deliberately prettify. Anything reaching here is an unrecognized name
  // — overwhelmingly a user-authored SDK span (`startSpan("Fetch user profile")`,
  // `handleWebhook`, …). The name the user chose is authoritative, so we show it
  // as-is instead of re-casing it. (v2 will gate the generic-prefix branches above
  // on instrumentation scope so user spans like `cache.get` also render verbatim.)
  return spanName
}

// Check if a span name matches Docker container patterns
export function isDockerSpan(spanName: string): boolean {
  return DOCKER_PATTERNS.some(({ pattern }) => pattern.test(spanName))
}

export function getSpanDisplayConfig(span: Span): SpanDisplayConfig {
  const obsType = getAttr(span, 'observation.type')
  const provider = getProviderName(span)
  const spanName = span.spanName
  const stepNumber = getAttr(span, 'step.number') || getAttr(span, 'span.step')

  // Provider API calls (GENERATION/LLM type or provider.* spans)
  if (obsType === 'GENERATION' || obsType === 'LLM' || spanName.startsWith('provider.')) {
    const model =
      getAttr(span, 'llm.response.model') || getAttr(span, 'llm.request.model')
    let title = getSpanDisplayName(spanName)
    if (stepNumber) {
      title = `${title} (Step ${stepNumber})`
    }
    return {
      title,
      subtitle: model,
      category: 'provider',
      importance: 'primary',
      provider: provider,
      stepNumber: stepNumber ? Number(stepNumber) : undefined,
    }
  }

  // Gateway root span (chat completion)
  if (spanName === 'gateway.chat.completion') {
    return {
      title: 'Chat Completion',
      subtitle: provider ? `via ${capitalize(provider)}` : undefined,
      category: 'gateway',
      importance: 'primary',
      provider: provider,
    }
  }

  // Semantic span types (agent / tool / sandbox / browser / memory / workflow /
  // integration / harness / etc) set their observation.type explicitly at
  // emission; map it straight to a category. Legacy types (SPAN/EVENT) return
  // undefined here so the gateway-centric name matching below still runs.
  const semanticCategory = categoryFromObservationType(obsType)
  if (semanticCategory) {
    return {
      title: getSpanDisplayName(spanName),
      category: semanticCategory,
      importance: importanceForCategory(semanticCategory),
    }
  }

  // Cache operations
  if (spanName.startsWith('cache.') || spanName.includes('.cache.')) {
    return {
      title: getSpanDisplayName(spanName),
      category: 'cache',
      importance: 'secondary',
    }
  }

  // Function execution (including Docker container operations)
  if (spanName.startsWith('function.') || isDockerSpan(spanName)) {
    const functionName = getAttr(span, 'function.name')
    const runtime = getAttr(span, 'function.runtime')
    return {
      title: getSpanDisplayName(spanName),
      subtitle: functionName || runtime,
      category: 'function',
      importance: 'secondary',
    }
  }

  // Tool loop operations
  if (spanName.startsWith('tool_loop.')) {
    const iteration = getAttr(span, 'tool_loop.iteration')
    return {
      title: getSpanDisplayName(spanName),
      subtitle: iteration ? `Iteration ${iteration}` : undefined,
      category: 'tool_loop',
      importance: 'secondary',
    }
  }

  // Other gateway operations
  if (spanName.startsWith('gateway.')) {
    return {
      title: getSpanDisplayName(spanName),
      category: 'internal',
      importance: 'detail',
    }
  }

  // Unknown span name: infer a semantic category from the name itself. Handles
  // external OTLP telemetry (Claude Code / Gemini CLI / Codex), the GenAI
  // semconv, and `namespace:operation` app spans (db: / memory: / llm: / ...) so
  // they get their own icon + colour instead of collapsing to a generic span.
  const inferred = categoryFromSpanName(spanName)
  if (inferred) {
    return {
      title: getSpanDisplayName(spanName),
      category: inferred,
      importance: importanceForCategory(inferred),
    }
  }

  // Fallback for genuinely unknown spans
  return {
    title: getSpanDisplayName(spanName),
    category: 'internal',
    importance: 'detail',
  }
}
