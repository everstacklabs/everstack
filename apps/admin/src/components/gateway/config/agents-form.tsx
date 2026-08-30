import { useState, useMemo } from 'react'
import {
  Button,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { Link } from '@tanstack/react-router'
import { useConfiguredProviders } from '@/hooks/vault/use-providers'
import { DeploymentTimeNote } from './deployment-time-note'
import { type FeaturesConfig, EMBEDDING_MODELS } from './types'

interface AgentsFormProps {
  config: FeaturesConfig
  onChange: (config: FeaturesConfig) => void
}

function SectionHeader({
  title,
  description,
  open,
  onToggle,
}: {
  title: string
  description: string
  open: boolean
  onToggle: () => void
}) {
  return (
    <CollapsibleTrigger
      onClick={onToggle}
      className="flex items-center justify-between w-full group py-2.5"
    >
      <div className="text-left">
        <h4 className="text-sm font-semibold text-white light:text-brand-main-50">{title}</h4>
        <p className="text-xs text-brand-main-200 mt-0.5">{description}</p>
      </div>
      <Iconify.Icon
        icon="mdi:chevron-down"
        className={`h-5 w-5 text-brand-main-300 transition-transform duration-200 ${open ? 'rotate-180' : ''}`}
      />
    </CollapsibleTrigger>
  )
}

function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col">
      <Label className="text-brand-main-300 text-xs mb-1.5">{label}</Label>
      {children}
      {hint ? (
        <p className="text-[11px] text-brand-main-300 mt-1.5">{hint}</p>
      ) : (
        <div className="h-[18px]" />
      )}
    </div>
  )
}

export function AgentsForm({ config, onChange }: AgentsFormProps) {
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({
    memory: true,
  })

  const toggle = (key: string) =>
    setOpenSections((prev) => ({ ...prev, [key]: !prev[key] }))

  const updateConfig = (updates: Partial<FeaturesConfig>) => {
    onChange({ ...config, ...updates })
  }

  const updateMemory = (updates: NonNullable<FeaturesConfig['memory']>) => {
    updateConfig({ memory: updates })
  }

  const updateMemoryNested = (
    key: 'qdrant' | 'pinecone' | 'weaviate',
    updates: Record<string, unknown>,
  ) => {
    const current =
      (config.memory?.[key] as Record<string, unknown> | undefined) ?? {}
    updateMemory({
      ...(config.memory ?? {}),
      [key]: { ...current, ...updates },
    })
  }

  const parseInteger = (value: string, fallback: number) => {
    const n = Number.parseInt(value, 10)
    return Number.isFinite(n) ? n : fallback
  }

  const { data: providersData } = useConfiguredProviders()

  // Filter embedding models to only those enabled in configured providers
  const availableEmbeddingModels = useMemo(() => {
    const providers = providersData?.providers ?? []
    // Collect all enabled model names from active providers that support embeddings
    const enabledModels = new Set<string>()
    for (const provider of providers) {
      if (!provider.isActive) continue
      const hasEmbeddings = provider.catalog?.capabilities?.embeddings
      if (!hasEmbeddings) continue
      const enabled = provider.configuration?.enabledModels ?? []
      for (const model of enabled) {
        enabledModels.add(model)
      }
    }
    // Filter the known embedding models to only those that are enabled
    return EMBEDDING_MODELS.filter((m) => enabledModels.has(m.value))
  }, [providersData])

  return (
    <div>
      <DeploymentTimeNote>
        Enable Agents takes effect per tenant. Memory backend selection
        (pgvector, qdrant, pinecone, weaviate) is deployment-time — the
        gateway pod has one connection per backend type and tenants can&rsquo;t
        run different stacks on a shared instance.
      </DeploymentTimeNote>
      {/* Enable Agents toggle */}
      <div className="flex items-center justify-between py-2.5">
        <div>
          <Label className="text-white light:text-brand-main-50 text-sm">Enable Agents</Label>
          <p className="text-xs text-brand-main-200 mt-0.5">
            Enable AI agent capabilities
          </p>
        </div>
        <Switch
          checked={(config as Record<string, unknown>).enableAgents as boolean ?? false}
          onCheckedChange={(checked) =>
            onChange({ ...config, enableAgents: checked } as FeaturesConfig)
          }
        />
      </div>

      {/* ── Memory ── */}
      <Collapsible open={openSections.memory} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="Memory"
            description="Vector store backend and embedding model for agent memory"
            open={!!openSections.memory}
            onToggle={() => toggle('memory')}
          />
          <CollapsibleContent>
            <div className="pb-4 space-y-4">
              <div className="flex items-center justify-between py-2.5">
                <div>
                  <Label className="text-white light:text-brand-main-50 text-sm">Enable Memory</Label>
                  <p className="text-xs text-brand-main-200 mt-0.5">
                    Controls features.enable_memory
                  </p>
                </div>
                <Switch
                  checked={config.enableMemory ?? false}
                  onCheckedChange={(checked) =>
                    updateConfig({ enableMemory: checked })
                  }
                />
              </div>

              <div className="grid grid-cols-2 gap-4">
                <Field label="Backend" hint="Vector store to use for memory">
                  <Select
                    value={config.memory?.backend ?? 'pgvector'}
                    onValueChange={(value) =>
                      updateMemory({ ...(config.memory ?? {}), backend: value })
                    }
                  >
                    <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="pgvector">pgvector</SelectItem>
                      <SelectItem value="qdrant">qdrant</SelectItem>
                      <SelectItem value="pinecone">pinecone</SelectItem>
                      <SelectItem value="weaviate">weaviate</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>
                <Field label="Embedding Model" hint="Only models enabled in your LLM providers are shown">
                  {availableEmbeddingModels.length > 0 ? (
                    <Select
                      value={config.memory?.embeddingModels?.[0]?.model ?? ''}
                      onValueChange={(value) => {
                        const match = EMBEDDING_MODELS.find((m) => m.value === value)
                        const dimension = match?.dimension ?? 1536
                        const prev = config.memory?.embeddingModels ?? []
                        const nextFirst = { ...(prev[0] ?? {}), model: value, dimension }
                        updateMemory({
                          ...(config.memory ?? {}),
                          embeddingModels: [nextFirst, ...prev.slice(1)],
                        })
                      }}
                    >
                      <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue placeholder="Select embedding model" />
                      </SelectTrigger>
                      <SelectContent>
                        {availableEmbeddingModels.map((model) => (
                          <SelectItem key={model.value} value={model.value}>
                            {model.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <Button
                      variant="outline"
                      className="w-full justify-start text-brand-main-300 bg-brand-main-700/50 border-brand-main-500"
                      asChild
                    >
                      <Link to="/vault/llm-providers">
                        <Iconify.Icon icon="mdi:key-plus" className="h-4 w-4 mr-2" />
                        Configure an LLM provider with embedding models
                      </Link>
                    </Button>
                  )}
                </Field>
              </div>

              {availableEmbeddingModels.length > 0 && (
                <Field label="Embedding Dimension" hint="Auto-filled from model, override if needed">
                  <Input
                    type="number"
                    value={config.memory?.embeddingModels?.[0]?.dimension ?? 1536}
                    onChange={(e) => {
                      const prev = config.memory?.embeddingModels ?? []
                      const nextFirst = {
                        ...(prev[0] ?? {}),
                        dimension: parseInteger(e.target.value, 1536),
                      }
                      updateMemory({
                        ...(config.memory ?? {}),
                        embeddingModels: [nextFirst, ...prev.slice(1)],
                      })
                    }}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              )}

              {config.memory?.backend === 'qdrant' && (
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Qdrant Address">
                    <Input
                      value={config.memory?.qdrant?.address ?? ''}
                      onChange={(e) =>
                        updateMemoryNested('qdrant', { address: e.target.value })
                      }
                      placeholder="localhost:6334"
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Qdrant API Key">
                    <Input
                      value={config.memory?.qdrant?.apiKey ?? ''}
                      onChange={(e) =>
                        updateMemoryNested('qdrant', { apiKey: e.target.value })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
              )}

              {config.memory?.backend === 'pinecone' && (
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Pinecone API Key">
                    <Input
                      value={config.memory?.pinecone?.apiKey ?? ''}
                      onChange={(e) =>
                        updateMemoryNested('pinecone', { apiKey: e.target.value })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Pinecone Cloud">
                    <Input
                      value={config.memory?.pinecone?.cloud ?? 'aws'}
                      onChange={(e) =>
                        updateMemoryNested('pinecone', { cloud: e.target.value })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Pinecone Region">
                    <Input
                      value={config.memory?.pinecone?.region ?? 'us-east-1'}
                      onChange={(e) =>
                        updateMemoryNested('pinecone', { region: e.target.value })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Pinecone Environment">
                    <Input
                      value={config.memory?.pinecone?.environment ?? ''}
                      onChange={(e) =>
                        updateMemoryNested('pinecone', { environment: e.target.value })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
              )}

              {config.memory?.backend === 'weaviate' && (
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Weaviate URL">
                    <Input
                      value={config.memory?.weaviate?.url ?? ''}
                      onChange={(e) =>
                        updateMemoryNested('weaviate', { url: e.target.value })
                      }
                      placeholder="http://localhost:8080"
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Weaviate API Key">
                    <Input
                      value={config.memory?.weaviate?.apiKey ?? ''}
                      onChange={(e) =>
                        updateMemoryNested('weaviate', { apiKey: e.target.value })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
              )}
            </div>
          </CollapsibleContent>
        </div>
      </Collapsible>
    </div>
  )
}
