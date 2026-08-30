import { useMemo, useState } from 'react'
import { Search, ChevronDown } from 'lucide-react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import type { McpServer } from '@/server/mcp'
import type { McpToolEntry } from './tools-tab-content'
import { mcpToolName } from './tool-catalog'

const { Button, Checkbox, Input, Collapsible, CollapsibleContent } = ui

const brandInputClass = 'bg-brand-main-900 border-brand-main-600'

interface McpTabContentProps {
  mcpServers: McpServer[]
  mcpTools: McpToolEntry[]
  selectedTools: string[]
  toggleTool: (name: string) => void
  setSelectedTools: React.Dispatch<React.SetStateAction<string[]>>
}

export function McpTabContent({
  mcpServers,
  mcpTools,
  selectedTools,
  toggleTool,
  setSelectedTools,
}: McpTabContentProps) {
  const [search, setSearch] = useState('')
  const searchLower = search.toLowerCase().trim()

  const toolsByServer = useMemo(() => {
    const map = new Map<string, McpToolEntry[]>()
    for (const t of mcpTools) {
      const list = map.get(t.serverId)
      if (list) list.push(t)
      else map.set(t.serverId, [t])
    }
    return map
  }, [mcpTools])

  const filteredServers = useMemo(() => {
    if (!searchLower) return mcpServers
    return mcpServers.filter((s) => {
      if (s.name.toLowerCase().includes(searchLower)) return true
      const tools = toolsByServer.get(s.id) ?? []
      return tools.some(
        (t) =>
          t.toolName.toLowerCase().includes(searchLower) ||
          (t.description ?? '').toLowerCase().includes(searchLower),
      )
    })
  }, [mcpServers, toolsByServer, searchLower])

  const attachedServerCount = useMemo(() => {
    let count = 0
    for (const server of mcpServers) {
      const tools = toolsByServer.get(server.id) ?? []
      if (tools.length === 0) continue
      const allSelected = tools.every((t) => selectedTools.includes(t.name))
      if (allSelected) count++
    }
    return count
  }, [mcpServers, toolsByServer, selectedTools])

  if (mcpServers.length === 0) {
    return (
      <div className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 px-6 py-10 text-center">
        <Iconify.Icon
          icon="heroicons:server-stack"
          className="mx-auto h-8 w-8 text-white/30 light:text-black/30"
        />
        <div className="mt-3 text-sm font-medium text-white/80 light:text-black/80">
          No MCP servers configured
        </div>
        <div className="mt-1 text-xs text-white/50 light:text-black/50">
          Register an MCP server in the gateway to make its tools available to agents.
        </div>
        <a
          href="/gateway/mcp"
          target="_blank"
          rel="noopener noreferrer"
          className="mt-4 inline-flex items-center justify-center rounded bg-brand-secondary-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-brand-secondary-600 transition-colors"
        >
          Open MCP Gateway
        </a>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-white/30 light:text-black/30" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search MCP servers and tools..."
            className={`${brandInputClass} pl-8 h-8 text-xs`}
          />
        </div>
        <span className="text-[11px] text-white/50 tabular-nums shrink-0 light:text-black/50">
          {attachedServerCount}/{mcpServers.length} attached
        </span>
      </div>

      {filteredServers.length === 0 && (
        <div className="text-center py-8 text-sm text-white/40 light:text-black/40">
          No MCP servers matching "{search}"
        </div>
      )}

      {filteredServers.map((server) => (
        <McpServerCard
          key={server.id}
          server={server}
          tools={toolsByServer.get(server.id) ?? []}
          selectedTools={selectedTools}
          toggleTool={toggleTool}
          setSelectedTools={setSelectedTools}
        />
      ))}
    </div>
  )
}

function McpServerCard({
  server,
  tools,
  selectedTools,
  toggleTool,
  setSelectedTools,
}: {
  server: McpServer
  tools: McpToolEntry[]
  selectedTools: string[]
  toggleTool: (name: string) => void
  setSelectedTools: React.Dispatch<React.SetStateAction<string[]>>
}) {
  const [expanded, setExpanded] = useState(false)

  const toolNames = useMemo(() => tools.map((t) => t.name), [tools])
  const selectedInGroup = toolNames.filter((n) => selectedTools.includes(n))
  const allSelected = toolNames.length > 0 && selectedInGroup.length === toolNames.length
  const someSelected = selectedInGroup.length > 0 && !allSelected

  // Stale tools attached on the agent that this server no longer publishes (
  // e.g. server's tool inventory shrank after attachment). Surface them so the
  // user can prune.
  const staleAttached = useMemo(() => {
    const prefix = mcpToolName(server.name, '')
    return selectedTools.filter(
      (t) => t.startsWith(prefix) && !toolNames.includes(t),
    )
  }, [selectedTools, server.name, toolNames])

  const handleAttachToggle = () => {
    if (allSelected) {
      setSelectedTools((prev) => prev.filter((t) => !toolNames.includes(t)))
    } else {
      setSelectedTools((prev) => {
        const next = new Set(prev)
        for (const n of toolNames) next.add(n)
        return Array.from(next)
      })
    }
  }

  const handlePruneStale = () => {
    if (staleAttached.length === 0) return
    setSelectedTools((prev) => prev.filter((t) => !staleAttached.includes(t)))
  }

  return (
    <div className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 overflow-hidden">
      <div className="flex items-center gap-3 px-3 py-2.5">
        <Checkbox
          checked={allSelected}
          // @ts-expect-error -- radix indeterminate
          indeterminate={someSelected}
          onCheckedChange={handleAttachToggle}
          disabled={tools.length === 0}
          className="border-brand-main-600 data-[state=checked]:bg-brand-secondary-500 data-[state=checked]:border-brand-secondary-500"
        />
        <Iconify.Icon
          icon="heroicons:server-stack"
          className="h-4 w-4 text-brand-secondary-400 shrink-0"
        />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-white/85 truncate light:text-black/85">
              {server.name}
            </span>
            <HealthDot status={server.healthStatus} />
            {tools.length > 0 ? (
              <span className="text-[10px] text-white/35 tabular-nums light:text-black/35">
                {selectedInGroup.length}/{toolNames.length}
              </span>
            ) : (
              <span className="text-[10px] text-white/35 light:text-black/35">no tools</span>
            )}
          </div>
          <div className="text-[11px] text-white/40 leading-tight truncate light:text-black/40">
            {server.url}
          </div>
        </div>
        {tools.length > 0 && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setExpanded((v) => !v)}
            className="h-6 px-2 text-[11px] text-white/50 hover:text-white/80 shrink-0 light:text-black/50 light:hover:text-black/80"
          >
            {expanded ?'Hide tools' : 'Customize tools'}
            <ChevronDown className={`ml-1 h-3 w-3 transition-transform ${expanded ? 'rotate-180' : ''}`} />
          </Button>
        )}
      </div>

      {staleAttached.length > 0 && (
        <div className="flex items-center gap-2 border-t border-amber-500/15 bg-amber-500/5 px-3 py-1.5">
          <Iconify.Icon
            icon="heroicons:exclamation-triangle"
            className="h-3 w-3 text-amber-400/70 shrink-0"
          />
          <span className="text-[11px] text-amber-300/70 flex-1">
            {staleAttached.length} attached tool{staleAttached.length === 1 ? '' : 's'} no longer published by this server.
          </span>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={handlePruneStale}
            className="h-6 px-2 text-[11px] text-amber-300 hover:text-amber-200"
          >
            Remove
          </Button>
        </div>
      )}

      {tools.length > 0 && (
        <Collapsible open={expanded} onOpenChange={setExpanded}>
          <CollapsibleContent className="border-t border-brand-main-800/50">
            <div className="grid grid-cols-1 gap-2 px-3 py-2.5 sm:grid-cols-2 xl:grid-cols-3">
              {tools.map((tool) => (
                <label
                  key={tool.name}
                  className="grid grid-cols-[auto_1fr] items-start gap-3 rounded border border-brand-main-800/60 bg-brand-main-900/35 px-3 py-2 transition-colors hover:border-brand-main-700/80 hover:bg-brand-main-800/35 cursor-pointer"
                >
                  <Checkbox
                    checked={selectedTools.includes(tool.name)}
                    onCheckedChange={() => toggleTool(tool.name)}
                    className="mt-0.5 border-brand-main-600 data-[state=checked]:bg-brand-secondary-500 data-[state=checked]:border-brand-secondary-500"
                  />
                  <div className="min-w-0">
                    <div className="text-xs font-medium text-white/85 truncate light:text-black/85">
                      {tool.toolName}
                    </div>
                    {tool.description && (
                      <div className="mt-1 text-[11px] leading-tight text-white/40 line-clamp-2 light:text-black/40">
                        {tool.description}
                      </div>
                    )}
                  </div>
                </label>
              ))}
            </div>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  )
}

function HealthDot({ status }: { status: McpServer['healthStatus'] }) {
  const cls =
    status === 'MCP_SERVER_HEALTH_STATUS_HEALTHY'
      ? 'bg-emerald-400'
      : status === 'MCP_SERVER_HEALTH_STATUS_UNHEALTHY'
        ? 'bg-red-400'
        : 'bg-white/30 light:bg-black/30'
  const label =
    status === 'MCP_SERVER_HEALTH_STATUS_HEALTHY'
      ? 'Healthy'
      : status === 'MCP_SERVER_HEALTH_STATUS_UNHEALTHY'
        ? 'Unhealthy'
        : 'Unknown'
  return (
    <span className="inline-flex items-center gap-1" title={label}>
      <span className={`size-1.5 rounded-full ${cls}`} />
    </span>
  )
}
