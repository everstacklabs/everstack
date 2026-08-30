import { useState } from 'react'
import { useSandboxCrons, useCreateCron, useUpdateCron, useDeleteCron } from '@/hooks/deployments/use-sandbox'
import { useSandboxContext, SandboxSessionPicker } from './sandbox-context'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'

const { Button, Input, Label, Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, Switch } = ui

// ─── Helpers ──────────────────────────────────────────────────────────

function formatDate(ts?: string | null): string {
    if (!ts) return '--'
    return new Date(ts).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

const STATUS_STYLES: Record<string, string> = {
    enabled: 'bg-green-500/20 text-green-400 light:text-green-600 border-green-500/30',
    disabled: 'bg-gray-500/20 text-gray-400 light:text-gray-600 border-gray-500/30',
}

// ─── Main Component ─────────────────────────────────────────────────

export function CronsTab() {
    const [dialogOpen, setDialogOpen] = useState(false)
    const [newName, setNewName] = useState('')
    const [newSchedule, setNewSchedule] = useState('')
    const [newCommand, setNewCommand] = useState('')
    const [newTimeout, setNewTimeout] = useState('300')
    const [newAutoRecreate, setNewAutoRecreate] = useState(false)

    const { activeSessionId: sessionId } = useSandboxContext()

    const { data: cronData, isLoading, error } = useSandboxCrons(sessionId)
    const crons = cronData?.crons ?? []
    const createMutation = useCreateCron()
    const updateMutation = useUpdateCron()
    const deleteMutation = useDeleteCron()

    const handleCreate = () => {
        if (!newName.trim() || !newSchedule.trim() || !newCommand.trim()) {
            toast.error('Name, schedule, and command are required')
            return
        }
        createMutation.mutate(
            {
                sessionId: sessionId!,
                name: newName.trim(),
                schedule: newSchedule.trim(),
                command: newCommand.trim(),
                timeoutSeconds: parseInt(newTimeout, 10) || 300,
                autoRecreate: newAutoRecreate,
            },
            {
                onSuccess: () => {
                    toast.success(`Cron "${newName}" created`)
                    setDialogOpen(false)
                    resetForm()
                },
                onError: (err) => toast.error(err.message),
            }
        )
    }

    const handleToggle = (cronId: string, enabled: boolean) => {
        updateMutation.mutate(
            { cronId, enabled: !enabled },
            {
                onSuccess: () => toast.success(enabled ? 'Cron disabled' : 'Cron enabled'),
                onError: (err) => toast.error(err.message),
            }
        )
    }

    const handleDelete = (cronId: string, name: string) => {
        deleteMutation.mutate(cronId, {
            onSuccess: () => toast.success(`Cron "${name}" deleted`),
            onError: (err) => toast.error(err.message),
        })
    }

    const resetForm = () => {
        setNewName('')
        setNewSchedule('')
        setNewCommand('')
        setNewTimeout('300')
        setNewAutoRecreate(false)
    }

    return (
        <div className="flex flex-col h-full">
            {/* Controls */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <SandboxSessionPicker />

                <div className="flex-1" />

                <button
                    onClick={() => setDialogOpen(true)}
                    disabled={!sessionId}
                    className="flex items-center gap-1 text-xs bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500/30 rounded px-2.5 py-1 hover:bg-brand-secondary-600/30 disabled:opacity-50"
                >
                    <Iconify.Icon icon="heroicons:plus" className="size-3" />
                    New Cron
                </button>
            </div>

            {/* Content */}
            <div className="flex-1 overflow-y-auto">
                {!sessionId ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:clock" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No session selected</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Select a running session to manage crons.
                        </p>
                    </div>
                ) : isLoading ? (
                    <div className="flex-1 flex items-center justify-center h-full">
                        <Loader loaderText="Loading crons..." />
                    </div>
                ) : error ? (
                    <div className="flex items-center justify-center h-full text-red-400 light:text-red-600 text-sm">
                        Error loading crons: {error.message}
                    </div>
                ) : crons.length === 0 ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:clock" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No cron jobs</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Create a cron to run commands on a schedule.
                        </p>
                    </div>
                ) : (
                    <div className="p-4">
                        <table className="w-full text-sm">
                            <thead>
                                <tr className="border-b border-brand-main-600 text-white/40 light:text-black/40 text-xs uppercase">
                                    <th className="text-left py-2 px-3 font-medium">Name</th>
                                    <th className="text-left py-2 px-3 font-medium">Schedule</th>
                                    <th className="text-left py-2 px-3 font-medium">Command</th>
                                    <th className="text-left py-2 px-3 font-medium">Status</th>
                                    <th className="text-left py-2 px-3 font-medium">Runs</th>
                                    <th className="text-left py-2 px-3 font-medium">Last Run</th>
                                    <th className="text-left py-2 px-3 font-medium">Next Run</th>
                                    <th className="text-right py-2 px-3 font-medium">Actions</th>
                                </tr>
                            </thead>
                            <tbody>
                                {crons.map((cron) => (
                                    <tr
                                        key={cron.id}
                                        className="border-b border-brand-main-700/50 hover:bg-brand-main-800/50"
                                    >
                                        <td className="py-2.5 px-3 text-white/80 light:text-black/80 font-medium">{cron.name}</td>
                                        <td className="py-2.5 px-3 font-mono text-xs text-white/60 light:text-black/60">{cron.schedule}</td>
                                        <td className="py-2.5 px-3 font-mono text-xs text-white/50 light:text-black/50 max-w-[200px] truncate" title={cron.command}>
                                            {cron.command}
                                        </td>
                                        <td className="py-2.5 px-3">
                                            <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border ${STATUS_STYLES[cron.enabled ? 'enabled' : 'disabled']}`}>
                                                {cron.enabled ? 'enabled' : 'disabled'}
                                            </span>
                                        </td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">
                                            {cron.runCount}
                                            {cron.errorCount > 0 && (
                                                <span className="text-red-400 light:text-red-600 ml-1">({cron.errorCount} err)</span>
                                            )}
                                        </td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{formatDate(cron.lastRunAt)}</td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{formatDate(cron.nextRunAt)}</td>
                                        <td className="py-2.5 px-3 text-right">
                                            <div className="flex items-center justify-end gap-1">
                                                <button
                                                    onClick={() => handleToggle(String(cron.id), cron.enabled)}
                                                    disabled={updateMutation.isPending}
                                                    className="p-1 rounded text-white/40 light:text-black/40 hover:text-white/70 light:hover:text-black/70 hover:bg-brand-main-700/50 transition-colors disabled:opacity-50"
                                                    title={cron.enabled ? 'Disable' : 'Enable'}
                                                >
                                                    <Iconify.Icon
                                                        icon={cron.enabled ? 'heroicons:pause' : 'heroicons:play'}
                                                        className="size-4"
                                                    />
                                                </button>
                                                <button
                                                    onClick={() => handleDelete(String(cron.id), cron.name)}
                                                    disabled={deleteMutation.isPending}
                                                    className="p-1 rounded text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-600 hover:bg-red-500/10 transition-colors disabled:opacity-50"
                                                    title="Delete"
                                                >
                                                    <Iconify.Icon icon="heroicons:trash" className="size-4" />
                                                </button>
                                            </div>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>

            {/* Create Cron Dialog */}
            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
                <DialogContent className="bg-brand-main-900 border-brand-main-600">
                    <DialogHeader>
                        <DialogTitle className="text-white light:text-brand-main-50">New Cron Job</DialogTitle>
                    </DialogHeader>
                    <div className="space-y-4 py-2">
                        <div className="space-y-1.5">
                            <Label htmlFor="cronName" className="text-xs text-white/60 light:text-black/60">Name</Label>
                            <Input
                                id="cronName"
                                value={newName}
                                onChange={(e) => setNewName(e.target.value)}
                                placeholder="e.g. health-check"
                                className="w-full"
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="cronSchedule" className="text-xs text-white/60 light:text-black/60">Schedule (cron expression)</Label>
                            <Input
                                id="cronSchedule"
                                value={newSchedule}
                                onChange={(e) => setNewSchedule(e.target.value)}
                                placeholder="*/5 * * * *"
                                className="w-full font-mono"
                            />
                            <p className="text-[10px] text-white/30 light:text-black/30">minute hour day-of-month month day-of-week</p>
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="cronCommand" className="text-xs text-white/60 light:text-black/60">Command</Label>
                            <Input
                                id="cronCommand"
                                value={newCommand}
                                onChange={(e) => setNewCommand(e.target.value)}
                                placeholder="python check.py"
                                className="w-full font-mono"
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="cronTimeout" className="text-xs text-white/60 light:text-black/60">Timeout (seconds)</Label>
                            <Input
                                id="cronTimeout"
                                type="number"
                                value={newTimeout}
                                onChange={(e) => setNewTimeout(e.target.value)}
                                min={1}
                                max={3600}
                                className="w-full"
                            />
                        </div>
                        <div className="flex items-center gap-2">
                            <Switch
                                checked={newAutoRecreate}
                                onCheckedChange={setNewAutoRecreate}
                            />
                            <Label className="text-xs text-white/60 light:text-black/60">Auto-recreate sandbox if destroyed</Label>
                        </div>
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => { setDialogOpen(false); resetForm() }}>
                            Cancel
                        </Button>
                        <Button
                            onClick={handleCreate}
                            disabled={createMutation.isPending || !newName || !newSchedule || !newCommand}
                        >
                            {createMutation.isPending ? 'Creating...' : 'Create'}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    )
}
