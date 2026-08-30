import yaml from 'js-yaml'
import { TaskPermissionMode } from '@everstack/proto/everstack/agents/v1/agents_pb'

/**
 * Projects a deployed agent back into the `evs` directory project format.
 *
 * This is the browser-side mirror of `evs agents pull` (cmd/agents/pull.go).
 * The two must agree: what this renders is what pulling the agent would write
 * to disk, so any change to the CLI exporter belongs here too.
 *
 * Kept free of React and of data-fetching so the mapping can be tested on
 * plain objects.
 */

/** Agent-config key `evs deploy` stamps the deployed project state under. */
export const PROJECT_META_KEY = 'agentproject'

/**
 * Mirrors the 5s grace window in checkDrift (cmd/agents/deploy.go): the
 * update that writes the stamp bumps updated_at fractionally after it.
 */
const DRIFT_GRACE_MS = 5_000

export type WorkspaceLanguage =
  | 'yaml'
  | 'markdown'
  | 'typescript'
  | 'javascript'
  | 'python'
  | 'json'
  | 'plaintext'

export interface WorkspaceFile {
  /** POSIX path relative to the workspace root, e.g. tools/get_time.ts */
  path: string
  language: WorkspaceLanguage
  content: string
}

export type SyncState =
  | { kind: 'in_sync'; deployedAt: string }
  | { kind: 'drifted'; deployedAt: string; updatedAt: string }
  | { kind: 'unmanaged' }

export interface AgentWorkspace {
  name: string
  files: WorkspaceFile[]
  sync: SyncState
  revision?: { id: string; number: number; digest: string }
}

/** The subset of an agent definition the projection reads. */
export interface ProjectedAgent {
  id: string
  name: string
  description?: string
  model: string
  systemPrompt?: string
  tools?: string[]
  config?: unknown
  maxTurns?: number
  maxToolCallsPerTurn?: number
  maxSteps?: number
  taskPermissionMode?: number
  updatedAt?: Date
}

/** The subset of a platform function the projection reads. */
export interface ProjectedFunction {
  name: string
  runtime?: string
  code?: string
}

export interface ProjectedRevision {
  id: string
  number: number
  digest: string
  files: Array<{ path: string; content: Uint8Array }>
}

/** The subset of an agent trigger the projection reads. */
export interface ProjectedTrigger {
  triggerType?: string
  name?: string
  cronExpression?: string
  cronTimezone?: string
  inputTemplate?: string
  eventType?: string
}

export interface WorkspaceInput {
  agent: ProjectedAgent
  revision?: ProjectedRevision
  /** Function-backed tools, keyed by tool name. */
  functions: Map<string, ProjectedFunction>
  triggers: ProjectedTrigger[]
  subagents: Array<{
    agent: ProjectedAgent
    revision?: ProjectedRevision
    triggers: ProjectedTrigger[]
  }>
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return value as Record<string, unknown>
}

/** Runtime to file extension, matching pullToolSource in pull.go. */
function extensionFor(runtime?: string): {
  ext: string
  language: WorkspaceLanguage
} {
  if (runtime === 'deno') return { ext: '.ts', language: 'typescript' }
  if (runtime === 'python3') return { ext: '.py', language: 'python' }
  return { ext: '.js', language: 'javascript' }
}

function languageForPath(path: string): WorkspaceLanguage {
  const extension = path.split('.').pop()?.toLowerCase()
  if (extension === 'yaml' || extension === 'yml') return 'yaml'
  if (extension === 'md' || extension === 'mdx') return 'markdown'
  if (extension === 'ts' || extension === 'tsx') return 'typescript'
  if (extension === 'js' || extension === 'mjs' || extension === 'jsx')
    return 'javascript'
  if (extension === 'py') return 'python'
  if (extension === 'json') return 'json'
  return 'plaintext'
}

const decoder = new TextDecoder('utf-8', { fatal: true })

function revisionFiles(
  revision: ProjectedRevision,
  prefix = '',
): WorkspaceFile[] {
  return revision.files.map((file) => {
    let content: string
    try {
      content = decoder.decode(file.content)
    } catch {
      content = `Binary file (${file.content.byteLength} bytes)`
    }
    return {
      path: `${prefix}${file.path}`,
      language: languageForPath(file.path),
      content,
    }
  })
}

function revisionSubagentPath(
  root: ProjectedAgent,
  revision: ProjectedRevision,
  subagentName: string,
): string {
  const meta = asRecord(asRecord(root.config)[PROJECT_META_KEY])
  const stampedPaths = asRecord(meta.subagent_paths)
  const stamped = stampedPaths[subagentName]
  if (typeof stamped === 'string' && stamped.trim()) {
    return stamped.replace(/^\.\//, '').replace(/\/$/, '')
  }

  const source = revision.files.find((file) => file.path === 'agent.yaml')
  if (source) {
    try {
      const doc = asRecord(yaml.load(decoder.decode(source.content)))
      const declared = Array.isArray(doc.subagents) ? doc.subagents : []
      const matched = declared.find((path) => {
        if (typeof path !== 'string') return false
        return (
          path.replace(/^\.\//, '').replace(/\/$/, '').split('/').pop() ===
          subagentName
        )
      })
      if (typeof matched === 'string') {
        return matched.replace(/^\.\//, '').replace(/\/$/, '')
      }
    } catch {
      // The server and CLI validate revision YAML. The workspace can still
      // display it if an older deployment contains malformed source.
    }
  }
  return `subagents/${subagentName}`
}

const TASK_MODE_NAMES: Record<number, string> = {
  [TaskPermissionMode.ASK]: 'ask',
  [TaskPermissionMode.ALWAYS]: 'always',
  [TaskPermissionMode.DENY]: 'deny',
}

function triggerEntry(trigger: ProjectedTrigger): Record<string, unknown> {
  const entry: Record<string, unknown> = {
    type: trigger.triggerType ?? '',
    name: trigger.name ?? '',
  }
  if (trigger.cronExpression) entry.schedule = trigger.cronExpression
  if (trigger.cronTimezone) entry.timezone = trigger.cronTimezone
  if (trigger.inputTemplate) entry.input = trigger.inputTemplate
  if (trigger.eventType) entry.event = trigger.eventType
  return entry
}

/**
 * Projects one agent into files under `prefix`. subagentPaths is threaded in
 * rather than derived here so a nested project never emits its own
 * `subagents:` key, matching the one-level limit the loader enforces.
 */
function projectAgent(
  agent: ProjectedAgent,
  functions: Map<string, ProjectedFunction>,
  triggers: ProjectedTrigger[],
  prefix: string,
  subagentPaths: string[],
): WorkspaceFile[] {
  const files: WorkspaceFile[] = []
  const config = { ...asRecord(agent.config) }

  files.push({
    path: `${prefix}instructions.md`,
    language: 'markdown',
    content: `${agent.systemPrompt ?? ''}\n`,
  })

  // skills/ from the inline config skills array.
  const skillPaths: string[] = []
  const rawSkills = config.skills
  if (Array.isArray(rawSkills)) {
    for (const raw of rawSkills) {
      const skill = asRecord(raw)
      const name = typeof skill.name === 'string' ? skill.name : ''
      const content = typeof skill.content === 'string' ? skill.content : ''
      if (!name || !content) continue
      files.push({
        path: `${prefix}skills/${name}/SKILL.md`,
        language: 'markdown',
        content,
      })
      skillPaths.push(`./skills/${name}`)
    }
  }

  // tools/: function-backed tools carry their source; the rest stay as
  // builtin names in agent.yaml with no file on disk.
  const toolEntries: string[] = []
  for (const tool of agent.tools ?? []) {
    const fn = functions.get(tool)
    if (fn?.code) {
      const { ext, language } = extensionFor(fn.runtime)
      files.push({
        path: `${prefix}tools/${tool}${ext}`,
        language,
        content: fn.code,
      })
      toolEntries.push(`./tools/${tool}${ext}`)
    } else {
      toolEntries.push(tool)
    }
  }

  // Platform-managed keys never appear in the exported agent.yaml.
  delete config.skills
  delete config[PROJECT_META_KEY]

  const limits: Record<string, unknown> = {
    max_turns: agent.maxTurns ?? 0,
    max_tool_calls_per_turn: agent.maxToolCallsPerTurn ?? 0,
  }
  if (agent.maxSteps && agent.maxSteps > 0) limits.max_steps = agent.maxSteps

  const doc: Record<string, unknown> = {
    name: agent.name,
    description: agent.description ?? '',
    model: agent.model,
    instructions: './instructions.md',
    limits,
    tools: toolEntries,
  }
  const taskMode = TASK_MODE_NAMES[agent.taskPermissionMode ?? 0]
  if (taskMode) doc.permissions = { task_mode: taskMode }
  if (Object.keys(config).length > 0) doc.config = config
  if (skillPaths.length > 0) doc.skills = skillPaths
  if (subagentPaths.length > 0) doc.subagents = subagentPaths
  if (triggers.length > 0) doc.triggers = triggers.map(triggerEntry)

  files.unshift({
    path: `${prefix}agent.yaml`,
    language: 'yaml',
    content: yaml.dump(doc, { lineWidth: 100, noRefs: true }),
  })

  return files
}

/**
 * Compares the deploy stamp against the agent's updated_at, reproducing the
 * decision checkDrift makes server-side. An agent with no stamp was never
 * deployed by `evs deploy` and is reported as unmanaged rather than drifted.
 */
export function syncStateFor(agent: ProjectedAgent): SyncState {
  const meta = asRecord(asRecord(agent.config)[PROJECT_META_KEY])
  const deployedAtRaw =
    typeof meta.deployed_at === 'string' ? meta.deployed_at : ''
  if (!deployedAtRaw) return { kind: 'unmanaged' }

  const deployedAt = new Date(deployedAtRaw)
  if (Number.isNaN(deployedAt.getTime()) || !agent.updatedAt) {
    // An unreadable stamp does not block the CLI, so do not claim drift here.
    return { kind: 'in_sync', deployedAt: deployedAtRaw }
  }

  if (agent.updatedAt.getTime() > deployedAt.getTime() + DRIFT_GRACE_MS) {
    return {
      kind: 'drifted',
      deployedAt: deployedAtRaw,
      updatedAt: agent.updatedAt.toISOString(),
    }
  }
  return { kind: 'in_sync', deployedAt: deployedAtRaw }
}

/** Builds the full workspace for one agent, including its subagents. */
export function buildAgentWorkspace(input: WorkspaceInput): AgentWorkspace {
  if (input.revision) {
    const files = revisionFiles(input.revision)
    for (const sub of input.subagents) {
      if (!sub.agent.name) continue
      const prefix = `${revisionSubagentPath(input.agent, input.revision, sub.agent.name)}/`
      if (sub.revision) {
        files.push(...revisionFiles(sub.revision, prefix))
        continue
      }
      const legacy = buildAgentWorkspace({
        agent: sub.agent,
        functions: input.functions,
        triggers: sub.triggers,
        subagents: [],
      })
      files.push(
        ...legacy.files.map((file) => ({ ...file, path: `${prefix}${file.path}` })),
      )
    }
    return {
      name: input.agent.name,
      files,
      sync: syncStateFor(input.agent),
      revision: {
        id: input.revision.id,
        number: input.revision.number,
        digest: input.revision.digest,
      },
    }
  }

  const subagentPaths: string[] = []
  const subagentFiles: WorkspaceFile[] = []

  for (const sub of input.subagents) {
    if (!sub.agent.name) continue
    subagentFiles.push(
      ...projectAgent(
        sub.agent,
        input.functions,
        sub.triggers,
        `subagents/${sub.agent.name}/`,
        [],
      ),
    )
    subagentPaths.push(`./subagents/${sub.agent.name}`)
  }

  const rootFiles = projectAgent(
    input.agent,
    input.functions,
    input.triggers,
    '',
    subagentPaths,
  )

  return {
    name: input.agent.name,
    files: [...rootFiles, ...subagentFiles],
    sync: syncStateFor(input.agent),
  }
}

// --- tree shaping -----------------------------------------------------------

export interface TreeNode {
  name: string
  path: string
  /** Directories have children; files carry an index into the file list. */
  children?: TreeNode[]
  file?: WorkspaceFile
}

/**
 * Turns flat POSIX paths into a directory tree. Directories sort first, then
 * files, each alphabetically, so the shape is stable across renders.
 */
export function buildFileTree(files: WorkspaceFile[]): TreeNode[] {
  const root: TreeNode = { name: '', path: '', children: [] }

  for (const file of files) {
    const segments = file.path.split('/')
    let node = root
    for (let i = 0; i < segments.length; i++) {
      const segment = segments[i]
      const isLeaf = i === segments.length - 1
      const path = segments.slice(0, i + 1).join('/')

      node.children ??= []
      let next = node.children.find((child) => child.name === segment)
      if (!next) {
        next = isLeaf
          ? { name: segment, path, file }
          : { name: segment, path, children: [] }
        node.children.push(next)
      }
      node = next
    }
  }

  const sort = (nodes: TreeNode[]): TreeNode[] => {
    nodes.sort((a, b) => {
      const aDir = a.children ? 0 : 1
      const bDir = b.children ? 0 : 1
      if (aDir !== bDir) return aDir - bDir
      return a.name.localeCompare(b.name)
    })
    for (const node of nodes) if (node.children) sort(node.children)
    return nodes
  }

  return sort(root.children ?? [])
}
