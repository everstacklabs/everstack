import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Settings2, Trash2, Plus } from 'lucide-react'
import {
    listLogColumns,
    upsertLogColumn,
    deleteLogColumn,
    type LogColumnInput,
} from '@/server/logs'
import type { LogCustomColumnDef } from '@everstack/proto/everstack/logs/v1/logs_service_pb'

const {
    Dialog,
    DialogContent,
    DialogTitle,
    DialogDescription,
    Button,
    Input,
    Label,
} = ui

export const LOG_COLUMNS_QUERY_KEY = ['log-custom-columns'] as const

const EMPTY_COLUMNS: LogCustomColumnDef[] = []

const KEY_PATTERN = /^[a-zA-Z0-9_]{1,64}$/

// LogColumnManager lets a tenant define which LogAttributes fields surface as
// columns on the logs view, so the log structure is the user's, not only ours.
// Logs carry a flat string attribute bag, so a column is just a label over an
// attribute key (no value type / alternate source like traces).
export function LogColumnManager() {
    const queryClient = useQueryClient()
    const { data: columns = EMPTY_COLUMNS } = useQuery({
        queryKey: LOG_COLUMNS_QUERY_KEY,
        queryFn: () => listLogColumns(),
        staleTime: 60_000,
    })
    const [open, setOpen] = useState(false)
    const [key, setKey] = useState('')
    const [label, setLabel] = useState('')
    const [attrKey, setAttrKey] = useState('')

    const invalidate = () =>
        queryClient.invalidateQueries({ queryKey: LOG_COLUMNS_QUERY_KEY })

    const upsert = useMutation({
        mutationFn: (input: LogColumnInput) => upsertLogColumn(input),
        onSuccess: () => {
            invalidate()
            setKey('')
            setLabel('')
            setAttrKey('')
        },
        onError: (e: unknown) =>
            toast.error(e instanceof Error ? e.message : 'Failed to add column'),
    })

    const remove = useMutation({
        mutationFn: (k: string) => deleteLogColumn(k),
        onSuccess: invalidate,
        onError: (e: unknown) =>
            toast.error(e instanceof Error ? e.message : 'Failed to remove column'),
    })

    const keyValid = KEY_PATTERN.test(key)
    const canAdd = keyValid && label.trim() !== '' && attrKey.trim() !== ''

    const onAdd = () => {
        if (!canAdd) return
        upsert.mutate({
            key,
            label: label.trim(),
            attrKey: attrKey.trim(),
            position: columns.length,
        })
    }

    return (
        <>
            <Button
                variant='outline'
                className='h-8 gap-1.5 text-xs'
                onClick={() => setOpen(true)}
            >
                <Settings2 className='size-3.5' />
                Columns
                {columns.length > 0 && (
                    <span className='text-white/50 light:text-black/50'>({columns.length})</span>
                )}
            </Button>

            <Dialog open={open} onOpenChange={setOpen}>
                <DialogContent className='max-w-lg'>
                    <DialogTitle>Custom log columns</DialogTitle>
                    <DialogDescription>
                        Show the log fields you care about. Each column reads a value
                        from a log's attributes.
                    </DialogDescription>

                    <div className='flex flex-col gap-2 mt-2'>
                        {columns.length === 0 ? (
                            <p className='text-xs text-white/40 light:text-black/40 py-2'>
                                No custom columns yet. Add one below.
                            </p>
                        ) : (
                            columns.map((c) => (
                                <div
                                    key={c.key}
                                    className='flex items-center justify-between gap-2 rounded-md border border-brand-main-600 bg-brand-main-800/60 px-3 py-2'
                                >
                                    <div className='flex flex-col min-w-0'>
                                        <span className='text-xs text-white/90 light:text-black/90 truncate'>
                                            {c.label}
                                        </span>
                                        <span className='text-[11px] text-white/40 light:text-black/40 font-mono truncate'>
                                            {c.attrKey}
                                        </span>
                                    </div>
                                    <Button
                                        variant='ghost'
                                        size='icon'
                                        className='size-7 shrink-0'
                                        disabled={remove.isPending}
                                        onClick={() => remove.mutate(c.key)}
                                    >
                                        <Trash2 className='size-3.5 text-white/50 light:text-black/50' />
                                    </Button>
                                </div>
                            ))
                        )}
                    </div>

                    <div className='mt-4 flex flex-col gap-3 border-t border-brand-main-600 pt-4'>
                        <div className='grid grid-cols-2 gap-3'>
                            <div className='flex flex-col gap-1'>
                                <Label className='text-xs'>Key</Label>
                                <Input
                                    value={key}
                                    onChange={(e) => setKey(e.target.value)}
                                    placeholder='environment'
                                    className='h-8 text-xs'
                                />
                                {key !== '' && !keyValid && (
                                    <span className='text-[11px] text-red-400 light:text-red-600'>
                                        Letters, numbers, underscore only.
                                    </span>
                                )}
                            </div>
                            <div className='flex flex-col gap-1'>
                                <Label className='text-xs'>Label</Label>
                                <Input
                                    value={label}
                                    onChange={(e) => setLabel(e.target.value)}
                                    placeholder='Environment'
                                    className='h-8 text-xs'
                                />
                            </div>
                        </div>
                        <div className='flex flex-col gap-1'>
                            <Label className='text-xs'>Attribute key</Label>
                            <Input
                                value={attrKey}
                                onChange={(e) => setAttrKey(e.target.value)}
                                placeholder='deployment.environment'
                                className='h-8 text-xs'
                            />
                        </div>
                        <Button
                            size='sm'
                            className='h-8 gap-1.5 self-start text-xs'
                            disabled={!canAdd || upsert.isPending}
                            onClick={onAdd}
                        >
                            <Plus className='size-3.5' />
                            Add column
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>
        </>
    )
}
