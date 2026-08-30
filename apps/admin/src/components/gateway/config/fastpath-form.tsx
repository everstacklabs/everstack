import { useState } from 'react'
import {
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
import { DeploymentTimeNote } from './deployment-time-note'
import { type FeaturesConfig } from './types'

interface FastpathFormProps {
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
      className="flex items-center justify-between w-full py-2.5 group"
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

function SwitchField({
  label,
  hint,
  checked,
  onCheckedChange,
}: {
  label: string
  hint?: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex flex-col">
      <Label className="text-brand-main-300 text-xs mb-1.5">{label}</Label>
      <div className="flex items-center justify-between border border-brand-main-600/50 rounded-md px-3 h-9">
        {hint && <p className="text-[11px] text-brand-main-300">{hint}</p>}
        {!hint && <span />}
        <Switch checked={checked} onCheckedChange={onCheckedChange} />
      </div>
      <div className="h-[18px]" />
    </div>
  )
}

export function FastpathForm({ config, onChange }: FastpathFormProps) {
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({
    auth: true,
    exactCache: false,
    semanticCache: false,
    streaming: false,
    connectionPool: false,
  })

  const toggle = (key: string) =>
    setOpenSections((prev) => ({ ...prev, [key]: !prev[key] }))

  const updateFastpath = (
    updates: Partial<NonNullable<FeaturesConfig['fastpath']>>,
  ) => {
    onChange({ ...config, fastpath: { ...config.fastpath, ...updates } })
  }

  const updateFastpathNested = (
    key: 'auth' | 'streaming' | 'connectionPool',
    updates: Record<string, unknown>,
  ) => {
    const current =
      (config.fastpath?.[key] as Record<string, unknown> | undefined) ?? {}
    updateFastpath({ [key]: { ...current, ...updates } } as Partial<
      NonNullable<FeaturesConfig['fastpath']>
    >)
  }

  const updateFastpathCache = (
    key: 'exact' | 'semantic',
    updates: Record<string, unknown>,
  ) => {
    const cache = config.fastpath?.cache ?? {}
    const current = (cache[key] as Record<string, unknown> | undefined) ?? {}
    updateFastpath({
      cache: { ...cache, [key]: { ...current, ...updates } },
    })
  }

  const parseInteger = (value: string, fallback: number) => {
    const n = Number.parseInt(value, 10)
    return Number.isFinite(n) ? n : fallback
  }

  const parseFloatValue = (value: string, fallback: number) => {
    const n = Number.parseFloat(value)
    return Number.isFinite(n) ? n : fallback
  }

  return (
    <div>
      <DeploymentTimeNote>
        FastPath tuning (bloom-filter sizes, cache max entries, TTLs,
        connection-pool sizing) is set at deployment time — these are
        global engine knobs and can&rsquo;t safely vary per tenant on a
        shared gateway. The Enable toggle still takes effect per tenant.
      </DeploymentTimeNote>
      {/* Enable toggle */}
      <div className="flex items-center justify-between py-2.5">
        <div>
          <Label className="text-sm text-white light:text-brand-main-50">Enable FastPath</Label>
          <p className="text-xs text-brand-main-200 mt-0.5">
            Low-latency optimized request path
          </p>
        </div>
        <Switch
          checked={config.fastpath?.enabled ?? false}
          onCheckedChange={(checked) => updateFastpath({ enabled: checked })}
        />
      </div>

      {/* ── Auth ── */}
      <Collapsible open={openSections.auth} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="Auth"
            description="Bloom filter and cache settings for fast authentication"
            open={!!openSections.auth}
            onToggle={() => toggle('auth')}
          />
          <CollapsibleContent>
            <div className="pb-4 space-y-4">
              <div className="grid grid-cols-3 gap-4">
                <Field label="Bloom Filter Size" hint="Number of entries in the bloom filter">
                  <Input
                    type="number"
                    value={config.fastpath?.auth?.bloomFilterSize ?? 100000}
                    onChange={(e) =>
                      updateFastpathNested('auth', {
                        bloomFilterSize: parseInteger(e.target.value, 100000),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="False Positive Rate" hint="Acceptable bloom filter FP rate">
                  <Input
                    type="number"
                    step="0.0001"
                    value={config.fastpath?.auth?.bloomFalsePositiveRate ?? 0.001}
                    onChange={(e) =>
                      updateFastpathNested('auth', {
                        bloomFalsePositiveRate: parseFloatValue(e.target.value, 0.001),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Cache TTL" hint="Auth cache time-to-live (e.g. 60s)">
                  <Input
                    value={config.fastpath?.auth?.cacheTtl ?? '60s'}
                    onChange={(e) =>
                      updateFastpathNested('auth', { cacheTtl: e.target.value })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              </div>
            </div>
          </CollapsibleContent>
        </div>
      </Collapsible>

      {/* ── Exact Cache ── */}
      <Collapsible open={openSections.exactCache} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="Exact Cache"
            description="Cache responses by exact request match"
            open={!!openSections.exactCache}
            onToggle={() => toggle('exactCache')}
          />
          <CollapsibleContent>
            <div className="pb-4 space-y-4">
              <div className="grid grid-cols-3 gap-4">
                <SwitchField
                  label="Enabled"
                  hint="Enable exact cache matching"
                  checked={config.fastpath?.cache?.exact?.enabled ?? true}
                  onCheckedChange={(checked) =>
                    updateFastpathCache('exact', { enabled: checked })
                  }
                />
                <Field label="Max Entries" hint="Maximum cached items">
                  <Input
                    type="number"
                    value={config.fastpath?.cache?.exact?.maxEntries ?? 50000}
                    onChange={(e) =>
                      updateFastpathCache('exact', {
                        maxEntries: parseInteger(e.target.value, 50000),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="TTL" hint="Cache time-to-live (e.g. 5m)">
                  <Input
                    value={config.fastpath?.cache?.exact?.ttl ?? '5m'}
                    onChange={(e) =>
                      updateFastpathCache('exact', { ttl: e.target.value })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              </div>
            </div>
          </CollapsibleContent>
        </div>
      </Collapsible>

      {/* ── Semantic Cache ── */}
      <Collapsible open={openSections.semanticCache} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="Semantic Cache"
            description="Cache responses by semantic similarity"
            open={!!openSections.semanticCache}
            onToggle={() => toggle('semanticCache')}
          />
          <CollapsibleContent>
            <div className="pb-4 space-y-4">
              <div className="grid grid-cols-3 gap-4">
                <SwitchField
                  label="Enabled"
                  hint="Enable semantic cache matching"
                  checked={config.fastpath?.cache?.semantic?.enabled ?? false}
                  onCheckedChange={(checked) =>
                    updateFastpathCache('semantic', { enabled: checked })
                  }
                />
                <Field label="Max Entries" hint="Maximum cached items">
                  <Input
                    type="number"
                    value={config.fastpath?.cache?.semantic?.maxEntries ?? 50000}
                    onChange={(e) =>
                      updateFastpathCache('semantic', {
                        maxEntries: parseInteger(e.target.value, 50000),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="TTL" hint="Cache time-to-live (e.g. 5m)">
                  <Input
                    value={config.fastpath?.cache?.semantic?.ttl ?? '5m'}
                    onChange={(e) =>
                      updateFastpathCache('semantic', { ttl: e.target.value })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              </div>
              <div className="grid grid-cols-4 gap-4">
                <Field label="Similarity Threshold" hint="0.0 to 1.0">
                  <Input
                    type="number"
                    step="0.01"
                    value={config.fastpath?.cache?.semantic?.similarityThreshold ?? 0.35}
                    onChange={(e) =>
                      updateFastpathCache('semantic', {
                        similarityThreshold: parseFloatValue(e.target.value, 0.35),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Algorithm" hint="Hashing algorithm">
                  <Select
                    value={config.fastpath?.cache?.semantic?.algorithm ?? 'minhash'}
                    onValueChange={(value) =>
                      updateFastpathCache('semantic', { algorithm: value })
                    }
                  >
                    <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="minhash">minhash</SelectItem>
                      <SelectItem value="onnx">onnx</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>
                <Field label="Num Hashes" hint="MinHash permutation count">
                  <Input
                    type="number"
                    value={config.fastpath?.cache?.semantic?.numHashes ?? 128}
                    onChange={(e) =>
                      updateFastpathCache('semantic', {
                        numHashes: parseInteger(e.target.value, 128),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Shingle Size" hint="Token window size">
                  <Input
                    type="number"
                    value={config.fastpath?.cache?.semantic?.shingleSize ?? 2}
                    onChange={(e) =>
                      updateFastpathCache('semantic', {
                        shingleSize: parseInteger(e.target.value, 2),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              </div>
            </div>
          </CollapsibleContent>
        </div>
      </Collapsible>

      {/* ── Streaming ── */}
      <Collapsible open={openSections.streaming} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="Streaming"
            description="Buffer and pool sizes for streaming responses"
            open={!!openSections.streaming}
            onToggle={() => toggle('streaming')}
          />
          <CollapsibleContent>
            <div className="pb-4 space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <Field label="Buffer Size" hint="Bytes per stream buffer">
                  <Input
                    type="number"
                    value={config.fastpath?.streaming?.bufferSize ?? 32768}
                    onChange={(e) =>
                      updateFastpathNested('streaming', {
                        bufferSize: parseInteger(e.target.value, 32768),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Pool Size" hint="Number of pooled buffers">
                  <Input
                    type="number"
                    value={config.fastpath?.streaming?.poolSize ?? 1024}
                    onChange={(e) =>
                      updateFastpathNested('streaming', {
                        poolSize: parseInteger(e.target.value, 1024),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              </div>
            </div>
          </CollapsibleContent>
        </div>
      </Collapsible>

      {/* ── Connection Pool ── */}
      <Collapsible open={openSections.connectionPool} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="Connection Pool"
            description="Pre-warmed connection pool for upstream providers"
            open={!!openSections.connectionPool}
            onToggle={() => toggle('connectionPool')}
          />
          <CollapsibleContent>
            <div className="pb-4 space-y-4">
              <div className="grid grid-cols-3 gap-4">
                <Field label="Max Idle Per Host" hint="Idle connections per upstream">
                  <Input
                    type="number"
                    value={config.fastpath?.connectionPool?.maxIdlePerHost ?? 256}
                    onChange={(e) =>
                      updateFastpathNested('connectionPool', {
                        maxIdlePerHost: parseInteger(e.target.value, 256),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Prewarm Connections" hint="Connections to open on start">
                  <Input
                    type="number"
                    value={config.fastpath?.connectionPool?.prewarmConnections ?? 10}
                    onChange={(e) =>
                      updateFastpathNested('connectionPool', {
                        prewarmConnections: parseInteger(e.target.value, 10),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <SwitchField
                  label="Prewarm On Startup"
                  hint="Open connections at boot"
                  checked={config.fastpath?.connectionPool?.prewarmOnStartup ?? true}
                  onCheckedChange={(checked) =>
                    updateFastpathNested('connectionPool', {
                      prewarmOnStartup: checked,
                    })
                  }
                />
              </div>
            </div>
          </CollapsibleContent>
        </div>
      </Collapsible>
    </div>
  )
}
