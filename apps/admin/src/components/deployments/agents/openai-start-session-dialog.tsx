import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import { useSession } from '@/hooks/auth/use-auth'
import { getApiBaseUrl } from '@/lib/api-url'
import { getActiveOrgId } from '@/lib/active-org'
import {
  openAIRequest,
  OpenAIRequestError,
  type OpenAICapabilities,
  type OpenAIConnection,
  type OpenAIConnectionInput,
  type OpenAISession,
  type OpenAISessionInput,
  type OpenAIToolSpec,
} from '@/server/openai-agents'
import { OpenAIToolPicker } from './openai-tool-picker'

const {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
} = ui

const errorText = (error: unknown) =>
  error instanceof Error
    ? error.message
    : 'Request failed. Refresh and try again.'

export function useOpenAIAgentsScope() {
  const { data: identity } = useSession()
  const userId = identity?.user?.user?.id ?? ''
  const orgId = getActiveOrgId() ?? ''
  return {
    authenticated: !!userId,
    scope: `${getApiBaseUrl()}|${orgId}|${userId}`,
  }
}

export function OpenAIStartSessionDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (session: OpenAISession) => void
}) {
  const { authenticated, scope } = useOpenAIAgentsScope()
  const cache = useQueryClient()
  const key = useMemo(() => ['openai-agents', scope], [scope])
  const [showConnection, setShowConnection] = useState(false)
  const [editingConnection, setEditingConnection] = useState<OpenAIConnection>()
  const [connectionId, setConnectionId] = useState('')
  const [model, setModel] = useState('')
  const [environment, setEnvironment] = useState('none')
  const [input, setInput] = useState('')
  const [tools, setTools] = useState<OpenAIToolSpec[]>([])
  const [intent, setIntent] = useState<OpenAISessionInput | null>(null)
  const capabilities = useQuery({
    queryKey: [...key, 'capabilities'],
    queryFn: () => openAIRequest<OpenAICapabilities>('/capabilities'),
    enabled: open && authenticated,
    retry: false,
  })
  const connections = useQuery({
    queryKey: [...key, 'connections'],
    queryFn: () => openAIRequest<{ data: OpenAIConnection[] }>('/connections'),
    enabled: open && authenticated,
    retry: false,
  })
  const currentConnection = connections.data?.data.find(
    (item) => item.id === connectionId,
  )
  const modes = capabilities.data?.environment_modes ?? []
  const create = useMutation({
    mutationFn: (body: OpenAISessionInput) =>
      openAIRequest<OpenAISession>('/sessions', 'POST', body),
    retry: false,
    onError: (error) => {
      if (
        error instanceof OpenAIRequestError &&
        [400, 403, 404].includes(error.status)
      )
        setIntent(null)
    },
    onSuccess: (session) => {
      setIntent(null)
      setInput('')
      void cache.invalidateQueries({ queryKey: key })
      onOpenChange(false)
      onCreated(session)
    },
  })
  const submit = () => {
    const body = intent ?? {
      connection_id: connectionId,
      idempotency_key: crypto.randomUUID(),
      model: model.trim(),
      environment: { type: environment },
      input,
      tools,
    }
    setIntent(body)
    create.mutate(body)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => !create.isPending && onOpenChange(next)}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto border-brand-main-700 bg-brand-main-900 text-brand-main-100 sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Start an OpenAI agent session</DialogTitle>
          <DialogDescription>
            OpenAI manages the agent session. Everstack supplies approved tools
            and optional sandbox compute.
          </DialogDescription>
        </DialogHeader>

        <div className="rounded-md border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-100 light:text-amber-800">
          OpenAI retains session state in the United States. Self-hosted compute
          does not enable Zero Data Retention.
        </div>

        {(capabilities.error || connections.error) && (
          <div
            role="alert"
            className="rounded-md border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-300 light:text-red-700"
          >
            {errorText(capabilities.error ?? connections.error)}
          </div>
        )}
        {capabilities.data && !capabilities.data.enabled && (
          <div
            role="status"
            className="text-sm text-white/55 light:text-black/55"
          >
            OpenAI Agents API is not configured on this instance.
          </div>
        )}

        {showConnection ? (
          <OpenAIConnectionForm
            existing={editingConnection}
            onSaved={() => {
              setShowConnection(false)
              setEditingConnection(undefined)
              void cache.invalidateQueries({
                queryKey: [...key, 'connections'],
              })
            }}
            onDeleted={() => {
              setConnectionId('')
              setModel('')
              setShowConnection(false)
              setEditingConnection(undefined)
              void cache.invalidateQueries({
                queryKey: [...key, 'connections'],
              })
            }}
            onCancel={() => {
              setShowConnection(false)
              setEditingConnection(undefined)
            }}
          />
        ) : (
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              submit()
            }}
          >
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-3">
                <Label htmlFor="openai-session-connection">
                  OpenAI connection
                </Label>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    setEditingConnection(undefined)
                    setShowConnection(true)
                  }}
                  disabled={!capabilities.data?.can_manage_connections}
                >
                  Add connection
                </Button>
              </div>
              <Select
                value={connectionId}
                onValueChange={(value) => {
                  setConnectionId(value)
                  setModel('')
                }}
                disabled={!!intent}
              >
                <SelectTrigger
                  id="openai-session-connection"
                  aria-label="OpenAI connection"
                  className="w-full border-brand-main-600 bg-brand-main-900"
                >
                  <SelectValue placeholder="Select a connection" />
                </SelectTrigger>
                <SelectContent>
                  {connections.data?.data.map((connection) => (
                    <SelectItem key={connection.id} value={connection.id}>
                      {connection.name} ({connection.status})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {currentConnection &&
                capabilities.data?.can_manage_connections && (
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setEditingConnection(currentConnection)
                      setShowConnection(true)
                    }}
                  >
                    Manage selected connection
                  </Button>
                )}
              {currentConnection?.last_error && (
                <p className="text-xs text-red-400">
                  {currentConnection.last_error}
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="openai-session-model">Model</Label>
              <Input
                id="openai-session-model"
                value={model}
                onChange={(event) => setModel(event.target.value)}
                list="openai-session-models"
                placeholder="Model available to this project"
                disabled={!!intent}
                className="border-brand-main-600 bg-brand-main-900"
              />
              <datalist id="openai-session-models">
                {currentConnection?.allowed_models?.map((name) => (
                  <option key={name} value={name} />
                ))}
              </datalist>
            </div>
            <div className="space-y-2">
              <Label htmlFor="openai-session-environment">
                Execution environment
              </Label>
              <Select
                value={environment}
                onValueChange={setEnvironment}
                disabled={!!intent}
              >
                <SelectTrigger
                  id="openai-session-environment"
                  aria-label="Execution environment"
                  className="w-full border-brand-main-600 bg-brand-main-900"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {modes.map((mode) => (
                    <SelectItem key={mode} value={mode}>
                      {mode === 'none'
                        ? 'OpenAI managed, no sandbox'
                        : 'Everstack sandbox'}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="openai-session-task">Task</Label>
              <Textarea
                id="openai-session-task"
                value={input}
                onChange={(event) => setInput(event.target.value)}
                rows={5}
                disabled={!!intent}
                className="border-brand-main-600 bg-brand-main-900"
              />
            </div>
            <OpenAIToolPicker
              supported={capabilities.data?.tool_types ?? []}
              value={tools}
              onChange={setTools}
              disabled={!!intent || !capabilities.data?.can_run_sessions}
            />
            {create.error && (
              <p
                role="alert"
                className="text-sm text-red-400 light:text-red-700"
              >
                {errorText(create.error)}{' '}
                {intent
                  ? 'Your original request is retained. Retry to recover it safely.'
                  : 'Update the request or connection before trying again.'}
              </p>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => onOpenChange(false)}
                disabled={create.isPending}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={
                  create.isPending ||
                  !capabilities.data?.enabled ||
                  !capabilities.data.can_run_sessions ||
                  !connectionId ||
                  !model.trim() ||
                  !input.trim() ||
                  !modes.includes(environment)
                }
              >
                {create.isPending
                  ? 'Starting…'
                  : intent
                    ? 'Recover session'
                    : 'Start session'}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

export function OpenAIConnectionForm({
  existing,
  onSaved,
  onCancel,
  onDeleted,
}: {
  existing?: OpenAIConnection
  onSaved: () => void
  onCancel: () => void
  onDeleted: () => void
}) {
  const [values, setValues] = useState<OpenAIConnectionInput>({
    name: existing?.name ?? '',
    api_key: '',
  })
  const [deleteOpen, setDeleteOpen] = useState(false)
  const save = useMutation({
    mutationFn: () =>
      openAIRequest<OpenAIConnection>(
        existing
          ? `/connections/${encodeURIComponent(existing.id)}`
          : '/connections',
        existing ? 'PATCH' : 'POST',
        Object.fromEntries(
          Object.entries(values).filter(([, value]) => value !== ''),
        ),
      ),
    retry: false,
    onSuccess: onSaved,
  })
  const remove = useMutation({
    mutationFn: () =>
      openAIRequest(
        `/connections/${encodeURIComponent(existing!.id)}`,
        'DELETE',
      ),
    retry: false,
    onSuccess: onDeleted,
  })
  return (
    <form
      className="space-y-4 rounded-md border border-brand-main-700 bg-brand-main-950/40 p-4"
      onSubmit={(event) => {
        event.preventDefault()
        save.mutate()
      }}
    >
      <div>
        <h3 className="text-sm font-medium">
          {existing ? 'Manage OpenAI connection' : 'Connect an OpenAI project'}
        </h3>
        <p className="mt-1 text-xs text-white/50 light:text-black/50">
          The application key stays on the server. Everstack sandbox sessions
          require a separate restricted environment key.
        </p>
      </div>
      {(
        [
          ['name', 'Connection name', false],
          ['api_key', 'Application API key', true],
          ['executor_key', 'Restricted executor key', true],
          ['project_id', 'Project ID', false],
          ['organization_id', 'Organization ID', false],
          ['principal_id', 'Service account or user ID', false],
        ] as const
      ).map(([name, label, secret]) => (
        <div className="space-y-2" key={name}>
          <Label htmlFor={`openai-${name}`}>{label}</Label>
          <Input
            id={`openai-${name}`}
            type={secret ? 'password' : 'text'}
            autoComplete={secret ? 'new-password' : 'off'}
            value={values[name] ?? ''}
            required={name === 'name' || (!existing && name === 'api_key')}
            onChange={(event) =>
              setValues({ ...values, [name]: event.target.value })
            }
            className="border-brand-main-600 bg-brand-main-900"
          />
        </div>
      ))}
      {(save.error || remove.error) && (
        <p role="alert" className="text-sm text-red-400">
          {errorText(save.error ?? remove.error)}
        </p>
      )}
      <div className="flex justify-end gap-2">
        {existing && (
          <Button
            type="button"
            variant="destructive"
            onClick={() => setDeleteOpen(true)}
            disabled={save.isPending || remove.isPending}
          >
            Delete
          </Button>
        )}
        <Button type="button" variant="ghost" onClick={onCancel}>
          Back
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending
            ? 'Saving…'
            : existing
              ? 'Update connection'
              : 'Save connection'}
        </Button>
      </div>
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent className="border-brand-main-700 bg-brand-main-900 text-brand-main-100">
          <AlertDialogHeader>
            <AlertDialogTitle>Delete connection?</AlertDialogTitle>
            <AlertDialogDescription>
              Sessions using {existing?.name} may no longer be continued.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-red-500/90 text-white hover:bg-red-500"
              onClick={(event) => {
                event.preventDefault()
                remove.mutate()
                setDeleteOpen(false)
              }}
            >
              Delete connection
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </form>
  )
}
