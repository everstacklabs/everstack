import { Label, Switch } from '@everstack/ui/components'
import { type FeaturesConfig } from './types'

interface McpGatewayFormProps {
  config: FeaturesConfig
  onChange: (config: FeaturesConfig) => void
}

export function McpGatewayForm({ config, onChange }: McpGatewayFormProps) {
  return (
    <div>
      <div className="flex items-center justify-between py-2.5">
        <div>
          <Label className="text-white light:text-brand-main-50 text-sm">Enable MCP Gateway</Label>
          <p className="text-xs text-brand-main-200 mt-0.5">
            Enable Model Context Protocol gateway support
          </p>
        </div>
        <Switch
          checked={config.mcpGateway?.enabled ?? false}
          onCheckedChange={(checked) =>
            onChange({ ...config, mcpGateway: { enabled: checked } })
          }
        />
      </div>
    </div>
  )
}
