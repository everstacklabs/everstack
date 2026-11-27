import { useState } from 'react'
import { ui } from '@everstack/ui'
import {
  describeInteropError,
  useDeleteRemoteAgent,
  useRemoteAgents,
  useUpsertRemoteAgent,
  type RemoteAgent,
} from '@/hooks/gateway/use-interop'
import { ExternalRuns, pendingRunRemote } from './external-runs'

const {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} = ui

type Protocol = RemoteAgent['protocol']

// Legacy first and the default: Everstack's own A2A server and most deployed
// agents speak it.
const PROTOCOLS: { value: Protocol; label: string }[] = [
  { value: 'a2a-legacy', label: 'Legacy A2A 0.2 / 0.3 · JSON-RPC' },
  { value: 'a2a-1.0', label: 'A2A 1.0 · JSON-RPC' },
]

const inputClass =
  'bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50'
const selectTriggerClass =
  'w-full bg-brand-main-900/60 border-brand-main-600 text-white light:text-brand-main-50'
const selectContentClass =
  'bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50'
const mutedClass = 'text-white/45 light:text-black/45'

export function RemoteAgents() {
  const remotes = useRemoteAgents()
  const upsert = useUpsertRemoteAgent()
  const del = useDeleteRemoteAgent()
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<RemoteAgent | null>(null)
  const [name, setName] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [protocol, setProtocol] = useState<Protocol>('a2a-legacy')
  const [routingTenant, setRoutingTenant] = useState('')
  const [token, setToken] = useState('')
  const [clearToken, setClearToken] = useState(false)
  // Starts on the connection of the last submission so returning to this tab
  // after a failed send can retry it unchanged.
  const [selected, setSelected] = useState(pendingRunRemote)

  const edit = (remote: RemoteAgent | null) => {
    setEditing(remote)
    setName(remote?.name ?? '')
    setEndpoint(remote?.endpoint ?? '')
    setProtocol(remote?.protocol ?? 'a2a-legacy')
    setRoutingTenant(remote?.routing_tenant ?? '')
    setToken('')
    setClearToken(false)
    upsert.reset()
    setOpen(true)
  }
  const changeProtocol = (value: Protocol) => {
    setProtocol(value)
    // A routing tenant is an A2A 1.0 field; legacy connections reject one.
    if (value === 'a2a-legacy') setRoutingTenant('')
  }
  const routing = protocol === 'a2a-1.0' ? routingTenant.trim() : ''
  const connectionChanged =
    editing !== null &&
    (endpoint.trim() !== editing.endpoint ||
      protocol !== editing.protocol ||
      routing !== (editing.routing_tenant ?? ''))
  const credentialChangeNeeded =
    Boolean(editing?.has_credential) &&
    connectionChanged &&
    !token &&
    !clearToken
  const save = () => {
    if (!name.trim() || !endpoint.trim()) return
    upsert.mutate(
      {
        name: name.trim(),
        endpoint: endpoint.trim(),
        protocol,
        routing_tenant: routing,
        auth_token: clearToken ? '' : token || undefined,
      },
      {
        onSuccess: () => {
          setOpen(false)
          setToken('')
          setSelected(name.trim())
        },
      },
    )
  }
  const list = remotes.data ?? []

  return (
    <div className="space-y-6 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-sm font-medium text-brand-secondary-100">
            External agents
          </h2>
          <p className="mt-1 max-w-xl text-xs leading-5 text-brand-main-300">
            Connections shared within this tenant. Save credentials here, then
            call agents by name. Runs and results stay available after a gateway
            restart.
          </p>
        </div>
        <Button variant="outline" onClick={() => edit(null)}>
          Add remote agent
        </Button>
      </div>
      {remotes.isLoading && (
        <p className={`text-sm ${mutedClass}`}>Loading connections…</p>
      )}
      {remotes.isError && (
        <p role="alert" className="text-sm text-danger">
          Unable to load connections.{' '}
          <button
            type="button"
            className="underline"
            onClick={() => remotes.refetch()}
          >
            Retry
          </button>
        </p>
      )}
      <div className="space-y-2">
        {list.map((remote) => {
          const isSelected = selected === remote.name
          return (
            <div
              key={remote.id}
              className={`flex flex-wrap items-center justify-between gap-3 rounded border px-3 py-2 ${isSelected ? 'border-brand-secondary-500/30 bg-brand-secondary-600/20' : 'border-brand-main-600/40 bg-brand-main-800/40'}`}
            >
              <button
                type="button"
                className="flex min-w-0 flex-1 flex-col text-left"
                onClick={() => setSelected(remote.name)}
                aria-label={`Use ${remote.name}`}
                aria-pressed={isSelected}
              >
                <span className="truncate text-sm text-brand-secondary-100">
                  {remote.name}
                </span>
                <span className="truncate text-[11px] text-brand-main-300">
                  {remote.protocol === 'a2a-1.0' ? 'A2A 1.0' : 'Legacy A2A'} ·{' '}
                  {remote.has_credential ? 'Credential saved' : 'No credential'}
                </span>
                <span className="truncate text-[11px] text-brand-main-300">
                  {remote.endpoint}
                </span>
              </button>
              <div className="flex shrink-0 gap-1">
                <Button variant="ghost" onClick={() => edit(remote)}>
                  Edit
                </Button>
                <Button
                  variant="ghost"
                  disabled={del.isPending}
                  onClick={() =>
                    del.mutate(
                      { id: remote.id },
                      {
                        onSuccess: () => {
                          if (selected === remote.name) setSelected('')
                        },
                      },
                    )
                  }
                >
                  Remove
                </Button>
              </div>
            </div>
          )
        })}
        {!remotes.isLoading && !remotes.isError && list.length === 0 && (
          <p className={`text-xs ${mutedClass}`}>
            Add a connection to send your first task.
          </p>
        )}
      </div>
      <ExternalRuns remotes={list} selected={selected} onSelect={setSelected} />
      <Dialog
        open={open}
        onOpenChange={(value) => {
          setOpen(value)
          if (!value) setToken('')
        }}
      >
        <DialogContent className="max-h-[90dvh] w-full max-w-[560px] overflow-auto">
          <DialogTitle>
            {editing ? 'Edit remote agent' : 'Add remote agent'}
          </DialogTitle>
          <DialogDescription className="text-brand-main-300">
            Use the service endpoint and protocol provided by the agent
            operator.
          </DialogDescription>
          <form
            className="mt-4 space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              save()
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="ra-name">Name</Label>
              <Input
                id="ra-name"
                value={name}
                disabled={Boolean(editing)}
                onChange={(event) => setName(event.target.value)}
                placeholder="research-bot"
                maxLength={128}
                required
                className={inputClass}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="ra-endpoint">Endpoint URL</Label>
              <Input
                id="ra-endpoint"
                type="url"
                value={endpoint}
                onChange={(event) => setEndpoint(event.target.value)}
                placeholder="https://agent.example/a2a"
                required
                className={inputClass}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="ra-protocol">Protocol</Label>
              <Select
                value={protocol}
                onValueChange={(value) => {
                  // Ignore the hidden native select's '' (see external-runs).
                  const option = PROTOCOLS.find((item) => item.value === value)
                  if (option) changeProtocol(option.value)
                }}
              >
                <SelectTrigger id="ra-protocol" className={selectTriggerClass}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent className={selectContentClass}>
                  {PROTOCOLS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-brand-main-200">
                Everstack agents and most deployed A2A servers use Legacy A2A.
              </p>
            </div>
            {protocol === 'a2a-1.0' && (
              <div className="space-y-2">
                <Label htmlFor="ra-routing">
                  Remote routing tenant (optional)
                </Label>
                <Input
                  id="ra-routing"
                  value={routingTenant}
                  onChange={(event) => setRoutingTenant(event.target.value)}
                  maxLength={1024}
                  className={inputClass}
                />
                <p className="text-xs text-brand-main-200">
                  Only set this if the remote agent specifies a routing tenant.
                </p>
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="ra-token">
                {editing?.has_credential
                  ? 'Replace bearer token'
                  : 'Bearer token (optional)'}
              </Label>
              <Input
                id="ra-token"
                type="password"
                autoComplete="new-password"
                disabled={clearToken}
                value={token}
                onChange={(event) => setToken(event.target.value)}
                className={inputClass}
              />
              {editing?.has_credential ? (
                <p
                  className={`text-xs ${credentialChangeNeeded ? 'text-warning' : 'text-brand-main-200'}`}
                >
                  Leave blank to keep the saved credential. Changing the
                  endpoint, protocol or routing tenant requires replacing or
                  removing it.
                </p>
              ) : (
                <p className="text-xs text-brand-main-200">
                  Tokens are only sent to https:// endpoints and need credential
                  encryption configured on the server.
                </p>
              )}
            </div>
            {editing?.has_credential && (
              <div className="flex items-center gap-2">
                <Checkbox
                  id="ra-clear-token"
                  checked={clearToken}
                  onCheckedChange={(value) => {
                    setClearToken(value === true)
                    if (value === true) setToken('')
                  }}
                />
                <Label
                  htmlFor="ra-clear-token"
                  className="cursor-pointer font-normal"
                >
                  Remove the saved credential
                </Label>
              </div>
            )}
            {upsert.isError && (
              <p role="alert" className="text-sm text-danger">
                Could not save. {describeInteropError(upsert.error)}
              </p>
            )}
            <div className="flex justify-end gap-3 border-t border-brand-main-700 pt-4">
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setOpen(false)
                  setToken('')
                }}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={upsert.isPending || !name.trim() || !endpoint.trim()}
              >
                {upsert.isPending ? 'Saving…' : 'Save connection'}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
