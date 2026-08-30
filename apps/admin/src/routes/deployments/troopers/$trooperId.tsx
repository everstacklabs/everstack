import { useState, useMemo, useCallback, useEffect } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { Trash2, Sparkles, ArrowUp } from 'lucide-react'
import { ui } from '@everstack/ui'
import { Button, toast, Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { create } from '@everstack/client'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import { useFunctions } from '@/hooks/deployments/use-functions'
import {
  useGitHubInstallations,
  useGitHubRepositories,
  useGitHubBranches,
} from '@/hooks/integrations/use-github'
import { resolveSandboxPricing } from '@/lib/sandbox-pricing'
import {
  useTrooper,
  useUpdateTrooper,
  useProvisionTrooper,
  useSleepTrooper,
  useWakeTrooper,
  useDeleteTrooper,
  useTrooperLinks,
  useCreateTrooperLink,
  useDeleteTrooperLink,
  useTrooperSession,
} from '@/hooks/deployments/use-troopers'
import { useSession_ } from '@/hooks/deployments/use-agents'
import { SessionTimeline } from '@/components/deployments/agents/session-timeline'
import {
  SessionStatus,
  AgentSessionSchema,
} from '@everstack/proto/everstack/agents/v1/agents_pb'

const {
  Input,
  Label,
  Textarea,
  Switch,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  SelectGroup,
  SelectLabel,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} = ui

const brandSelectTriggerClass =
  'w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandSelectContentClass =
  'bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandInputClass = 'bg-brand-main-900 border-brand-main-600'
const brandTextareaClass =
  'bg-brand-main-900 border-brand-main-600 focus-visible:border-brand-secondary-500 focus-visible:ring-brand-secondary-500 focus-visible:ring-[1px]'

const TAB_CLASS =
  'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

const TROOPER_VM_IMAGE = 'ubuntu:22.04'
const _sandboxPricing = resolveSandboxPricing(undefined)

function formatCurrency(amount: number, currency = 'USD'): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency,
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  }).format(amount)
}

function parseGitHubRepo(
  value: string,
): { owner: string; repo: string; fullName: string } | null {
  const raw = value.trim()
  if (!raw) return null
  let normalized = raw
  const githubPrefix = 'https://github.com/'
  if (normalized.startsWith(githubPrefix))
    normalized = normalized.slice(githubPrefix.length)
  normalized = normalized.replace(/\.git$/, '').replace(/^\/+|\/+$/g, '')
  const parts = normalized.split('/')
  if (parts.length < 2) return null
  const owner = parts[0]
  const repo = parts[1]
  if (!owner || !repo) return null
  return { owner, repo, fullName: `${owner}/${repo}` }
}

const STATUS_STYLES: Record<string, string> = {
  created:
    'bg-gray-500/20 text-gray-300 light:text-gray-700 border-gray-500/30',
  provisioning:
    'bg-blue-500/20 text-blue-400 light:text-blue-600 border-blue-500/30',
  running:
    'bg-green-500/20 text-green-400 light:text-green-600 border-green-500/30',
  sleeping:
    'bg-yellow-500/20 text-yellow-400 light:text-yellow-700 border-yellow-500/30',
  waking: 'bg-blue-500/20 text-blue-400 light:text-blue-600 border-blue-500/30',
  failed: 'bg-red-500/20 text-red-400 light:text-red-600 border-red-500/30',
  terminated: 'bg-red-500/20 text-red-300 light:text-red-600 border-red-500/30',
}

function formatTrooperStatus(status: string): string {
  if (status === 'sleeping') return 'Idle'
  return status.charAt(0).toUpperCase() + status.slice(1)
}

export const Route = createFileRoute('/deployments/troopers/$trooperId')({
  component: TrooperDetailPage,
})

function TrooperDetailPage() {
  const { trooperId } = Route.useParams()
  const { data: trooper, isLoading, error } = useTrooper(trooperId)
  const deleteMutation = useDeleteTrooper()
  const provisionMutation = useProvisionTrooper()
  const sleepMutation = useSleepTrooper()
  const wakeMutation = useWakeTrooper()
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [activeTab, setActiveTab] = useState('session')

  if (isLoading) {
    return (
      <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading instance..." />
      </div>
    )
  }

  if (error || !trooper) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center gap-4">
        <div className="text-red-400 light:text-red-600">
          {error?.message ?? 'Instance not found'}
        </div>
        <Link
          to="/deployments/troopers"
          className="text-sm text-brand-secondary-400 hover:text-brand-secondary-300"
        >
          Back to instances
        </Link>
      </div>
    )
  }

  const isCreated = trooper.status === 'created'
  const isRunning = trooper.status === 'running'
  const isSleeping = trooper.status === 'sleeping'

  const isSessionTab = activeTab === 'session'

  return (
    <div className="flex flex-col h-full w-full">
      {/* Header + tab bar — always shrink-0 */}
      <div className="shrink-0 p-4 pb-0 space-y-4">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            {/* <Link
                            to="/deployments/troopers"
                            className="text-white/40 light:text-black/40 hover:text-white/70 light:hover:text-black/70 transition-colors"
                        >
                            <Iconify.Icon icon="heroicons:arrow-left" className="size-4" />
                        </Link>
                        <span className="h-2.5 w-2.5 shrink-0 rounded-full border border-white/20 light:border-black/20" style={{ backgroundColor: trooper.color || '#64748b' }} />
                        <span className="text-sm font-medium text-brand-secondary-100">
                            {trooper.icon && <span className="mr-1">{trooper.icon}</span>}
                            {trooper.name}
                        </span> */}
            <Tabs
              value={activeTab}
              onValueChange={setActiveTab}
              className="w-fit"
            >
              <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                <TabsTrigger className={TAB_CLASS} value="session">
                  Session
                </TabsTrigger>
                <TabsTrigger className={TAB_CLASS} value="overview">
                  Overview
                </TabsTrigger>
                <TabsTrigger className={TAB_CLASS} value="identity">
                  Identity
                </TabsTrigger>
                <TabsTrigger className={TAB_CLASS} value="agent">
                  Agent
                </TabsTrigger>
                <TabsTrigger className={TAB_CLASS} value="sandbox">
                  Sandbox
                </TabsTrigger>
                <TabsTrigger className={TAB_CLASS} value="links">
                  Links
                </TabsTrigger>
                <TabsTrigger className={TAB_CLASS} value="workers">
                  Workers
                </TabsTrigger>
              </TabsList>
            </Tabs>
          </div>
          <div className="flex items-center gap-1">
            <span
              className={`px-1.5 py-0.5 rounded text-[10px] font-medium border ${STATUS_STYLES[trooper.status] ?? STATUS_STYLES.created}`}
            >
              {formatTrooperStatus(trooper.status)}
            </span>
            {isCreated && (
              <button
                type="button"
                onClick={() =>
                  provisionMutation.mutate(trooper.id, {
                    onSuccess: () => toast.success('Provisioning instance...'),
                    onError: (e) => toast.error(`Failed: ${e.message}`),
                  })
                }
                disabled={provisionMutation.isPending}
                className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors text-blue-400/70 light:text-blue-600/70"
                title="Provision"
              >
                <Iconify.Icon
                  icon="heroicons:rocket-launch"
                  className="size-4"
                />
              </button>
            )}
            {isRunning && (
              <button
                type="button"
                onClick={() =>
                  sleepMutation.mutate(trooper.id, {
                    onSuccess: () => toast.success('Instance sleeping...'),
                    onError: (e) => toast.error(`Failed: ${e.message}`),
                  })
                }
                disabled={sleepMutation.isPending}
                className="p-1 rounded hover:bg-yellow-500/20 hover:text-yellow-400 light:hover:text-yellow-700 transition-colors text-yellow-400/70 light:text-yellow-700/70"
                title="Sleep"
              >
                <Iconify.Icon icon="heroicons:pause" className="size-4" />
              </button>
            )}
            {isSleeping && (
              <button
                type="button"
                onClick={() =>
                  wakeMutation.mutate(trooper.id, {
                    onSuccess: () => toast.success('Waking instance...'),
                    onError: (e) => toast.error(`Failed: ${e.message}`),
                  })
                }
                disabled={wakeMutation.isPending}
                className="p-1 rounded hover:bg-green-500/20 hover:text-green-400 light:hover:text-green-600 transition-colors text-green-400/70 light:text-green-600/70"
                title="Wake"
              >
                <Iconify.Icon icon="heroicons:play" className="size-4" />
              </button>
            )}
            <button
              type="button"
              onClick={() => setDeleteConfirmOpen(true)}
              className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
              title="Delete instance"
            >
              <Trash2 size={14} />
            </button>
          </div>
        </div>
      </div>

      {/* Session tab — fills remaining height, not inside the scrollable area */}
      {isSessionTab && (
        <div className="flex-1 min-h-0 overflow-hidden">
          <TrooperSessionTab trooperId={trooper.id} />
        </div>
      )}

      {/* Other tabs — scrollable content area */}
      {!isSessionTab && (
        <div className="flex-1 overflow-y-auto p-4 pt-0">
          <Tabs value={activeTab} className="w-full">
            {/* Hidden TabsList — needed for Radix Tabs context but already rendered above */}
            <TabsList className="hidden">
              <TabsTrigger value="session">Session</TabsTrigger>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="identity">Identity</TabsTrigger>
              <TabsTrigger value="agent">Agent</TabsTrigger>
              <TabsTrigger value="sandbox">Sandbox</TabsTrigger>
              <TabsTrigger value="links">Links</TabsTrigger>
              <TabsTrigger value="workers">Workers</TabsTrigger>
            </TabsList>

            <TabsContent value="overview" className="space-y-6 mt-4">
              <div className="grid grid-cols-2 gap-4">
                <ConfigField label="Name" value={trooper.name} />
                <ConfigField label="Model" value={trooper.model} mono />
                <ConfigField
                  label="Status"
                  value={formatTrooperStatus(trooper.status)}
                />
                <ConfigField
                  label="Description"
                  value={trooper.description || '-'}
                />
                <ConfigField
                  label="Created"
                  value={formatTimestamp(trooper.createdAt)}
                />
                <ConfigField
                  label="Updated"
                  value={formatTimestamp(trooper.updatedAt)}
                />
              </div>

              {trooper.systemPrompt && (
                <div className="space-y-1">
                  <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    System Prompt
                  </div>
                  <div className="rounded-md bg-brand-main-800/50 p-3 text-sm text-brand-main-100 whitespace-pre-wrap">
                    {trooper.systemPrompt}
                  </div>
                </div>
              )}

              {(trooper.tools?.length ?? 0) > 0 && (
                <div className="space-y-1">
                  <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Tools ({trooper.tools!.length})
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {trooper.tools!.map((tool) => (
                      <span
                        key={tool}
                        className="px-2 py-1 rounded text-xs bg-purple-500/20 text-purple-300 light:text-purple-600 font-mono"
                      >
                        {tool}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              {trooper.identity && (
                <div className="space-y-3">
                  <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Identity Preview
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    {trooper.identity.soulMd && (
                      <IdentityPreview
                        label="SOUL.md"
                        content={trooper.identity.soulMd}
                      />
                    )}
                    {trooper.identity.identityMd && (
                      <IdentityPreview
                        label="IDENTITY.md"
                        content={trooper.identity.identityMd}
                      />
                    )}
                    {trooper.identity.userMd && (
                      <IdentityPreview
                        label="USER.md"
                        content={trooper.identity.userMd}
                      />
                    )}
                    {trooper.identity.roleMd && (
                      <IdentityPreview
                        label="ROLE.md"
                        content={trooper.identity.roleMd}
                      />
                    )}
                  </div>
                </div>
              )}

              {trooper.sandbox && (
                <div className="space-y-2">
                  <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Sandbox
                  </div>
                  <div className="grid grid-cols-3 gap-3">
                    <ConfigField
                      label="Image"
                      value={trooper.sandbox.image}
                      mono
                    />
                    <ConfigField
                      label="CPU"
                      value={String(trooper.sandbox.cpuLimit)}
                    />
                    <ConfigField
                      label="Memory"
                      value={`${trooper.sandbox.memoryMb} MB`}
                    />
                    <ConfigField
                      label="Disk"
                      value={`${trooper.sandbox.diskMb} MB`}
                    />
                    <ConfigField
                      label="Network"
                      value={trooper.sandbox.networkMode}
                    />
                    <ConfigField
                      label="SSH"
                      value={
                        trooper.sandbox.sshEnabled ? 'Enabled' : 'Disabled'
                      }
                    />
                  </div>
                </div>
              )}
            </TabsContent>

            <TabsContent value="identity" className="mt-4">
              <IdentityTab trooperId={trooper.id} trooper={trooper} />
            </TabsContent>

            <TabsContent value="agent" className="mt-4">
              <AgentTab trooper={trooper} />
            </TabsContent>

            <TabsContent value="sandbox" className="mt-4">
              <SandboxTab trooper={trooper} />
            </TabsContent>

            <TabsContent value="links" className="mt-4">
              <LinksTab trooperId={trooper.id} />
            </TabsContent>

            <TabsContent value="workers" className="mt-4">
              <WorkersTab trooper={trooper} />
            </TabsContent>
          </Tabs>
        </div>
      )}

      {/* Delete confirmation dialog matching agents pattern */}
      <Dialog open={deleteConfirmOpen} onOpenChange={setDeleteConfirmOpen}>
        <DialogContent className="w-[500px]">
          <DialogTitle>Delete Instance</DialogTitle>
          <DialogDescription className="text-brand-main-100">
            Are you sure you want to delete{' '}
            <strong className="text-brand-main-100">{trooper.name}</strong>?
            This action cannot be undone and the instance sandbox will be
            terminated.
          </DialogDescription>
          <div className="flex justify-end gap-3 mt-4">
            <Button
              variant="outline"
              onClick={() => setDeleteConfirmOpen(false)}
              disabled={deleteMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
              onClick={() =>
                deleteMutation.mutate(trooper.id, {
                  onSuccess: () => {
                    toast.success('Instance deleted')
                    setDeleteConfirmOpen(false)
                    window.history.back()
                  },
                  onError: (e) => toast.error(`Failed: ${e.message}`),
                })
              }
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ─── Overview Helpers ────────────────────────────────────────────────

function ConfigField({
  label,
  value,
  mono,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="space-y-0.5">
      <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
        {label}
      </div>
      <div className={`text-sm text-brand-main-100 ${mono ? 'font-mono' : ''}`}>
        {value}
      </div>
    </div>
  )
}

function IdentityPreview({
  label,
  content,
}: {
  label: string
  content: string
}) {
  return (
    <div className="rounded-md bg-brand-main-800/50 p-3">
      <div className="text-[10px] text-white/40 light:text-black/40 font-medium mb-1">
        {label}
      </div>
      <div className="text-xs text-brand-main-200 whitespace-pre-wrap line-clamp-4 font-mono">
        {content}
      </div>
    </div>
  )
}

// ─── Identity Tab ────────────────────────────────────────────────────

function IdentityTab({
  trooperId,
  trooper,
}: {
  trooperId: string
  trooper: {
    identity: {
      soulMd: string
      identityMd: string
      userMd: string
      roleMd: string
    }
  }
}) {
  const updateMutation = useUpdateTrooper()
  const [soulMd, setSoulMd] = useState(trooper.identity?.soulMd ?? '')
  const [identityMd, setIdentityMd] = useState(
    trooper.identity?.identityMd ?? '',
  )
  const [userMd, setUserMd] = useState(trooper.identity?.userMd ?? '')
  const [roleMd, setRoleMd] = useState(trooper.identity?.roleMd ?? '')

  function handleSave() {
    updateMutation.mutate(
      { id: trooperId, identity: { soulMd, identityMd, userMd, roleMd } },
      {
        onSuccess: () => toast.success('Identity files updated'),
        onError: (e) => toast.error(`Failed: ${e.message}`),
      },
    )
  }

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="edit-soul">SOUL.md</Label>
        <Textarea
          id="edit-soul"
          value={soulMd}
          onChange={(e) => setSoulMd(e.target.value)}
          placeholder="Core personality and values..."
          className={`${brandTextareaClass} h-32 font-mono text-xs`}
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="edit-identity">IDENTITY.md</Label>
        <Textarea
          id="edit-identity"
          value={identityMd}
          onChange={(e) => setIdentityMd(e.target.value)}
          placeholder="Who this agent is..."
          className={`${brandTextareaClass} h-32 font-mono text-xs`}
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="edit-user">USER.md</Label>
        <Textarea
          id="edit-user"
          value={userMd}
          onChange={(e) => setUserMd(e.target.value)}
          placeholder="User preferences and context..."
          className={`${brandTextareaClass} h-32 font-mono text-xs`}
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="edit-role">ROLE.md</Label>
        <Textarea
          id="edit-role"
          value={roleMd}
          onChange={(e) => setRoleMd(e.target.value)}
          placeholder="Role-specific instructions..."
          className={`${brandTextareaClass} h-32 font-mono text-xs`}
        />
      </div>
      <div className="flex justify-end">
        <Button onClick={handleSave} disabled={updateMutation.isPending}>
          {updateMutation.isPending ? 'Saving...' : 'Save Identity'}
        </Button>
      </div>
    </div>
  )
}

// ─── Agent Tab ───────────────────────────────────────────────────────

function AgentTab({
  trooper,
}: {
  trooper: {
    id: string
    model: string
    systemPrompt: string
    tools: string[]
    maxTurns: number
    maxToolCallsPerTurn: number
  }
}) {
  const updateMutation = useUpdateTrooper()
  const { data: gatewayModels = [], isLoading: modelsLoading } =
    useGatewayModels()
  const { data: functions = [] } = useFunctions()
  const [model, setModel] = useState(trooper.model)
  const [systemPrompt, setSystemPrompt] = useState(trooper.systemPrompt ?? '')
  const [selectedTools, setSelectedTools] = useState<string[]>(
    trooper.tools ?? [],
  )
  const [maxTurns, setMaxTurns] = useState(trooper.maxTurns)
  const [maxToolCallsPerTurn, setMaxToolCallsPerTurn] = useState(
    trooper.maxToolCallsPerTurn,
  )

  const toggleTool = (toolName: string) => {
    setSelectedTools((prev) =>
      prev.includes(toolName)
        ? prev.filter((t) => t !== toolName)
        : [...prev, toolName],
    )
  }

  function handleSave() {
    updateMutation.mutate(
      {
        id: trooper.id,
        model,
        systemPrompt,
        tools: selectedTools,
        maxTurns,
        maxToolCallsPerTurn,
      },
      {
        onSuccess: () => toast.success('Agent config updated'),
        onError: (e) => toast.error(`Failed: ${e.message}`),
      },
    )
  }

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="edit-model">Model</Label>
        {gatewayModels.length > 0 ? (
          <Select value={model} onValueChange={setModel}>
            <SelectTrigger id="edit-model" className={brandSelectTriggerClass}>
              <SelectValue
                placeholder={
                  modelsLoading ? 'Loading models...' : 'Select a model'
                }
              />
            </SelectTrigger>
            <SelectContent className={brandSelectContentClass}>
              {gatewayModels.map((group) => (
                <SelectGroup key={group.provider}>
                  <SelectLabel>{group.provider}</SelectLabel>
                  {group.models.map((m) => (
                    <SelectItem key={m} value={m}>
                      {m}
                    </SelectItem>
                  ))}
                </SelectGroup>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            id="edit-model"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="anthropic/claude-sonnet-4-20250514"
            className={brandInputClass}
          />
        )}
      </div>
      <div className="space-y-2">
        <Label htmlFor="edit-prompt">System Prompt</Label>
        <Textarea
          id="edit-prompt"
          value={systemPrompt}
          onChange={(e) => setSystemPrompt(e.target.value)}
          placeholder="You are a helpful assistant..."
          className={`${brandTextareaClass} h-40 font-mono text-xs`}
        />
      </div>

      {functions.length > 0 && (
        <div className="space-y-2">
          <Label>
            Tools
            {selectedTools.length > 0 && (
              <span className="ml-1 text-[10px] text-brand-secondary-300">
                ({selectedTools.length})
              </span>
            )}
          </Label>
          <div className="flex flex-wrap gap-2 max-h-36 overflow-y-auto p-2 rounded-md border border-brand-main-800">
            {functions.map((fn) => (
              <button
                key={fn.name}
                type="button"
                onClick={() => toggleTool(fn.name)}
                className={`px-2 py-1 rounded text-xs transition-colors ${
                  selectedTools.includes(fn.name)
                    ? 'bg-brand-secondary-600 text-white'
                    : 'bg-brand-main-800 text-white/60 light:text-black/60 hover:text-white/80 light:hover:text-black/80'
                } light:hover:text-black/80`}
              >
                {fn.name}
              </button>
            ))}
          </div>
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-2">
          <Label htmlFor="edit-turns">Max Turns</Label>
          <Input
            id="edit-turns"
            type="number"
            value={maxTurns}
            onChange={(e) => setMaxTurns(Number(e.target.value))}
            className={brandInputClass}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="edit-tcpt">Max Tool Calls/Turn</Label>
          <Input
            id="edit-tcpt"
            type="number"
            value={maxToolCallsPerTurn}
            onChange={(e) => setMaxToolCallsPerTurn(Number(e.target.value))}
            className={brandInputClass}
          />
        </div>
      </div>
      <div className="flex justify-end">
        <Button onClick={handleSave} disabled={updateMutation.isPending}>
          {updateMutation.isPending ? 'Saving...' : 'Save Agent Config'}
        </Button>
      </div>
    </div>
  )
}

// ─── Sandbox Tab ─────────────────────────────────────────────────────

function SandboxTab({
  trooper,
}: {
  trooper: {
    id: string
    sandbox?: {
      image: string
      cpuLimit: number
      memoryMb: number
      diskMb: number
      networkMode: string
      sshEnabled: boolean
      gitRepoUrl: string
      gitBranch: string
    }
  }
}) {
  const updateMutation = useUpdateTrooper()
  const sb = trooper.sandbox
  const [cpuLimit, setCpuLimit] = useState(sb?.cpuLimit ?? 1.0)
  const [memoryMb, setMemoryMb] = useState(sb?.memoryMb ?? 512)
  const [diskMb, setDiskMb] = useState(sb?.diskMb ?? 2048)
  const [networkMode, setNetworkMode] = useState(sb?.networkMode ?? 'allow')
  const [sshEnabled, setSshEnabled] = useState(sb?.sshEnabled ?? false)
  const [gitRepoUrl, setGitRepoUrl] = useState(sb?.gitRepoUrl ?? '')
  const [gitBranch, setGitBranch] = useState(sb?.gitBranch ?? '')
  const [gitInstallationId, setGitInstallationId] = useState('')

  const {
    data: gitHubInstallations = [],
    isLoading: gitHubInstallationsLoading,
  } = useGitHubInstallations()
  const selectedGitInstallationId = Number(gitInstallationId)
  const parsedRepo = useMemo(() => parseGitHubRepo(gitRepoUrl), [gitRepoUrl])
  const { data: gitHubReposData, isLoading: gitHubReposLoading } =
    useGitHubRepositories(
      Number.isFinite(selectedGitInstallationId)
        ? selectedGitInstallationId
        : 0,
      { page: 1, perPage: 50 },
    )
  const { data: gitHubBranches = [], isLoading: gitHubBranchesLoading } =
    useGitHubBranches(
      Number.isFinite(selectedGitInstallationId)
        ? selectedGitInstallationId
        : 0,
      parsedRepo?.owner ?? '',
      parsedRepo?.repo ?? '',
      { page: 1, perPage: 100 },
    )
  const gitHubRepos = gitHubReposData?.repositories ?? []
  const selectedRepoIsInList = useMemo(
    () =>
      !!parsedRepo &&
      gitHubRepos.some((repo) => repo.fullName === parsedRepo.fullName),
    [gitHubRepos, parsedRepo],
  )

  const estimatedPricing = useMemo(() => {
    const memoryGb = memoryMb / 1024
    const diskGb = diskMb / 1024
    const hourlyRaw =
      cpuLimit * _sandboxPricing.cpuPerHourUsd +
      memoryGb * _sandboxPricing.memoryGbPerHourUsd +
      diskGb * _sandboxPricing.diskGbPerHourUsd +
      _sandboxPricing.platformFeePerHourUsd
    const hourly = _sandboxPricing.enabled ? hourlyRaw : 0
    const daily = hourly * 24
    const monthly = daily * 30
    return { hourly, daily, monthly }
  }, [cpuLimit, memoryMb, diskMb])

  function handleSave() {
    updateMutation.mutate(
      {
        id: trooper.id,
        sandbox: {
          image: TROOPER_VM_IMAGE,
          cpuLimit,
          memoryMb,
          diskMb,
          networkMode,
          sshEnabled,
          gitRepoUrl: gitRepoUrl.trim() || undefined,
          gitBranch: gitBranch.trim() || undefined,
        } as any,
      },
      {
        onSuccess: () => toast.success('Sandbox config updated'),
        onError: (e) => toast.error(`Failed: ${e.message}`),
      },
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 rounded-md bg-brand-main-900/40 border border-brand-main-700/60 px-3 py-2">
        <span className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
          Sandbox Image
        </span>
        <span className="text-xs text-zinc-300 light:text-zinc-700 font-mono">
          {TROOPER_VM_IMAGE}
        </span>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <div className="space-y-2">
          <Label htmlFor="edit-cpu">CPU</Label>
          <Input
            id="edit-cpu"
            type="number"
            step="0.5"
            value={cpuLimit}
            onChange={(e) => setCpuLimit(Number(e.target.value))}
            className={brandInputClass}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="edit-mem">Memory (MB)</Label>
          <Input
            id="edit-mem"
            type="number"
            value={memoryMb}
            onChange={(e) => setMemoryMb(Number(e.target.value))}
            className={brandInputClass}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="edit-disk">Disk (MB)</Label>
          <Input
            id="edit-disk"
            type="number"
            value={diskMb}
            onChange={(e) => setDiskMb(Number(e.target.value))}
            className={brandInputClass}
          />
        </div>
      </div>

      {/* Pricing Estimate */}
      <div className="rounded-md border border-brand-main-700/60 bg-brand-main-900/30 p-3 space-y-2">
        <div className="text-[11px] font-medium uppercase tracking-wider text-white/50 light:text-black/50">
          Estimated Cost
        </div>
        <div className="grid grid-cols-3 gap-2">
          <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
            <p className="text-[10px] text-white/45 light:text-black/45">
              Per hour
            </p>
            <p className="text-sm font-semibold text-white light:text-brand-main-50">
              {formatCurrency(estimatedPricing.hourly)}
            </p>
          </div>
          <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
            <p className="text-[10px] text-white/45 light:text-black/45">
              Per day
            </p>
            <p className="text-sm font-semibold text-white light:text-brand-main-50">
              {formatCurrency(estimatedPricing.daily)}
            </p>
          </div>
          <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
            <p className="text-[10px] text-white/45 light:text-black/45">
              Per month
            </p>
            <p className="text-sm font-semibold text-white light:text-brand-main-50">
              {formatCurrency(estimatedPricing.monthly)}
            </p>
          </div>
        </div>
      </div>

      <div className="space-y-2">
        <Label>Network Mode</Label>
        <Select value={networkMode} onValueChange={setNetworkMode}>
          <SelectTrigger className={brandSelectTriggerClass}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent className={brandSelectContentClass}>
            <SelectItem value="allow">Allow</SelectItem>
            <SelectItem value="deny">Deny</SelectItem>
            <SelectItem value="egress-only">Egress Only</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="flex items-center justify-between rounded-md border border-brand-main-800/70 bg-brand-main-900/30 px-3 py-2">
        <div className="space-y-0.5">
          <Label className="text-xs text-white/75 light:text-black/75">
            SSH Enabled
          </Label>
          <p className="text-[11px] text-white/40 light:text-black/40">
            Allow SSH connections into the instance sandbox
          </p>
        </div>
        <Switch checked={sshEnabled} onCheckedChange={setSshEnabled} />
      </div>

      {/* GitHub Source */}
      <div className="space-y-3 rounded-md border border-brand-main-700/80 bg-brand-main-900/25 p-3">
        <div className="text-[11px] font-medium uppercase tracking-wider text-white/50 light:text-black/50">
          GitHub Source (optional)
        </div>

        <div className="space-y-1">
          <Label className="text-xs text-white/60 light:text-black/60">
            GitHub Installation
          </Label>
          <Select
            value={gitInstallationId || '__none__'}
            onValueChange={(value) =>
              setGitInstallationId(value === '__none__' ? '' : value)
            }
          >
            <SelectTrigger className={brandSelectTriggerClass}>
              <SelectValue
                placeholder={
                  gitHubInstallationsLoading
                    ? 'Loading installations...'
                    : 'Select installation'
                }
              />
            </SelectTrigger>
            <SelectContent className={brandSelectContentClass}>
              <SelectItem value="__none__">None</SelectItem>
              {gitHubInstallations.map((inst) => (
                <SelectItem
                  key={inst.installationId}
                  value={String(inst.installationId)}
                >
                  {inst.accountLogin} ({inst.accountType})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {gitHubInstallations.length === 0 && !gitHubInstallationsLoading && (
            <p className="text-[11px] text-white/35 light:text-black/35">
              No linked GitHub installations found. Connect GitHub in Settings.
            </p>
          )}
        </div>

        {selectedGitInstallationId > 0 && (
          <div className="space-y-1">
            <Label className="text-xs text-white/60 light:text-black/60">
              Repository
            </Label>
            <Select
              value={
                selectedRepoIsInList && parsedRepo
                  ? parsedRepo.fullName
                  : '__custom__'
              }
              onValueChange={(value) => {
                if (value === '__custom__') return
                setGitRepoUrl(value)
                const selectedRepo = gitHubRepos.find(
                  (repo) => repo.fullName === value,
                )
                if (selectedRepo && !gitBranch)
                  setGitBranch(selectedRepo.defaultBranch || '')
              }}
            >
              <SelectTrigger className={brandSelectTriggerClass}>
                <SelectValue
                  placeholder={
                    gitHubReposLoading
                      ? 'Loading repositories...'
                      : 'Select repository'
                  }
                />
              </SelectTrigger>
              <SelectContent className={brandSelectContentClass}>
                <SelectItem value="__custom__">
                  Custom / manual input
                </SelectItem>
                {gitHubRepos.map((repo) => (
                  <SelectItem key={repo.id} value={repo.fullName}>
                    {repo.fullName}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

        {selectedGitInstallationId > 0 && parsedRepo && (
          <div className="space-y-1">
            <Label className="text-xs text-white/60 light:text-black/60">
              Branch
            </Label>
            <Select
              value={gitBranch || '__default__'}
              onValueChange={(value) =>
                setGitBranch(value === '__default__' ? '' : value)
              }
            >
              <SelectTrigger className={brandSelectTriggerClass}>
                <SelectValue
                  placeholder={
                    gitHubBranchesLoading
                      ? 'Loading branches...'
                      : 'Select branch'
                  }
                />
              </SelectTrigger>
              <SelectContent className={brandSelectContentClass}>
                <SelectItem value="__default__">Default branch</SelectItem>
                {gitHubBranches.map((branch) => (
                  <SelectItem key={branch.name} value={branch.name}>
                    {branch.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

        {!selectedGitInstallationId && (
          <>
            <div className="space-y-1">
              <Label className="text-xs text-white/60 light:text-black/60">
                Git Repo URL
              </Label>
              <Input
                value={gitRepoUrl}
                onChange={(e) => setGitRepoUrl(e.target.value)}
                placeholder="https://github.com/org/repo"
                className={brandInputClass}
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs text-white/60 light:text-black/60">
                Git Branch
              </Label>
              <Input
                value={gitBranch}
                onChange={(e) => setGitBranch(e.target.value)}
                placeholder="main"
                className={brandInputClass}
              />
            </div>
          </>
        )}
      </div>

      <div className="flex justify-end">
        <Button onClick={handleSave} disabled={updateMutation.isPending}>
          {updateMutation.isPending ? 'Saving...' : 'Save Sandbox Config'}
        </Button>
      </div>
    </div>
  )
}

// ─── Links Tab ───────────────────────────────────────────────────────

function LinksTab({ trooperId }: { trooperId: string }) {
  const { data } = useTrooperLinks(trooperId)
  const links = data?.links ?? []
  const createLinkMutation = useCreateTrooperLink()
  const deleteLinkMutation = useDeleteTrooperLink()

  const [targetType, setTargetType] = useState('trooper')
  const [targetId, setTargetId] = useState('')
  const [targetName, setTargetName] = useState('')
  const [linkType, setLinkType] = useState('peer')

  function handleAddLink() {
    if (!targetId.trim()) {
      toast.error('Target ID is required')
      return
    }
    createLinkMutation.mutate(
      {
        sourceTrooperId: trooperId,
        targetType,
        targetId: targetId.trim(),
        targetName: targetName.trim() || undefined,
        linkType,
      },
      {
        onSuccess: () => {
          toast.success('Link created')
          setTargetId('')
          setTargetName('')
        },
        onError: (e) => toast.error(`Failed: ${e.message}`),
      },
    )
  }

  return (
    <div className="space-y-4">
      <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
        Connections ({links.length})
      </div>

      {links.length > 0 ? (
        <div className="space-y-2">
          {links.map((link) => (
            <div
              key={link.id}
              className="flex items-center justify-between p-2.5 rounded-md bg-brand-main-900/60 border border-brand-main-600"
            >
              <div className="flex items-center gap-2">
                <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-purple-500/20 text-purple-300 light:text-purple-600">
                  {link.targetType}
                </span>
                <span className="text-sm text-zinc-200 light:text-zinc-800 font-medium">
                  {link.targetName || link.targetId}
                </span>
                <span className="text-[10px] text-zinc-500 light:text-zinc-600">
                  {link.linkType}
                </span>
              </div>
              <button
                type="button"
                onClick={() =>
                  deleteLinkMutation.mutate(link.id, {
                    onSuccess: () => toast.success('Link removed'),
                    onError: (e) => toast.error(`Failed: ${e.message}`),
                  })
                }
                disabled={deleteLinkMutation.isPending}
                className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
      ) : (
        <div className="text-sm text-brand-main-200 py-4 text-center">
          No links yet
        </div>
      )}

      <div className="border-t border-brand-main-700/60 pt-4 space-y-3">
        <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider">
          Add Link
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-2">
            <Label>Target Type</Label>
            <Select value={targetType} onValueChange={setTargetType}>
              <SelectTrigger className={brandSelectTriggerClass}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent className={brandSelectContentClass}>
                <SelectItem value="trooper">Instance</SelectItem>
                <SelectItem value="agent">Agent</SelectItem>
                <SelectItem value="human">Human</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>Link Type</Label>
            <Select value={linkType} onValueChange={setLinkType}>
              <SelectTrigger className={brandSelectTriggerClass}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent className={brandSelectContentClass}>
                <SelectItem value="peer">Peer</SelectItem>
                <SelectItem value="collaborator">Collaborator</SelectItem>
                <SelectItem value="supervisor">Supervisor</SelectItem>
                <SelectItem value="subordinate">Subordinate</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
        <div className="space-y-2">
          <Label htmlFor="link-target-id">Target ID</Label>
          <Input
            id="link-target-id"
            value={targetId}
            onChange={(e) => setTargetId(e.target.value)}
            placeholder="UUID of the target"
            className={brandInputClass}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="link-target-name">Target Name (optional)</Label>
          <Input
            id="link-target-name"
            value={targetName}
            onChange={(e) => setTargetName(e.target.value)}
            placeholder="Display name"
            className={brandInputClass}
          />
        </div>
        <Button onClick={handleAddLink} disabled={createLinkMutation.isPending}>
          {createLinkMutation.isPending ? 'Adding...' : 'Add Link'}
        </Button>
      </div>
    </div>
  )
}

// ─── Workers Tab ─────────────────────────────────────────────────────

// ─── Trooper Session Tab ───────────────────────────────────────────

function TrooperSessionTab({ trooperId }: { trooperId: string }) {
  const { sessionId, startSession, isStreaming, events, resetSessionPointer } =
    useTrooperSession(trooperId)
  // Only query the DB for real session IDs — temp IDs (ws-pending-*) are store-only.
  const isRealSessionId = !!sessionId && !sessionId.startsWith('ws-pending-')
  const sessionQuery = useSession_(isRealSessionId ? sessionId : '')
  const {
    data: session,
    isLoading: isSessionLoading,
    isError: isSessionError,
  } = sessionQuery
  const [userInput, setUserInput] = useState('')
  const hasStoreEvents = events.length > 0

  // Retry the session query when it fails but we have store events (CQRS lag).
  // The session was just created — the projection simply hasn't caught up yet.
  useEffect(() => {
    if (!isSessionError || !hasStoreEvents) return
    const timer = setTimeout(() => {
      sessionQuery.refetch()
    }, 1500)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isSessionError, hasStoreEvents])

  useEffect(() => {
    if (!sessionId || !isSessionError || isStreaming) return
    // Don't reset the pointer if we still have SSE events in the store —
    // the session exists but CQRS projection hasn't caught up yet.
    if (hasStoreEvents) return
    const errorText = String(sessionQuery.error?.message ?? '').toLowerCase()
    const isNotFound =
      errorText.includes('not found') ||
      errorText.includes('404') ||
      errorText.includes('unknown session')
    if (!isNotFound) return
    resetSessionPointer()
  }, [
    sessionId,
    isSessionError,
    isStreaming,
    hasStoreEvents,
    resetSessionPointer,
    sessionQuery.error?.message,
  ])

  // Create a stub session so SessionTimeline can mount immediately while the DB catch-up fetch is in flight.
  // Once the real session loads from useSession_(), it replaces the stub seamlessly.
  // We keep the stub visible while streaming, loading, OR when store has events (CQRS lag),
  // so that SSE events aren't lost by unmounting SessionTimeline prematurely.
  const effectiveSession = useMemo(() => {
    if (session) return session
    if (!sessionId) return null
    // Keep the stub alive if the store has events (stream completed but DB hasn't caught up).
    if (hasStoreEvents || isStreaming || isSessionLoading) {
      return create(AgentSessionSchema, {
        id: sessionId,
        status: SessionStatus.RUNNING,
        agentId: '',
        tenantId: '',
        turns: [],
        turnCount: 0,
        totalTokens: 0,
      })
    }
    return null
  }, [session, sessionId, isSessionLoading, isStreaming, hasStoreEvents])

  const handleSend = useCallback(async () => {
    const text = userInput.trim()
    if (!text) return
    setUserInput('')
    await startSession(text)
  }, [userInput, startSession])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        handleSend()
      }
    },
    [handleSend],
  )

  // Once we have a session (real or stub), delegate entirely to SessionTimeline
  if (effectiveSession) {
    return (
      <div className="flex flex-col h-full w-full">
        <div className="flex-1 overflow-hidden">
          <SessionTimeline session={effectiveSession} />
        </div>
      </div>
    )
  }

  // Initial state — no session yet, show chat input to start one
  return (
    <div className="flex flex-col h-full">
      <div className="flex-1 flex flex-col items-center justify-center text-brand-main-300">
        <Sparkles className="w-8 h-8 mb-3 text-brand-secondary-500/30" />
        <p className="text-sm">Start a conversation with the instance agent</p>
      </div>
      <div className="shrink-0 border-t border-brand-main-800/30">
        <div className="max-w-3xl mx-auto px-4 py-3">
          <div className="relative rounded border border-brand-main-600 bg-brand-main-950 focus-within:ring-1 focus-within:ring-brand-secondary-500 transition-colors">
            <textarea
              value={userInput}
              onChange={(e) => setUserInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Start an instance session..."
              rows={1}
              className="w-full bg-transparent text-sm text-white light:text-brand-main-50 placeholder:text-white/25 light:placeholder:text-black/25 resize-none outline-none max-h-32 scrollbar-thin px-3 pt-3 pb-1"
              disabled={isStreaming}
            />
            <div className="flex items-center justify-between px-2 pb-2">
              <div />
              <div className="flex items-center gap-2">
                {userInput.trim() && (
                  <span className="text-[11px] text-brand-main-300 font-light hidden sm:flex items-center gap-1 select-none">
                    <kbd className="bg-white/10 light:bg-black/10 px-1.5 py-0.5 rounded text-[10px] font-mono opacity-50">
                      ↵
                    </kbd>
                    to send ·
                    <kbd className="bg-white/10 light:bg-black/10 px-1.5 py-0.5 rounded text-[10px] font-mono opacity-50">
                      ⇧↵
                    </kbd>
                    for newline
                  </span>
                )}
                {userInput.trim() && (
                  <Button
                    size="xs"
                    variant="default"
                    type="button"
                    onClick={handleSend}
                    disabled={isStreaming}
                  >
                    <ArrowUp className="w-4 h-4" />
                  </Button>
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

// ─── Workers Tab ─────────────────────────────────────────────────────

function WorkersTab({
  trooper,
}: {
  trooper: { id: string; workers?: { maxConcurrentWorkers: number } }
}) {
  const updateMutation = useUpdateTrooper()
  const [maxConcurrentWorkers, setMaxConcurrentWorkers] = useState(
    trooper.workers?.maxConcurrentWorkers ?? 3,
  )

  function handleSave() {
    updateMutation.mutate(
      {
        id: trooper.id,
        workers: { maxConcurrentWorkers } as any,
      },
      {
        onSuccess: () => toast.success('Workers config updated'),
        onError: (e) => toast.error(`Failed: ${e.message}`),
      },
    )
  }

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="edit-workers">Max Concurrent Workers</Label>
        <Input
          id="edit-workers"
          type="number"
          value={maxConcurrentWorkers}
          onChange={(e) => setMaxConcurrentWorkers(Number(e.target.value))}
          className={`${brandInputClass} w-40`}
        />
        <p className="text-[11px] text-white/40 light:text-black/40">
          Maximum number of parallel worker branches in this instance
        </p>
      </div>
      <div className="flex justify-end">
        <Button onClick={handleSave} disabled={updateMutation.isPending}>
          {updateMutation.isPending ? 'Saving...' : 'Save Workers Config'}
        </Button>
      </div>
    </div>
  )
}
