import { useMemo, useState } from 'react'
import { useQueries } from '@tanstack/react-query'
import Editor, { type Monaco } from '@monaco-editor/react'
import { ui } from '@everstack/ui'
import { Button, Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { toDate } from '@everstack/client'
import { AgentLinkType } from '@everstack/proto/everstack/agents/v1/agents_pb'
import { materialOceanTheme } from '@/components/deployments/functions/code-editor'
import { listAgentTriggers } from '@/server/agent-triggers'
import { getActiveAgentRevision } from '@/server/agents'
import { useAgents, useAgentLinks } from '@/hooks/deployments/use-agents'
import { useFunctions } from '@/hooks/deployments/use-functions'
import { useSession } from '@/hooks/auth/use-auth'
import { cn } from '@/lib/utils'
import {
  buildAgentWorkspace,
  buildFileTree,
  type ProjectedAgent,
  type ProjectedFunction,
  type ProjectedRevision,
  type ProjectedTrigger,
  type SyncState,
  type TreeNode,
  type WorkspaceFile,
} from './agent-workspace-projection'

const { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } = ui

/** Query key shape is shared with useAgentTriggers so the cache is reused. */
const TRIGGERS_QUERY_KEY = ['agent-triggers']

function useOrganizationId(): string {
  const { data: session } = useSession()
  return session?.user?.organizations?.[0]?.id ?? ''
}

/**
 * Triggers for the root agent and every subagent. A subagent's agent.yaml
 * lists its own triggers, so fetching only the root would make the rendered
 * project disagree with what `evs agents pull` writes.
 */
function useTriggersForAgents(agentIds: string[]) {
  const orgId = useOrganizationId()
  return useQueries({
    queries: agentIds.map((id) => ({
      queryKey: [...TRIGGERS_QUERY_KEY, orgId, id],
      queryFn: async () => (await listAgentTriggers(id, orgId)).triggers ?? [],
      enabled: !!id && !!orgId,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    })),
  })
}

type AgentRecord = ReturnType<typeof useAgents>['data'] extends
  | Array<infer T>
  | undefined
  ? T
  : never

function toProjected(agent: AgentRecord): ProjectedAgent {
  return {
    id: agent.id,
    name: agent.name,
    description: agent.description,
    model: agent.model,
    systemPrompt: agent.systemPrompt,
    tools: agent.tools,
    config: agent.config,
    maxTurns: agent.maxTurns,
    maxToolCallsPerTurn: agent.maxToolCallsPerTurn,
    maxSteps: agent.maxSteps,
    taskPermissionMode: agent.taskPermissionMode,
    updatedAt: toDate(agent.updatedAt) ?? undefined,
  }
}

const LANGUAGE_ICONS: Record<string, string> = {
  yaml: 'vscode-icons:file-type-yaml',
  markdown: 'vscode-icons:file-type-markdown',
  typescript: 'vscode-icons:file-type-typescript',
  javascript: 'vscode-icons:file-type-js',
  python: 'vscode-icons:file-type-python',
  json: 'vscode-icons:file-type-json',
}

function SyncBadge({
  sync,
  agentName,
}: {
  sync: SyncState
  agentName: string
}) {
  if (sync.kind === 'unmanaged') {
    return (
      <div className="flex items-center gap-2">
        <span className="inline-flex items-center gap-1.5 rounded bg-brand-main-800/70 px-2 py-1 text-[11px] text-white/55 light:text-black/55">
          <span className="size-1.5 rounded-full bg-white/35" />
          Not managed by evs
        </span>
        <span className="text-[11px] text-white/35 light:text-black/35">
          Created in the dashboard, never deployed from a project
        </span>
      </div>
    )
  }

  if (sync.kind === 'drifted') {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <span className="inline-flex items-center gap-1.5 rounded bg-amber-500/15 px-2 py-1 text-[11px] text-amber-300">
          <span className="size-1.5 rounded-full bg-amber-400" />
          Drifted
        </span>
        <span className="text-[11px] text-white/45 light:text-black/45">
          Edited in the dashboard since the last deploy. Reconcile with
        </span>
        <code className="rounded bg-brand-main-900/70 px-1.5 py-0.5 font-mono text-[11px] text-brand-secondary-300">
          evs agents pull {agentName}
        </code>
      </div>
    )
  }

  return (
    <span className="inline-flex items-center gap-1.5 rounded bg-emerald-500/15 px-2 py-1 text-[11px] text-emerald-300">
      <span className="size-1.5 rounded-full bg-emerald-400" />
      In sync with the last deploy
    </span>
  )
}

function FileTree({
  nodes,
  selectedPath,
  onSelect,
  depth = 0,
}: {
  nodes: TreeNode[]
  selectedPath: string | null
  onSelect: (file: WorkspaceFile) => void
  depth?: number
}) {
  return (
    <div className="space-y-0.5">
      {nodes.map((node) => {
        if (node.children) {
          return (
            <div key={node.path}>
              <div
                className="flex items-center gap-1.5 px-2 py-1 text-[12px] text-white/45 light:text-black/45"
                style={{ paddingLeft: `${depth * 12 + 8}px` }}
              >
                <Iconify.Icon
                  icon="lucide:folder"
                  className="size-3.5 shrink-0 text-brand-secondary-400/70"
                />
                <span className="truncate">{node.name}/</span>
              </div>
              <FileTree
                nodes={node.children}
                selectedPath={selectedPath}
                onSelect={onSelect}
                depth={depth + 1}
              />
            </div>
          )
        }

        const isSelected = node.path === selectedPath
        return (
          <button
            key={node.path}
            type="button"
            onClick={() => node.file && onSelect(node.file)}
            style={{ paddingLeft: `${depth * 12 + 8}px` }}
            className={cn(
              'flex w-full items-center gap-1.5 rounded px-2 py-1 text-left text-[12px] transition-colors',
              isSelected
                ? 'bg-brand-secondary-600/20 text-brand-secondary-200'
                : 'text-white/65 hover:bg-white/[0.04] light:text-black/65',
            )}
          >
            <Iconify.Icon
              icon={
                LANGUAGE_ICONS[node.file?.language ?? 'json'] ?? 'lucide:file'
              }
              className="size-3.5 shrink-0"
            />
            <span className="truncate">{node.name}</span>
          </button>
        )
      })}
    </div>
  )
}

export function AgentWorkspace() {
  const { data: agents = [], isLoading, error } = useAgents()
  const orgId = useOrganizationId()
  const [agentId, setAgentId] = useState<string>('')
  const [selectedPath, setSelectedPath] = useState<string | null>(null)

  const rootAgent = useMemo(
    () => agents.find((a) => a.id === agentId) ?? agents[0],
    [agents, agentId],
  )

  const { data: functions = [] } = useFunctions()
  const { data: links = [] } = useAgentLinks(rootAgent?.id ?? '')

  const subagents = useMemo(() => {
    const byId = new Map(agents.map((a) => [a.id, a]))
    return links
      .filter(
        (link) =>
          link.linkType === AgentLinkType.SUBORDINATE &&
          link.targetType === 'agent',
      )
      .map((link) => byId.get(link.targetId))
      .filter((a): a is AgentRecord => !!a)
  }, [agents, links])

  const triggerAgentIds = useMemo(
    () =>
      [rootAgent?.id, ...subagents.map((a) => a.id)].filter(
        (id): id is string => !!id,
      ),
    [rootAgent, subagents],
  )
  const triggerQueries = useTriggersForAgents(triggerAgentIds)
  const revisionQueries = useQueries({
    queries: triggerAgentIds.map((id) => ({
      queryKey: ['agent-revisions', orgId, id],
      queryFn: () => getActiveAgentRevision(id, orgId),
      enabled: !!id && !!orgId,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    })),
  })
  const revisionError = revisionQueries.find((query) => query.error)?.error

  const workspace = useMemo(() => {
    if (!rootAgent) return null

    const fnMap = new Map<string, ProjectedFunction>()
    for (const fn of functions) {
      if (fn.isolated?.code) {
        fnMap.set(fn.name, {
          name: fn.name,
          runtime: fn.isolated.runtime,
          code: fn.isolated.code,
        })
      }
    }

    const triggersFor = (id: string): ProjectedTrigger[] => {
      const index = triggerAgentIds.indexOf(id)
      return index >= 0 ? (triggerQueries[index]?.data ?? []) : []
    }

    const revisionFor = (id: string): ProjectedRevision | undefined => {
      const index = triggerAgentIds.indexOf(id)
      return index >= 0 ? (revisionQueries[index]?.data ?? undefined) : undefined
    }

    return buildAgentWorkspace({
      agent: toProjected(rootAgent),
      revision: revisionFor(rootAgent.id),
      functions: fnMap,
      triggers: triggersFor(rootAgent.id),
      subagents: subagents.map((a) => ({
        agent: toProjected(a),
        revision: revisionFor(a.id),
        triggers: triggersFor(a.id),
      })),
    })
  }, [
    functions,
    revisionQueries,
    rootAgent,
    subagents,
    triggerAgentIds,
    triggerQueries,
  ])

  const tree = useMemo(
    () => (workspace ? buildFileTree(workspace.files) : []),
    [workspace],
  )

  // Default to agent.yaml, and fall back whenever the selection is no longer
  // part of the current agent's project.
  const activeFile = useMemo(() => {
    if (!workspace) return null
    return (
      workspace.files.find((f) => f.path === selectedPath) ??
      workspace.files.find((f) => f.path === 'agent.yaml') ??
      workspace.files[0] ??
      null
    )
  }, [selectedPath, workspace])

  if (isLoading || revisionQueries.some((query) => query.isLoading)) {
    return (
      <div className="flex h-full items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading agents..." />
      </div>
    )
  }

  if (error || revisionError) {
    const loadError = error ?? revisionError
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
        <Iconify.Icon
          icon="lucide:triangle-alert"
          className="size-6 text-red-300"
        />
        <p className="text-sm text-white light:text-brand-main-50">
          Agent workspace could not be loaded
        </p>
        <p className="text-xs text-white/45 light:text-black/45">
          {loadError instanceof Error ? loadError.message : String(loadError)}
        </p>
      </div>
    )
  }

  if (!rootAgent || !workspace) {
    return (
      <div className="flex h-full flex-col items-center justify-center px-6 text-center">
        <div className="mb-4 flex size-11 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/80 text-brand-secondary-300">
          <Iconify.Icon icon="lucide:folder-tree" className="size-5" />
        </div>
        <p className="text-sm font-medium text-white light:text-brand-main-50">
          No agents yet
        </p>
        <p className="mt-1 max-w-sm text-xs text-white/45 light:text-black/45">
          Scaffold one with <code className="font-mono">evs init</code>, then{' '}
          <code className="font-mono">evs deploy</code> to see its project here.
        </p>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-brand-main-800/40 bg-brand-main-900/20 px-3 py-2">
        <Select
          value={rootAgent.id}
          onValueChange={(value) => {
            setAgentId(value)
            setSelectedPath(null)
          }}
        >
          <SelectTrigger className="h-8 w-[220px] bg-brand-main-900/60 text-xs">
            <SelectValue placeholder="Agent" />
          </SelectTrigger>
          <SelectContent>
            {agents.map((a) => (
              <SelectItem key={a.id} value={a.id}>
                {a.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <SyncBadge sync={workspace.sync} agentName={rootAgent.name} />

        {workspace.revision && (
          <span className="rounded bg-brand-main-800/70 px-2 py-1 font-mono text-[11px] text-white/45 light:text-black/45">
            revision {workspace.revision.number} ·{' '}
            {workspace.revision.digest.slice(0, 8)}
          </span>
        )}

        <div className="ml-auto flex items-center gap-2">
          <span className="text-[11px] text-white/35 light:text-black/35">
            Read-only. Change this project with the evs CLI.
          </span>
        </div>
      </div>

      <div className="grid min-h-0 flex-1 grid-rows-[minmax(0,35%)_minmax(0,1fr)] overflow-hidden lg:grid-cols-[260px_minmax(0,1fr)] lg:grid-rows-1">
        <aside className="min-h-0 min-w-0 overflow-y-auto border-b border-brand-main-800/40 p-2 lg:border-b-0 lg:border-r">
          <div className="mb-1.5 flex items-center gap-1.5 px-2 text-[11px] uppercase tracking-wide text-white/35 light:text-black/35">
            <Iconify.Icon icon="lucide:folder-open" className="size-3.5" />
            {rootAgent.name}/
          </div>
          <FileTree
            nodes={tree}
            selectedPath={activeFile?.path ?? null}
            onSelect={(file) => setSelectedPath(file.path)}
          />
        </aside>

        <section className="flex min-h-0 min-w-0 flex-col overflow-hidden">
          {activeFile ? (
            <>
              <div className="flex h-9 shrink-0 items-center justify-between gap-3 border-b border-brand-main-800/40 px-3">
                <span className="truncate font-mono text-[11px] text-white/60 light:text-black/60">
                  {activeFile.path}
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 shrink-0 px-2 text-[11px]"
                  onClick={() => {
                    void navigator.clipboard?.writeText(activeFile.content)
                  }}
                >
                  <Iconify.Icon icon="lucide:copy" className="mr-1.5 size-3" />
                  Copy
                </Button>
              </div>
              <div className="min-h-0 flex-1">
                <Editor
                  key={activeFile.path}
                  height="100%"
                  language={activeFile.language}
                  value={activeFile.content}
                  beforeMount={(monaco: Monaco) =>
                    monaco.editor.defineTheme(
                      'material-ocean',
                      materialOceanTheme,
                    )
                  }
                  theme="material-ocean"
                  options={{
                    readOnly: true,
                    domReadOnly: true,
                    automaticLayout: true,
                    minimap: { enabled: false },
                    scrollBeyondLastLine: false,
                    fontSize: 13,
                    lineNumbers: 'on',
                    folding: true,
                    wordWrap: 'on',
                  }}
                  loading={
                    <div className="flex h-full items-center justify-center text-sm text-brand-main-400">
                      Loading viewer...
                    </div>
                  }
                />
              </div>
            </>
          ) : null}
        </section>
      </div>
    </div>
  )
}
