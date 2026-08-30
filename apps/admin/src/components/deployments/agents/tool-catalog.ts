export interface ToolDef {
  name: string
  label: string
  description: string
}

export interface ToolCategoryDef {
  id: string
  label: string
  icon: string
  description: string
  dependency?: 'sandbox' | 'browser' | 'memory' | 'spawn'
  dependencyHint?: string
  tools: ToolDef[]
}

export const TOOL_CATALOG: ToolCategoryDef[] = [
  {
    id: 'sandbox',
    label: 'Sandbox',
    icon: 'heroicons:cube',
    description: 'Execute code, manage files, and run commands in an isolated sandbox environment.',
    dependency: 'sandbox',
    dependencyHint: 'Requires sandbox to be enabled in the Sandbox tab.',
    tools: [
      { name: 'sandbox_shell', label: 'Shell', description: 'Run shell commands in the sandbox' },
      { name: 'sandbox_execute', label: 'Execute', description: 'Execute code snippets in the sandbox runtime' },
      { name: 'sandbox_write_file', label: 'Write File', description: 'Create or overwrite files in the sandbox' },
      { name: 'sandbox_read_file', label: 'Read File', description: 'Read file contents from the sandbox' },
      { name: 'sandbox_list_files', label: 'List Files', description: 'List files and directories in the sandbox' },
      { name: 'sandbox_edit', label: 'Edit', description: 'Apply targeted edits to files in the sandbox' },
      { name: 'sandbox_grep', label: 'Grep', description: 'Search file contents using regex patterns' },
      { name: 'sandbox_glob', label: 'Glob', description: 'Find files matching glob patterns' },
      { name: 'sandbox_patch', label: 'Patch', description: 'Apply unified diff patches to files' },
      { name: 'sandbox_expose_port', label: 'Expose Port', description: 'Expose a port from the sandbox for external access' },
      { name: 'sandbox_unexpose_port', label: 'Unexpose Port', description: 'Remove an exposed port' },
      { name: 'sandbox_list_ports', label: 'List Ports', description: 'List currently exposed ports' },
      { name: 'sandbox_list_templates', label: 'List Templates', description: 'List available sandbox templates' },
      { name: 'sandbox_set_template', label: 'Set Template', description: 'Switch the sandbox to a different template' },
      { name: 'sandbox_git_clone', label: 'Git Clone', description: 'Clone a git repository into the sandbox' },
      { name: 'schedule_cron', label: 'Schedule Cron', description: 'Schedule recurring tasks in the sandbox' },
    ],
  },
  {
    id: 'browser',
    label: 'Browser',
    icon: 'heroicons:globe-alt',
    description: 'Automate browser interactions — navigate, click, type, screenshot, and more.',
    dependency: 'browser',
    dependencyHint: 'Requires browser automation to be enabled in the Sandbox tab.',
    tools: [
      { name: 'browser_navigate', label: 'Navigate', description: 'Navigate to a URL' },
      { name: 'browser_observe', label: 'Observe', description: 'Observe the current page state and elements' },
      { name: 'browser_click', label: 'Click', description: 'Click on a page element' },
      { name: 'browser_type', label: 'Type', description: 'Type text into an input field' },
      { name: 'browser_screenshot', label: 'Screenshot', description: 'Capture a screenshot of the page' },
      { name: 'browser_evaluate', label: 'Evaluate', description: 'Execute JavaScript in the browser' },
      { name: 'browser_wait', label: 'Wait', description: 'Wait for an element or condition' },
      { name: 'browser_scroll', label: 'Scroll', description: 'Scroll the page or an element' },
      { name: 'browser_select', label: 'Select', description: 'Select an option from a dropdown' },
      { name: 'browser_tabs', label: 'Tabs', description: 'Manage browser tabs' },
    ],
  },
  {
    id: 'web',
    label: 'Web',
    icon: 'heroicons:magnifying-glass',
    description: 'Search the web and fetch content from URLs.',
    tools: [
      { name: 'web_search', label: 'Web Search', description: 'Search the web via self-hosted SearXNG' },
      { name: 'web_fetch', label: 'Web Fetch', description: 'Fetch and extract content from a URL' },
    ],
  },
  {
    id: 'memory',
    label: 'Memory',
    icon: 'heroicons:circle-stack',
    description: 'Store and retrieve persistent knowledge across sessions.',
    dependency: 'memory',
    dependencyHint: 'Requires memory to be enabled in the Memory tab.',
    tools: [
      { name: 'memory_store', label: 'Store Memory', description: 'Save a fact or piece of knowledge to memory' },
      { name: 'memory_query', label: 'Query Memory', description: 'Search and retrieve stored memories' },
    ],
  },
  {
    id: 'coordination',
    label: 'Agent Coordination',
    icon: 'heroicons:squares-2x2',
    description: 'Spawn sub-agents, delegate jobs, and fork context for parallel work.',
    dependency: 'spawn',
    dependencyHint: 'Requires sub-agent spawning or forking to be enabled in the Behavior tab.',
    tools: [
      { name: 'spawn_agent', label: 'Spawn Agent', description: 'Create and run a sub-agent for a subtask' },
      { name: 'parallel_tasks', label: 'Parallel Tasks', description: 'Run multiple sub-agent tasks in parallel' },
      { name: 'check_job', label: 'Check Job', description: 'Check the status of an async background job' },
      { name: 'delegate_job', label: 'Delegate Job', description: 'Delegate a job to another agent' },
      { name: 'fork', label: 'Fork', description: 'Fork the current context for parallel exploration' },
    ],
  },
  {
    id: 'messaging',
    label: 'Messaging',
    icon: 'heroicons:chat-bubble-left-right',
    description: 'Send and receive messages between agents.',
    tools: [
      { name: 'send_message', label: 'Send Message', description: 'Send a message to another agent' },
      { name: 'check_messages', label: 'Check Messages', description: 'Check for incoming messages' },
      { name: 'read_channel_history', label: 'Read Channel History', description: 'Read message history from a channel' },
    ],
  },
  {
    id: 'storage',
    label: 'Storage',
    icon: 'heroicons:arrow-up-tray',
    description: 'Upload, download, and manage file artifacts.',
    tools: [
      { name: 'upload_artifact', label: 'Upload Artifact', description: 'Upload a file artifact to storage' },
      { name: 'download_artifact', label: 'Download Artifact', description: 'Download a file artifact from storage' },
      { name: 'list_artifacts', label: 'List Artifacts', description: 'List available file artifacts' },
    ],
  },
  {
    id: 'triggers',
    label: 'Triggers',
    icon: 'heroicons:bolt',
    description: 'Create and manage automated triggers.',
    tools: [
      { name: 'create_trigger', label: 'Create Trigger', description: 'Create a new automated trigger' },
      { name: 'list_triggers', label: 'List Triggers', description: 'List existing triggers' },
      { name: 'delete_trigger', label: 'Delete Trigger', description: 'Delete an automated trigger' },
    ],
  },
  {
    id: 'workflows',
    label: 'Workflows',
    icon: 'heroicons:arrow-path',
    description: 'Create multi-step workflows.',
    tools: [
      { name: 'create_workflow', label: 'Create Workflow', description: 'Create a new multi-step workflow' },
    ],
  },
  {
    id: 'repository',
    label: 'Repository',
    icon: 'heroicons:code-bracket',
    description: 'Read and search files in connected repositories.',
    tools: [
      { name: 'repo_glob', label: 'Repo Glob', description: 'Find files in the repo matching glob patterns' },
      { name: 'repo_read_file', label: 'Repo Read File', description: 'Read a file from the connected repository' },
    ],
  },
  {
    id: 'interaction',
    label: 'User Interaction',
    icon: 'heroicons:user',
    description: 'Ask the user questions and wait for responses.',
    tools: [
      { name: 'ask_user', label: 'Ask User', description: 'Ask the user a question and wait for their response' },
    ],
  },
  {
    id: 'skills',
    label: 'Skills',
    icon: 'heroicons:academic-cap',
    description: 'Use installed skills during agent execution.',
    tools: [
      { name: 'use_skill', label: 'Use Skill', description: 'Execute an installed skill by name' },
    ],
  },
  {
    id: 'interop',
    label: 'Interop',
    icon: 'lucide:waypoints',
    description: 'Call external agents over open protocols (A2A).',
    tools: [
      {
        name: 'call_external_agent',
        label: 'Call External Agent',
        description: 'Call a remote A2A agent (Google ADK or any A2A server) and use its response',
      },
    ],
  },
]

/** Flat set of all built-in tool names for quick lookup. */
export const BUILTIN_TOOL_NAMES = new Set(
  TOOL_CATALOG.flatMap((cat) => cat.tools.map((t) => t.name)),
)

/**
 * Build the namespaced tool name for a federated MCP tool. Mirrors
 * `mcpToolName` in internal/agents/runtime/tools/mcp_tool.go — non-alphanumeric
 * chars in the server name are replaced with underscores.
 */
export function mcpToolName(serverName: string, toolName: string): string {
  const safe = serverName.replace(/[^a-zA-Z0-9_]/g, '_')
  return `mcp__${safe}__${toolName}`
}
