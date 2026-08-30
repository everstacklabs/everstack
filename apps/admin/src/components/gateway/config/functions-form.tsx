import { useState } from 'react'
import {
  Input,
  Label,
  Switch,
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { DeploymentTimeNote } from './deployment-time-note'
import { type FeaturesConfig } from './types'

interface FunctionsFormProps {
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

export function FunctionsForm({ config, onChange }: FunctionsFormProps) {
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({
    general: true,
    pool: false,
  })

  const toggle = (key: string) =>
    setOpenSections((prev) => ({ ...prev, [key]: !prev[key] }))

  const updateIsolatedFunctions = (
    updates: Partial<NonNullable<FeaturesConfig['isolatedFunctions']>>,
  ) => {
    onChange({
      ...config,
      isolatedFunctions: { ...config.isolatedFunctions, ...updates },
    })
  }

  const updateIsolatedPool = (updates: Record<string, unknown>) => {
    const pool = config.isolatedFunctions?.pool ?? {}
    updateIsolatedFunctions({ pool: { ...pool, ...updates } })
  }

  const parseInteger = (value: string, fallback: number) => {
    const n = Number.parseInt(value, 10)
    return Number.isFinite(n) ? n : fallback
  }

  const parseCsv = (value: string) => {
    return value
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
  }

  return (
    <div>
      <DeploymentTimeNote>
        Default timeout and memory take effect per tenant. Container image
        prefix, Docker host, and pool sizing are set at deployment time —
        these can&rsquo;t safely vary per tenant on a shared gateway.
      </DeploymentTimeNote>
      {/* ── General ── */}
      <Collapsible open={openSections.general} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="General"
            description="Container image, timeout, and Docker host settings"
            open={!!openSections.general}
            onToggle={() => toggle('general')}
          />
          <CollapsibleContent>
            <div className="pb-4 space-y-4">
              <div className="grid grid-cols-3 gap-4">
                <Field label="Image Prefix" hint="Container registry prefix">
                  <Input
                    value={config.isolatedFunctions?.imagePrefix ?? ''}
                    onChange={(e) =>
                      updateIsolatedFunctions({ imagePrefix: e.target.value })
                    }
                    placeholder="ghcr.io/everstacklabs/runtime"
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Default Timeout" hint="Milliseconds">
                  <Input
                    type="number"
                    value={config.isolatedFunctions?.defaultTimeoutMs ?? 30000}
                    onChange={(e) =>
                      updateIsolatedFunctions({
                        defaultTimeoutMs: parseInteger(e.target.value, 30000),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Default Memory" hint="Megabytes">
                  <Input
                    type="number"
                    value={config.isolatedFunctions?.defaultMemoryMb ?? 512}
                    onChange={(e) =>
                      updateIsolatedFunctions({
                        defaultMemoryMb: parseInteger(e.target.value, 512),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              </div>

              <div className="grid grid-cols-2 gap-4">
                <Field label="Docker Host" hint="Docker daemon socket or TCP address">
                  <Input
                    value={config.isolatedFunctions?.dockerHost ?? ''}
                    onChange={(e) =>
                      updateIsolatedFunctions({ dockerHost: e.target.value })
                    }
                    placeholder="unix:///var/run/docker.sock"
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <SwitchField
                  label="Docker Auto Detect"
                  hint="Try common Docker socket paths"
                  checked={config.isolatedFunctions?.dockerAutoDetect ?? true}
                  onCheckedChange={(checked) =>
                    updateIsolatedFunctions({ dockerAutoDetect: checked })
                  }
                />
              </div>

              <Field label="Docker Fallback Hosts" hint="Comma-separated list of fallback Docker hosts">
                <Input
                  value={(config.isolatedFunctions?.dockerFallbackHosts ?? []).join(', ')}
                  onChange={(e) =>
                    updateIsolatedFunctions({
                      dockerFallbackHosts: parseCsv(e.target.value),
                    })
                  }
                  placeholder="tcp://host.docker.internal:2375, unix:///var/run/docker.sock"
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
            </div>
          </CollapsibleContent>
        </div>
      </Collapsible>

      {/* ── Warm Pool ── */}
      <Collapsible open={openSections.pool} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="Warm Pool"
            description="Pre-created container pool for faster cold starts"
            open={!!openSections.pool}
            onToggle={() => toggle('pool')}
          />
          <CollapsibleContent>
            <div className="pb-4 space-y-4">
              <div className="grid grid-cols-3 gap-4">
                <SwitchField
                  label="Pool Enabled"
                  hint="Reuse warm containers"
                  checked={config.isolatedFunctions?.pool?.enabled ?? false}
                  onCheckedChange={(checked) =>
                    updateIsolatedPool({ enabled: checked })
                  }
                />
                <Field label="Min / Runtime">
                  <Input
                    type="number"
                    value={config.isolatedFunctions?.pool?.minPerRuntime ?? 1}
                    onChange={(e) =>
                      updateIsolatedPool({
                        minPerRuntime: parseInteger(e.target.value, 1),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Max / Runtime">
                  <Input
                    type="number"
                    value={config.isolatedFunctions?.pool?.maxPerRuntime ?? 10}
                    onChange={(e) =>
                      updateIsolatedPool({
                        maxPerRuntime: parseInteger(e.target.value, 10),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              </div>
              <div className="grid grid-cols-3 gap-4">
                <Field label="Idle Timeout" hint="Seconds">
                  <Input
                    type="number"
                    value={config.isolatedFunctions?.pool?.idleTimeoutSeconds ?? 300}
                    onChange={(e) =>
                      updateIsolatedPool({
                        idleTimeoutSeconds: parseInteger(e.target.value, 300),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <Field label="Max Uses">
                  <Input
                    type="number"
                    value={config.isolatedFunctions?.pool?.maxUses ?? 100}
                    onChange={(e) =>
                      updateIsolatedPool({
                        maxUses: parseInteger(e.target.value, 100),
                      })
                    }
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <SwitchField
                  label="Warmup On Start"
                  hint="Pre-create warm containers"
                  checked={config.isolatedFunctions?.pool?.warmupOnStart ?? false}
                  onCheckedChange={(checked) =>
                    updateIsolatedPool({ warmupOnStart: checked })
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
