import { ResponsiveTable, type ColumnConfig, type RowAction } from '@/ui/table'
import dayjs from 'dayjs'
import { type ProviderStatus } from '@/server/providers'
import { ProviderDisplay } from './provider-icon'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { useProviderAPIKeys } from '@/hooks/vault'
import { cn } from '@everstack/utils/functions/cn'
import LocalizedFormat from 'dayjs/plugin/localizedFormat'

dayjs.extend(LocalizedFormat)

const { Badge } = ui

interface ProvidersTableProps {
    providers: ProviderStatus[]
    onConfigure: (providerName: string) => void
    onToggle: (providerName: string, isActive: boolean) => void
    onDelete: (providerName: string) => void
    onToggleDefault: (providerName: string, setAsDefault: boolean) => void
    isConfiguredTable?: boolean
}

export function ProvidersTable({
    providers,
    onConfigure,
    onToggle,
    onDelete,
    onToggleDefault,
    isConfiguredTable = false
}: ProvidersTableProps) {

    const columns: ColumnConfig<ProviderStatus>[] = [
        {
            id: 'provider',
            header: 'Provider',
            width: 280,
            minWidth: 250,
            resizable: false,
            render: (provider) => (
                <div className="flex items-center gap-3">
                    <div className={cn(
                        'p-1.5 rounded-md flex-shrink-0',
                        provider.isActive ? 'bg-brand-secondary-500/10' : 'bg-brand-main-800'
                    )}>
                        <ProviderDisplay
                            providerName={provider.catalog?.name || ''}
                            isActive={provider.isActive}
                            useImage={true}
                        />
                    </div>
                    <div className="flex flex-col min-w-0">
                        <span className="text-sm font-medium text-white light:text-brand-main-50 truncate">
                            {provider.catalog?.displayName}
                        </span>
                        <div className="flex items-center gap-2">
                            <span className="text-xs text-white/50 light:text-black/50 truncate">
                                {provider.catalog?.name}
                            </span>
                        </div>
                    </div>
                </div>
            )
        },
        ...(isConfiguredTable ? [
            {
                id: 'status',
                header: 'Status',
                resizable: false,
                width: 140,
                minWidth: 120,
                render: (provider: ProviderStatus) => {
                    const isDefault = provider.configuration?.customSettings?.default === 'true'
                    return (
                        <div className="flex items-center gap-2 flex-wrap">
                            {isDefault && (
                                <Badge className="text-xs rounded-sm px-1.5 py-0.5 bg-blue-600/20 text-blue-400 light:text-blue-600 border border-blue-600/30">
                                    Default
                                </Badge>
                            )}
                            <Badge variant={provider.isActive ? "active" : "secondary"} className="text-xs rounded-sm px-1.5 py-0.5 border-brand-main-400">
                                {provider.isActive ? 'Active' : 'Inactive'}
                            </Badge>
                        </div>
                    )
                }
            },
            {
                id: 'apiKeys',
                header: 'API Keys',
                resizable: false,
                width: 120,
                minWidth: 100,
                render: (provider: ProviderStatus) => <APIKeysCell provider={provider} />
            },
            {
                id: 'configuredAt',
                header: 'Configured At',
                resizable: false,
                width: 150,
                minWidth: 130,
                render: (provider: ProviderStatus) => {
                    const date = provider.configuration?.createdAt
                    if (!date) return <span className="text-white/30 light:text-black/30 text-xs">-</span>
                    return (
                        <span className="text-sm text-white/70 light:text-black/70">
                            {dayjs(date).format('MMM D, YYYY')}
                        </span>
                    )
                }
            },
            {
                id: 'lastUsedAt',
                header: 'Last Used At',
                resizable: false,
                width: 160,
                minWidth: 140,
                render: (provider: ProviderStatus) => {
                    const date = (provider as any).lastUsedAt || provider.configuration?.lastUsedAt
                    if (!date) return <span className="text-white/30 light:text-black/30 text-xs">-</span>
                    return (
                        <span className="text-sm text-white/70 light:text-black/70">
                            {dayjs(date).format('MMM D, YYYY LT')}
                        </span>
                    )
                }
            }
        ] : []),
        {
            id: 'models',
            header: 'Models',
            resizable: false,
            width: 140,
            minWidth: 120,
            render: (provider) => (
                <span className="text-sm text-white/70 light:text-black/70">
                    {isConfiguredTable
                        ? `${provider.configuredModelsCount} enabled`
                        : `${provider.availableModelsCount} available`
                    }
                </span>
            )
        },
        {
            id: 'baseUrl',
            header: 'Base URL',
            resizable: false,
            width: 200,
            minWidth: 150,
            render: (provider) => {
                const baseUrl = provider.configuration?.customBaseUrl || provider.catalog?.baseUrl
                if (!baseUrl) return <span className="text-white/30 light:text-black/30 text-xs">-</span>
                return (
                    <span className="text-xs text-white/50 light:text-black/50 truncate block max-w-[200px]" title={baseUrl}>
                        {baseUrl}
                    </span>
                )
            }
        }
    ]

    const rowActions: RowAction<ProviderStatus>[] = [
        {
            label: isConfiguredTable ? 'Configure' : 'Set Up',
            icon: <Iconify.Icon icon="heroicons:cog-6-tooth" className="w-4 h-4" />,
            onClick: (provider) => onConfigure(provider.catalog?.name || '')
        },
        ...(isConfiguredTable ? [
            {
                label: 'Set as Default',
                icon: <Iconify.Icon icon="heroicons:star" className="w-4 h-4" />,
                onClick: (provider: ProviderStatus) => onToggleDefault(provider.catalog?.name || '', true),
                disabled: (provider: ProviderStatus) => !provider.isActive || provider.configuration?.customSettings?.default === 'true'
            },
            {
                label: 'Unset Default',
                icon: <Iconify.Icon icon="heroicons:star-solid" className="w-4 h-4" />,
                onClick: (provider: ProviderStatus) => onToggleDefault(provider.catalog?.name || '', false),
                disabled: (provider: ProviderStatus) => provider.configuration?.customSettings?.default !== 'true'
            },
            {
                label: 'Enable',
                icon: <Iconify.Icon icon="heroicons:eye" className="w-4 h-4" />,
                onClick: (provider: ProviderStatus) => onToggle(provider.catalog?.name || '', true),
                disabled: (provider: ProviderStatus) => provider.isActive
            },
            {
                label: 'Disable',
                icon: <Iconify.Icon icon="heroicons:eye-slash" className="w-4 h-4" />,
                onClick: (provider: ProviderStatus) => onToggle(provider.catalog?.name || '', false),
                disabled: (provider: ProviderStatus) => !provider.isActive
            },
            {
                label: 'Delete',
                icon: <Iconify.Icon icon="heroicons:trash" className="w-4 h-4" />,
                variant: 'destructive' as const,
                onClick: (provider: ProviderStatus) => onDelete(provider.catalog?.name || '')
            }
        ] : [])
    ]

    return (
        <div className="w-full border-y border-brand-main-600 overflow-hidden bg-brand-main-900/20">
            <ResponsiveTable
                columns={columns}
                data={providers}
                rowActions={rowActions}
                enableResizing={false}
                minTableWidth="100%"
                onRowClick={(provider) => onConfigure(provider.catalog?.name || '')}
            />
        </div>
    )
}

function APIKeysCell({ provider }: { provider: ProviderStatus }) {
    const { data: apiKeysData } = useProviderAPIKeys(provider.configuration?.id || '')
    const apiKeys = apiKeysData?.keys || []

    if (apiKeys.length > 0) {
        return (
            <Badge className="text-xs rounded-sm px-1.5 py-0.5 bg-blue-600/20 text-blue-400 light:text-blue-600 border border-blue-600/30">
                {apiKeys.length} Keys
            </Badge>
        )
    }

    return (
        <Badge variant="outline" className="text-xs rounded-sm px-1.5 py-0.5 bg-orange-600/20 text-orange-400 light:text-orange-600 border border-orange-600/30">
            No Keys
        </Badge>
    )
}
