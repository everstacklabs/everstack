import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { ProvidersService } from '@everstack/proto/everstack/providers/providers_service_pb'
import type {
  ListProviderCatalogRequest,
  ListProviderCatalogResponse,
  ListConfiguredProvidersRequest,
  ListConfiguredProvidersResponse,
  GetProviderRequest,
  GetProviderResponse,
  ConfigureProviderRequest,
  ConfigureProviderResponse,
  ToggleProviderRequest,
  ToggleProviderResponse,
  DeleteProviderConfigurationRequest,
  DeleteProviderConfigurationResponse,
  GetSyncStatusRequest,
  GetSyncStatusResponse,
  GetConfigYAMLRequest,
  GetConfigYAMLResponse,
  SaveConfigYAMLRequest,
  SaveConfigYAMLResponse,
  ReloadConfigRequest,
  ReloadConfigResponse,
  ProviderCatalogEntry,
  ProviderStatus,
} from '@everstack/proto/everstack/providers/providers_pb'
import {
  ListProviderCatalogRequestSchema,
  ListConfiguredProvidersRequestSchema,
  GetProviderRequestSchema,
  ConfigureProviderRequestSchema,
  ToggleProviderRequestSchema,
  DeleteProviderConfigurationRequestSchema,
  GetSyncStatusRequestSchema,
  GetConfigYAMLRequestSchema,
  SaveConfigYAMLRequestSchema,
  ReloadConfigRequestSchema,
} from '@everstack/proto/everstack/providers/providers_pb'

const env = ((typeof import.meta !== 'undefined'
  ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
  : undefined) ?? {}) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

// No API key interceptor needed - same-origin requests are allowed
const transport = createServerTransport(undefined, {
  baseUrl: `${baseUrl}${connectBase}`,
  interceptors: [],
})
const providersClient = createClientFor(ProvidersService)(transport)

export type ConfigureProviderParams = {
  providerName: string
  apiKey?: string // Optional for updates (required only for initial configuration)
  apiKeyName?: string
  apiKeyWeight?: number
  enabledModels: string[]
  customBaseUrl?: string
  customSettings?: Record<string, string>
}

/**
 * Fetches the full provider catalog (all available providers from defaults)
 */
export async function listProviderCatalog(): Promise<ListProviderCatalogResponse> {
  const req: ListProviderCatalogRequest = create(
    ListProviderCatalogRequestSchema,
    {},
  )
  return providersClient.listProviderCatalog(req)
}

/**
 * Fetches only the providers that have been configured by the user
 */
export async function listConfiguredProviders(): Promise<ListConfiguredProvidersResponse> {
  const req: ListConfiguredProvidersRequest = create(
    ListConfiguredProvidersRequestSchema,
    {},
  )
  return providersClient.listConfiguredProviders(req)
}

/**
 * Fetches detailed information about a specific provider
 */
export async function getProvider(
  providerName: string,
): Promise<GetProviderResponse> {
  const req: GetProviderRequest = create(GetProviderRequestSchema, {
    providerName,
  })
  return providersClient.getProvider(req)
}

/**
 * Creates or updates a provider configuration
 */
export async function configureProvider(
  params: ConfigureProviderParams,
): Promise<ConfigureProviderResponse> {
  const {
    providerName,
    apiKey,
    apiKeyName,
    apiKeyWeight,
    enabledModels,
    customBaseUrl,
    customSettings,
  } = params
  const req: ConfigureProviderRequest = create(ConfigureProviderRequestSchema, {
    providerName,
    apiKey: apiKey ?? '', // Empty string if not provided (for updates)
    apiKeyName: apiKeyName ?? '',
    apiKeyWeight: apiKeyWeight ?? 1,
    enabledModels,
    customBaseUrl: customBaseUrl ?? '',
    customSettings: customSettings ?? {},
  })
  return providersClient.configureProvider(req)
}

/**
 * Toggles a provider's active status
 */
export async function toggleProvider(
  providerName: string,
  isActive: boolean,
): Promise<ToggleProviderResponse> {
  const req: ToggleProviderRequest = create(ToggleProviderRequestSchema, {
    providerName,
    isActive,
  })
  return providersClient.toggleProvider(req)
}

/**
 * Deletes a provider configuration
 */
export async function deleteProviderConfiguration(
  providerName: string,
): Promise<DeleteProviderConfigurationResponse> {
  const req: DeleteProviderConfigurationRequest = create(
    DeleteProviderConfigurationRequestSchema,
    { providerName },
  )
  return providersClient.deleteProviderConfiguration(req)
}

/**
 * Gets the sync status between YAML and database
 */
export async function getSyncStatus(): Promise<GetSyncStatusResponse> {
  const req: GetSyncStatusRequest = create(GetSyncStatusRequestSchema, {})
  return providersClient.getSyncStatus(req)
}

/**
 * Gets the current YAML config content
 */
export async function getConfigYAML(): Promise<GetConfigYAMLResponse> {
  const req: GetConfigYAMLRequest = create(GetConfigYAMLRequestSchema, {})
  return providersClient.getConfigYAML(req)
}

/**
 * Saves YAML config and syncs to database
 */
export async function saveConfigYAML(
  yamlContent: string,
): Promise<SaveConfigYAMLResponse> {
  const req: SaveConfigYAMLRequest = create(SaveConfigYAMLRequestSchema, {
    yamlContent,
  })
  return providersClient.saveConfigYAML(req)
}

/**
 * Reloads config from YAML to database and triggers gateway reload
 */
export async function reloadConfig(): Promise<ReloadConfigResponse> {
  const req: ReloadConfigRequest = create(ReloadConfigRequestSchema, {})
  return providersClient.reloadConfig(req)
}

// Re-export types for convenience
export type { ProviderCatalogEntry, ProviderStatus }
