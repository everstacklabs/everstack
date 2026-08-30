import type { Span } from "@everstack/proto/everstack/traces/v1/traces_pb"


export type SpanCategory =
    // Gateway-centric (legacy) categories
    | 'gateway'      // Gateway root operations
    | 'provider'     // LLM provider API calls
    | 'internal'     // Internal processing spans
    | 'cache'        // Cache lookup/store operations
    | 'function'     // Isolated function execution
    | 'tool_loop'    // Tool loop iterations
    // Semantic taxonomy (traces-module-replan section 5) — mirrors the Go
    // telemetry.ObservationType set so a span renders the same whether it ran
    // inside an agent, a workflow, a harness, or an integration call.
    | 'agent'        // Agent / sub-agent / turn
    | 'tool'         // Tool call
    | 'retriever'    // Memory / RAG / vector query
    | 'embedding'    // Embedding generation
    | 'sandbox'      // Code execution (exec / fs / git)
    | 'browser'      // Browser automation
    | 'computer'     // Computer-use action
    | 'guardrail'    // Safety / policy check
    | 'scorer'       // Eval / facet / judge execution
    | 'workflow'     // Workflow / Studio node, chain, composite step
    | 'control'      // Control flow (ifElse / router / loadBalancer)
    | 'http'         // httpRequest / webhook
    | 'integration'  // Connector call (GitHub, GitLab, ...)
    | 'harness'      // ADK / harness run
    | 'media'        // tts / stt / voiceClone

export type SpanImportance = 'primary' | 'secondary' | 'detail'

/**
 * Map a backend observation.type (telemetry.ObservationType) to a UI SpanCategory.
 * Returns undefined for the legacy/compat types (SPAN, EVENT, GENERATION, LLM) so
 * the span-name based classification in getSpanDisplayConfig still runs for them.
 */
export function categoryFromObservationType(obsType?: string): SpanCategory | undefined {
    switch ((obsType ?? '').toUpperCase()) {
        case 'AGENT': return 'agent'
        case 'TOOL': return 'tool'
        case 'RETRIEVER': return 'retriever'
        case 'EMBEDDING': return 'embedding'
        case 'CACHE': return 'cache'
        case 'SANDBOX': return 'sandbox'
        case 'BROWSER': return 'browser'
        case 'COMPUTER': return 'computer'
        case 'GUARDRAIL': return 'guardrail'
        case 'SCORER': return 'scorer'
        case 'WORKFLOW':
        case 'CHAIN': return 'workflow'
        case 'CONTROL': return 'control'
        case 'HTTP': return 'http'
        case 'INTEGRATION': return 'integration'
        case 'HARNESS': return 'harness'
        case 'MEDIA': return 'media'
        default: return undefined
    }
}

/**
 * Coding-agent OTLP telemetry prefixes (Claude Code, Gemini CLI, Codex, ...).
 * These SDKs don't set observation.type and don't use our gateway-centric span
 * names, so without this their spans all collapse onto the generic `internal`
 * waveform. We route the operation to its semantic category and default the
 * agent loop's own spans (interaction / session / turn) to `agent`.
 */
const CODING_AGENT_PREFIXES = [
    'claude_code', 'claude-code', 'claudecode',
    'gemini_cli', 'gemini-cli', 'geminicli',
    'codex', 'opencode', 'aider', 'cline', 'goose', 'crush', 'amp',
    'cursor', 'copilot', 'github_copilot', 'github-copilot',
]

/**
 * Leading-namespace-token -> category, for `namespace:operation` (and
 * `namespace.operation`) style app spans, e.g. `db:select-user`,
 * `memory:add-message`, `llm:primary-turn`, `process-message`. The token is
 * everything before the first separator, so the convention reads at a glance.
 */
const SPAN_NAMESPACE_CATEGORY: Record<string, SpanCategory> = {
    // model calls
    llm: 'provider', generation: 'provider', completion: 'provider',
    chat: 'provider', inference: 'provider', model: 'provider', gen_ai: 'provider',
    embed: 'embedding', embedding: 'embedding', embeddings: 'embedding',
    // orchestration roots
    agent: 'agent', interaction: 'agent', turn: 'agent', session: 'agent',
    subagent: 'agent', react: 'agent', reasoning: 'agent', plan: 'agent', think: 'agent',
    workflow: 'workflow', chain: 'workflow', graph: 'workflow', pipeline: 'workflow',
    step: 'workflow', node: 'workflow', flow: 'workflow', dag: 'workflow',
    task: 'workflow', job: 'workflow', stage: 'workflow', process: 'workflow', e2e: 'workflow',
    // tools / connectors
    tool: 'tool', tools: 'tool', mcp: 'tool', skill: 'tool', func: 'function', fn: 'function',
    integration: 'integration', connector: 'integration', github: 'integration',
    gitlab: 'integration', slack: 'integration', stripe: 'integration', jira: 'integration',
    linear: 'integration', notion: 'integration',
    // data / retrieval
    retriever: 'retriever', retrieval: 'retriever', rag: 'retriever', search: 'retriever',
    vector: 'retriever', vectordb: 'retriever', knn: 'retriever', memory: 'retriever',
    recall: 'retriever', history: 'retriever', context: 'retriever', knowledge: 'retriever', index: 'retriever',
    db: 'cache', sql: 'cache', postgres: 'cache', postgresql: 'cache', pg: 'cache',
    mysql: 'cache', sqlite: 'cache', mongo: 'cache', mongodb: 'cache', redis: 'cache',
    database: 'cache', prisma: 'cache', drizzle: 'cache', query: 'cache', cache: 'cache',
    kv: 'cache', dynamo: 'cache',
    // io / execution
    http: 'http', https: 'http', fetch: 'http', request: 'http', req: 'http', api: 'http',
    rest: 'http', grpc: 'http', rpc: 'http', webhook: 'http', sms: 'http', email: 'http',
    mail: 'http', notify: 'http', notification: 'http', net: 'http',
    sandbox: 'sandbox', exec: 'sandbox', shell: 'sandbox', command: 'sandbox', cmd: 'sandbox',
    bash: 'sandbox', sh: 'sandbox', fs: 'sandbox', file: 'sandbox', git: 'sandbox',
    run: 'sandbox', build: 'sandbox', compile: 'sandbox', container: 'sandbox', docker: 'sandbox',
    browser: 'browser', page: 'browser', dom: 'browser', playwright: 'browser',
    puppeteer: 'browser', selenium: 'browser', navigate: 'browser', crawl: 'browser', scrape: 'browser',
    computer: 'computer', gui: 'computer', screen: 'computer', screenshot: 'computer',
    click: 'computer', keyboard: 'computer', mouse: 'computer',
    // safety / eval
    guardrail: 'guardrail', moderation: 'guardrail', moderate: 'guardrail', safety: 'guardrail',
    policy: 'guardrail', pii: 'guardrail', redact: 'guardrail', validate: 'guardrail',
    validation: 'guardrail', guard: 'guardrail',
    scorer: 'scorer', score: 'scorer', eval: 'scorer', evaluation: 'scorer', evaluate: 'scorer',
    judge: 'scorer', assert: 'scorer', assertion: 'scorer', facet: 'scorer', grade: 'scorer',
    // media
    tts: 'media', stt: 'media', voice: 'media', audio: 'media', transcribe: 'media',
    transcription: 'media', speech: 'media', image: 'media', vision: 'media', ocr: 'media', video: 'media',
    // harness / control-flow plumbing
    harness: 'harness', adk: 'harness', autogen: 'harness', crewai: 'harness', langgraph: 'harness',
    control: 'control', router: 'control', route: 'control', branch: 'control',
    ifelse: 'control', loadbalancer: 'control', condition: 'control', switch: 'control', effect: 'control',
}

/**
 * Infer a SpanCategory from a raw span name. This is the last-resort classifier
 * for spans that carry no observation.type and don't match the gateway-centric
 * naming: external OTLP telemetry (Claude Code / Gemini CLI / Codex), the
 * OpenTelemetry GenAI semconv, and `namespace:operation` app spans. Returns
 * undefined when nothing matches so the caller can fall back to `internal`.
 */
export function categoryFromSpanName(spanName?: string): SpanCategory | undefined {
    const name = (spanName ?? '').toLowerCase().trim()
    if (!name) return undefined

    if (CODING_AGENT_PREFIXES.some((p) => name.startsWith(p))) {
        if (/(^|[._\s-])(llm|api|request|completion|generation|inference|message|response)([._\s-]|$)/.test(name)) return 'provider'
        if (/(^|[._\s-])(tool|mcp|function)([._\s-]|$)/.test(name)) return 'tool'
        if (/embed/.test(name)) return 'embedding'
        return 'agent'
    }

    // Precise leading-token lookup for the `namespace:operation` convention.
    const head = name.split(/[:.\s/_-]+/)[0] ?? ''
    if (head && SPAN_NAMESPACE_CATEGORY[head]) return SPAN_NAMESPACE_CATEGORY[head]

    // Backstop keyword sweep for dotted/spaced OTel span names.
    if (/\b(llm|completion|generation|inference|gen[_-]?ai)\b/.test(name)) return 'provider'
    if (/embed/.test(name)) return 'embedding'
    if (/\b(tool|mcp)\b/.test(name)) return 'tool'
    if (/\bagent\b/.test(name)) return 'agent'
    if (/\b(workflow|pipeline|chain|graph)\b/.test(name)) return 'workflow'
    if (/\b(retriev|vector|rag|memory|search)\b/.test(name)) return 'retriever'
    if (/\b(sql|postgres|mysql|mongo|redis|database|db|query)\b/.test(name)) return 'cache'
    if (/\b(http|fetch|request|webhook|api|grpc)\b/.test(name)) return 'http'
    if (/\b(sandbox|exec|shell|bash|git|fs)\b/.test(name)) return 'sandbox'
    if (/\b(browser|playwright|puppeteer|crawl|scrape)\b/.test(name)) return 'browser'
    if (/\b(guardrail|moderation|safety|policy|pii)\b/.test(name)) return 'guardrail'
    if (/\b(scorer|score|eval|judge|assert)\b/.test(name)) return 'scorer'
    if (/\b(tts|stt|voice|audio|speech|vision)\b/.test(name)) return 'media'

    return undefined
}

const PRIMARY_CATEGORIES: ReadonlySet<SpanCategory> = new Set<SpanCategory>(['agent', 'workflow'])

/** Default importance for a semantic category (roots are primary, the rest secondary). */
export function importanceForCategory(category: SpanCategory): SpanImportance {
    return PRIMARY_CATEGORIES.has(category) ? 'primary' : 'secondary'
}

export interface SpanDisplayConfig {
    title: string
    subtitle?: string
    category: SpanCategory
    importance: SpanImportance
    provider?: string
    stepNumber?: number  // For ordering provider calls in tool loops
}


export function getAttr(span: Span, key: string, fallback?: any) {
    return span.spanAttributes?.[key] ?? fallback
}

export function getMetric(span: Span, key: string): number {
    const val = span.spanAttributes?.[key]
    return val ? Number(val) : 0
}

// Span-level input/output keys across semantic conventions, mirroring the
// backend spanInputAttrs/spanOutputAttrs in semconv.go. A generation span reads
// its own messages first (llm.request.messages / gen_ai / OpenInference).
const SPAN_LOCAL_INPUT_KEYS = [
    'llm.request.messages',
    'gen_ai.input.messages',
    'gen_ai.prompt',
    'input.value',
    'input',
] as const

const SPAN_TRACE_INPUT_KEYS = ['trace.input'] as const

const SPAN_LOCAL_OUTPUT_KEYS = [
    'llm.response.choices',
    'gen_ai.output.messages',
    'gen_ai.completion',
    'output.value',
    'embedding.output',
    'output',
] as const

const SPAN_TRACE_OUTPUT_KEYS = ['trace.output'] as const

export type SpanIOPayloadScope = 'span' | 'trace'

export interface SpanIOPayload {
    value: string
    key: string
    scope: SpanIOPayloadScope
}

function normalizeIOValue(value: unknown): string {
    if (value === null || value === undefined) return ''
    if (typeof value === 'string') return value
    if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') {
        return String(value)
    }
    try {
        return JSON.stringify(value)
    } catch {
        return String(value)
    }
}

function getFirstPayload(
    span: Span,
    keys: readonly string[],
    scope: SpanIOPayloadScope,
): SpanIOPayload | undefined {
    for (const key of keys) {
        const value = normalizeIOValue(span.spanAttributes?.[key])
        if (value) return { value, key, scope }
    }
    return undefined
}

export function getSpanInputPayload(
    span: Span,
    options: { includeTraceFallback?: boolean } = {},
): SpanIOPayload | undefined {
    return (
        getFirstPayload(span, SPAN_LOCAL_INPUT_KEYS, 'span') ||
        (options.includeTraceFallback
            ? getFirstPayload(span, SPAN_TRACE_INPUT_KEYS, 'trace')
            : undefined)
    )
}

export function getSpanOutputPayload(
    span: Span,
    options: { includeTraceFallback?: boolean } = {},
): SpanIOPayload | undefined {
    return (
        getFirstPayload(span, SPAN_LOCAL_OUTPUT_KEYS, 'span') ||
        (options.includeTraceFallback
            ? getFirstPayload(span, SPAN_TRACE_OUTPUT_KEYS, 'trace')
            : undefined)
    )
}

/** First non-empty input payload for a span, across all supported semconvs. */
export function getSpanInput(span: Span): string {
    return getSpanInputPayload(span, { includeTraceFallback: true })?.value || ''
}

/** First non-empty output payload for a span, across all supported semconvs. */
export function getSpanOutput(span: Span): string {
    return getSpanOutputPayload(span, { includeTraceFallback: true })?.value || ''
}
