import { useFunctions, useDeleteFunction, useUpdateFunction } from '@/hooks/deployments/use-functions'
import { type Function, ExecutionMode } from '@/server/functions'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Trash2, Pencil, ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { useSearch } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { FunctionModeBadge } from './function-mode-badge'
import { safeBigIntToNumber } from '@/utils/trace-formatters'

const { Dialog, DialogContent, DialogTitle, DialogDescription, Button, Switch } = ui

interface FunctionsTableProps {
    functions: Function[]
    onEdit?: (fn: Function) => void
}

const MethodBadge = ({ method }: { method: string }) => {
    return (
        <span className={`px-1 py-0.5 rounded text-[10px] mr-1 font-medium whitespace-nowrap ${method === 'POST' ? 'bg-blue-500/20 text-blue-300 light:text-blue-600' : method === 'GET' ? 'bg-green-500/20 text-green-300 light:text-green-600' : method === 'PUT' ? 'bg-yellow-500/20 text-yellow-300 light:text-yellow-700' : method === 'DELETE' ? 'bg-red-500/20 text-red-300 light:text-red-600' : 'bg-gray-500/20 text-gray-400 light:text-gray-600'
            }`}>
            {method}
        </span>
    )
}

const RUNTIME_DISPLAY: Record<string, { label: string; className: string }> = {
    nodejs20: { label: 'Node.js 20', className: 'bg-green-500/20 text-green-300 light:text-green-600' },
    deno: { label: 'Deno', className: 'bg-blue-500/20 text-blue-300 light:text-blue-600' },
    python3: { label: 'Python 3', className: 'bg-yellow-500/20 text-yellow-300 light:text-yellow-700' },
}

const RuntimeBadge = ({ runtime }: { runtime: string }) => {
    const display = RUNTIME_DISPLAY[runtime] ?? { label: runtime, className: 'bg-gray-500/20 text-gray-400 light:text-gray-600' }
    return (
        <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium whitespace-nowrap ${display.className}`}>
            {display.label}
        </span>
    )
}

export function FunctionsTable({ functions, onEdit }: FunctionsTableProps) {
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState<string>('')
    const deleteFunctionMutation = useDeleteFunction()
    const updateFunctionMutation = useUpdateFunction()
    const listFunctionsQuery = useFunctions()

    const search = useSearch({ strict: false })

    const filteredFunctions = useMemo(() => {
        let filtered = [...functions]

        // Apply search filter
        const searchTerm = (search as any)?.search?.toLowerCase()
        if (searchTerm) {
            filtered = filtered.filter(fn =>
                fn.name.toLowerCase().includes(searchTerm) ||
                fn.description.toLowerCase().includes(searchTerm)
            )
        }

        // Sort by created date (newest first)
        filtered.sort((a, b) => {
            const aTime = a.createdAt?.seconds ? (typeof a.createdAt.seconds === 'bigint' ? safeBigIntToNumber(a.createdAt.seconds) : Number(a.createdAt.seconds)) : 0
            const bTime = b.createdAt?.seconds ? (typeof b.createdAt.seconds === 'bigint' ? safeBigIntToNumber(b.createdAt.seconds) : Number(b.createdAt.seconds)) : 0
            return bTime - aTime
        })

        return filtered
    }, [functions, search])

    const handleDelete = async (id: string) => {
        try {
            await deleteFunctionMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('Function deleted successfully')
            listFunctionsQuery.refetch()
        } catch (error) {
            console.error('Failed to delete function:', error)
            toast.error('Failed to delete function')
        }
    }

    const handleToggleEnabled = async (fn: Function) => {
        try {
            await updateFunctionMutation.mutateAsync({
                id: fn.id,
                enabled: !fn.enabled,
            })
            toast.success(`Function ${fn.enabled ? 'disabled' : 'enabled'} successfully`)
            listFunctionsQuery.refetch()
        } catch (error) {
            console.error('Failed to toggle function:', error)
            toast.error('Failed to update function')
        }
    }

    const columns: ColumnConfig<Function>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 220,
            minWidth: 180,
            render: (fn: Function) => (
                <div className='flex flex-col'>
                    <span className='truncate font-medium'>{fn.name}</span>
                    {fn.description && (
                        <span className='truncate text-xs text-brand-main-100'>{fn.description}</span>
                    )}
                </div>
            )
        },
        {
            id: 'mode',
            header: 'Mode',
            width: 100,
            minWidth: 80,
            render: (fn: Function) => <FunctionModeBadge mode={fn.mode} />
        },
        {
            id: 'config',
            header: 'Configuration',
            width: 200,
            minWidth: 150,
            render: (fn: Function) => {
                if (fn.mode === ExecutionMode.WEBHOOK && fn.webhook) {
                    return (
                        <span className='truncate text-sm inline-flex items-center space-x-1 text-brand-main-100' title={fn.webhook.url}>
                            <MethodBadge method={fn.webhook.method} /> {fn.webhook.url}
                        </span>
                    )
                }
                if (fn.mode === ExecutionMode.PROXY && fn.proxy) {
                    return (
                        <span className='truncate inline-flex items-center space-x-1 text-sm text-brand-main-100' title={`${fn.proxy.baseUrl}${fn.proxy.path}`}>
                            <MethodBadge method={fn.proxy.method} /> {fn.proxy.baseUrl}{fn.proxy.path}
                        </span>
                    )
                }
                if (fn.mode === ExecutionMode.ISOLATED && fn.isolated) {
                    return (
                        <span className='truncate inline-flex items-center space-x-1 text-sm text-brand-main-100'>
                            <RuntimeBadge runtime={fn.isolated.runtime} />
                        </span>
                    )
                }
                return <span className='text-sm text-brand-main-100'>-</span>
            }
        },
        {
            id: 'createdAt',
            header: 'Created',
            width: 160,
            minWidth: 140,
            render: (fn: Function) => (
                <span className='truncate text-sm text-brand-main-100'>{formatTimestamp(fn.createdAt)}</span>
            )
        },
        {
            id: 'enabled',
            header: 'Enabled',
            width: 80,
            minWidth: 80,
            render: (fn: Function) => (
                <Switch
                    checked={fn.enabled}
                    onCheckedChange={() => handleToggleEnabled(fn)}
                    disabled={updateFunctionMutation.isPending}
                />
            )
        },
        {
            id: 'actions',
            header: '',
            width: 80,
            minWidth: 80,
            maxWidth: 80,
            resizable: false,
            render: (fn: Function) => (
                <div className='flex items-center gap-2 justify-center'>
                    <button
                        type='button'
                        className='p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors'
                        onClick={() => onEdit?.(fn)}
                        title='Edit function'
                    >
                        <Pencil size={14} />
                    </button>
                    <button
                        type='button'
                        className='p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors'
                        onClick={() => {
                            setDeleteConfirmId(fn.id)
                            setDeleteConfirmName(fn.name)
                        }}
                        title='Delete function'
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            )
        }
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            <ResponsiveTable
                columns={columns}
                data={filteredFunctions}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={functions.length === 0 ? 'No functions found.' : 'No functions match your search.'}
            />

            {/* Delete Confirmation Dialog */}
            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className='w-[500px]'>
                    <DialogTitle>Delete Function</DialogTitle>
                    <DialogDescription className='text-brand-main-100'>
                        Are you sure you want to delete <strong className='text-brand-main-100'>{deleteConfirmName}</strong>? This action cannot be undone and any applications using this function will stop working.
                    </DialogDescription>
                    <div className='flex justify-end gap-3 mt-4'>
                        <Button
                            variant='outline'
                            onClick={() => setDeleteConfirmId(null)}
                            disabled={deleteFunctionMutation.isPending}
                        >
                            Cancel
                        </Button>
                        <Button
                            variant='destructive'
                            className='bg-destructive/60 text-brand-main-100 hover:bg-destructive/90'
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteFunctionMutation.isPending}
                        >
                            {deleteFunctionMutation.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    )
}
