import {
  Label,
  Switch,
} from '@everstack/ui/components'
import { type FeaturesConfig, FEATURES_FIELD_METADATA } from './types'

interface FeaturesFormProps {
  config: FeaturesConfig
  onChange: (config: FeaturesConfig) => void
}

const FEATURE_KEYS = Object.keys(FEATURES_FIELD_METADATA) as Array<
  keyof typeof FEATURES_FIELD_METADATA
>

const SERVER_FLAGS: {
  key: keyof NonNullable<FeaturesConfig['server']>
  label: string
  description: string
}[] = [
  {
    key: 'enableNewLoadBalancer',
    label: 'New Load Balancer',
    description: 'Use the next-generation load balancer implementation',
  },
  {
    key: 'enableExperimentalApiV2',
    label: 'Experimental API v2',
    description: 'Enable the v2 API surface (experimental)',
  },
  {
    key: 'enableDebugEndpoints',
    label: 'Debug Endpoints',
    description: 'Expose /debug/* endpoints for diagnostics',
  },
  {
    key: 'enableProfiling',
    label: 'Profiling',
    description: 'Enable pprof profiling endpoints',
  },
]

export function FeaturesForm({ config, onChange }: FeaturesFormProps) {
  const updateBoolean = (key: string, value: boolean) => {
    onChange({ ...config, [key]: value })
  }

  const updateServer = (
    updates: Partial<NonNullable<FeaturesConfig['server']>>,
  ) => {
    onChange({ ...config, server: { ...config.server, ...updates } })
  }

  return (
    <div>
      {/* Gateway Runtime Toggles */}
      <div className="space-y-0">
        <h4 className="text-xs font-medium text-brand-main-300 uppercase tracking-wider mb-2">
          Gateway Runtime Toggles
        </h4>
        {FEATURE_KEYS.map((key) => {
          const metadata = FEATURES_FIELD_METADATA[key]
          const value = config[key as keyof FeaturesConfig] ?? false

          return (
            <div
              key={key}
              className="flex items-center justify-between py-2.5 border-b border-brand-main-600/30 last:border-0"
            >
              <div>
                <Label className="text-white light:text-brand-main-50 text-sm">{metadata.label}</Label>
                <p className="text-xs text-brand-main-200 mt-0.5">
                  {metadata.description}
                </p>
              </div>
              <Switch
                checked={value as boolean}
                onCheckedChange={(checked) => updateBoolean(key, checked)}
              />
            </div>
          )
        })}
      </div>

      {/* Server Feature Flags */}
      <div className="border-t border-brand-main-600/30 pt-4 mt-2 space-y-0">
        <h4 className="text-xs font-medium text-brand-main-300 uppercase tracking-wider mb-2">
          Server Feature Flags
        </h4>
        {SERVER_FLAGS.map(({ key, label, description }) => (
          <div
            key={key}
            className="flex items-center justify-between py-2.5 border-b border-brand-main-600/30 last:border-0"
          >
            <div>
              <Label className="text-white light:text-brand-main-50 text-sm">{label}</Label>
              <p className="text-xs text-brand-main-200 mt-0.5">
                {description}
              </p>
            </div>
            <Switch
              checked={config.server?.[key] ?? false}
              onCheckedChange={(checked) => updateServer({ [key]: checked })}
            />
          </div>
        ))}
      </div>
    </div>
  )
}
