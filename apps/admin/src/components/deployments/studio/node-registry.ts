import type { NodeTypeMeta, StudioNodeType } from './types'

// Default config factory functions for each node type
export const defaultStartConfig = () => ({
  systemPrompt: '',
})

export const defaultAuthConfig = () => ({
  mode: 'api_key' as const,
  headerName: 'Authorization',
})

export const defaultRateLimiterConfig = () => ({})

export const defaultCacheConfig = () => ({
  type: 'semantic',
  ttl: 3600,
  maxEntries: 1000,
  similarityThreshold: 0.95,
})

export const defaultRouterConfig = () => ({
  mappings: [] as Array<{ model: string; provider: string }>,
})

export const defaultLoadBalancerConfig = () => ({
  strategy: 'router',
  weights: [] as Array<{ provider: string; weight: number }>,
  fallback: '',
})

export const defaultInputGuardrailsConfig = () => ({
  piiDetection: false,
  promptInjection: false,
  contentFilter: false,
})

export const defaultOutputGuardrailsConfig = () => ({
  jailbreakDetection: false,
  hallucinationDetection: false,
  toxicityDetection: false,
})

export const defaultProviderConfig = () => ({
  providerType: 'openai',
  model: 'gpt-4o',
  baseUrl: '',
  maxTokens: 4096,
  functions: [] as string[],
})

export const defaultAgentConfig = () => ({
  agentId: '',
  agentName: '',
  useInline: false,
  inlineAgent: {
    name: '',
    model: '',
    systemPrompt: '',
    tools: [] as string[],
    temperature: 0.7,
    maxTokens: 4096,
    browser: {
      enabled: false,
      headless: true,
    },
  },
  maxIterations: 25,
  maxToolCallsPerTurn: 10,
  turnTimeout: '5m',
  contextMode: 'inherit' as const,
  memoryEnabled: false,
  memoryCollection: 'default',
  memoryTopK: 5,
  memoryMinScore: 0,
  memoryStoreResponses: false,
})

export const defaultFunctionConfig = () => ({
  functionId: '',
  functionName: '',
  functionMode: '',
  parameterMappings: {} as Record<string, string>,
  timeoutMs: 30000,
})

export const defaultResponseConfig = () => ({
  format: 'openai',
  streaming: true,
  includeUsage: true,
  includeTimings: false,
})

export const defaultHttpRequestConfig = () => ({
  method: 'GET',
  url: '',
  headers: {} as Record<string, string>,
  body: '',
  timeoutMs: 30000,
  responseVariable: 'response',
})

export const defaultWebhookConfig = () => ({
  url: '',
  method: 'POST',
  headers: {} as Record<string, string>,
  bodyTemplate: '',
  retries: 3,
  timeoutMs: 10000,
})

export const defaultIfElseConfig = () => ({
  conditionType: 'expression' as const,
  conditionExpression: '',
})

export const defaultMemoryConfig = () => ({
  operation: 'query' as const,
  collection: 'default',
  contentSource: 'input' as const,
  chunkSize: 512,
  topK: 5,
  minScore: 0,
  outputVariable: 'memory_results',
})

// Node type metadata registry - all 14 types
export const defaultTTSConfig = () => ({
  provider: 'qwen',
  model: 'qwen3-tts-flash',
  voice: 'Cherry',
  voiceCloneProfileId: '',
  language: 'en',
  speed: 1.0,
  responseFormat: 'mp3',
  inputText: '',
  instructions: '',
  temperature: 0,
  topP: 0,
  stability: 0,
  similarity: 0,
  style: 0,
  enhancement: false,
  speakerBoost: 0,
})

export const defaultSTTConfig = () => ({
  provider: 'openai',
  model: 'whisper-1',
  language: '',
  responseFormat: 'json',
  timestampGranularities: [] as string[],
})

export const defaultVoiceCloneConfig = () => ({
  provider: 'qwen',
  model: 'qwen3-tts-vc-2026-01-22',
  voiceCloneProfileId: '',
  preferredName: '',
  inputText: '',
  speed: 1.0,
  instructions: '',
  temperature: 0,
  topP: 0,
  stability: 0,
  similarity: 0,
  style: 0,
  enhancement: false,
  speakerBoost: 0,
})

export const NODE_REGISTRY: Record<StudioNodeType, NodeTypeMeta> = {
  start: {
    type: 'start',
    label: 'Start',
    category: 'input',
    color: '#059669', // emerald-600
    icon: 'lucide:play',
    handles: [{ type: 'source', id: 'out', position: 'bottom', label: 'Out' }],
    defaultConfig: defaultStartConfig(),
    maxInstances: 1,
  },
  auth: {
    type: 'auth',
    label: 'Auth',
    category: 'middleware',
    color: '#2563eb', // blue-600
    icon: 'lucide:shield',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultAuthConfig(),
  },
  rateLimiter: {
    type: 'rateLimiter',
    label: 'Rate Limiter',
    category: 'middleware',
    color: '#d97706', // amber-600
    icon: 'lucide:gauge',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultRateLimiterConfig(),
  },
  cache: {
    type: 'cache',
    label: 'Cache',
    category: 'middleware',
    color: '#0891b2', // cyan-600
    icon: 'lucide:database',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'hit', position: 'bottom', label: 'Hit' },
      { type: 'source', id: 'miss', position: 'bottom', label: 'Miss' },
    ],
    defaultConfig: defaultCacheConfig(),
  },
  router: {
    type: 'router',
    label: 'Router',
    category: 'processing',
    color: '#9333ea', // purple-600
    icon: 'lucide:git-branch',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultRouterConfig(),
  },
  loadBalancer: {
    type: 'loadBalancer',
    label: 'Load Balancer',
    category: 'processing',
    color: '#4f46e5', // indigo-600
    icon: 'lucide:scale',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultLoadBalancerConfig(),
  },
  inputGuardrails: {
    type: 'inputGuardrails',
    label: 'Input Guardrails',
    category: 'middleware',
    color: '#e11d48', // rose-600
    icon: 'lucide:shield-alert',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'pass', position: 'bottom', label: 'Pass' },
      { type: 'source', id: 'block', position: 'bottom', label: 'Block' },
    ],
    defaultConfig: defaultInputGuardrailsConfig(),
  },
  outputGuardrails: {
    type: 'outputGuardrails',
    label: 'Output Guardrails',
    category: 'middleware',
    color: '#ea580c', // orange-600
    icon: 'lucide:shield-check',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'pass', position: 'bottom', label: 'Pass' },
      { type: 'source', id: 'block', position: 'bottom', label: 'Block' },
    ],
    defaultConfig: defaultOutputGuardrailsConfig(),
  },
  provider: {
    type: 'provider',
    label: 'Provider',
    category: 'ai',
    color: '#7c3aed', // violet-600
    icon: 'lucide:cloud',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultProviderConfig(),
  },
  agent: {
    type: 'agent',
    label: 'Agent',
    category: 'ai',
    color: '#f59e0b', // amber-500
    icon: 'lucide:bot',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultAgentConfig(),
  },
  function: {
    type: 'function',
    label: 'Function',
    category: 'processing',
    color: '#0284c7', // sky-600
    icon: 'lucide:code',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultFunctionConfig(),
  },
  httpRequest: {
    type: 'httpRequest',
    label: 'HTTP Request',
    category: 'processing',
    color: '#f59e0b', // amber-500
    icon: 'lucide:globe',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultHttpRequestConfig(),
  },
  webhook: {
    type: 'webhook',
    label: 'Webhook',
    category: 'processing',
    color: '#8b5cf6', // violet-500
    icon: 'lucide:webhook',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultWebhookConfig(),
  },
  memory: {
    type: 'memory',
    label: 'Memory',
    category: 'ai',
    color: '#6366f1', // indigo-500
    icon: 'lucide:brain',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultMemoryConfig(),
  },
  ifElse: {
    type: 'ifElse',
    label: 'If/Else',
    category: 'logic',
    color: '#ec4899', // pink-500
    icon: 'lucide:git-fork',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'true', position: 'bottom', label: 'True' },
      { type: 'source', id: 'false', position: 'bottom', label: 'False' },
    ],
    defaultConfig: defaultIfElseConfig(),
  },
  response: {
    type: 'response',
    label: 'Response',
    category: 'output',
    color: '#0d9488', // teal-600
    icon: 'lucide:send',
    handles: [{ type: 'target', id: 'in', position: 'top' }],
    defaultConfig: defaultResponseConfig(),
    maxInstances: 1,
  },
  tts: {
    type: 'tts',
    label: 'Text to Speech',
    category: 'ai',
    color: '#14b8a6', // teal-500
    icon: 'lucide:volume-2',
    requiredTier: 'Pro',
    featureKey: 'voice',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultTTSConfig(),
  },
  stt: {
    type: 'stt',
    label: 'Speech to Text',
    category: 'ai',
    color: '#f97316', // orange-500
    icon: 'lucide:mic',
    requiredTier: 'Pro',
    featureKey: 'voice',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultSTTConfig(),
  },
  voiceClone: {
    type: 'voiceClone',
    label: 'Voice Clone',
    category: 'ai',
    color: '#a855f7', // purple-500
    icon: 'lucide:copy',
    requiredTier: 'Pro',
    featureKey: 'voice',
    handles: [
      { type: 'target', id: 'in', position: 'top' },
      { type: 'source', id: 'out', position: 'bottom', label: 'Out' },
    ],
    defaultConfig: defaultVoiceCloneConfig(),
  },
}

// Get registry entries grouped by category
export const NODE_CATEGORIES: Array<{
  category: string
  label: string
  nodes: NodeTypeMeta[]
}> = [
  {
    category: 'input',
    label: 'Input',
    nodes: Object.values(NODE_REGISTRY).filter((n) => n.category === 'input'),
  },
  {
    category: 'middleware',
    label: 'Middleware',
    nodes: Object.values(NODE_REGISTRY).filter(
      (n) => n.category === 'middleware',
    ),
  },
  {
    category: 'ai',
    label: 'AI',
    nodes: Object.values(NODE_REGISTRY).filter((n) => n.category === 'ai'),
  },
  {
    category: 'processing',
    label: 'Processing',
    nodes: Object.values(NODE_REGISTRY).filter(
      (n) => n.category === 'processing',
    ),
  },
  {
    category: 'logic',
    label: 'Logic',
    nodes: Object.values(NODE_REGISTRY).filter((n) => n.category === 'logic'),
  },
  {
    category: 'output',
    label: 'Output',
    nodes: Object.values(NODE_REGISTRY).filter((n) => n.category === 'output'),
  },
]

// Get default config for a node type
export function getDefaultConfig(type: StudioNodeType) {
  const fns: Record<StudioNodeType, () => unknown> = {
    start: defaultStartConfig,
    auth: defaultAuthConfig,
    rateLimiter: defaultRateLimiterConfig,
    cache: defaultCacheConfig,
    router: defaultRouterConfig,
    loadBalancer: defaultLoadBalancerConfig,
    inputGuardrails: defaultInputGuardrailsConfig,
    outputGuardrails: defaultOutputGuardrailsConfig,
    provider: defaultProviderConfig,
    agent: defaultAgentConfig,
    function: defaultFunctionConfig,
    httpRequest: defaultHttpRequestConfig,
    webhook: defaultWebhookConfig,
    ifElse: defaultIfElseConfig,
    memory: defaultMemoryConfig,
    response: defaultResponseConfig,
    tts: defaultTTSConfig,
    stt: defaultSTTConfig,
    voiceClone: defaultVoiceCloneConfig,
  }
  const fn = fns[type]
  if (!fn) return {}
  return fn()
}
