import { Icon } from '@iconify/react'
import type { TelemetryConfig } from './types'

interface TelemetryFormProps {
  config: TelemetryConfig
  onChange: (config: TelemetryConfig) => void
}

// External telemetry export is on the roadmap. The form was misleading
// because the toggle wrote to runtime_config but nothing in the gateway
// read it back, so users could "enable" exports that never happened.
// See docs/audits/telemetry-export.md for the full scope. Until that
// ships, surface the truth instead of a half-wired form.
export function TelemetryForm(_: TelemetryFormProps) {
  return (
    <div className="rounded-md border border-brand-main-600/40 bg-brand-main-700/30 p-6">
      <div className="flex items-start gap-3">
        <Icon
          icon="mdi:clock-outline"
          className="text-brand-main-200 mt-0.5 shrink-0"
          width={20}
          height={20}
        />
        <div className="flex flex-col gap-1.5">
          <h3 className="text-white light:text-brand-main-50 text-sm font-medium">
            Telemetry export — coming soon
          </h3>
          <p className="text-xs text-brand-main-200 leading-relaxed">
            Your traces and logs are already captured by Everstack and shown
            in the Traces and Logs dashboards.
          </p>
          <p className="text-xs text-brand-main-200 leading-relaxed">
            Forwarding to your own OpenTelemetry collector (Datadog, Honeycomb,
            Grafana Cloud, etc.) is on the roadmap. Until then this section is
            read-only.
          </p>
        </div>
      </div>
    </div>
  )
}
