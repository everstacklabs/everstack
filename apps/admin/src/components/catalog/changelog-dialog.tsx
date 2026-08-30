import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { useChangelog } from '@/hooks/vault/use-catalog'

const { Dialog, DialogContent, DialogHeader, DialogTitle, Badge } = ui

interface ChangelogModalProps {
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function ChangelogModal({ open, onOpenChange }: ChangelogModalProps) {
    const { data: changelog, isLoading } = useChangelog()

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <Iconify.Icon icon="material-symbols:history" className="size-5" />
                        Model catalog updates
                    </DialogTitle>
                </DialogHeader>

                {isLoading && (
                    <div className="flex items-center justify-center py-8">
                        <Iconify.Icon icon="eos-icons:loading" className="size-6 animate-spin" />
                        <span className="ml-2">Loading changelog...</span>
                    </div>
                )}

                {changelog && changelog.entries && changelog.entries.length > 0 ? (
                    <div className="space-y-6">
                        {changelog.entries.map((entry, index) => (
                            <div key={index} className="space-y-3">
                                <div className="flex items-center gap-3">
                                    <Badge variant="outline" className="font-mono">
                                        v{entry.version}
                                    </Badge>
                                    <span className="text-sm text-white/70 light:text-black/70">
                                        {entry.date}
                                    </span>
                                </div>

                                <p className="text-sm">{entry.description}</p>

                                {entry.newModels && entry.newModels.length > 0 && (
                                    <div>
                                        <h4 className="text-sm font-medium mb-2 text-green-600 dark:text-green-400">
                                            New models
                                        </h4>
                                        <ul className="text-sm space-y-1 ml-4">
                                            {entry.newModels.map((model, idx) => (
                                                <li key={idx} className="list-disc">{model}</li>
                                            ))}
                                        </ul>
                                    </div>
                                )}

                                {entry.newProviders && entry.newProviders.length > 0 && (
                                    <div>
                                        <h4 className="text-sm font-medium mb-2 text-blue-600 dark:text-blue-400">
                                            New providers
                                        </h4>
                                        <ul className="text-sm space-y-1 ml-4">
                                            {entry.newProviders.map((provider, idx) => (
                                                <li key={idx} className="list-disc">{provider}</li>
                                            ))}
                                        </ul>
                                    </div>
                                )}

                                {entry.updatedModels && entry.updatedModels.length > 0 && (
                                    <div>
                                        <h4 className="text-sm font-medium mb-2 text-amber-600 dark:text-amber-400">
                                            Updated models
                                        </h4>
                                        <ul className="text-sm space-y-1 ml-4">
                                            {entry.updatedModels.map((model, idx) => (
                                                <li key={idx} className="list-disc">{model}</li>
                                            ))}
                                        </ul>
                                    </div>
                                )}

                                {entry.deprecatedModels && entry.deprecatedModels.length > 0 && (
                                    <div>
                                        <h4 className="mb-2 text-sm font-medium text-red-500">
                                            Deprecated models
                                        </h4>
                                        <ul className="ml-4 space-y-1 text-sm">
                                            {entry.deprecatedModels.map((model, idx) => (
                                                <li key={idx} className="list-disc">{model}</li>
                                            ))}
                                        </ul>
                                    </div>
                                )}

                                {entry.pricingChanges && entry.pricingChanges.length > 0 && (
                                    <div>
                                        <h4 className="text-sm font-medium mb-2 text-purple-600 dark:text-purple-400">
                                            Pricing updates
                                        </h4>
                                        <ul className="text-sm space-y-1 ml-4">
                                            {entry.pricingChanges.map((change, idx) => (
                                                <li key={idx} className="list-disc">{change}</li>
                                            ))}
                                        </ul>
                                    </div>
                                )}

                                {index < (changelog.entries?.length || 0) - 1 && (
                                    <hr className="my-4 border-border" />
                                )}
                            </div>
                        ))}
                    </div>
                ) : (
                    !isLoading && (
                        <div className="text-center py-8 text-white/70 light:text-black/70">
                            No changelog entries available
                        </div>
                    )
                )}
            </DialogContent>
        </Dialog>
    )
}
