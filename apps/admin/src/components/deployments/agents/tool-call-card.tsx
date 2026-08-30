import { useState, useMemo, lazy, Suspense } from 'react'
import {
  Terminal,
  Database,
  ChevronRight,
  ChevronDown,
  Box,
  ExternalLink,
  GitBranch,
  Globe,
  Workflow,
  Sparkles,
  Clock,
} from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { ui } from '@everstack/ui'

import { AgentMarkdown } from './agent-markdown'

const WorkflowResultCard = lazy(() =>
  import('./workflow-result-card').then((m) => ({
    default: m.WorkflowResultCard,
  })),
)

const { Collapsible, CollapsibleContent, CollapsibleTrigger } = ui

export const SANDBOX_TOOLS = new Set([
  'sandbox_execute',
  'sandbox_shell',
  'sandbox_write_file',
  'sandbox_read_file',
  'sandbox_list_files',
  'sandbox_expose_port',
  'sandbox_unexpose_port',
  'sandbox_list_ports',
  'sandbox_list_templates',
  'sandbox_set_template',
])

export const SANDBOX_TOOL_LABELS: Record<string, string> = {
  sandbox_execute: 'Terminal',
  sandbox_shell: 'Terminal',
  sandbox_write_file: 'Write File',
  sandbox_read_file: 'Read File',
  sandbox_list_files: 'List Files',
  sandbox_expose_port: 'Expose Port',
  sandbox_unexpose_port: 'Close Port',
  sandbox_list_ports: 'List Ports',
  sandbox_list_templates: 'List Templates',
  sandbox_set_template: 'Set Template',
}

const SPAWN_TOOLS = new Set(['spawn_agent'])
const MEMORY_TOOLS = new Set(['memory_store', 'memory_query', 'memory_search'])
const WEB_TOOLS = new Set(['web_search', 'web_fetch'])
const WORKFLOW_TOOLS = new Set(['create_workflow'])
const SKILL_TOOLS = new Set(['use_skill'])

const TOOL_LABELS: Record<string, string> = {
  ...{
    sandbox_execute: 'Terminal',
    sandbox_shell: 'Terminal',
    sandbox_write_file: 'Write File',
    sandbox_read_file: 'Read File',
    sandbox_list_files: 'List Files',
    sandbox_expose_port: 'Expose Port',
    sandbox_unexpose_port: 'Close Port',
    sandbox_list_ports: 'List Ports',
    sandbox_list_templates: 'List Templates',
    sandbox_set_template: 'Set Template',
  },
  browser_navigate: 'Browser Navigate',
  browser_observe: 'Browser Observe',
  browser_click: 'Browser Click',
  browser_type: 'Browser Type',
  browser_screenshot: 'Browser Screenshot',
  browser_evaluate: 'Browser Evaluate',
  browser_wait: 'Browser Wait',
  browser_scroll: 'Browser Scroll',
  browser_select: 'Browser Select',
  browser_tabs: 'Browser Tabs',
  web_search: 'Web Search',
  web_fetch: 'Web Fetch',
  create_workflow: 'Create Workflow',
  spawn_agent: 'Spawn Agent',
  memory_store: 'Store Memory',
  memory_query: 'Query Memory',
  memory_search: 'Search Memory',
  create_trigger: 'Create Automation',
  schedule_cron: 'Schedule Cron',
  use_skill: 'Use Skill',
}

function humanizeToolName(toolName: string) {
  return toolName
    .replace(/^sandbox_/, '')
    .replace(/^browser_/, '')
    .replace(/^memory_/, '')
    .replace(/^web_/, '')
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (match) => match.toUpperCase())
}

interface ToolCallCardProps {
  toolCallId: string
  toolName: string
  toolArgs?: string
  toolResult?: string
  toolSuccess?: boolean
  toolDurationMs?: number
  status?: 'running' | 'done' | 'failed'
  sandboxId?: string
  sandboxExitCode?: number
  sandboxDurationMs?: number
  sandboxParentDurationMs?: number
  sessionId?: string
  agentId?: string
}

function tryParseJson(str: string | undefined): any | null {
  if (!str) return null
  try {
    return JSON.parse(str)
  } catch {
    return null
  }
}

/** Compact stdout preview shown when a sandbox tool card is collapsed */
function SandboxPreview({ result }: { result: string }) {
  const parsed = tryParseJson(result)
  const output = parsed
    ? (parsed.stdout ?? parsed.output ?? parsed.result)
    : result

  if (!output || typeof output !== 'string') return null

  const lines = output.split('\n')
  const preview = lines.slice(0, 5).join('\n')
  const truncated = lines.length > 5

  return (
    <div className="px-3 pb-1.5 -mt-0.5">
      <pre className="text-[10px] font-mono text-white/35 whitespace-pre-wrap break-all leading-tight light:text-black/35">
        {preview}
        {truncated && '\n...'}
      </pre>
    </div>
  )
}

/** brand-secondary-tinted banner showing sandbox metadata and "View Sandbox" link */
function SandboxBanner({
  sandboxId,
  sandboxExitCode,
  sandboxDurationMs,
  sandboxParentDurationMs,
  sessionId,
  status,
}: {
  sandboxId: string
  sandboxExitCode?: number
  sandboxDurationMs?: number
  sandboxParentDurationMs?: number
  sessionId?: string
  status?: 'running' | 'done' | 'failed'
}) {
  return (
    <div className="flex items-center gap-2 px-3 py-1.5 bg-brand-secondary-500/8 border-t border-brand-secondary-500/10 text-[10px]">
      <Box className="w-3 h-3 text-brand-secondary-400/70 shrink-0" />
      <span className="font-mono text-brand-secondary-400/60">
        {sandboxId.slice(0, 12)}
      </span>
      {status === 'running' && (
        <span className="flex items-center gap-1 text-brand-secondary-300">
          <span className="inline-block w-1.5 h-1.5 rounded-full bg-brand-secondary-400 animate-pulse" />
          Executing in sandbox
        </span>
      )}
      {sandboxExitCode != null && status !== 'running' && (
        <span
          className={`px-1.5 py-0.5 rounded font-mono font-medium ${
            sandboxExitCode === 0
              ? 'bg-green-500/15 text-green-400'
              : 'bg-red-500/15 text-red-400'
          }`}
        >
          exit {sandboxExitCode}
        </span>
      )}
      {sandboxDurationMs != null && sandboxDurationMs > 0 && (
        <span className="text-white/30 light:text-black/30">{sandboxDurationMs}ms</span>
      )}
      {sandboxParentDurationMs != null && sandboxParentDurationMs > 0 && (
        <span className="text-white/30 light:text-black/30">total {sandboxParentDurationMs}ms</span>
      )}
      {sessionId && (
        <Link
          to="/deployments/sandboxes"
          search={{ tab: 'instances' }}
          target="_blank"
          className="ml-auto flex items-center gap-1 text-brand-secondary-400/70 hover:text-brand-secondary-300 transition-colors"
        >
          View Sandboxes <ExternalLink className="w-2.5 h-2.5" />
        </Link>
      )}
    </div>
  )
}

/** Render sandbox tool arguments: adapts display based on tool name */
function SandboxArgs({ args, toolName }: { args: string; toolName: string }) {
  const parsed = tryParseJson(args)
  if (!parsed) return <GenericJson text={args} />

  // File-based sandbox tools: show path prominently
  if (
    toolName === 'sandbox_write_file' ||
    toolName === 'sandbox_read_file' ||
    toolName === 'sandbox_list_files'
  ) {
    const path = parsed.path ?? parsed.file_path ?? parsed.directory
    const content = parsed.content ?? parsed.data
    const rest = { ...parsed }
    delete rest.path
    delete rest.file_path
    delete rest.directory
    delete rest.content
    delete rest.data

    return (
      <div className="space-y-2">
        {path && (
          <div>
            <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
              Path
            </div>
            <div className="flex items-start gap-2 rounded bg-black/30 border border-brand-main-800/40 px-3 py-2">
              <Box className="w-3.5 h-3.5 text-brand-secondary-400/70 shrink-0 mt-0.5" />
              <code className="text-[11px] font-mono text-brand-secondary-300/90 whitespace-pre-wrap break-all">
                {path}
              </code>
            </div>
          </div>
        )}
        {content && toolName === 'sandbox_write_file' && (
          <div>
            <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
              Content
            </div>
            <div className="rounded bg-black/30 border border-brand-main-800/40 px-3 py-2 max-h-48 overflow-y-auto">
              <pre className="text-[11px] font-mono text-white/70 whitespace-pre-wrap break-all light:text-black/70">
                {content}
              </pre>
            </div>
          </div>
        )}
        {Object.keys(rest).length > 0 && (
          <GenericJson text={JSON.stringify(rest, null, 2)} label="Options" />
        )}
      </div>
    )
  }

  // sandbox_execute / sandbox_shell: existing command/code logic
  const command = parsed.command ?? parsed.cmd ?? parsed.script
  const code = parsed.code
  const language = parsed.language ?? parsed.lang ?? ''
  const rest = { ...parsed }
  delete rest.command
  delete rest.cmd
  delete rest.script
  delete rest.code
  delete rest.language
  delete rest.lang

  return (
    <div className="space-y-2">
      {command && (
        <div>
          <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
            Command
          </div>
          <div className="flex items-start gap-2 rounded bg-black/30 border border-brand-main-800/40 px-3 py-2">
            <Terminal className="w-3.5 h-3.5 text-green-400/70 shrink-0 mt-0.5" />
            <code className="text-[11px] font-mono text-green-300/90 whitespace-pre-wrap break-all">
              {command}
            </code>
          </div>
        </div>
      )}
      {code && (
        <div>
          <div className="flex items-center gap-2 mb-1">
            <span className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30">
              Code
            </span>
            {language && (
              <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-brand-secondary-500/15 text-brand-secondary-300">
                {language}
              </span>
            )}
          </div>
          <div className="rounded bg-black/30 border border-brand-main-800/40 max-h-72 overflow-y-auto">
            <AgentMarkdown>{`\`\`\`${language}\n${code}\n\`\`\``}</AgentMarkdown>
          </div>
        </div>
      )}
      {Object.keys(rest).length > 0 && (
        <GenericJson text={JSON.stringify(rest, null, 2)} label="Options" />
      )}
    </div>
  )
}

/** Render port tool arguments: show port number and protocol */
function PortArgs({ args, toolName }: { args: string; toolName: string }) {
  const parsed = tryParseJson(args)
  if (!parsed) return <GenericJson text={args} />

  const port = parsed.port
  const protocol = parsed.protocol

  return (
    <div className="space-y-2">
      {port != null && (
        <div className="flex items-center gap-2">
          <span className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30">
            Port
          </span>
          <code className="text-[11px] font-mono text-brand-secondary-300/90 px-1.5 py-0.5 rounded bg-brand-secondary-500/10">
            {port}
          </code>
          {protocol && protocol !== 'tcp' && (
            <span className="text-[10px] font-mono text-white/30 light:text-black/30">
              {protocol}
            </span>
          )}
        </div>
      )}
      {toolName === 'sandbox_list_ports' && (
        <p className="text-[11px] text-white/40 light:text-black/40">Listing all exposed ports</p>
      )}
    </div>
  )
}

/** Render port exposure result: show URL as clickable link */
function PortResult({ result }: { result: string }) {
  const urlMatch = result.match(/(https?:\/\/\S+)/)
  if (urlMatch) {
    return (
      <div>
        <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
          Exposed URL
        </div>
        <div className="flex items-center gap-2 rounded bg-brand-secondary-500/8 border border-brand-secondary-500/15 px-3 py-2">
          <ExternalLink className="w-3.5 h-3.5 text-brand-secondary-400/70 shrink-0" />
          <a
            href={urlMatch[1]}
            target="_blank"
            rel="noopener noreferrer"
            className="text-[11px] font-mono text-brand-secondary-300 hover:text-brand-secondary-200 underline underline-offset-2 decoration-brand-secondary-500/30 hover:decoration-brand-secondary-400/50 transition-colors break-all"
          >
            {urlMatch[1]}
          </a>
        </div>
      </div>
    )
  }

  // Fallback: plain text result (e.g. unexpose confirmation, list output)
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
        Result
      </div>
      <div className="rounded bg-black/30 border border-brand-main-800/40 px-3 py-2 max-h-64 overflow-y-auto">
        <pre className="text-[11px] font-mono text-white/70 whitespace-pre-wrap break-all light:text-black/70">
          {result}
        </pre>
      </div>
    </div>
  )
}

/** Render sandbox_execute result: terminal-style stdout/stderr */
function SandboxResult({ result }: { result: string }) {
  const parsed = tryParseJson(result)

  // Plain text result — render as terminal output
  if (!parsed) {
    return (
      <div>
        <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
          Output
        </div>
        <div className="rounded bg-black/30 border border-brand-main-800/40 px-3 py-2 max-h-64 overflow-y-auto">
          <pre className="text-[11px] font-mono text-white/70 whitespace-pre-wrap break-all light:text-black/70">
            {result}
          </pre>
        </div>
      </div>
    )
  }

  const stdout = parsed.stdout ?? parsed.output ?? parsed.result
  const stderr = parsed.stderr ?? parsed.error_output
  const exitCode = parsed.exit_code ?? parsed.exitCode ?? parsed.return_code

  // If no recognizable fields, fall back to formatted JSON
  if (stdout == null && stderr == null && exitCode == null) {
    return <GenericJson text={JSON.stringify(parsed, null, 2)} label="Result" />
  }

  return (
    <div className="space-y-2">
      {exitCode != null && (
        <div className="flex items-center gap-2">
          <span className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30">
            Exit code
          </span>
          <span
            className={`px-1.5 py-0.5 rounded text-[10px] font-mono font-medium ${
              exitCode === 0
                ? 'bg-green-500/15 text-green-400'
                : 'bg-red-500/15 text-red-400'
            }`}
          >
            {exitCode}
          </span>
        </div>
      )}
      {stdout && (
        <div>
          <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
            Output
          </div>
          <div className="rounded bg-black/30 border border-brand-main-800/40 px-3 py-2 max-h-64 overflow-y-auto">
            <pre className="text-[11px] font-mono text-white/70 whitespace-pre-wrap break-all light:text-black/70">
              {stdout}
            </pre>
          </div>
        </div>
      )}
      {stderr && (
        <div>
          <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
            Stderr
          </div>
          <div className="rounded bg-red-950/30 border border-red-500/15 px-3 py-2 max-h-48 overflow-y-auto">
            <pre className="text-[11px] font-mono text-red-300/80 whitespace-pre-wrap break-all">
              {stderr}
            </pre>
          </div>
        </div>
      )}
    </div>
  )
}

/** Render spawn_agent arguments: show task and config inline */
function SpawnArgs({ args }: { args: string }) {
  const parsed = tryParseJson(args)
  if (!parsed) return <GenericJson text={args} />

  const task = parsed.task ?? parsed.prompt ?? parsed.input ?? parsed.message
  const model = parsed.model
  const agentId = parsed.agent_id ?? parsed.agentId
  const rest = { ...parsed }
  delete rest.task
  delete rest.prompt
  delete rest.input
  delete rest.message
  delete rest.model
  delete rest.agent_id
  delete rest.agentId

  return (
    <div className="space-y-2">
      {task && (
        <div>
          <div className="text-[10px] uppercase tracking-wider text-white/25 mb-1 light:text-black/25">
            Task
          </div>
          <p className="text-[11px] text-white/60 leading-relaxed light:text-black/60">{task}</p>
        </div>
      )}
      {(model || agentId) && (
        <div className="flex items-center gap-3">
          {model && (
            <span className="text-[10px] font-mono text-white/30 light:text-black/30">{model}</span>
          )}
          {agentId && (
            <span className="text-[10px] font-mono text-white/20 light:text-black/20">
              {agentId}
            </span>
          )}
        </div>
      )}
      {Object.keys(rest).length > 0 && (
        <GenericJson text={JSON.stringify(rest, null, 2)} label="Config" />
      )}
    </div>
  )
}

/** Render spawn_agent result */
function SpawnResult({ result }: { result: string }) {
  const parsed = tryParseJson(result)

  const content = parsed
    ? (parsed.result ??
      parsed.output ??
      parsed.response ??
      parsed.message ??
      parsed.content)
    : null

  if (typeof content === 'string') {
    return (
      <div>
        <div className="text-[10px] uppercase tracking-wider text-white/25 mb-1 light:text-black/25">
          Result
        </div>
        <div className="rounded bg-brand-main-900/40 border border-brand-main-800/20 px-3 py-2 max-h-48 overflow-y-auto text-[11px] text-white/60 light:text-black/60">
          <AgentMarkdown>{content}</AgentMarkdown>
        </div>
      </div>
    )
  }

  if (!parsed) {
    return (
      <div>
        <div className="text-[10px] uppercase tracking-wider text-white/25 mb-1 light:text-black/25">
          Result
        </div>
        <p className="text-[11px] text-white/50 light:text-black/50">{result}</p>
      </div>
    )
  }

  return <GenericJson text={JSON.stringify(parsed, null, 2)} label="Result" />
}

/** Render memory tool arguments: show key + content inline */
function MemoryArgs({ args }: { args: string }) {
  const parsed = tryParseJson(args)
  if (!parsed) return <GenericJson text={args} />

  const key = parsed.key ?? parsed.namespace ?? parsed.id ?? parsed.name
  const query = parsed.query ?? parsed.search ?? parsed.q
  const content = parsed.content ?? parsed.value ?? parsed.data
  const rest = { ...parsed }
  delete rest.key
  delete rest.namespace
  delete rest.id
  delete rest.name
  delete rest.query
  delete rest.search
  delete rest.q
  delete rest.content
  delete rest.value
  delete rest.data

  return (
    <div className="space-y-2">
      {key && (
        <div className="flex items-center gap-2">
          <span className="text-[10px] uppercase tracking-wider text-white/25 light:text-black/25">
            Key
          </span>
          <code className="text-[11px] font-mono text-white/50 light:text-black/50">{key}</code>
        </div>
      )}
      {query && (
        <div>
          <div className="text-[10px] uppercase tracking-wider text-white/25 mb-1 light:text-black/25">
            Query
          </div>
          <p className="text-[11px] text-white/50 light:text-black/50">{query}</p>
        </div>
      )}
      {content && (
        <div>
          <div className="text-[10px] uppercase tracking-wider text-white/25 mb-1 light:text-black/25">
            Content
          </div>
          <div className="rounded bg-brand-main-900/40 border border-brand-main-800/20 px-3 py-2 max-h-48 overflow-y-auto text-[11px] text-white/60 light:text-black/60">
            <AgentMarkdown>{String(content)}</AgentMarkdown>
          </div>
        </div>
      )}
      {Object.keys(rest).length > 0 && (
        <GenericJson text={JSON.stringify(rest, null, 2)} label="Options" />
      )}
    </div>
  )
}

/** Render memory tool result */
function MemoryResult({ result }: { result: string }) {
  const parsed = tryParseJson(result)

  const content = parsed
    ? (parsed.content ??
      parsed.value ??
      parsed.data ??
      parsed.message ??
      parsed.result)
    : null

  if (typeof content === 'string') {
    return (
      <div>
        <div className="text-[10px] uppercase tracking-wider text-white/25 mb-1 light:text-black/25">
          Result
        </div>
        <div className="rounded bg-brand-main-900/40 border border-brand-main-800/20 px-3 py-2 max-h-48 overflow-y-auto text-[11px] text-white/60 light:text-black/60">
          <AgentMarkdown>{content}</AgentMarkdown>
        </div>
      </div>
    )
  }

  if (!parsed) {
    return (
      <div>
        <div className="text-[10px] uppercase tracking-wider text-white/25 mb-1 light:text-black/25">
          Result
        </div>
        <p className="text-[11px] text-white/50 light:text-black/50">{result}</p>
      </div>
    )
  }

  return <GenericJson text={JSON.stringify(parsed, null, 2)} label="Result" />
}

/** Parse a domain from a URL string */
function extractDomain(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return url
  }
}

/** Parse search result entries from the numbered markdown format */
function parseSearchSources(
  text: string,
): Array<{ title: string; url: string; description: string }> {
  const sources: Array<{ title: string; url: string; description: string }> = []
  // Match pattern: N. **Title** (URL)\n   Description
  const re =
    /\d+\.\s+\*\*(.+?)\*\*\s+\((\S+?)\)\n\s+(.+?)(?=\n\n|\n\d+\.|\s*$)/gs
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) {
    sources.push({ title: m[1], url: m[2], description: m[3].trim() })
  }
  return sources
}

/** Render web_search arguments: show query prominently */
function WebSearchArgs({ args }: { args: string }) {
  const parsed = tryParseJson(args)
  if (!parsed) return <GenericJson text={args} />

  const query = parsed.query ?? parsed.q ?? parsed.search
  const maxResults = parsed.max_results

  return (
    <div className="space-y-2">
      {query && (
        <div className="flex items-start gap-2 rounded bg-blue-500/8 border border-blue-500/15 px-3 py-2">
          <Globe className="w-3.5 h-3.5 text-blue-400/70 shrink-0 mt-0.5" />
          <span className="text-[11px] text-blue-300/90 font-medium">
            {query}
          </span>
          {maxResults && (
            <span className="ml-auto text-[10px] text-white/30 shrink-0 light:text-black/30">
              max {maxResults}
            </span>
          )}
        </div>
      )}
    </div>
  )
}

/** Perplexity-style source cards for web_search results */
function WebSearchResult({ result }: { result: string }) {
  const sources = useMemo(() => parseSearchSources(result), [result])

  if (sources.length === 0) {
    return (
      <div>
        <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
          Result
        </div>
        <div className="rounded bg-black/30 border border-brand-main-800/40 px-3 py-2 max-h-64 overflow-y-auto">
          <pre className="text-[11px] font-mono text-white/70 whitespace-pre-wrap break-all light:text-black/70">
            {result}
          </pre>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30">
          {sources.length} source{sources.length !== 1 ? 's' : ''}
        </div>
        <div className="text-[10px] text-white/20 light:text-black/20">Top results</div>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
        {sources.map((src, i) => {
          const domain = extractDomain(src.url)
          return (
            <a
              key={i}
              href={src.url}
              target="_blank"
              rel="noopener noreferrer"
              className="rounded-lg bg-brand-main-800/50 border border-brand-main-700/30 hover:border-blue-500/30 hover:bg-brand-main-800/70 transition-all p-2.5 group"
            >
              <div className="flex items-center justify-between mb-2">
                <div className="flex items-center gap-2 min-w-0">
                  <span className="flex items-center justify-center w-4 h-4 rounded-full bg-blue-500/15 text-[9px] font-bold text-blue-400 shrink-0">
                    {i + 1}
                  </span>
                  <img
                    src={`https://www.google.com/s2/favicons?domain=${domain}&sz=32`}
                    alt=""
                    className="w-3.5 h-3.5 rounded-sm shrink-0"
                    loading="lazy"
                  />
                  <span className="text-[9px] text-white/30 truncate light:text-black/30">
                    {domain}
                  </span>
                </div>
                <span className="text-[9px] text-white/20 group-hover:text-white/40 light:text-black/20 light:group-hover:text-black/40">
                  Open
                </span>
              </div>
              <div className="text-[11px] text-white/80 font-medium leading-tight line-clamp-2 group-hover:text-white/95 transition-colors light:text-black/80 light:group-hover:text-black/95">
                {src.title}
              </div>
              <div className="text-[10px] text-white/40 leading-tight mt-1 line-clamp-3 light:text-black/40">
                {src.description}
              </div>
            </a>
          )
        })}
      </div>
    </div>
  )
}

/** Render web_fetch arguments: show URL prominently */
function WebFetchArgs({ args }: { args: string }) {
  const parsed = tryParseJson(args)
  if (!parsed) return <GenericJson text={args} />

  const url = parsed.url
  const maxLength = parsed.max_length

  return (
    <div className="space-y-2">
      {url && (
        <div className="flex items-start gap-2 rounded bg-blue-500/8 border border-blue-500/15 px-3 py-2">
          <Globe className="w-3.5 h-3.5 text-blue-400/70 shrink-0 mt-0.5" />
          <code className="text-[11px] font-mono text-blue-300/90 whitespace-pre-wrap break-all">
            {url}
          </code>
          {maxLength && (
            <span className="ml-auto text-[10px] text-white/30 shrink-0 light:text-black/30">
              max {maxLength.toLocaleString()} chars
            </span>
          )}
        </div>
      )}
    </div>
  )
}

/** Render web_fetch result: clean content block with extraction method */
function WebFetchResult({ result }: { result: string }) {
  const isJina = result.includes('(via Jina Reader,')
  const methodBadge = isJina ? 'Jina' : 'Local'

  // Extract char count from the "N characters" in the header
  const charMatch = result.match(/(\d+) characters\)/)
  const charCount = charMatch ? parseInt(charMatch[1], 10) : null

  // Strip the header line to show just the content
  const contentStart = result.indexOf('\n\n')
  const content = contentStart >= 0 ? result.slice(contentStart + 2) : result

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <span className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30">
          Content
        </span>
        <span
          className={`px-1.5 py-0.5 rounded text-[9px] font-medium ${
            isJina ? 'bg-blue-500/15 text-blue-400' : 'bg-white/5 text-white/40 light:bg-black/5 light:text-black/40'
          }`}
        >
          {methodBadge}
        </span>
        {charCount != null && (
          <span className="text-[10px] text-white/25 light:text-black/25">
            {charCount.toLocaleString()} chars
          </span>
        )}
      </div>
      <div className="rounded bg-black/30 border border-brand-main-800/40 px-3 py-2 max-h-64 overflow-y-auto">
        <pre className="text-[11px] font-mono text-white/70 whitespace-pre-wrap break-all light:text-black/70">
          {content}
        </pre>
      </div>
    </div>
  )
}

/** Render create_workflow arguments: show name and node count */
function WorkflowArgs({ args }: { args: string }) {
  const parsed = tryParseJson(args)
  if (!parsed) return <GenericJson text={args} />

  const name = parsed.name
  const description = parsed.description
  const nodeCount = Array.isArray(parsed.nodes) ? parsed.nodes.length : 0
  const edgeCount = Array.isArray(parsed.edges) ? parsed.edges.length : 0

  return (
    <div className="space-y-2">
      {name && (
        <div className="flex items-start gap-2 rounded bg-violet-500/8 border border-violet-500/15 px-3 py-2">
          <Workflow className="w-3.5 h-3.5 text-violet-400/70 shrink-0 mt-0.5" />
          <div>
            <span className="text-[11px] text-violet-300/90 font-medium">
              {name}
            </span>
            {description && (
              <p className="text-[10px] text-white/35 mt-0.5 light:text-black/35">{description}</p>
            )}
          </div>
          <span className="ml-auto text-[10px] text-white/30 shrink-0 light:text-black/30">
            {nodeCount} node{nodeCount !== 1 ? 's' : ''}, {edgeCount} edge
            {edgeCount !== 1 ? 's' : ''}
          </span>
        </div>
      )}
    </div>
  )
}

/** Render use_skill arguments: show skill name prominently */
function SkillArgs({ args }: { args: string }) {
  const parsed = tryParseJson(args)
  if (!parsed) return <GenericJson text={args} />

  const skillName = parsed.skill_name ?? parsed.name

  return (
    <div className="space-y-2">
      {skillName && (
        <div className="flex items-start gap-2 rounded bg-black/30 border border-brand-main-800/40 px-3 py-2">
          <Sparkles className="w-3.5 h-3.5 text-brand-secondary-400/70 shrink-0 mt-0.5" />
          <div className="min-w-0">
            <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
              Skill
            </div>
            <span className="text-[11px] font-mono text-brand-secondary-300/90 break-all">
              {skillName}
            </span>
          </div>
        </div>
      )}
    </div>
  )
}

/** Render use_skill result: show skill content as markdown */
function SkillResult({ result }: { result: string }) {
  // Extract skill name from "## Skill: name" header if present
  const nameMatch = result.match(/^## Skill: (.+)\n/)
  const skillName = nameMatch?.[1]
  const content = nameMatch ? result.slice(nameMatch[0].length) : result

  // Show truncated preview with the skill name badge
  const lines = content.split('\n')
  const preview = lines.slice(0, 8).join('\n')
  const truncated = lines.length > 8

  return (
    <div className="space-y-2">
      {skillName && (
        <div className="flex items-center gap-2">
          <span className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30">
            Skill Loaded
          </span>
          <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-brand-secondary-500/15 text-brand-secondary-300">
            {skillName}
          </span>
          <span className="text-[10px] text-white/25 light:text-black/25">
            {content.length.toLocaleString()} chars
          </span>
        </div>
      )}
      <div className="rounded bg-black/30 border border-brand-main-800/40 px-3 py-2 max-h-48 overflow-y-auto">
        <pre className="text-[11px] font-mono text-white/60 whitespace-pre-wrap break-all light:text-black/60">
          {preview}
          {truncated && '\n...'}
        </pre>
      </div>
    </div>
  )
}

/** Generic JSON block used as fallback */
function GenericJson({
  text,
  label = 'Arguments',
}: {
  text: string
  label?: string
}) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wider text-white/30 mb-1 light:text-black/30">
        {label}
      </div>
      <pre className="text-white/50 overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px] max-h-48 overflow-y-auto light:text-black/50">
        {text}
      </pre>
    </div>
  )
}

export function ToolCallCard({
  toolName,
  toolArgs,
  toolResult,
  toolSuccess,
  toolDurationMs,
  status = 'done',
  sandboxId,
  sandboxExitCode,
  sandboxDurationMs,
  sandboxParentDurationMs,
  sessionId,
  agentId,
}: ToolCallCardProps) {
  const [open, setOpen] = useState(false)

  const isSandbox = SANDBOX_TOOLS.has(toolName)
  const isPortTool =
    toolName === 'sandbox_expose_port' ||
    toolName === 'sandbox_unexpose_port' ||
    toolName === 'sandbox_list_ports'
  const isSpawn = SPAWN_TOOLS.has(toolName)
  const isMemory = MEMORY_TOOLS.has(toolName)
  const isWeb = WEB_TOOLS.has(toolName)
  const isWorkflow = WORKFLOW_TOOLS.has(toolName)
  const isSkill = SKILL_TOOLS.has(toolName)
  const isAutomationTool =
    toolName === 'create_trigger' || toolName === 'schedule_cron'

  const statusIndicator =
    status === 'running' ? (
      <span className="inline-block w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse" />
    ) : status === 'failed' || toolSuccess === false ? (
      <span className="inline-block w-1.5 h-1.5 rounded-full bg-red-400" />
    ) : (
      <span className="inline-block w-1.5 h-1.5 rounded-full bg-green-400/70" />
    )

  const toolIcon = isSandbox ? (
    <Terminal className="w-3 h-3 text-white/25 light:text-black/25" />
  ) : isSpawn ? (
    <GitBranch className="w-3 h-3 text-white/25 light:text-black/25" />
  ) : isMemory ? (
    <Database className="w-3 h-3 text-white/25 light:text-black/25" />
  ) : isWeb ? (
    <Globe className="w-3 h-3 text-blue-400/50" />
  ) : isWorkflow ? (
    <Workflow className="w-3 h-3 text-violet-400/50" />
  ) : isSkill ? (
    <Sparkles className="w-3 h-3 text-amber-400/50" />
  ) : null

  // Parse skill name from args for dynamic label
  const skillLabel = useMemo(() => {
    if (!isSkill || !toolArgs) return null
    const parsed = tryParseJson(toolArgs)
    return parsed?.skill_name ?? parsed?.name ?? null
  }, [isSkill, toolArgs])

  // Parse workflow result for inline link
  const workflowMeta = useMemo(() => {
    if (!isWorkflow || !toolResult) return null
    const parsed = tryParseJson(toolResult)
    if (!parsed?.workflow_id) return null
    return {
      id: parsed.workflow_id as string,
      name: (parsed.name ?? 'Workflow') as string,
    }
  }, [isWorkflow, toolResult])

  const automationMeta = useMemo(() => {
    if (!isAutomationTool || !toolResult || !agentId) return null

    const parsedArgs = tryParseJson(toolArgs)
    const triggerName = parsedArgs?.name
    const lines = toolResult.split('\n')
    const idLine = lines.find((line) => line.toLowerCase().includes('id:'))
    const scheduleLine = lines.find(
      (line) =>
        line.toLowerCase().includes('cron:') ||
        line.toLowerCase().includes('schedule:'),
    )
    const extractedId = idLine?.split(':').slice(1).join(':').trim()
    const normalized = toolResult.toLowerCase()

    let label = 'Automation updated'
    if (normalized.includes('deleted successfully')) {
      label =
        toolName === 'create_trigger'
          ? 'Automation deleted'
          : 'Schedule deleted'
    } else if (normalized.includes('created successfully')) {
      label =
        toolName === 'create_trigger'
          ? 'Automation created'
          : 'Schedule created'
    } else if (
      normalized.includes('found ') ||
      normalized.includes('no cron jobs found')
    ) {
      label =
        toolName === 'create_trigger' ? 'Automation list' : 'Schedule list'
    }

    return {
      label,
      name: triggerName || extractedId || 'Open automations',
      detail: scheduleLine?.split(':').slice(1).join(':').trim() || undefined,
    }
  }, [agentId, isAutomationTool, toolArgs, toolName, toolResult])

  const formattedArgs = useMemo(() => {
    if (!toolArgs) return null
    try {
      return JSON.stringify(JSON.parse(toolArgs), null, 2)
    } catch {
      return toolArgs
    }
  }, [toolArgs])

  const formattedResult = useMemo(() => {
    if (!toolResult) return null
    try {
      return JSON.stringify(JSON.parse(toolResult), null, 2)
    } catch {
      return toolResult
    }
  }, [toolResult])

  const triggerClassName = isWeb
    ? 'border-brand-secondary-500/20 bg-brand-secondary-500/8 hover:bg-brand-secondary-500/12'
    : isWorkflow
      ? 'border-brand-main-500/25 bg-brand-main-500/10 hover:bg-brand-main-500/14'
      : isAutomationTool
        ? 'border-brand-secondary-500/18 bg-brand-secondary-500/6 hover:bg-brand-secondary-500/10'
        : 'border-brand-main-600 bg-brand-main-900/40 hover:bg-brand-main-800/60'

  const headerTextClassName = isWeb
    ? 'text-brand-secondary-200'
    : isWorkflow
      ? 'text-brand-main-100'
      : 'text-white/40 light:text-black/40'

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger asChild>
        <button
          type="button"
          className={`flex w-full items-center gap-2 rounded border px-3 py-1.5 transition-colors text-left ${triggerClassName}`}
        >
          {statusIndicator}
          {toolIcon}
          <span className={`text-xs font-mono ${headerTextClassName}`}>
            {isSkill && skillLabel
              ? `Skill: ${skillLabel}`
              : (TOOL_LABELS[toolName] ?? humanizeToolName(toolName))}
          </span>
          {toolDurationMs != null && toolDurationMs > 0 && (
            <span className="text-[10px] text-white/20 ml-auto light:text-black/20">
              {toolDurationMs}ms
            </span>
          )}
          {open ? (
            <ChevronDown className="w-3 h-3 text-white/25 light:text-black/25" />
          ) : (
            <ChevronRight className="w-3 h-3 text-white/25 light:text-black/25" />
          )}
        </button>
      </CollapsibleTrigger>
      {!open && isSandbox && toolResult && (
        <SandboxPreview result={toolResult} />
      )}
      {workflowMeta && (
        <div className="flex items-center gap-2 border-t border-brand-main-500/15 bg-brand-main-500/8 px-3 py-1.5 text-[10px]">
          <Workflow className="w-3 h-3 text-brand-main-300/80 shrink-0" />
          <span className="text-white/40 light:text-black/40">{workflowMeta.name}</span>
          <Link
            to="/deployments/studio/$workflowId"
            params={{ workflowId: workflowMeta.id }}
            className="ml-auto flex items-center gap-1 text-brand-main-300/80 transition-colors hover:text-brand-main-200"
          >
            Open in Studio <ExternalLink className="w-2.5 h-2.5" />
          </Link>
        </div>
      )}
      {automationMeta && agentId && (
        <div className="flex items-center gap-2 border-t border-brand-secondary-500/12 bg-brand-secondary-500/6 px-3 py-1.5 text-[10px]">
          <Clock className="w-3 h-3 text-brand-secondary-300/80 shrink-0" />
          <div className="min-w-0 text-white/45 light:text-black/45">
            <span className="text-white/65 light:text-black/65">{automationMeta.label}:</span>{' '}
            <span className="truncate">{automationMeta.name}</span>
            {automationMeta.detail ? (
              <span className="text-white/30 light:text-black/30"> - {automationMeta.detail}</span>
            ) : null}
          </div>
          <Link
            to="/deployments/agents/$agentId/automations"
            params={{ agentId }}
            className="ml-auto flex items-center gap-1 text-brand-secondary-300/80 transition-colors hover:text-brand-secondary-200"
          >
            View Automations <ExternalLink className="w-2.5 h-2.5" />
          </Link>
        </div>
      )}
      {isSandbox && sandboxId && (
        <SandboxBanner
          sandboxId={sandboxId}
          sandboxExitCode={sandboxExitCode}
          sandboxDurationMs={sandboxDurationMs}
          sandboxParentDurationMs={sandboxParentDurationMs}
          sessionId={sessionId}
          status={status}
        />
      )}
      <CollapsibleContent>
        <div className="mt-1 rounded bg-brand-main-900/40 border border-brand-main-800/20 p-3 space-y-3 text-xs">
          {/* Arguments */}
          {toolArgs &&
            (isPortTool ? (
              <PortArgs args={toolArgs} toolName={toolName} />
            ) : isSandbox ? (
              <SandboxArgs args={toolArgs} toolName={toolName} />
            ) : isSpawn ? (
              <SpawnArgs args={toolArgs} />
            ) : isMemory ? (
              <MemoryArgs args={toolArgs} />
            ) : isWeb ? (
              toolName === 'web_search' ? (
                <WebSearchArgs args={toolArgs} />
              ) : (
                <WebFetchArgs args={toolArgs} />
              )
            ) : isWorkflow ? (
              <WorkflowArgs args={toolArgs} />
            ) : isSkill ? (
              <SkillArgs args={toolArgs} />
            ) : (
              <GenericJson text={formattedArgs!} />
            ))}

          {/* Result */}
          {toolResult &&
            (isPortTool ? (
              <PortResult result={toolResult} />
            ) : isSandbox ? (
              <SandboxResult result={toolResult} />
            ) : isSpawn ? (
              <SpawnResult result={toolResult} />
            ) : isMemory ? (
              <MemoryResult result={toolResult} />
            ) : isWeb ? (
              toolName === 'web_search' ? (
                <WebSearchResult result={toolResult} />
              ) : (
                <WebFetchResult result={toolResult} />
              )
            ) : isWorkflow ? (
              <Suspense
                fallback={
                  <div className="text-[11px] text-white/30 light:text-black/30">
                    Loading workflow preview...
                  </div>
                }
              >
                <WorkflowResultCard resultJson={toolResult} />
              </Suspense>
            ) : isSkill ? (
              <SkillResult result={toolResult} />
            ) : (
              <GenericJson text={formattedResult!} label="Result" />
            ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}
