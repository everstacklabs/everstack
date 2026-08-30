import {
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
} from '@everstack/ui/components'
import { type CacheConfig, CACHE_TYPES } from './types'

interface CacheFormProps {
  config: CacheConfig
  onChange: (config: CacheConfig) => void
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

export function CacheForm({ config, onChange }: CacheFormProps) {
  const updateConfig = (updates: Partial<CacheConfig>) => {
    onChange({ ...config, ...updates })
  }

  return (
    <div>
      {/* Enable Toggle */}
      <div className="flex items-center justify-between py-2.5">
        <div>
          <Label className="text-white light:text-brand-main-50 text-sm">Enable Caching</Label>
          <p className="text-xs text-brand-main-200 mt-0.5">
            Cache responses to reduce API costs
          </p>
        </div>
        <Switch
          checked={config.enabled}
          onCheckedChange={(enabled) => updateConfig({ enabled })}
        />
      </div>

      {config.enabled && (
        <>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Cache Type">
              <Select
                value={config.type}
                onValueChange={(value) => updateConfig({ type: value })}
              >
                <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                  <SelectValue placeholder="Select cache type" />
                </SelectTrigger>
                <SelectContent>
                  {CACHE_TYPES.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="TTL" hint='Cache duration (e.g. "10m", "1h", "24h")'>
              <Input
                value={config.ttl}
                onChange={(e) => updateConfig({ ttl: e.target.value })}
                placeholder="10m"
                className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
              />
            </Field>
          </div>

          {/* Memory-specific */}
          {config.type === 'memory' && (
            <Field label="Max Cache Size" hint="Maximum number of items to cache in memory">
              <Input
                type="number"
                min={1000}
                value={config.memoryMaxSize ?? 50000}
                onChange={(e) =>
                  updateConfig({ memoryMaxSize: parseInt(e.target.value) || 50000 })
                }
                className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
              />
            </Field>
          )}

          {/* Redis-specific */}
          {config.type === 'redis' && (
            <div className="grid grid-cols-3 gap-4">
              <Field label="Redis Address" hint="Redis server host:port">
                <Input
                  value={config.redisAddress ?? ''}
                  onChange={(e) => updateConfig({ redisAddress: e.target.value })}
                  placeholder="localhost:6379"
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
              <Field label="Database" hint="Redis database number (0-15)">
                <Input
                  type="number"
                  min={0}
                  max={15}
                  value={config.redisDb ?? 0}
                  onChange={(e) =>
                    updateConfig({ redisDb: parseInt(e.target.value) || 0 })
                  }
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
              <Field label="Pool Size" hint="Redis connection pool size">
                <Input
                  type="number"
                  min={1}
                  value={config.redisPoolSize ?? 100}
                  onChange={(e) =>
                    updateConfig({ redisPoolSize: parseInt(e.target.value) || 100 })
                  }
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
            </div>
          )}
        </>
      )}
    </div>
  )
}
