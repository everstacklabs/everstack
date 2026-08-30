import type { JSONSchema7 } from 'json-schema'

// ============================================================================
// JSON Schemas for Runtime Config Sections
// These schemas provide intellisense, validation, and documentation
// Field names match the proto-generated camelCase format
// ============================================================================

export const rateLimitSchema: JSONSchema7 = {
  $schema: 'http://json-schema.org/draft-07/schema#',
  title: 'Rate Limit Configuration',
  description: 'Configure rate limiting for the gateway',
  type: 'object',
  properties: {
    enabled: {
      type: 'boolean',
      description: 'Enable or disable rate limiting',
      default: false,
    },
    requestsPerMinute: {
      type: 'integer',
      description: 'Maximum number of requests allowed per minute',
      minimum: 1,
      default: 1000,
    },
    burst: {
      type: 'integer',
      description: 'Maximum burst size allowed above the rate limit',
      minimum: 1,
      default: 100,
    },
    keySource: {
      type: 'string',
      description:
        'How to identify unique clients for rate limiting. Options: correlation (use correlation ID from request header), ip (use client IP address), api_key (use API key from request)',
      enum: ['correlation', 'ip', 'api_key'],
      default: 'correlation',
    },
  },
  additionalProperties: false,
}

export const loadBalancerSchema: JSONSchema7 = {
  $schema: 'http://json-schema.org/draft-07/schema#',
  title: 'Load Balancer Configuration',
  description: 'Configure load balancing across providers',
  type: 'object',
  properties: {
    enabled: {
      type: 'boolean',
      description: 'Enable or disable load balancing',
      default: false,
    },
    strategy: {
      type: 'string',
      description:
        'Load balancing strategy. Options: round_robin (distribute evenly), weighted (distribute by weights), least_connections (route to least busy)',
      enum: ['round_robin', 'weighted', 'least_connections'],
      default: 'round_robin',
    },
    keySource: {
      type: 'string',
      description:
        'How to maintain session affinity. Options: correlation, ip, api_key',
      enum: ['correlation', 'ip', 'api_key'],
      default: 'correlation',
    },
  },
  additionalProperties: false,
}

export const featuresSchema: JSONSchema7 = {
  $schema: 'http://json-schema.org/draft-07/schema#',
  title: 'Features Configuration',
  description:
    'Gateway features, sandbox, memory, and fastpath runtime configuration',
  type: 'object',
  properties: {
    enableStreaming: {
      type: 'boolean',
      description: 'Enable streaming responses from LLM providers',
      default: true,
    },
    enableEmbeddings: {
      type: 'boolean',
      description: 'Enable text embedding endpoints',
      default: true,
    },
    enableFunctionCalling: {
      type: 'boolean',
      description: 'Enable function/tool calling capabilities',
      default: true,
    },
    enableResponseCaching: {
      type: 'boolean',
      description: 'Cache responses to reduce API costs',
      default: false,
    },
    enableSse: {
      type: 'boolean',
      description:
        'Enable Server-Sent Events (SSE) format for streaming responses',
      default: true,
    },
    enableRequestLogging: {
      type: 'boolean',
      description: 'Log all incoming requests',
      default: true,
    },
    enableHealthChecks: {
      type: 'boolean',
      description: 'Enable health check endpoints',
      default: true,
    },
    enableAgents: {
      type: 'boolean',
      description: 'Enable AI agent capabilities',
      default: false,
    },
    server: {
      type: 'object',
      properties: {
        enableNewLoadBalancer: { type: 'boolean' },
        enableExperimentalApiV2: { type: 'boolean' },
        enableDebugEndpoints: { type: 'boolean' },
        enableProfiling: { type: 'boolean' },
      },
      additionalProperties: true,
    },
    edge: {
      type: 'object',
      properties: {
        url: { type: 'string' },
        pollInterval: { type: 'string' },
        cacheDir: { type: 'string' },
      },
      additionalProperties: true,
    },
    gateway: {
      type: 'object',
      properties: {
        enableFunctionCalling: { type: 'boolean' },
        enableEmbeddings: { type: 'boolean' },
        enableAgents: { type: 'boolean' },
        enableStreaming: { type: 'boolean' },
        enableModelFineTuning: { type: 'boolean' },
        enableCustomModels: { type: 'boolean' },
        enableModelMetrics: { type: 'boolean' },
        enableCostTracking: { type: 'boolean' },
        enableResponseCaching: { type: 'boolean' },
        enableRequestBatching: { type: 'boolean' },
        enableConnectionPooling: { type: 'boolean' },
        enableRequestLogging: { type: 'boolean' },
        enablePerformanceMonitoring: { type: 'boolean' },
        enableHealthChecks: { type: 'boolean' },
        enableSse: { type: 'boolean' },
        enableFastpath: { type: 'boolean' },
        enableExactCache: { type: 'boolean' },
        enableSemanticCache: { type: 'boolean' },
        enableBufferPooling: { type: 'boolean' },
      },
      additionalProperties: true,
    },
    enableMemory: {
      type: 'boolean',
      description: 'Enable vector memory features',
      default: false,
    },
    memory: {
      type: 'object',
      properties: {
        backend: { type: 'string' },
        embeddingModels: {
          type: 'array',
          items: {
            type: 'object',
            properties: {
              model: { type: 'string' },
              dimension: { type: 'integer' },
            },
            additionalProperties: true,
          },
        },
        qdrant: {
          type: 'object',
          properties: {
            address: { type: 'string' },
            apiKey: { type: 'string' },
          },
          additionalProperties: true,
        },
        pinecone: {
          type: 'object',
          properties: {
            apiKey: { type: 'string' },
            cloud: { type: 'string' },
            region: { type: 'string' },
            environment: { type: 'string' },
          },
          additionalProperties: true,
        },
        weaviate: {
          type: 'object',
          properties: {
            url: { type: 'string' },
            apiKey: { type: 'string' },
          },
          additionalProperties: true,
        },
      },
      additionalProperties: true,
    },
    isolatedFunctions: {
      type: 'object',
      properties: {
        imagePrefix: { type: 'string' },
        defaultTimeoutMs: { type: 'integer' },
        defaultMemoryMb: { type: 'integer' },
        dockerHost: { type: 'string' },
        dockerAutoDetect: { type: 'boolean' },
        dockerFallbackHosts: { type: 'array', items: { type: 'string' } },
        pool: {
          type: 'object',
          properties: {
            enabled: { type: 'boolean' },
            minPerRuntime: { type: 'integer' },
            maxPerRuntime: { type: 'integer' },
            idleTimeoutSeconds: { type: 'integer' },
            maxUses: { type: 'integer' },
            warmupOnStart: { type: 'boolean' },
          },
          additionalProperties: true,
        },
      },
      additionalProperties: true,
    },
    fastpath: {
      type: 'object',
      properties: {
        enabled: { type: 'boolean' },
        auth: {
          type: 'object',
          properties: {
            bloomFilterSize: { type: 'integer' },
            bloomFalsePositiveRate: { type: 'number' },
            cacheTtl: { type: 'string' },
          },
          additionalProperties: true,
        },
        cache: {
          type: 'object',
          properties: {
            exact: {
              type: 'object',
              properties: {
                enabled: { type: 'boolean' },
                maxEntries: { type: 'integer' },
                ttl: { type: 'string' },
              },
              additionalProperties: true,
            },
            semantic: {
              type: 'object',
              properties: {
                enabled: { type: 'boolean' },
                maxEntries: { type: 'integer' },
                ttl: { type: 'string' },
                similarityThreshold: { type: 'number' },
                algorithm: { type: 'string' },
                numHashes: { type: 'integer' },
                shingleSize: { type: 'integer' },
              },
              additionalProperties: true,
            },
          },
          additionalProperties: true,
        },
        streaming: {
          type: 'object',
          properties: {
            bufferSize: { type: 'integer' },
            poolSize: { type: 'integer' },
          },
          additionalProperties: true,
        },
        connectionPool: {
          type: 'object',
          properties: {
            maxIdlePerHost: { type: 'integer' },
            prewarmConnections: { type: 'integer' },
            prewarmOnStartup: { type: 'boolean' },
          },
          additionalProperties: true,
        },
      },
      additionalProperties: true,
    },
    sandbox: {
      type: 'object',
      properties: {
        enabled: { type: 'boolean' },
        backend: { type: 'string' },
        defaultImage: { type: 'string' },
        allowedImages: { type: 'array', items: { type: 'string' } },
        dnsServers: { type: 'array', items: { type: 'string' } },
        maxCpu: { type: 'number' },
        maxMemoryMb: { type: 'integer' },
        maxDiskMb: { type: 'integer' },
        maxTimeoutSeconds: { type: 'integer' },
        defaultKeepWarmIdleSeconds: { type: 'integer' },
        maxConcurrentCreates: { type: 'integer' },
        docker: {
          type: 'object',
          properties: {
            host: { type: 'string' },
            autoPull: { type: 'boolean' },
            autoBuild: { type: 'boolean' },
          },
          additionalProperties: true,
        },
        kubernetes: {
          type: 'object',
          properties: {
            kubeconfig: { type: 'string' },
            namespace: { type: 'string' },
            imagePullPolicy: { type: 'string' },
            serviceAccount: { type: 'string' },
            nodeSelector: { type: 'object' },
          },
          additionalProperties: true,
        },
        ssh: {
          type: 'object',
          properties: {
            listenAddr: { type: 'string' },
            host: { type: 'string' },
          },
          additionalProperties: true,
        },
        portExposure: {
          type: 'object',
          properties: {
            enabled: { type: 'boolean' },
            baseDomain: { type: 'string' },
            listenAddr: { type: 'string' },
            maxPortsPerSandbox: { type: 'integer' },
            requirePreviewToken: { type: 'boolean' },
            requestTimeoutSeconds: { type: 'integer' },
            maxRequestBodyMb: { type: 'integer' },
            tls: {
              type: 'object',
              properties: {
                enabled: { type: 'boolean' },
                certPath: { type: 'string' },
                keyPath: { type: 'string' },
                autocert: { type: 'boolean' },
                autocertDir: { type: 'string' },
              },
              additionalProperties: true,
            },
            cors: {
              type: 'object',
              properties: {
                enabled: { type: 'boolean' },
                allowedOrigins: { type: 'array', items: { type: 'string' } },
                allowedMethods: { type: 'array', items: { type: 'string' } },
                allowedHeaders: { type: 'array', items: { type: 'string' } },
                maxAgeSeconds: { type: 'integer' },
              },
              additionalProperties: true,
            },
          },
          additionalProperties: true,
        },
        firecracker: {
          type: 'object',
          properties: {
            binaryPath: { type: 'string' },
            kernelPath: { type: 'string' },
            rootfsDir: { type: 'string' },
            workDir: { type: 'string' },
            poolMinSize: { type: 'integer' },
            poolMaxSize: { type: 'integer' },
            poolMaxTotal: { type: 'integer' },
            replenishIntervalMs: { type: 'integer' },
            replenishBatch: { type: 'integer' },
            warmupOnStart: { type: 'boolean' },
          },
          additionalProperties: true,
        },
      },
      additionalProperties: true,
    },
    mcpGateway: {
      type: 'object',
      properties: {
        enabled: { type: 'boolean' },
      },
      additionalProperties: true,
    },
  },
  additionalProperties: true,
}

export const cacheSchema: JSONSchema7 = {
  $schema: 'http://json-schema.org/draft-07/schema#',
  title: 'Cache Configuration',
  description: 'Configure response caching',
  type: 'object',
  properties: {
    enabled: {
      type: 'boolean',
      description: 'Enable or disable caching',
      default: false,
    },
    type: {
      type: 'string',
      description:
        'Cache storage type. Options: memory (fast, lost on restart), redis (persistent, distributed)',
      enum: ['memory', 'redis'],
      default: 'memory',
    },
    ttl: {
      type: 'string',
      description: 'Cache time-to-live duration (e.g., "10m", "1h", "24h")',
      pattern: '^[0-9]+(s|m|h|d)$',
      default: '10m',
    },
    memoryMaxSize: {
      type: 'integer',
      description:
        'Maximum number of items to cache in memory (memory type only)',
      minimum: 1000,
      default: 50000,
    },
    redisAddress: {
      type: 'string',
      description: 'Redis server address (redis type only)',
      default: 'localhost:6379',
    },
    redisDb: {
      type: 'integer',
      description: 'Redis database number (0-15)',
      minimum: 0,
      maximum: 15,
      default: 0,
    },
    redisPoolSize: {
      type: 'integer',
      description: 'Redis connection pool size',
      minimum: 1,
      default: 100,
    },
  },
  additionalProperties: false,
}

export const telemetrySchema: JSONSchema7 = {
  $schema: 'http://json-schema.org/draft-07/schema#',
  title: 'Telemetry Configuration',
  description: 'Configure OpenTelemetry tracing and metrics',
  type: 'object',
  properties: {
    enabled: {
      type: 'boolean',
      description: 'Enable or disable telemetry',
      default: false,
    },
    samplingRate: {
      type: 'number',
      description:
        'Percentage of requests to trace (0.0 to 1.0, where 1.0 = 100%)',
      minimum: 0,
      maximum: 1,
      default: 0.1,
    },
    granularity: {
      type: 'string',
      description:
        'Level of detail in traces. Options: minimal (basic logging), standard (timing and provider info), detailed (full trace)',
      enum: ['minimal', 'standard', 'detailed'],
      default: 'standard',
    },
    traceProviderCalls: {
      type: 'boolean',
      description: 'Trace individual provider API calls',
      default: true,
    },
    traceStreamChunks: {
      type: 'boolean',
      description: 'Trace individual streaming chunks',
      default: false,
    },
    traceFallbacks: {
      type: 'boolean',
      description: 'Trace fallback behavior',
      default: true,
    },
    collectorUrl: {
      type: 'string',
      description: 'OpenTelemetry collector URL',
      default: 'localhost:4317',
    },
    serviceName: {
      type: 'string',
      description: 'Service name for traces',
      default: 'everstack-gateway',
    },
  },
  additionalProperties: false,
}

export const corsSchema: JSONSchema7 = {
  $schema: 'http://json-schema.org/draft-07/schema#',
  title: 'CORS Configuration',
  description: 'Configure Cross-Origin Resource Sharing',
  type: 'object',
  properties: {
    enabled: {
      type: 'boolean',
      description: 'Enable or disable CORS',
      default: true,
    },
    allowedOrigins: {
      type: 'array',
      description: 'List of allowed origins (use "*" for all)',
      items: { type: 'string' },
      default: ['*'],
    },
    allowedMethods: {
      type: 'array',
      description: 'List of allowed HTTP methods',
      items: {
        type: 'string',
        enum: ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'OPTIONS', 'HEAD'],
      },
      default: ['GET', 'POST', 'PUT', 'DELETE', 'OPTIONS'],
    },
    allowedHeaders: {
      type: 'array',
      description: 'List of allowed headers (use "*" for all)',
      items: { type: 'string' },
      default: ['*'],
    },
    exposedHeaders: {
      type: 'array',
      description: 'Headers that can be accessed by the client',
      items: { type: 'string' },
      default: [],
    },
    allowCredentials: {
      type: 'boolean',
      description: 'Allow cookies and authorization headers',
      default: false,
    },
    maxAge: {
      type: 'string',
      description: 'How long to cache preflight responses (in seconds)',
      default: '3600',
    },
  },
  additionalProperties: false,
}

// Map section names to their schemas
export const CONFIG_SCHEMAS: Record<string, JSONSchema7> = {
  rate_limit: rateLimitSchema,
  load_balancer: loadBalancerSchema,
  features: featuresSchema,
  agents: featuresSchema,
  functions: featuresSchema,
  mcp_gateway: featuresSchema,
  sandbox: featuresSchema,
  cache: cacheSchema,
  fastpath: featuresSchema,
  telemetry: telemetrySchema,
  cors: corsSchema,
}
