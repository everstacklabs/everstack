import {
  Input,
  Label,
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
  Switch,
  Textarea,
} from '@everstack/ui/components'
import { Icon } from '@iconify/react'
import { useAgents } from '@/hooks/deployments/use-agents'
import { useFunctions } from '@/hooks/deployments/use-functions'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import type { AgentConfig } from '../../types'

interface Props {
  config: AgentConfig
  onChange: (config: AgentConfig) => void
}

const LEGACY_INLINE_MODEL_DEFAULT = 'gpt-4o'

export function AgentConfigForm({ config, onChange }: Props) {
  const { data: agents = [], isLoading: agentsLoading } = useAgents()
  const { data: functions = [], isLoading: functionsLoading } = useFunctions()
  const { data: gatewayModels = [], isLoading: modelsLoading } =
    useGatewayModels()
  const selectedAgent = agents.find((agent) => agent.id === config.agentId)
  const selectedAgentConfig = selectedAgent?.config as
    | Record<string, unknown>
    | undefined
  const selectedAgentBrowser = selectedAgentConfig?.browser as
    | { enabled?: boolean; headless?: boolean }
    | undefined
  const inlineBrowser = config.inlineAgent.browser ?? {
    enabled: false,
    headless: true,
  }

  const handleAgentSelect = (agentId: string) => {
    const agent = agents.find((a) => a.id === agentId)
    if (agent) {
      const shouldUseAgentModel =
        !config.inlineAgent.model ||
        config.inlineAgent.model === LEGACY_INLINE_MODEL_DEFAULT
      onChange({
        ...config,
        agentId: agent.id,
        agentName: agent.name,
        agentModel: agent.model,
        inlineAgent: {
          ...config.inlineAgent,
          model: shouldUseAgentModel ? agent.model : config.inlineAgent.model,
        },
      })
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <Label className="text-sm text-brand-main-200">Use Inline Config</Label>
        <Switch
          checked={config.useInline}
          onCheckedChange={(v) =>
            onChange({
              ...config,
              useInline: v,
              inlineAgent: {
                ...config.inlineAgent,
                model:
                  !config.inlineAgent.model ||
                  config.inlineAgent.model === LEGACY_INLINE_MODEL_DEFAULT
                    ? config.agentModel || ''
                    : config.inlineAgent.model,
              },
            })
          }
        />
      </div>

      {!config.useInline ? (
        <>
          <div className="space-y-1.5 w-full">
            <Label className="text-sm text-brand-main-200">Agent</Label>
            <Select value={config.agentId} onValueChange={handleAgentSelect}>
              <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                <SelectValue
                  placeholder={agentsLoading ? 'Loading...' : 'Select an agent'}
                />
              </SelectTrigger>
              <SelectContent>
                {agents.map((agent) => (
                  <SelectItem key={agent.id} value={agent.id}>
                    {agent.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {config.agentName && (
            <div className="space-y-1.5">
              <Label className="text-sm text-brand-main-200">Agent Name</Label>
              <div className="text-sm text-brand-main-300 bg-brand-main-800 rounded px-3 py-1.5">
                {config.agentName}
              </div>
            </div>
          )}
          {config.agentModel && (
            <div className="space-y-1.5">
              <Label className="text-sm text-brand-main-200">Model</Label>
              <div className="text-sm text-brand-main-300 bg-brand-main-800 rounded px-3 py-1.5">
                {config.agentModel}
              </div>
            </div>
          )}
        </>
      ) : (
        <>
          <div className="space-y-1.5">
            <Label className="text-sm text-brand-main-200">Name</Label>
            <Input
              value={config.inlineAgent.name}
              onChange={(e) =>
                onChange({
                  ...config,
                  inlineAgent: { ...config.inlineAgent, name: e.target.value },
                })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
              placeholder="Agent name"
            />
          </div>
          <div className="space-y-1.5 w-full">
            <Label className="text-sm text-brand-main-200">Model</Label>
            <Select
              value={config.inlineAgent.model}
              onValueChange={(v) =>
                onChange({
                  ...config,
                  inlineAgent: { ...config.inlineAgent, model: v },
                })
              }
            >
              <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                <SelectValue
                  placeholder={
                    modelsLoading ? 'Loading models...' : 'Select a model'
                  }
                />
              </SelectTrigger>
              <SelectContent>
                {gatewayModels.length > 0 ? (
                  gatewayModels.map((group) => (
                    <SelectGroup key={group.provider}>
                      <SelectLabel>{group.provider}</SelectLabel>
                      {group.models.map((m) => (
                        <SelectItem key={m} value={m}>
                          {m}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  ))
                ) : (
                  <>
                    <SelectItem value="gpt-4o">gpt-4o</SelectItem>
                    <SelectItem value="gpt-4o-mini">gpt-4o-mini</SelectItem>
                    <SelectItem value="claude-sonnet-4-20250514">
                      claude-sonnet-4-20250514
                    </SelectItem>
                    <SelectItem value="claude-haiku-4-20250414">
                      claude-haiku-4-20250414
                    </SelectItem>
                  </>
                )}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label className="text-sm text-brand-main-200">System Prompt</Label>
            <Textarea
              value={config.inlineAgent.systemPrompt}
              onChange={(e) =>
                onChange({
                  ...config,
                  inlineAgent: {
                    ...config.inlineAgent,
                    systemPrompt: e.target.value,
                  },
                })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
              placeholder="Enter system prompt..."
              rows={4}
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-sm text-brand-main-200">Temperature</Label>
            <Input
              type="number"
              step={0.1}
              min={0}
              max={2}
              value={config.inlineAgent.temperature}
              onChange={(e) =>
                onChange({
                  ...config,
                  inlineAgent: {
                    ...config.inlineAgent,
                    temperature: Number(e.target.value),
                  },
                })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-sm text-brand-main-200">Max Tokens</Label>
            <Input
              type="number"
              value={config.inlineAgent.maxTokens}
              onChange={(e) =>
                onChange({
                  ...config,
                  inlineAgent: {
                    ...config.inlineAgent,
                    maxTokens: Number(e.target.value),
                  },
                })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-sm text-brand-main-200">Tools</Label>
            {functionsLoading ? (
              <div className="text-sm text-brand-main-400">
                Loading functions...
              </div>
            ) : (
              <div className="space-y-1 max-h-32 overflow-y-auto">
                {functions.map((fn) => (
                  <label
                    key={fn.id}
                    className="flex items-center gap-2 text-sm text-brand-main-300 cursor-pointer"
                  >
                    <input
                      type="checkbox"
                      checked={config.inlineAgent.tools.includes(fn.name)}
                      onChange={(e) => {
                        const tools = e.target.checked
                          ? [...config.inlineAgent.tools, fn.name]
                          : config.inlineAgent.tools.filter(
                              (t) => t !== fn.name,
                            )
                        onChange({
                          ...config,
                          inlineAgent: { ...config.inlineAgent, tools },
                        })
                      }}
                      className="rounded border-brand-main-500"
                    />
                    {fn.name}
                  </label>
                ))}
              </div>
            )}
          </div>
        </>
      )}

      <div className="border border-brand-main-600 bg-brand-main-800/40 rounded p-3 space-y-3">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-start gap-2.5">
            <div className="mt-0.5 flex size-7 shrink-0 items-center justify-center border border-brand-main-600 bg-brand-main-900 rounded">
              <Icon
                icon="lucide:mouse-pointer-click"
                className="size-3.5 text-brand-secondary-300"
              />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-medium text-white light:text-brand-main-50">
                Computer use
              </p>
              <p className="mt-0.5 text-xs leading-5 text-brand-main-300">
                Browser actions appear in the run trace and snapshots are
                retained as execution artifacts.
              </p>
            </div>
          </div>
          {config.useInline ? (
            <Switch
              checked={inlineBrowser.enabled}
              onCheckedChange={(enabled) =>
                onChange({
                  ...config,
                  inlineAgent: {
                    ...config.inlineAgent,
                    browser: { ...inlineBrowser, enabled },
                  },
                })
              }
            />
          ) : (
            <span
              className={`mt-1 shrink-0 rounded border px-2 py-0.5 text-[11px] ${
                selectedAgentBrowser?.enabled
                  ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300'
                  : 'border-brand-main-600 bg-brand-main-900 text-brand-main-300'
              }`}
            >
              {selectedAgentBrowser?.enabled
                ? 'Enabled on agent'
                : 'Disabled on agent'}
            </span>
          )}
        </div>

        {config.useInline && inlineBrowser.enabled && (
          <div className="flex items-center justify-between border-t border-brand-main-600/70 pt-3">
            <div>
              <Label className="text-sm text-brand-main-200">
                Run headless
              </Label>
              <p className="mt-0.5 text-xs text-brand-main-400">
                Snapshots and replay remain available without a live viewport.
              </p>
            </div>
            <Switch
              checked={inlineBrowser.headless}
              onCheckedChange={(headless) =>
                onChange({
                  ...config,
                  inlineAgent: {
                    ...config.inlineAgent,
                    browser: { ...inlineBrowser, headless },
                  },
                })
              }
            />
          </div>
        )}

        <div className="flex items-center gap-2 border-t border-brand-main-600/70 pt-2 text-[11px] text-brand-main-400">
          <Icon icon="lucide:clock-3" className="size-3.5" />
          <span>
            $0.01 per active browser hour · 60 second minimum · idle pool time
            is not billed
          </span>
        </div>
      </div>

      <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider mt-6 mb-2">
        Advanced
      </div>

      <div className="space-y-1.5">
        <Label className="text-sm text-brand-main-200">Max Iterations</Label>
        <Input
          type="number"
          value={config.maxIterations}
          onChange={(e) =>
            onChange({ ...config, maxIterations: Number(e.target.value) })
          }
          className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
        />
      </div>
      <div className="space-y-1.5">
        <Label className="text-sm text-brand-main-200">
          Max Tool Calls Per Turn
        </Label>
        <Input
          type="number"
          value={config.maxToolCallsPerTurn}
          onChange={(e) =>
            onChange({ ...config, maxToolCallsPerTurn: Number(e.target.value) })
          }
          className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
        />
      </div>
      <div className="space-y-1.5">
        <Label className="text-sm text-brand-main-200">Turn Timeout</Label>
        <Input
          value={config.turnTimeout}
          onChange={(e) => onChange({ ...config, turnTimeout: e.target.value })}
          className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
          placeholder="e.g. 5m"
        />
      </div>
      <div className="space-y-1.5 w-full">
        <Label className="text-sm text-brand-main-200">Context Mode</Label>
        <Select
          value={config.contextMode}
          onValueChange={(v) =>
            onChange({
              ...config,
              contextMode: v as 'inherit' | 'isolated' | 'custom',
            })
          }
        >
          <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
            <SelectValue placeholder="Select context mode" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="inherit">Inherit</SelectItem>
            <SelectItem value="isolated">Isolated</SelectItem>
            <SelectItem value="custom">Custom</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider mt-6 mb-2">
        Memory (RAG)
      </div>

      <div className="flex items-center justify-between">
        <Label className="text-sm text-brand-main-200">Enable Memory</Label>
        <Switch
          checked={config.memoryEnabled ?? false}
          onCheckedChange={(v) => onChange({ ...config, memoryEnabled: v })}
        />
      </div>

      {config.memoryEnabled && (
        <>
          <div className="space-y-1.5">
            <Label className="text-sm text-brand-main-200">
              Collection Name
            </Label>
            <Input
              value={config.memoryCollection ?? 'default'}
              onChange={(e) =>
                onChange({ ...config, memoryCollection: e.target.value })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
              placeholder="default"
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-sm text-brand-main-200">Top K Results</Label>
            <Input
              type="number"
              value={config.memoryTopK ?? 5}
              onChange={(e) =>
                onChange({ ...config, memoryTopK: Number(e.target.value) })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
              min={1}
              max={100}
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-sm text-brand-main-200">Min Score</Label>
            <Input
              type="number"
              step={0.05}
              value={config.memoryMinScore ?? 0}
              onChange={(e) =>
                onChange({ ...config, memoryMinScore: Number(e.target.value) })
              }
              className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
              min={0}
              max={1}
            />
          </div>
          <div className="flex items-center justify-between">
            <Label className="text-sm text-brand-main-200">
              Store Responses
            </Label>
            <Switch
              checked={config.memoryStoreResponses ?? false}
              onCheckedChange={(v) =>
                onChange({ ...config, memoryStoreResponses: v })
              }
            />
          </div>
        </>
      )}
    </div>
  )
}
