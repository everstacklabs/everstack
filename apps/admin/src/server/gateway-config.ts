import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { ConfigService } from '@everstack/proto/everstack/config/v1/config_service_pb'
import type {
  GetRuntimeConfigRequest,
  UpdateRuntimeConfigRequest,
  RuntimeConfig,
  GetRuntimeConfigSectionRequest,
  UpdateRuntimeConfigSectionRequest,
  ResetRuntimeConfigSectionRequest,
  ConfigSection,
} from '@everstack/proto/everstack/config/v1/config_service_pb'
import {
  GetRuntimeConfigRequestSchema,
  UpdateRuntimeConfigRequestSchema,
  GetRuntimeConfigSectionRequestSchema,
  UpdateRuntimeConfigSectionRequestSchema,
  ResetRuntimeConfigSectionRequestSchema,
} from '@everstack/proto/everstack/config/v1/config_service_pb'

const env = ((typeof import.meta !== 'undefined'
  ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
  : undefined) ?? {}) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
  baseUrl: `${baseUrl}${connectBase}`,
  interceptors: [],
})

const configClient = createClientFor(ConfigService)(transport)

// Section names for type safety
export type ConfigSectionName =
  | 'rate_limit'
  | 'load_balancer'
  | 'features'
  | 'agents'
  | 'functions'
  | 'mcp_gateway'
  | 'sandbox'
  | 'cache'
  | 'fastpath'
  | 'telemetry'
  | 'cors'

export const CONFIG_SECTIONS: ConfigSectionName[] = [
  'rate_limit',
  'load_balancer',
  'features',
  'agents',
  'functions',
  'mcp_gateway',
  'sandbox',
  'cache',
  'fastpath',
  'telemetry',
  'cors',
]

export const CONFIG_SECTION_LABELS: Record<ConfigSectionName, string> = {
  rate_limit: 'Rate Limiting',
  load_balancer: 'Load Balancer',
  features: 'Features',
  agents: 'Agents',
  functions: 'Functions',
  mcp_gateway: 'MCP Gateway',
  sandbox: 'Sandbox',
  cache: 'Cache',
  fastpath: 'FastPath',
  telemetry: 'Telemetry',
  cors: 'CORS',
}

/**
 * Fetches the full runtime configuration
 */
export async function getRuntimeConfig(): Promise<RuntimeConfig> {
  const req: GetRuntimeConfigRequest = create(GetRuntimeConfigRequestSchema, {})
  const response = await configClient.getRuntimeConfig(req)
  return response
}

/**
 * Updates the full runtime configuration
 */
export async function updateRuntimeConfig(
  config: Partial<RuntimeConfig>,
): Promise<RuntimeConfig> {
  const req: UpdateRuntimeConfigRequest = create(
    UpdateRuntimeConfigRequestSchema,
    {
      config: config as RuntimeConfig,
    },
  )
  return configClient.updateRuntimeConfig(req)
}

/**
 * Fetches a specific configuration section
 */
export async function getRuntimeConfigSection(
  section: ConfigSectionName,
): Promise<ConfigSection> {
  const req: GetRuntimeConfigSectionRequest = create(
    GetRuntimeConfigSectionRequestSchema,
    {
      section,
    },
  )
  return configClient.getRuntimeConfigSection(req)
}

/**
 * Updates a specific configuration section using structured data
 */
export async function updateRuntimeConfigSection(
  section: ConfigSectionName,
  config: Record<string, unknown>,
): Promise<ConfigSection> {
  const req: UpdateRuntimeConfigSectionRequest = create(
    UpdateRuntimeConfigSectionRequestSchema,
    {
      section,
      config: config as unknown as UpdateRuntimeConfigSectionRequest['config'],
    },
  )
  return configClient.updateRuntimeConfigSection(req)
}

/**
 * Updates a specific configuration section using YAML content
 */
export async function updateRuntimeConfigSectionYAML(
  section: ConfigSectionName,
  yamlContent: string,
): Promise<ConfigSection> {
  const req: UpdateRuntimeConfigSectionRequest = create(
    UpdateRuntimeConfigSectionRequestSchema,
    {
      section,
      yamlContent,
    },
  )
  return configClient.updateRuntimeConfigSection(req)
}

/**
 * Resets a configuration section to its default values
 */
export async function resetRuntimeConfigSection(
  section: ConfigSectionName,
): Promise<ConfigSection> {
  const req: ResetRuntimeConfigSectionRequest = create(
    ResetRuntimeConfigSectionRequestSchema,
    {
      section,
    },
  )
  return configClient.resetRuntimeConfigSection(req)
}
