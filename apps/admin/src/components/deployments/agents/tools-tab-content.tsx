import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { TOOL_CATALOG, type ToolCategoryDef } from './tool-catalog'
import type { Function } from '@/server/functions'

export interface McpToolEntry {
  /** Namespaced name persisted to agent.tools (e.g. "mcp__github__list_repos"). */
  name: string
  /** Raw tool name as exposed by the MCP server. */
  toolName: string
  serverId: string
  serverName: string
  description: string
}

const { Button, Checkbox, Input } = ui

const brandInputClass = 'bg-brand-main-900 border-brand-main-600'

type DisplayTool = ToolCategoryDef['tools'][number] & {
  isRuntime?: boolean
  isExplicit?: boolean
  isDisabledRuntime?: boolean
}

type DisplayCategory = Omit<ToolCategoryDef, 'tools'> & {
  tools: DisplayTool[]
}

interface ToolsTabContentProps {
  selectedTools: string[]
  toggleTool: (name: string) => void
  setSelectedTools: React.Dispatch<React.SetStateAction<string[]>>
  runtimeToolNames: string[]
  disabledRuntimeTools: string[]
  toggleRuntimeTool: (name: string) => void
  sandboxEnabled: boolean
  browserEnabled: boolean
  memoryEnabled: boolean
  spawnEnabled: boolean
  functions: Function[]
  mcpTools?: McpToolEntry[]
}

export function ToolsTabContent({
  selectedTools,
  toggleTool,
  setSelectedTools,
  runtimeToolNames,
  disabledRuntimeTools,
  toggleRuntimeTool,
  sandboxEnabled,
  browserEnabled,
  memoryEnabled,
  spawnEnabled,
  functions,
  mcpTools = [],
}: ToolsTabContentProps) {
  const [search, setSearch] = useState('')
  const searchLower = search.toLowerCase().trim()
  const runtimeToolSet = useMemo(
    () => new Set(runtimeToolNames),
    [runtimeToolNames],
  )
  const explicitSelectedTools = useMemo(
    () => selectedTools.filter((tool) => !runtimeToolSet.has(tool)),
    [selectedTools, runtimeToolSet],
  )

  const dependencyMet: Record<string, boolean> = {
    sandbox: sandboxEnabled,
    browser: browserEnabled,
    memory: memoryEnabled,
    spawn: spawnEnabled,
  }

  const categorizedCatalog = useMemo<DisplayCategory[]>(
    () =>
      TOOL_CATALOG.map((cat) => ({
        ...cat,
        tools: cat.tools
          .map((tool) => ({
            ...tool,
            isRuntime: runtimeToolSet.has(tool.name),
            isExplicit: explicitSelectedTools.includes(tool.name),
            isDisabledRuntime: disabledRuntimeTools.includes(tool.name),
          }))
          .filter((tool) => {
            if (!searchLower) return true
            return (
              tool.name.toLowerCase().includes(searchLower) ||
              tool.label.toLowerCase().includes(searchLower) ||
              tool.description.toLowerCase().includes(searchLower)
            )
          }),
      })).filter((cat) => cat.tools.length > 0),
    [disabledRuntimeTools, explicitSelectedTools, runtimeToolSet, searchLower],
  )

  const filteredMcpTools = useMemo(() => {
    if (!mcpTools.length) return []
    if (!searchLower) return mcpTools
    return mcpTools.filter(
      (t) =>
        t.name.toLowerCase().includes(searchLower) ||
        t.toolName.toLowerCase().includes(searchLower) ||
        t.serverName.toLowerCase().includes(searchLower) ||
        (t.description ?? '').toLowerCase().includes(searchLower),
    )
  }, [mcpTools, searchLower])

  const mcpToolsByServer = useMemo(() => {
    const groups = new Map<string, { serverId: string; serverName: string; tools: McpToolEntry[] }>()
    for (const t of filteredMcpTools) {
      const existing = groups.get(t.serverId)
      if (existing) {
        existing.tools.push(t)
      } else {
        groups.set(t.serverId, { serverId: t.serverId, serverName: t.serverName, tools: [t] })
      }
    }
    return Array.from(groups.values())
  }, [filteredMcpTools])

  const filteredFunctions = useMemo(() => {
    if (!functions.length) return []
    if (!searchLower) return functions
    return functions.filter(
      (fn) =>
        fn.name.toLowerCase().includes(searchLower) ||
        (fn.description ?? '').toLowerCase().includes(searchLower),
    )
  }, [functions, searchLower])

  const availableFunctions = useMemo(
    () =>
      filteredFunctions.filter(
        (fn) => !explicitSelectedTools.includes(fn.name),
      ),
    [explicitSelectedTools, filteredFunctions],
  )
  const enabledToolCount =
    explicitSelectedTools.length +
    runtimeToolNames.filter((tool) => !disabledRuntimeTools.includes(tool))
      .length

  const handleClearAll = () => {
    setSelectedTools([])
    for (const toolName of runtimeToolNames) {
      if (!disabledRuntimeTools.includes(toolName)) {
        toggleRuntimeTool(toolName)
      }
    }
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-white/30 light:text-black/30" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search tools..."
            className={`${brandInputClass} pl-8 h-8 text-xs`}
          />
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <span className="text-[11px] text-white/50 tabular-nums light:text-black/50">
            {enabledToolCount} enabled
          </span>
          {enabledToolCount > 0 && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={handleClearAll}
              className="h-6 px-2 text-[11px] text-white/40 hover:text-white/70 light:text-black/40 light:hover:text-black/70"
            >
              Clear all
            </Button>
          )}
        </div>
      </div>

      {/* Categories */}
      {categorizedCatalog.map((cat) => (
        <ToolCategorySection
          key={cat.id}
          category={cat}
          selectedTools={selectedTools}
          toggleTool={toggleTool}
          setSelectedTools={setSelectedTools}
          toggleRuntimeTool={toggleRuntimeTool}
          depMet={cat.dependency ? dependencyMet[cat.dependency] : true}
        />
      ))}

      {/* MCP Servers */}
      {mcpToolsByServer.map((group) => (
        <McpServerSection
          key={group.serverId}
          serverName={group.serverName}
          tools={group.tools}
          selectedTools={selectedTools}
          toggleTool={toggleTool}
          setSelectedTools={setSelectedTools}
        />
      ))}

      {/* User Functions */}
      {availableFunctions.length > 0 && (
        <div className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 overflow-hidden">
          <FunctionsCategoryHeader
            functions={availableFunctions}
            setSelectedTools={setSelectedTools}
            functionSelectedCount={0}
            totalFunctions={availableFunctions.length}
          />
          <div className="grid grid-cols-1 gap-2 px-3 pb-3 sm:grid-cols-2 xl:grid-cols-3">
            {availableFunctions.map((fn) => (
              <label
                key={fn.name}
                className="grid grid-cols-[auto_1fr] items-start gap-3 rounded border border-brand-main-800/60 bg-brand-main-900/35 px-3 py-2 transition-colors hover:border-brand-main-700/80 hover:bg-brand-main-800/35 cursor-pointer"
              >
                <Checkbox
                  checked={selectedTools.includes(fn.name)}
                  onCheckedChange={() => toggleTool(fn.name)}
                  className="mt-0.5 border-brand-main-600 data-[state=checked]:bg-brand-secondary-500 data-[state=checked]:border-brand-secondary-500"
                />
                <div className="min-w-0">
                  <div className="text-xs font-medium text-white/85 truncate light:text-black/85">
                    {fn.name}
                  </div>
                  {fn.description && (
                    <div className="mt-1 text-[11px] text-white/40 leading-tight line-clamp-2 light:text-black/40">
                      {fn.description}
                    </div>
                  )}
                </div>
              </label>
            ))}
          </div>
        </div>
      )}

      {categorizedCatalog.length === 0 &&
        availableFunctions.length === 0 &&
        mcpToolsByServer.length === 0 &&
        searchLower && (
          <div className="text-center py-8 text-sm text-white/40 light:text-black/40">
            No tools matching "{search}"
          </div>
        )}
    </div>
  )
}

function ToolCategorySection({
  category,
  selectedTools,
  toggleTool,
  setSelectedTools,
  toggleRuntimeTool,
  depMet,
}: {
  category: DisplayCategory
  selectedTools: string[]
  toggleTool: (name: string) => void
  setSelectedTools: React.Dispatch<React.SetStateAction<string[]>>
  toggleRuntimeTool: (name: string) => void
  depMet: boolean
}) {
  const selectedInCat = category.tools.filter((tool) =>
    tool.isRuntime
      ? !tool.isDisabledRuntime
      : selectedTools.includes(tool.name),
  )
  const totalInCat = category.tools.length
  const allSelected = selectedInCat.length === totalInCat
  const someSelected = selectedInCat.length > 0 && !allSelected

  const handleGroupToggle = () => {
    if (allSelected) {
      for (const tool of category.tools) {
        if (tool.isRuntime && !tool.isDisabledRuntime) {
          toggleRuntimeTool(tool.name)
        }
      }
      setSelectedTools((prev) =>
        prev.filter((t) => !category.tools.some((tool) => tool.name === t)),
      )
    } else {
      for (const tool of category.tools) {
        if (tool.isRuntime && tool.isDisabledRuntime) {
          toggleRuntimeTool(tool.name)
        }
      }
      setSelectedTools((prev) => {
        const existing = new Set(prev)
        for (const tool of category.tools) {
          if (tool.isRuntime) continue
          existing.add(tool.name)
        }
        return Array.from(existing)
      })
    }
  }

  return (
    <div className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 overflow-hidden">
      {/* Category header */}
      <div className="flex items-center gap-3 border-b border-brand-main-800/50 px-3 py-2.5">
        <Checkbox
          checked={allSelected}
          // @ts-expect-error -- radix indeterminate
          indeterminate={someSelected}
          onCheckedChange={handleGroupToggle}
          className="border-brand-main-600 data-[state=checked]:bg-brand-secondary-500 data-[state=checked]:border-brand-secondary-500"
        />
        <Iconify.Icon
          icon={category.icon}
          className="h-4 w-4 text-brand-secondary-400 shrink-0"
        />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-white/85 light:text-black/85">
              {category.label}
            </span>
            <span className="text-[10px] text-white/35 tabular-nums light:text-black/35">
              {selectedInCat.length}/{totalInCat}
            </span>
          </div>
          <div className="text-[11px] text-white/40 leading-tight light:text-black/40">
            {category.description}
          </div>
        </div>
      </div>

      {/* Dependency hint */}
      {category.dependency && !depMet && (
        <div className="flex items-center gap-2 px-3 py-1.5 bg-amber-500/5 border-b border-amber-500/10">
          <Iconify.Icon
            icon="heroicons:exclamation-triangle"
            className="h-3 w-3 text-amber-400/70 shrink-0"
          />
          <span className="text-[11px] text-amber-300/70">
            {category.dependencyHint}
          </span>
        </div>
      )}

      {/* Tool grid */}
      <div className="grid grid-cols-1 gap-2 px-3 py-2.5 sm:grid-cols-2 xl:grid-cols-3">
        {category.tools.map((tool) => (
          <label
            key={tool.name}
            className={`grid grid-cols-[auto_1fr] items-start gap-3 rounded border px-3 py-2 cursor-pointer transition-colors ${
              category.dependency && !depMet
                ? 'border-brand-main-800/40 bg-brand-main-900/20 opacity-50 hover:bg-brand-main-800/20'
                : 'border-brand-main-800/60 bg-brand-main-900/35 hover:border-brand-main-700/80 hover:bg-brand-main-800/35'
            }`}
          >
            <Checkbox
              checked={
                tool.isRuntime
                  ? !tool.isDisabledRuntime
                  : selectedTools.includes(tool.name)
              }
              onCheckedChange={() =>
                tool.isRuntime
                  ? toggleRuntimeTool(tool.name)
                  : toggleTool(tool.name)
              }
              className="mt-0.5 border-brand-main-600 data-[state=checked]:bg-brand-secondary-500 data-[state=checked]:border-brand-secondary-500"
            />
            <div className="min-w-0">
              <div className="flex items-center gap-2 text-xs font-medium text-white/85 light:text-black/85">
                <span className="truncate">{tool.label}</span>
                {tool.isRuntime ? (
                  <span className="text-[10px] text-brand-secondary-300/70">
                    Built-in
                  </span>
                ) : tool.isExplicit ? (
                  <span className="text-[10px] text-white/30 light:text-black/30">Explicit</span>
                ) : null}
              </div>
              <div className="mt-1 text-[11px] leading-tight text-white/40 line-clamp-2 light:text-black/40">
                {tool.description}
              </div>
            </div>
          </label>
        ))}
      </div>
    </div>
  )
}

function FunctionsCategoryHeader({
  functions,
  setSelectedTools,
  functionSelectedCount,
  totalFunctions,
}: {
  functions: Function[]
  setSelectedTools: React.Dispatch<React.SetStateAction<string[]>>
  functionSelectedCount: number
  totalFunctions: number
}) {
  const fnNames = functions.map((fn) => fn.name)
  const allSelected =
    functionSelectedCount === totalFunctions && totalFunctions > 0
  const someSelected = functionSelectedCount > 0 && !allSelected

  const handleGroupToggle = () => {
    if (allSelected) {
      setSelectedTools((prev) => prev.filter((t) => !fnNames.includes(t)))
    } else {
      setSelectedTools((prev) => {
        const existing = new Set(prev)
        for (const n of fnNames) existing.add(n)
        return Array.from(existing)
      })
    }
  }

  return (
    <div className="flex items-center gap-3 px-3 py-2.5 border-b border-brand-main-800/50">
      <Checkbox
        checked={allSelected}
        // @ts-expect-error -- radix indeterminate
        indeterminate={someSelected}
        onCheckedChange={handleGroupToggle}
        className="border-brand-main-600 data-[state=checked]:bg-brand-secondary-500 data-[state=checked]:border-brand-secondary-500"
      />
      <Iconify.Icon
        icon="heroicons:bolt-slash"
        className="h-4 w-4 text-brand-secondary-400 shrink-0"
      />
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium text-white/85 light:text-black/85">Functions</span>
          <span className="text-[10px] text-white/35 tabular-nums light:text-black/35">
            {functionSelectedCount}/{totalFunctions}
          </span>
        </div>
        <div className="text-[11px] text-white/40 leading-tight light:text-black/40">
          User-created serverless functions registered in your workspace.
        </div>
      </div>
    </div>
  )
}

function McpServerSection({
  serverName,
  tools,
  selectedTools,
  toggleTool,
  setSelectedTools,
}: {
  serverName: string
  tools: McpToolEntry[]
  selectedTools: string[]
  toggleTool: (name: string) => void
  setSelectedTools: React.Dispatch<React.SetStateAction<string[]>>
}) {
  const toolNames = useMemo(() => tools.map((t) => t.name), [tools])
  const selectedInGroup = toolNames.filter((n) => selectedTools.includes(n))
  const allSelected =
    selectedInGroup.length === toolNames.length && toolNames.length > 0
  const someSelected = selectedInGroup.length > 0 && !allSelected

  const handleGroupToggle = () => {
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

  return (
    <div className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 overflow-hidden">
      <div className="flex items-center gap-3 border-b border-brand-main-800/50 px-3 py-2.5">
        <Checkbox
          checked={allSelected}
          // @ts-expect-error -- radix indeterminate
          indeterminate={someSelected}
          onCheckedChange={handleGroupToggle}
          className="border-brand-main-600 data-[state=checked]:bg-brand-secondary-500 data-[state=checked]:border-brand-secondary-500"
        />
        <Iconify.Icon
          icon="heroicons:server-stack"
          className="h-4 w-4 text-brand-secondary-400 shrink-0"
        />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-white/85 truncate light:text-black/85">
              MCP · {serverName}
            </span>
            <span className="text-[10px] text-white/35 tabular-nums light:text-black/35">
              {selectedInGroup.length}/{toolNames.length}
            </span>
          </div>
          <div className="text-[11px] text-white/40 leading-tight light:text-black/40">
            Tools federated from this MCP server.
          </div>
        </div>
      </div>

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
              <div className="flex items-center gap-2 text-xs font-medium text-white/85 light:text-black/85">
                <span className="truncate">{tool.toolName}</span>
                <span className="text-[10px] text-brand-secondary-300/70">
                  MCP
                </span>
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
    </div>
  )
}
