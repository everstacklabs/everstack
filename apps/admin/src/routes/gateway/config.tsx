import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useCallback } from 'react'
import yaml from 'js-yaml'
import { z } from 'zod'
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Loader,
  Alert,
  AlertDescription,
} from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { useRuntimeConfigSection } from '@/hooks/gateway/use-runtime-config'
import { useAuthMode } from '@/hooks/auth/use-auth'
import {
  CONFIG_SECTIONS,
  CONFIG_SECTION_LABELS,
  type ConfigSectionName,
} from '@/server/gateway-config'
import {
  ConfigSectionWrapper,
  RateLimitForm,
  LoadBalancerForm,
  FeaturesForm,
  AgentsForm,
  FunctionsForm,
  McpGatewayForm,
  SandboxForm,
  CacheForm,
  FastpathForm,
  TelemetryForm,
  CORSForm,
  type RateLimitConfig,
  type LoadBalancerConfig,
  type FeaturesConfig,
  type CacheConfig,
  type TelemetryConfig,
  type CORSConfig,
} from '@/components/gateway/config'
import {
  useGatewayConfigActions,
  useSectionStates,
} from '@/stores/gateway-config-store'

// Key transformation utilities for YAML (snake_case) <-> Config (camelCase)
function snakeToCamel(str: string): string {
  return str.replace(/_([a-z])/g, (_, letter) => letter.toUpperCase())
}

function camelToSnake(str: string): string {
  return str.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`)
}

function transformKeys<T>(
  obj: unknown,
  transformer: (key: string) => string,
): T {
  if (obj === null || obj === undefined) return obj as T
  if (Array.isArray(obj)) {
    return obj.map((item) => transformKeys(item, transformer)) as T
  }
  if (typeof obj === 'object') {
    const result: Record<string, unknown> = {}
    for (const [key, value] of Object.entries(obj)) {
      result[transformer(key)] = transformKeys(value, transformer)
    }
    return result as T
  }
  return obj as T
}

function yamlToConfig<T>(parsed: unknown): T {
  return transformKeys<T>(parsed, snakeToCamel)
}

function configToYaml<T>(config: T): T {
  return transformKeys<T>(config, camelToSnake)
}

const configSearchSchema = z.object({
  tab: z.enum(CONFIG_SECTIONS as [ConfigSectionName, ...ConfigSectionName[]]).catch('rate_limit'),
})

export const Route = createFileRoute('/gateway/config')({
  component: GatewayConfigPage,
  validateSearch: configSearchSchema,
})

// Sections that have a dedicated nav surface elsewhere or are not yet
// configurable from this page. Filtered out of the tab list below.
// MCP gateway is managed under its own /mcp section in the sidebar.
const HIDDEN_SECTIONS_BASE = new Set<ConfigSectionName>(['mcp_gateway'])

// Sections that have no effect for managed-cloud tenants and would only
// mislead them. Sandbox backend is picked once at gateway pod boot from
// EVS_SANDBOX_BACKEND (set in our helm values to firecracker-agent for
// every cloud pod) — there is no per-tenant override path. Showing the
// tab lets a tenant click around resource limits and ports that nothing
// reads, then hit Save and assume it took effect.
const HIDDEN_SECTIONS_CLOUD = new Set<ConfigSectionName>([
  ...HIDDEN_SECTIONS_BASE,
  'sandbox',
])

const SECTION_ICONS: Record<ConfigSectionName, string> = {
  rate_limit: 'mdi:speedometer',
  load_balancer: 'mdi:scale-balance',
  features: 'mdi:toggle-switch',
  agents: 'mdi:robot-outline',
  functions: 'mdi:function-variant',
  mcp_gateway: 'mdi:transit-connection-variant',
  sandbox: 'mdi:cube-outline',
  cache: 'mdi:database',
  fastpath: 'mdi:lightning-bolt',
  telemetry: 'mdi:chart-timeline',
  cors: 'mdi:web',
}

// Maps UI section names to their backend API section names.
// These tabs are UI-only splits that read/write the same `features` backend section.
const SECTION_API_MAP: Partial<Record<ConfigSectionName, ConfigSectionName>> = {
  agents: 'features',
  functions: 'features',
  mcp_gateway: 'features',
  sandbox: 'features',
  fastpath: 'features',
}

type SectionConfig = {
  rate_limit: RateLimitConfig
  load_balancer: LoadBalancerConfig
  features: FeaturesConfig
  agents: FeaturesConfig
  functions: FeaturesConfig
  mcp_gateway: FeaturesConfig
  sandbox: FeaturesConfig
  cache: CacheConfig
  fastpath: FeaturesConfig
  telemetry: TelemetryConfig
  cors: CORSConfig
}

function ConfigSection<T extends ConfigSectionName>({
  section,
}: {
  section: T
}) {
  const apiSection = SECTION_API_MAP[section] ?? section
  const { data, isLoading, error } = useRuntimeConfigSection(apiSection)
  const sectionStates = useSectionStates()
  const {
    setSectionConfig,
    setSectionYAML,
    setSectionInitialized,
    setSectionHasChanges,
  } = useGatewayConfigActions()

  // Get stored state for this section
  const storedState = sectionStates[section]

  // Derive local state from store - this ensures we always show what's in the store
  const localConfig = storedState.config as SectionConfig[T] | null
  const localYAML = storedState.yaml

  // Initialize from API data only once per section
  useEffect(() => {
    // Skip if we've already initialized this section
    if (storedState.isInitialized) return

    // Skip if data hasn't loaded yet
    if (!data?.config && !data?.yamlContent) return

    if (data?.config) {
      const configObj = data.config as unknown
      const typedConfig = configObj as SectionConfig[T]
      setSectionConfig(
        section,
        typedConfig as unknown as Record<string, unknown>,
      )
    }
    if (data?.yamlContent) {
      setSectionYAML(section, data.yamlContent)
    }

    setSectionInitialized(section, true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, section, storedState.isInitialized])

  const handleConfigChange = useCallback(
    (newConfig: SectionConfig[T]) => {
      setSectionConfig(section, newConfig as unknown as Record<string, unknown>)
      setSectionHasChanges(section, true)

      // Sync form changes to YAML (convert camelCase to snake_case)
      try {
        const snakeCaseConfig = configToYaml(newConfig)
        const yamlString = yaml.dump(snakeCaseConfig, {
          indent: 2,
          lineWidth: -1,
        })
        setSectionYAML(section, yamlString)
      } catch {
        // Failed to convert to YAML, ignore
      }
    },
    [section, setSectionConfig, setSectionHasChanges, setSectionYAML],
  )

  const handleYAMLChange = useCallback(
    (yamlString: string) => {
      setSectionYAML(section, yamlString)
      setSectionHasChanges(section, true)

      // Try to parse YAML and sync to form config (convert snake_case to camelCase)
      try {
        const parsed = yaml.load(yamlString)
        if (parsed && typeof parsed === 'object') {
          const camelCaseConfig = yamlToConfig<SectionConfig[T]>(parsed)
          setSectionConfig(
            section,
            camelCaseConfig as unknown as Record<string, unknown>,
          )
        }
      } catch {
        // YAML is invalid, don't update config - user is still editing
      }
    },
    [section, setSectionYAML, setSectionHasChanges, setSectionConfig],
  )

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader loaderText="Loading configuration..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex items-center justify-center h-48 text-red-400 light:text-red-600">
        <Iconify.Icon icon="mdi:alert-circle" className="h-5 w-5 mr-2" />
        Failed to load configuration
      </div>
    )
  }

  if (!localConfig) {
    return null
  }

  const renderForm = () => {
    switch (section) {
      case 'rate_limit':
        return (
          <RateLimitForm
            config={localConfig as RateLimitConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'load_balancer':
        return (
          <LoadBalancerForm
            config={localConfig as LoadBalancerConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'features':
        return (
          <FeaturesForm
            config={localConfig as FeaturesConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'agents':
        return (
          <AgentsForm
            config={localConfig as FeaturesConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'functions':
        return (
          <FunctionsForm
            config={localConfig as FeaturesConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'mcp_gateway':
        return (
          <McpGatewayForm
            config={localConfig as FeaturesConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'sandbox':
        return (
          <SandboxForm
            config={localConfig as FeaturesConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'cache':
        return (
          <CacheForm
            config={localConfig as CacheConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'fastpath':
        return (
          <FastpathForm
            config={localConfig as FeaturesConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'telemetry':
        return (
          <TelemetryForm
            config={localConfig as TelemetryConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      case 'cors':
        return (
          <CORSForm
            config={localConfig as CORSConfig}
            onChange={(config) =>
              handleConfigChange(config as SectionConfig[T])
            }
          />
        )
      default:
        return null
    }
  }

  return (
    <ConfigSectionWrapper
      title={CONFIG_SECTION_LABELS[section]}
      description={getSectionDescription(section)}
      section={section}
      yamlContent={localYAML}
      onYAMLChange={handleYAMLChange}
    >
      {renderForm()}
    </ConfigSectionWrapper>
  )
}

function getSectionDescription(section: ConfigSectionName): string {
  switch (section) {
    case 'rate_limit':
      return 'Control the rate of incoming requests to prevent abuse'
    case 'load_balancer':
      return 'Configure how requests are distributed across providers'
    case 'features':
      return 'Configure gateway runtime toggles, server flags, edge distribution, and fastpath'
    case 'agents':
      return 'Configure agent capabilities and memory backend'
    case 'functions':
      return 'Configure container-based isolated function execution and warm pooling'
    case 'mcp_gateway':
      return 'Configure Model Context Protocol gateway support'
    case 'sandbox':
      return 'Configure agent sandbox backends, resource limits, and port exposure'
    case 'cache':
      return 'Configure response caching to reduce API costs'
    case 'fastpath':
      return 'Configure low-latency request path with auth caching, exact/semantic response caching, and connection pooling'
    case 'telemetry':
      return 'Forwarding traces and logs to your own collector — coming soon'
    case 'cors':
      return 'Configure Cross-Origin Resource Sharing policies'
    default:
      return ''
  }
}

function GatewayConfigPage() {
  const { tab } = Route.useSearch()
  const navigate = Route.useNavigate()
  const sectionStates = useSectionStates()
  const { data: authMode } = useAuthMode()
  const isSelfHosted = authMode?.mode === 'self_hosted'
  const hiddenSections = isSelfHosted ? HIDDEN_SECTIONS_BASE : HIDDEN_SECTIONS_CLOUD

  // Bookmarked/external links to a now-hidden tab (e.g. ?tab=sandbox in
  // cloud) would render a blank pane because TabsContent filters out the
  // section. Redirect once on mount to the default tab.
  useEffect(() => {
    if (hiddenSections.has(tab)) {
      navigate({
        search: (prev) => ({ ...prev, tab: 'rate_limit' as ConfigSectionName }),
        replace: true,
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, hiddenSections])

  return (
    <div className="flex flex-col w-full px-6 py-6 max-w-6xl mx-auto">
      {isSelfHosted && (
        <Alert className="mb-6 bg-brand-secondary-700/20 border-brand-secondary-600/40 text-brand-secondary-300">
          <Iconify.Icon
            icon="mdi:information"
            className="h-4 w-4 text-brand-secondary-300"
          />
          <AlertDescription className="text-brand-secondary-300">
            <strong className="font-semibold">Important:</strong> In self-hosted
            deployments, CLI YAML remains the primary source of truth. Changes
            made here will be overridden by CLI configuration on restart.
          </AlertDescription>
        </Alert>
      )}
      <Tabs
        value={tab}
        onValueChange={(value) =>
          navigate({
            search: (prev) => ({ ...prev, tab: value as ConfigSectionName }),
            replace: true,
          })
        }
        className="!flex-row items-start gap-6 w-full"
      >
        <TabsList className="!inline-flex flex-col w-44 shrink-0 bg-brand-main-800/50 border border-brand-main-600 rounded p-1 !h-fit gap-0.5 sticky top-6">
          {(Object.keys(CONFIG_SECTION_LABELS) as ConfigSectionName[])
            .filter((s) => !hiddenSections.has(s))
            .map(
            (section) => (
              <TabsTrigger
                key={section}
                value={section}
                className="relative flex items-center gap-2 w-full justify-start data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1.5 px-2.5 text-sm"
              >
                <Iconify.Icon
                  icon={SECTION_ICONS[section]}
                  className="h-4 w-4 shrink-0"
                />
                {CONFIG_SECTION_LABELS[section]}
                {sectionStates[section].hasChanges && (
                  <Iconify.Icon
                    icon="ph:dot-outline"
                    className="absolute top-0 right-0 h-5 w-5 text-brand-secondary-400"
                  />
                )}
              </TabsTrigger>
            ),
          )}
        </TabsList>

        <div className="flex-1 min-w-0">
          {(Object.keys(CONFIG_SECTION_LABELS) as ConfigSectionName[])
            .filter((s) => !hiddenSections.has(s))
            .map(
            (section) => (
              <TabsContent
                key={section}
                value={section}
                forceMount
                className="mt-0 data-[state=inactive]:hidden"
              >
                <ConfigSection section={section} />
              </TabsContent>
            ),
          )}
        </div>
      </Tabs>
    </div>
  )
}
