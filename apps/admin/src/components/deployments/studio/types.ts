import type { Node, Edge } from '@xyflow/react'

// All 14 node types
export type StudioNodeType =
  | 'start'
  | 'auth'
  | 'rateLimiter'
  | 'cache'
  | 'router'
  | 'loadBalancer'
  | 'inputGuardrails'
  | 'outputGuardrails'
  | 'provider'
  | 'agent'
  | 'function'
  | 'httpRequest'
  | 'webhook'
  | 'ifElse'
  | 'memory'
  | 'response'
  | 'tts'
  | 'stt'
  | 'voiceClone'

// Per-node-type config interfaces
export interface StartConfig {
  systemPrompt: string
}

export interface AuthConfig {
  mode: 'api_key' | 'jwt' | 'webhook' | 'none'
  headerName: string
  // JWT fields
  issuer?: string
  audience?: string
  jwksUrl?: string
  // Webhook fields
  webhookUrl?: string
  webhookMethod?: string
  webhookHeaders?: Record<string, string>
}

export interface RateLimiterConfig {
  // Rate limiting is driven by upstream provider response headers.
  // No user-configurable fields — the gateway's GlobalMonitor handles backoff.
  [key: string]: unknown
}

export interface CacheConfig {
  type: string
  ttl: number
  maxEntries: number
  similarityThreshold: number
}

export interface RouterConfig {
  mappings: Array<{ model: string; provider: string }>
}

export interface LoadBalancerConfig {
  strategy: string
  weights: Array<{ provider: string; weight: number }>
  fallback: string
}

export interface InputGuardrailsConfig {
  piiDetection: boolean
  promptInjection: boolean
  contentFilter: boolean
}

export interface OutputGuardrailsConfig {
  jailbreakDetection: boolean
  hallucinationDetection: boolean
  toxicityDetection: boolean
}

export interface ProviderConfig {
  providerType: string
  model: string
  baseUrl: string
  maxTokens: number
}

export interface SandboxConfig {
  enabled: boolean
  image: string
  cpuLimit: number
  memoryMb: number
  diskMb: number
  timeoutSeconds: number
  networkMode: 'deny' | 'whitelist' | 'allow'
  allowedHosts: string[]
  envVars: Record<string, string>
  tools: string[]
}

export interface AgentConfig {
  agentId: string
  agentName: string
  agentModel?: string
  useInline: boolean
  inlineAgent: {
    name: string
    model: string
    systemPrompt: string
    tools: string[]
    temperature: number
    maxTokens: number
    browser: {
      enabled: boolean
      headless: boolean
    }
  }
  maxIterations: number
  maxToolCallsPerTurn: number
  turnTimeout: string
  contextMode: 'inherit' | 'isolated' | 'custom'
  sandbox?: SandboxConfig
  memoryEnabled: boolean
  memoryCollection: string
  memoryTopK: number
  memoryMinScore: number
  memoryStoreResponses: boolean
}

export interface FunctionConfig {
  functionId: string
  functionName: string
  functionMode: string
  parameterMappings: Record<string, string>
  timeoutMs: number
}

export interface ResponseConfig {
  format: string
  streaming: boolean
  includeUsage: boolean
  includeTimings: boolean
}

export interface HttpRequestConfig {
  method: string
  url: string
  headers: Record<string, string>
  body: string
  timeoutMs: number
  responseVariable: string
}

export interface WebhookConfig {
  url: string
  method: string
  headers: Record<string, string>
  bodyTemplate: string
  retries: number
  timeoutMs: number
}

export interface IfElseConfig {
  conditionType: 'expression' | 'jsonpath' | 'header'
  conditionExpression: string
}

export interface MemoryConfig {
  operation: 'store' | 'query'
  collection: string
  contentSource: 'input' | 'variable' | 'previous' | 'static'
  variableName?: string
  staticContent?: string
  source?: string
  chunkSize: number
  topK: number
  minScore: number
  embeddingModel?: string
  outputVariable: string
}

export interface TTSConfig {
  provider: string
  model: string
  voice: string
  voiceCloneProfileId: string
  language: string
  speed: number
  responseFormat: string
  inputText: string // Static text or template like {{$prev.content}}, empty = use previous node output
  instructions: string // Natural language style instructions (Qwen instruct models)
  temperature: number
  topP: number
  stability: number // Voice stability (0-1)
  similarity: number // Voice similarity boost (0-1)
  style: number // Style exaggeration (0-1)
  enhancement: boolean // Audio enhancement (normalize + noise gate)
  speakerBoost: number // Speaker volume boost (0-1)
}

export interface STTConfig {
  provider: string
  model: string
  language: string
  responseFormat: string
  timestampGranularities: string[]
}

export interface VoiceCloneConfig {
  provider: string
  model: string
  voiceCloneProfileId: string
  preferredName: string
  inputText: string // Text to synthesize with the cloned voice, empty = use previous node output
  speed: number
  instructions: string
  temperature: number
  topP: number
  stability: number
  similarity: number
  style: number
  enhancement: boolean
  speakerBoost: number
}

// Union of all config types
export type NodeConfig =
  | StartConfig
  | AuthConfig
  | RateLimiterConfig
  | CacheConfig
  | RouterConfig
  | LoadBalancerConfig
  | InputGuardrailsConfig
  | OutputGuardrailsConfig
  | ProviderConfig
  | AgentConfig
  | FunctionConfig
  | ResponseConfig
  | HttpRequestConfig
  | WebhookConfig
  | IfElseConfig
  | MemoryConfig
  | TTSConfig
  | STTConfig
  | VoiceCloneConfig

// Node data that React Flow uses
export interface StudioNodeData {
  nodeType: StudioNodeType
  label: string
  config: NodeConfig
  isConfigured: boolean
  [key: string]: unknown // React Flow needs this for generic Node<T>
}

// React Flow node and edge types
export type StudioNode = Node<StudioNodeData>
export type StudioEdge = Edge<{ label?: string }>

// Handle definition for a node type
export interface HandleDef {
  type: 'source' | 'target'
  id: string
  position: 'top' | 'bottom' | 'left' | 'right'
  label?: string
}

// Node category for the palette
export type NodeCategory =
  | 'input'
  | 'middleware'
  | 'ai'
  | 'processing'
  | 'logic'
  | 'output'

// Node type metadata for the palette/registry
export interface NodeTypeMeta {
  type: StudioNodeType
  label: string
  category: NodeCategory
  color: string
  icon: string
  handles: HandleDef[]
  defaultConfig: NodeConfig
  maxInstances?: number
  /** If set, this node requires a paid tier (shown as a badge in the palette) */
  requiredTier?: 'Basic' | 'Pro' | 'Enterprise'
  /** Feature key used to gate this node */
  featureKey?: string
}

// Workflow data model (matches backend proto)
export interface Workflow {
  id: string
  name: string
  description?: string
  nodes: StudioNode[]
  edges: StudioEdge[]
  viewport?: { x: number; y: number; zoom: number }
  enabled: boolean
  version: number
  createdAt: string
  updatedAt: string
}
