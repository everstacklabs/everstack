import { useState } from 'react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { useCatalogStatus, useTriggerCatalogSync } from '@/hooks/vault/use-catalog'
import { toast } from '@everstack/ui/components'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'

dayjs.extend(relativeTime)

const { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger, Button, Badge } = ui

export function CatalogSyncSettings() {
    const { data: status, isLoading } = useCatalogStatus()
    const triggerSyncMutation = useTriggerCatalogSync()
    const [isSyncing, setIsSyncing] = useState(false)
    const [open, setOpen] = useState(false)

    const handleManualSync = async () => {
        setIsSyncing(true)
        try {
            await triggerSyncMutation.mutateAsync(true) // Force sync
            toast.success('Catalog synced successfully!')
        } catch (err) {
            toast.error(`Failed to sync catalog: ${err instanceof Error ? err.message : 'Unknown error'}`)
        } finally {
            setIsSyncing(false)
        }
    }

    const lastSyncTime = status?.lastSync && status.lastSync !== '' ? new Date(status.lastSync) : null
    const lastCheckTime = status?.lastCheck && status.lastCheck !== '' ? new Date(status.lastCheck) : null

    return (
        <Sheet open={open} onOpenChange={setOpen}>
            <SheetTrigger asChild>
                <Button
                    variant="ghost"
                    className="focus:ring-0 border-brand-main-500 hover:bg-brand-main-600 text-white light:text-brand-main-50"
                >
                    <Iconify.Icon icon="material-symbols:settings" className="size-5" />
                    {/* {status?.hasUpdates && (
                        <Badge variant="secondary" className="ml-2 bg-brand-secondary-500/20 text-brand-secondary-300 h-5 px-1.5">
                            <Iconify.Icon icon="material-symbols:update" className="size-3" />
                        </Badge>
                    )} */}
                </Button>
            </SheetTrigger>
            <SheetContent className="bg-brand-main-800 border-brand-main-500 sm:max-w-lg">
                <SheetHeader>
                    <SheetTitle className="text-white light:text-brand-main-50 text-sm flex items-center gap-2">
                        <Iconify.Icon icon="ri:refresh-line" className="size-4" />
                        Catalog Sync Settings
                    </SheetTitle>
                </SheetHeader>

                {isLoading ? (
                    <div className="flex items-center text-xs justify-center py-8">
                        <Iconify.Icon icon="eos-icons:loading" className="size-6 animate-spin text-white light:text-brand-main-50" />
                        <span className="ml-2 text-white light:text-brand-main-50">Loading catalog settings...</span>
                    </div>
                ) : (
                    <div className="space-y-6 mt-6 px-3">
                        {/* Sync Status */}
                        <div className="space-y-4">
                            <div className="flex items-center justify-between p-4 rounded-lg bg-brand-main-700">
                                <div className="space-y-1">
                                    <p className="text-xs font-medium text-white light:text-brand-main-50">Automatic Sync</p>
                                    <p className="text-xs text-white/60 light:text-black/60">
                                        Automatically check for catalog updates daily
                                    </p>
                                </div>
                                <Badge
                                    variant={status?.syncEnabled ? "default" : "outline"}
                                    className={status?.syncEnabled ? "bg-green-600" : ""}
                                >
                                    {status?.syncEnabled ? "Enabled" : "Disabled"}
                                </Badge>
                            </div>

                            {/* Current Status */}
                            <div className="grid grid-cols-2 gap-4 p-4 rounded-lg bg-brand-main-700">
                                <div className="space-y-1">
                                    <p className="text-xs text-white/60 light:text-black/60">Current Version</p>
                                    <p className="text-sm font-mono text-white light:text-brand-main-50">
                                        {status?.currentVersion || 'N/A'}
                                    </p>
                                </div>
                                <div className="space-y-1">
                                    <p className="text-xs text-white/60 light:text-black/60">Remote Version</p>
                                    <p className="text-sm font-mono text-white light:text-brand-main-50">
                                        {status?.remoteVersion || 'N/A'}
                                    </p>
                                </div>
                                {lastSyncTime && (
                                    <div className="space-y-1">
                                        <p className="text-xs text-white/60 light:text-black/60">Last Sync</p>
                                        <p className="text-sm text-white light:text-brand-main-50">
                                            {dayjs(lastSyncTime).fromNow()}
                                        </p>
                                    </div>
                                )}
                                {lastCheckTime && (
                                    <div className="space-y-1">
                                        <p className="text-xs text-white/60 light:text-black/60">Last Check</p>
                                        <p className="text-sm text-white light:text-brand-main-50">
                                            {dayjs(lastCheckTime).fromNow()}
                                        </p>
                                    </div>
                                )}
                            </div>

                            {/* Sync URL */}
                            <div className="space-y-2">
                                <p className="text-sm font-medium text-white light:text-brand-main-50">Sync Source</p>
                                <div className="flex items-center gap-2 p-3 rounded-lg bg-brand-main-700">
                                    <Iconify.Icon icon="material-symbols:link" className="size-4 text-white/60 light:text-black/60" />
                                    <code className="text-xs text-white/80 light:text-black/80 break-all">
                                        {status?.syncUrl || 'Not configured'}
                                    </code>
                                </div>
                            </div>
                        </div>

                        {/* Actions */}
                        <div className="flex items-center gap-3 pt-4 border-t border-brand-main-600">
                            <Button
                                onClick={handleManualSync}
                                disabled={isSyncing}
                            >
                                {isSyncing ? (
                                    <>
                                        <Iconify.Icon icon="icon-park-outline:loading-four" className="size-5 animate-spin" />
                                        Syncing...
                                    </>
                                ) : (
                                    <>
                                        <Iconify.Icon icon="fluent:arrow-sync-circle-16-regular" className="size-5" />
                                        Sync Now
                                    </>
                                )}
                            </Button>

                            {status?.hasUpdates && (
                                <p className="text-sm text-white/60 light:text-black/60">
                                    {status.newModelsCount + status.newProvidersCount} new items available
                                </p>
                            )}
                        </div>
                    </div>
                )}
            </SheetContent>
        </Sheet>
    )
}

