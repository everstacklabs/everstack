import {
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Input,
  Button,
} from '@everstack/ui/components'
import { Plus, Trash2 } from 'lucide-react'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import { getProviderAsset, formatProviderName } from '@/components/providers/provider-icon'
import { Iconify } from '@everstack/ui/icons'
import { cn } from '@everstack/utils/functions/cn'
import {
  type LoadBalancerConfig,
  type FallbackConfig,
  type FallbackFactorConfig,
  type FallbackModelConfig,
  LOAD_BALANCER_STRATEGIES,
  RATE_LIMIT_KEY_SOURCES,
} from './types'

interface LoadBalancerFormProps {
  config: LoadBalancerConfig
  onChange: (config: LoadBalancerConfig) => void
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

function ProviderLogo({ provider, className }: { provider: string; className?: string }) {
  const asset = getProviderAsset(provider)
  if (asset.type === 'image') {
    return (
      <img
        src={asset.value}
        alt={provider}
        className={cn('object-contain', asset.light && 'brightness-0 invert', className)}
      />
    )
  }
  return <Iconify.Icon icon={asset.value} className={className} />
}

function ProviderLabel({ provider }: { provider: string }) {
  return (
    <span className="flex items-center gap-2">
      <ProviderLogo provider={provider} className="size-4 shrink-0" />
      {formatProviderName(provider)}
    </span>
  )
}

const FALLBACK_STRATEGIES = [
  { value: 'priority', label: 'Priority (sequential)' },
  { value: 'round_robin', label: 'Round Robin' },
  { value: 'parallel', label: 'Parallel (race)' },
] as const

export function LoadBalancerForm({ config, onChange }: LoadBalancerFormProps) {
  const { data: gatewayModels = [] } = useGatewayModels()

  const updateConfig = (updates: Partial<LoadBalancerConfig>) => {
    onChange({ ...config, ...updates })
  }

  const updateFallback = (updates: Partial<FallbackConfig>) => {
    updateConfig({
      fallback: { ...(config.fallback ?? { enabled: false }), ...updates },
    })
  }

  const fallback = config.fallback ?? { enabled: false }

  const updateFactor = (index: number, updates: Partial<FallbackFactorConfig>) => {
    const factors = [...(fallback.factors ?? [])]
    factors[index] = { ...factors[index], ...updates }
    updateFallback({ factors })
  }

  const addFactor = () => {
    const factors = [...(fallback.factors ?? [])]
    factors.push({
      name: `Factor ${factors.length + 1}`,
      strategy: 'priority',
      models: [],
      timeoutMs: 30000,
      backoffMs: 1000,
      maxAttempts: 3,
    })
    updateFallback({ factors })
  }

  const removeFactor = (index: number) => {
    const factors = [...(fallback.factors ?? [])]
    factors.splice(index, 1)
    updateFallback({ factors })
  }

  const addModelToFactor = (factorIndex: number) => {
    const factors = [...(fallback.factors ?? [])]
    const models = [...(factors[factorIndex].models ?? [])]
    models.push({ provider: '', model: '' })
    factors[factorIndex] = { ...factors[factorIndex], models }
    updateFallback({ factors })
  }

  const updateModelInFactor = (factorIndex: number, modelIndex: number, updates: Partial<FallbackModelConfig>) => {
    const factors = [...(fallback.factors ?? [])]
    const models = [...(factors[factorIndex].models ?? [])]
    models[modelIndex] = { ...models[modelIndex], ...updates }
    factors[factorIndex] = { ...factors[factorIndex], models }
    updateFallback({ factors })
  }

  const removeModelFromFactor = (factorIndex: number, modelIndex: number) => {
    const factors = [...(fallback.factors ?? [])]
    const models = [...(factors[factorIndex].models ?? [])]
    models.splice(modelIndex, 1)
    factors[factorIndex] = { ...factors[factorIndex], models }
    updateFallback({ factors })
  }

  // Build flat list of provider → models for dropdowns
  const providerNames = gatewayModels.map((g) => g.provider)

  const getModelsForProvider = (provider: string) => {
    return gatewayModels.find((g) => g.provider === provider)?.models ?? []
  }

  return (
    <div>
      {/* Enable Toggle */}
      <div className="flex items-center justify-between py-2.5">
        <div>
          <Label className="text-white light:text-brand-main-50 text-sm">Enable Load Balancer</Label>
          <p className="text-xs text-brand-main-200 mt-0.5">
            Distribute requests across multiple providers
          </p>
        </div>
        <Switch
          checked={config.enabled}
          onCheckedChange={(enabled) => updateConfig({ enabled })}
        />
      </div>

      {config.enabled && (
        <div className="space-y-6">
          <div className="grid grid-cols-2 gap-4">
            <Field label="Strategy" hint="How to distribute requests across providers">
              <Select
                value={config.strategy}
                onValueChange={(value) => updateConfig({ strategy: value })}
              >
                <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                  <SelectValue placeholder="Select strategy" />
                </SelectTrigger>
                <SelectContent>
                  {LOAD_BALANCER_STRATEGIES.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="Session Key" hint="How to maintain session affinity">
              <Select
                value={config.keySource}
                onValueChange={(value) => updateConfig({ keySource: value })}
              >
                <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                  <SelectValue placeholder="Select key source" />
                </SelectTrigger>
                <SelectContent>
                  {RATE_LIMIT_KEY_SOURCES.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>

          {/* Fallback Section */}
          <div className="border-t border-brand-main-600 pt-4">
            <div className="flex items-center justify-between py-2.5">
              <div>
                <Label className="text-white light:text-brand-main-50 text-sm">Enable Fallback</Label>
                <p className="text-xs text-brand-main-200 mt-0.5">
                  Automatically try alternative providers when the primary fails
                </p>
              </div>
              <Switch
                checked={fallback.enabled}
                onCheckedChange={(enabled) => updateFallback({ enabled })}
              />
            </div>

            {fallback.enabled && (
              <div className="space-y-4 mt-2">
                {/* Default fallback model */}
                <div className="rounded-lg border border-brand-main-600 bg-brand-main-800/30 p-4 space-y-3">
                  <Label className="text-white light:text-brand-main-50 text-xs font-medium">Default Fallback Model</Label>
                  <p className="text-[11px] text-brand-main-300">
                    Used when the primary model fails and no factor matches
                  </p>
                  <div className="grid grid-cols-2 gap-3">
                    <Select
                      value={fallback.default?.provider ?? ''}
                      onValueChange={(provider) =>
                        updateFallback({
                          default: { ...(fallback.default ?? { provider: '', model: '' }), provider, model: '' },
                        })
                      }
                    >
                      <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue placeholder="Provider" />
                      </SelectTrigger>
                      <SelectContent>
                        {providerNames.map((p) => (
                          <SelectItem key={p} value={p}><ProviderLabel provider={p} /></SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <Select
                      value={fallback.default?.model ?? ''}
                      onValueChange={(model) =>
                        updateFallback({
                          default: { ...(fallback.default ?? { provider: '', model: '' }), model },
                        })
                      }
                    >
                      <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue placeholder="Model" />
                      </SelectTrigger>
                      <SelectContent>
                        {getModelsForProvider(fallback.default?.provider ?? '').map((m) => (
                          <SelectItem key={m} value={m}>{m}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </div>

                {/* Fallback Factors */}
                <div className="space-y-3">
                  <div className="flex items-center justify-between">
                    <Label className="text-white light:text-brand-main-50 text-xs font-medium">Fallback Factors</Label>
                    <Button
                      variant="ghost"
                      onClick={addFactor}
                      className="text-brand-secondary-400 hover:text-brand-secondary-300"
                    >
                      Add Factor
                    </Button>
                  </div>

                  {(fallback.factors ?? []).map((factor, fi) => (
                    <div
                      key={fi}
                      className="rounded-lg border border-brand-main-600 bg-brand-main-800/30 p-4 space-y-3"
                    >
                      <div className="flex items-center justify-between">
                        <Input
                          value={factor.name}
                          onChange={(e) => updateFactor(fi, { name: e.target.value })}
                          className="bg-transparent border-none text-white light:text-brand-main-50 text-sm font-medium p-0 h-auto focus-visible:ring-0"
                          placeholder="Factor name"
                        />
                        <Button
                          variant="ghost"
                          onClick={() => removeFactor(fi)}
                          className="text-red-400 light:text-red-600 w-7.5 h-7.5 hover:text-red-300 light:hover:text-red-600"
                        >
                          <Trash2 className="size-3.5" />
                        </Button>
                      </div>

                      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
                        <div>
                          <Label className="text-brand-main-300 text-[10px] mb-1 block">Strategy</Label>
                          <Select
                            value={factor.strategy}
                            onValueChange={(v) => updateFactor(fi, { strategy: v })}
                          >
                            <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {FALLBACK_STRATEGIES.map((s) => (
                                <SelectItem key={s.value} value={s.value}>{s.label}</SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </div>
                        <div>
                          <Label className="text-brand-main-300 text-[10px] mb-1 block">Timeout (ms)</Label>
                          <Input
                            type="number"
                            value={factor.timeoutMs ?? 30000}
                            onChange={(e) => updateFactor(fi, { timeoutMs: Number(e.target.value) })}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                          />
                        </div>
                        <div>
                          <Label className="text-brand-main-300 text-[10px] mb-1 block">Backoff (ms)</Label>
                          <Input
                            type="number"
                            value={factor.backoffMs ?? 1000}
                            onChange={(e) => updateFactor(fi, { backoffMs: Number(e.target.value) })}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                          />
                        </div>
                        <div>
                          <Label className="text-brand-main-300 text-[10px] mb-1 block">Max Attempts</Label>
                          <Input
                            type="number"
                            value={factor.maxAttempts ?? 3}
                            onChange={(e) => updateFactor(fi, { maxAttempts: Number(e.target.value) })}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                          />
                        </div>
                      </div>

                      {/* Models in this factor */}
                      <div className="space-y-2">
                        <div className="flex items-center justify-between">
                          <Label className="text-brand-main-300 text-[10px]">Fallback Models</Label>
                          <Button
                            variant="ghost"
                            onClick={() => addModelToFactor(fi)}
                            className="text-brand-secondary-400 hover:text-brand-secondary-300"
                          >
                            <Plus className="size-2.5" />
                            Add
                          </Button>
                        </div>
                        {factor.models.map((model, mi) => (
                          <div key={mi} className="flex items-center gap-2">
                            <Select
                              value={model.provider}
                              onValueChange={(provider) =>
                                updateModelInFactor(fi, mi, { provider, model: '' })
                              }
                            >
                              <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                                <SelectValue placeholder="Provider" />
                              </SelectTrigger>
                              <SelectContent>
                                {providerNames.map((p) => (
                                  <SelectItem key={p} value={p}><ProviderLabel provider={p} /></SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                            <Select
                              value={model.model}
                              onValueChange={(m) => updateModelInFactor(fi, mi, { model: m })}
                            >
                              <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                                <SelectValue placeholder="Model" />
                              </SelectTrigger>
                              <SelectContent>
                                {getModelsForProvider(model.provider).map((m) => (
                                  <SelectItem key={m} value={m}>{m}</SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                            <Button
                              variant="ghost"
                              onClick={() => removeModelFromFactor(fi, mi)}
                              className="text-red-400 light:text-red-600 w-7.5 h-7.5 hover:text-red-300 light:hover:text-red-600"
                            >
                              <Trash2 className="size-3.5" />
                            </Button>
                          </div>
                        ))}
                        {factor.models.length === 0 && (
                          <p className="text-[10px] text-brand-main-300/50 py-1">
                            No fallback models added yet
                          </p>
                        )}
                      </div>
                    </div>
                  ))}

                  {(fallback.factors ?? []).length === 0 && (
                    <p className="text-xs text-brand-main-300/50 py-2 text-center">
                      No fallback factors configured. Add one to define fallback behavior.
                    </p>
                  )}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
