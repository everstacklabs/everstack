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
import { type RateLimitConfig, RATE_LIMIT_KEY_SOURCES } from './types'

interface RateLimitFormProps {
  config: RateLimitConfig
  onChange: (config: RateLimitConfig) => void
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

export function RateLimitForm({ config, onChange }: RateLimitFormProps) {
  const updateConfig = (updates: Partial<RateLimitConfig>) => {
    onChange({ ...config, ...updates })
  }

  return (
    <div>
      {/* Enable Toggle */}
      <div className="flex items-center justify-between py-2.5">
        <div>
          <Label className="text-white light:text-brand-main-50 text-sm">Enable Rate Limiting</Label>
          <p className="text-xs text-brand-main-200 mt-0.5">
            Limit the number of requests per minute
          </p>
        </div>
        <Switch
          checked={config.enabled}
          onCheckedChange={(enabled) => updateConfig({ enabled })}
        />
      </div>

      {config.enabled && (
        <div className="grid grid-cols-3 gap-4">
          <Field label="Requests Per Minute" hint="Maximum requests allowed per minute">
            <Input
              type="number"
              min={1}
              value={config.requestsPerMinute}
              onChange={(e) =>
                updateConfig({ requestsPerMinute: parseInt(e.target.value) || 0 })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
            />
          </Field>
          <Field label="Burst Size" hint="Maximum burst above the rate limit">
            <Input
              type="number"
              min={1}
              value={config.burst}
              onChange={(e) =>
                updateConfig({ burst: parseInt(e.target.value) || 0 })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
            />
          </Field>
          <Field label="Rate Limit Key" hint="How to identify unique clients">
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
      )}
    </div>
  )
}
