import type {
  RateLimitConfigJson,
  LoadBalancerConfigJson,
  FeaturesConfigJson as ProtoFeaturesConfig,
  CacheConfigJson,
  TelemetryConfigJson,
  CORSConfigJson,
  RuntimeConfigJson,
} from '@everstack/proto/everstack/config/v1/config_service_pb'

export type RateLimitConfig = RateLimitConfigJson
export type FallbackModelConfig = {
  provider: string
  model: string
  maxTokens?: number
  temperature?: number
}

export type FallbackFactorConfig = {
  name: string
  priority?: number
  strategy: string // "priority" | "round_robin" | "parallel"
  models: FallbackModelConfig[]
  timeoutMs?: number
  backoffMs?: number
  maxAttempts?: number
}

export type FallbackConfig = {
  enabled: boolean
  default?: FallbackModelConfig
  factors?: FallbackFactorConfig[]
}

export type LoadBalancerConfig = LoadBalancerConfigJson & {
  fallback?: FallbackConfig
}
export type CacheConfig = CacheConfigJson
export type TelemetryConfig = TelemetryConfigJson
export type CORSConfig = CORSConfigJson
export type RuntimeConfig = RuntimeConfigJson

export interface FeaturesConfig extends ProtoFeaturesConfig {
  server?: {
    enableNewLoadBalancer?: boolean
    enableExperimentalApiV2?: boolean
    enableDebugEndpoints?: boolean
    enableProfiling?: boolean
  }
  gateway?: {
    enableFunctionCalling?: boolean
    enableEmbeddings?: boolean
    enableAgents?: boolean
    enableStreaming?: boolean
    enableModelFineTuning?: boolean
    enableCustomModels?: boolean
    enableModelMetrics?: boolean
    enableCostTracking?: boolean
    enableResponseCaching?: boolean
    enableRequestBatching?: boolean
    enableConnectionPooling?: boolean
    enableRequestLogging?: boolean
    enablePerformanceMonitoring?: boolean
    enableHealthChecks?: boolean
    enableSse?: boolean
    enableFastpath?: boolean
    enableExactCache?: boolean
    enableSemanticCache?: boolean
    enableBufferPooling?: boolean
  }
  edge?: {
    url?: string
    pollInterval?: string
    cacheDir?: string
  }
  enableMemory?: boolean
  memory?: {
    backend?: string
    embeddingModels?: Array<{
      model?: string
      dimension?: number
    }>
    qdrant?: {
      address?: string
      apiKey?: string
    }
    pinecone?: {
      apiKey?: string
      cloud?: string
      region?: string
      environment?: string
    }
    weaviate?: {
      url?: string
      apiKey?: string
    }
  }
  isolatedFunctions?: {
    imagePrefix?: string
    defaultTimeoutMs?: number
    defaultMemoryMb?: number
    dockerHost?: string
    dockerAutoDetect?: boolean
    dockerFallbackHosts?: string[]
    pool?: {
      enabled?: boolean
      minPerRuntime?: number
      maxPerRuntime?: number
      idleTimeoutSeconds?: number
      maxUses?: number
      warmupOnStart?: boolean
    }
  }
  fastpath?: {
    enabled?: boolean
    auth?: {
      bloomFilterSize?: number
      bloomFalsePositiveRate?: number
      cacheTtl?: string
    }
    cache?: {
      exact?: {
        enabled?: boolean
        maxEntries?: number
        ttl?: string
      }
      semantic?: {
        enabled?: boolean
        maxEntries?: number
        ttl?: string
        similarityThreshold?: number
        algorithm?: string
        numHashes?: number
        shingleSize?: number
      }
    }
    streaming?: {
      bufferSize?: number
      poolSize?: number
    }
    connectionPool?: {
      maxIdlePerHost?: number
      prewarmConnections?: number
      prewarmOnStartup?: boolean
    }
  }
  sandbox?: {
    enabled?: boolean
    backend?: 'docker' | 'kubernetes' | 'firecracker' | string
    defaultImage?: string
    allowedImages?: string[]
    dnsServers?: string[]
    maxCpu?: number
    maxMemoryMb?: number
    maxDiskMb?: number
    maxTimeoutSeconds?: number
    defaultKeepWarmIdleSeconds?: number
    maxConcurrentCreates?: number
    docker?: {
      host?: string
      autoPull?: boolean
      autoBuild?: boolean
    }
    kubernetes?: {
      kubeconfig?: string
      namespace?: string
      imagePullPolicy?: string
      serviceAccount?: string
      nodeSelector?: Record<string, string>
    }
    ssh?: {
      listenAddr?: string
      host?: string
    }
    portExposure?: {
      enabled?: boolean
      baseDomain?: string
      listenAddr?: string
      maxPortsPerSandbox?: number
      requirePreviewToken?: boolean
      requestTimeoutSeconds?: number
      maxRequestBodyMb?: number
      tls?: {
        enabled?: boolean
        certPath?: string
        keyPath?: string
        autocert?: boolean
        autocertDir?: string
      }
      cors?: {
        enabled?: boolean
        allowedOrigins?: string[]
        allowedMethods?: string[]
        allowedHeaders?: string[]
        maxAgeSeconds?: number
      }
    }
    firecracker?: {
      binaryPath?: string
      kernelPath?: string
      rootfsDir?: string
      workDir?: string
      poolMinSize?: number
      poolMaxSize?: number
      poolMaxTotal?: number
      replenishIntervalMs?: number
      replenishBatch?: number
      warmupOnStart?: boolean
    }
  }
  mcpGateway?: {
    enabled?: boolean
  }
}

export type ViewMode = 'form' | 'yaml'

// ============================================================================
// UI Metadata for Config Forms
// These provide labels and descriptions for rendering forms
// The keys MUST match the proto field names for type safety
// ============================================================================

export interface FieldMetadata {
  label: string
  description: string
}

// Features config field metadata - keys match FeaturesConfigJson.
// Only toggles that actually drive gateway behaviour are listed here:
//   enableStreaming    → IsStreamingEnabled() in api.go + processors.go
//   enableSse          → IsSSEEnabled() in api.go (gates SSE middleware)
//   enableEmbeddings   → processEmbeddings() rejects with FailedPrecondition
//   enableFunctionCalling → processChatCompletion() rejects requests with `tools`
// The previous list also included enableResponseCaching (duplicates the
// dedicated Cache panel toggle), enableRequestLogging (gateway-wide ops
// concern, not a per-tenant runtime knob), and enableHealthChecks
// (gating /healthz would break Kubernetes liveness probes). Those are
// removed rather than shipping non-functional UI. See
// docs/audits/runtime-config.md.
export const FEATURES_FIELD_METADATA: Record<string, FieldMetadata> = {
  enableStreaming: {
    label: 'Streaming',
    description: 'Enable streaming responses from LLM providers',
  },
  enableEmbeddings: {
    label: 'Embeddings',
    description: 'Enable text embedding endpoints',
  },
  enableFunctionCalling: {
    label: 'Function Calling',
    description: 'Enable function/tool calling capabilities',
  },
  enableSse: {
    label: 'Server-Sent Events (SSE)',
    description: 'Enable SSE format for streaming responses',
  },
} as const

// Telemetry trace options metadata
export const TELEMETRY_TRACE_METADATA: Record<string, FieldMetadata> = {
  traceProviderCalls: {
    label: 'Provider Calls',
    description: 'Trace individual provider API calls',
  },
  traceStreamChunks: {
    label: 'Stream Chunks',
    description: 'Trace individual streaming chunks',
  },
  traceFallbacks: {
    label: 'Fallbacks',
    description: 'Trace fallback behavior',
  },
} as const

// Rate limit key source options
export const RATE_LIMIT_KEY_SOURCES = [
  { value: 'correlation', label: 'Correlation ID' },
  { value: 'ip', label: 'IP Address' },
  { value: 'api_key', label: 'API Key' },
] as const

// Load balancer strategy options
export const LOAD_BALANCER_STRATEGIES = [
  { value: 'round_robin', label: 'Round Robin' },
  { value: 'weighted', label: 'Weighted' },
  { value: 'least_connections', label: 'Least Connections' },
] as const

// Cache type options
export const CACHE_TYPES = [
  { value: 'memory', label: 'Memory' },
  { value: 'redis', label: 'Redis' },
] as const

// Telemetry granularity options
export const TELEMETRY_GRANULARITIES = [
  { value: 'minimal', label: 'Minimal' },
  { value: 'standard', label: 'Standard' },
  { value: 'detailed', label: 'Detailed' },
] as const

export const EMBEDDING_MODELS = [
  {
    value: 'text-embedding-3-small',
    label: 'text-embedding-3-small (OpenAI)',
    dimension: 1536,
  },
  {
    value: 'text-embedding-3-large',
    label: 'text-embedding-3-large (OpenAI)',
    dimension: 3072,
  },
  {
    value: 'text-embedding-ada-002',
    label: 'text-embedding-ada-002 (OpenAI)',
    dimension: 1536,
  },
  {
    value: 'embed-english-v3.0',
    label: 'embed-english-v3.0 (Cohere)',
    dimension: 1024,
  },
  {
    value: 'nomic-embed-text',
    label: 'nomic-embed-text (Ollama)',
    dimension: 768,
  },
] as const
