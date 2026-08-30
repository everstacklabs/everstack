import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Trash2 } from 'lucide-react'
import { ui } from '@everstack/ui'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import {
  useTroopers,
  useDeleteTrooper,
  useProvisionTrooper,
  useSleepTrooper,
  useWakeTrooper,
} from '@/hooks/deployments/use-troopers'
import type { Trooper } from '@/server/troopers'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { TrooperFormSheet } from '@/components/deployments/troopers/trooper-form-sheet'

const {
  Button,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} = ui

export const Route = createFileRoute('/deployments/troopers/')({
  component: TroopersPage,
})

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

const EMPTY_TROOPERS: Trooper[] = []

function TroopersPage() {
  const navigate = useNavigate()
  const [statusFilter, setStatusFilter] = useState<string>('all')
  const [showCreate, setShowCreate] = useState(false)
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const [deleteConfirmName, setDeleteConfirmName] = useState('')

  const { data, isLoading, error } = useTroopers()
  const allTroopers = data?.troopers ?? EMPTY_TROOPERS
  const troopers =
    statusFilter === 'all'
      ? allTroopers
      : allTroopers.filter((ws) => ws.status === statusFilter)

  const deleteMutation = useDeleteTrooper()
  const provisionMutation = useProvisionTrooper()
  const sleepMutation = useSleepTrooper()
  const wakeMutation = useWakeTrooper()

  if (isLoading) {
    return (
      <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading instances..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex-1 flex items-center justify-center text-red-400 light:text-red-600">
        Error loading instances: {error.message}
      </div>
    )
  }

  function handleDelete(id: string) {
    deleteMutation.mutate(id, {
      onSuccess: () => {
        toast.success('Instance deleted')
        setDeleteConfirmId(null)
      },
      onError: (e) => toast.error(`Failed: ${e.message}`),
    })
  }

  const columns: ColumnConfig<Trooper>[] = [
    {
      id: 'name',
      header: 'Name',
      width: 260,
      minWidth: 140,
      render: (ws) => (
        <Link
          to="/deployments/troopers/$trooperId"
          params={{ trooperId: ws.id }}
          className="flex min-w-0 items-center gap-2"
        >
          <span
            className="h-2.5 w-2.5 shrink-0 rounded-full border border-white/20 light:border-black/20"
            style={{ backgroundColor: ws.color || '#64748b' }}
          />
          <span className="truncate font-medium text-brand-secondary-100 text-xs">
            {ws.icon && <span className="mr-1">{ws.icon}</span>}
            {ws.name}
          </span>
        </Link>
      ),
    },
    {
      id: 'model',
      header: 'Model',
      width: 200,
      minWidth: 120,
      render: (ws) => (
        <span className="text-xs text-zinc-400 light:text-zinc-600 font-mono truncate">
          {ws.model || '--'}
        </span>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      width: 120,
      minWidth: 90,
      render: (ws) => (
        <span
          className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium border ${STATUS_STYLES[ws.status] ?? STATUS_STYLES.created}`}
        >
          {formatTrooperStatus(ws.status)}
        </span>
      ),
    },
    {
      id: 'image',
      header: 'Image',
      width: 160,
      minWidth: 120,
      render: (ws) => (
        <span className="text-xs text-zinc-500 light:text-zinc-600 font-mono">
          {ws.sandbox?.image || '--'}
        </span>
      ),
    },
    {
      id: 'createdAt',
      header: 'Created',
      width: 180,
      minWidth: 140,
      render: (ws) => (
        <span className="text-xs text-zinc-500 light:text-zinc-600">
          {ws.createdAt ? new Date(ws.createdAt).toLocaleString() : '--'}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      width: 100,
      minWidth: 80,
      maxWidth: 120,
      resizable: false,
      render: (ws) => {
        const isCreated = ws.status === 'created'
        const isRunning = ws.status === 'running'
        const isSleeping = ws.status === 'sleeping'
        const isIntermediate = ['provisioning', 'waking'].includes(ws.status)

        return (
          <div
            className="flex items-center gap-1 justify-center"
            data-row-actions
          >
            {isCreated && (
              <button
                type="button"
                onClick={() =>
                  provisionMutation.mutate(ws.id, {
                    onSuccess: () => toast.success('Provisioning instance...'),
                    onError: (e) => toast.error(`Failed: ${e.message}`),
                  })
                }
                disabled={provisionMutation.isPending}
                className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors"
                title="Provision"
              >
                <Iconify.Icon
                  icon="heroicons:rocket-launch"
                  className="size-3.5"
                />
              </button>
            )}
            {isRunning && (
              <button
                type="button"
                onClick={() =>
                  sleepMutation.mutate(ws.id, {
                    onSuccess: () => toast.success('Instance sleeping...'),
                    onError: (e) => toast.error(`Failed: ${e.message}`),
                  })
                }
                disabled={sleepMutation.isPending}
                className="p-1 rounded hover:bg-yellow-500/20 hover:text-yellow-400 light:hover:text-yellow-700 transition-colors"
                title="Sleep"
              >
                <Iconify.Icon icon="heroicons:pause" className="size-3.5" />
              </button>
            )}
            {isSleeping && (
              <button
                type="button"
                onClick={() =>
                  wakeMutation.mutate(ws.id, {
                    onSuccess: () => toast.success('Waking instance...'),
                    onError: (e) => toast.error(`Failed: ${e.message}`),
                  })
                }
                disabled={wakeMutation.isPending}
                className="p-1 rounded hover:bg-green-500/20 hover:text-green-400 light:hover:text-green-600 transition-colors"
                title="Wake"
              >
                <Iconify.Icon icon="heroicons:play" className="size-3.5" />
              </button>
            )}
            {!isIntermediate && (
              <button
                type="button"
                onClick={() => {
                  setDeleteConfirmId(ws.id)
                  setDeleteConfirmName(ws.name)
                }}
                className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                title="Delete instance"
              >
                <Trash2 size={14} />
              </button>
            )}
            {isIntermediate && (
              <span className="text-[10px] text-white/40 light:text-black/40 animate-pulse">
                {ws.status}...
              </span>
            )}
          </div>
        )
      },
    },
  ]

  return (
    <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
      <div className="shrink-0 flex items-center justify-between px-4 py-2 border-b border-brand-main-800/40 bg-brand-main-900/20">
        <div className="flex items-center gap-2">
          <span className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
            Status
          </span>
          <Select value={statusFilter} onValueChange={setStatusFilter}>
            <SelectTrigger className="h-8 w-[150px] bg-brand-main-900/60 border-brand-main-700 text-xs text-zinc-200 light:text-zinc-800">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
              <SelectItem value="all">All ({allTroopers.length})</SelectItem>
              <SelectItem value="created">Created</SelectItem>
              <SelectItem value="running">Running</SelectItem>
              <SelectItem value="sleeping">Sleeping</SelectItem>
              <SelectItem value="failed">Failed</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
      <ResponsiveTable
        columns={columns}
        data={troopers}
        enableResizing={true}
        minTableWidth="100%"
        onRowClick={(ws) =>
          navigate({
            to: '/deployments/troopers/$trooperId',
            params: { trooperId: ws.id },
          })
        }
        emptyMessage={
          allTroopers.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-12">
              <div className="relative mb-6">
                <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                  <Iconify.Icon
                    icon="lucide:hard-drive"
                    className="size-8 text-brand-secondary-400"
                  />
                </div>
              </div>
              <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
                No instances yet
              </h3>
              <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                Create an instance to get started.
              </p>
            </div>
          ) : (
            'No instances match your filter.'
          )
        }
        rowKey={(ws) => ws.id}
      />
      {/* Delete confirmation dialog matching agents pattern */}
      <Dialog
        open={deleteConfirmId !== null}
        onOpenChange={(open) => !open && setDeleteConfirmId(null)}
      >
        <DialogContent className="w-[500px]">
          <DialogTitle>Delete Instance</DialogTitle>
          <DialogDescription className="text-brand-main-100">
            Are you sure you want to delete{' '}
            <strong className="text-brand-main-100">{deleteConfirmName}</strong>
            ? This action cannot be undone and the instance sandbox will be
            terminated.
          </DialogDescription>
          <div className="flex justify-end gap-3 mt-4">
            <Button
              variant="outline"
              onClick={() => setDeleteConfirmId(null)}
              disabled={deleteMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
              onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <TrooperFormSheet open={showCreate} onOpenChange={setShowCreate} />
    </div>
  )
}
