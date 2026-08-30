import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Settings2, Trash2, Plus } from 'lucide-react'
import {
    listCustomColumns,
    upsertCustomColumn,
    deleteCustomColumn,
    type CustomColumnInput,
} from '@/server/traces'
import type { CustomColumnDef } from '@everstack/proto/everstack/traces/v1/traces_service_pb'

const {
    Dialog,
    DialogContent,
    DialogTitle,
    DialogDescription,
    Button,
    Input,
    Label,
    Select,
    SelectTrigger,
    SelectValue,
    SelectContent,
    SelectItem,
} = ui

const DIALOG_CONTENT_CLASS = 'max-w-2xl gap-0 overflow-hidden p-0'
const DIALOG_HEADER_CLASS = 'border-b border-brand-main-600 px-5 py-4 pr-10 light:border-border'
const DIALOG_BODY_CLASS = 'flex max-h-[min(680px,calc(100vh-10rem))] flex-col overflow-hidden'
const LIST_CLASS = 'flex max-h-72 flex-col gap-2 overflow-auto px-5 py-4 scrollbar-macos'
const FORM_CLASS = 'border-t border-brand-main-600 bg-brand-main-950/45 px-5 py-4 light:border-border light:bg-black/[0.02]'
const FIELD_CLASS = 'space-y-1.5'
const INPUT_CLASS = 'h-9 w-full rounded border-brand-main-600 bg-brand-main-950/60 text-xs text-brand-main-50 placeholder:text-brand-main-50 light:bg-white light:text-black light:placeholder:text-black'
const SELECT_TRIGGER_CLASS = 'h-9 w-full rounded border-brand-main-600 bg-brand-main-950/60 text-xs text-zinc-200 light:bg-white light:text-black'
const SELECT_CONTENT_CLASS = 'bg-brand-main-900 border-brand-main-600 text-zinc-200 light:bg-popover light:text-popover-foreground'
const LABEL_CLASS = 'text-[11px] font-medium uppercase tracking-wide text-brand-main-50 light:text-black'
const HELP_CLASS = 'min-h-4 text-[11px] leading-snug text-brand-main-50 light:text-black'

export const CUSTOM_COLUMNS_QUERY_KEY = ['trace-custom-columns'] as const

const EMPTY_COLUMNS: CustomColumnDef[] = []

const VALUE_TYPES = ['string', 'number', 'bool', 'date'] as const

const SOURCES = [
    { value: 'metadata_path', label: 'Metadata field', refLabel: 'Metadata field', refHint: 'customer_tier' },
    { value: 'attribute_path', label: 'Span attribute', refLabel: 'Attribute key', refHint: 'gen_ai.request.model' },
    { value: 'score_name', label: 'Score', refLabel: 'Score name', refHint: 'helpfulness' },
] as const

const KEY_PATTERN = /^[a-zA-Z0-9_]{1,64}$/

// CustomColumnManager lets a tenant define which trace dimensions show as
// columns. v1 sources values from a per-call metadata field, so the trace
// structure is the user's, not only ours.
export function CustomColumnManager() {
    const queryClient = useQueryClient()
    const { data: columns = EMPTY_COLUMNS } = useQuery({
        queryKey: CUSTOM_COLUMNS_QUERY_KEY,
        queryFn: () => listCustomColumns(),
        staleTime: 60_000,
    })
    const [open, setOpen] = useState(false)
    const [key, setKey] = useState('')
    const [label, setLabel] = useState('')
    const [valueType, setValueType] = useState<string>('string')
    const [source, setSource] = useState<string>('metadata_path')
    const [sourceRef, setSourceRef] = useState('')

    const sourceMeta = SOURCES.find((s) => s.value === source) ?? SOURCES[0]

    const invalidate = () =>
        queryClient.invalidateQueries({ queryKey: CUSTOM_COLUMNS_QUERY_KEY })

    const upsert = useMutation({
        mutationFn: (input: CustomColumnInput) => upsertCustomColumn(input),
        onSuccess: () => {
            invalidate()
            setKey('')
            setLabel('')
            setSourceRef('')
            setValueType('string')
            setSource('metadata_path')
        },
        onError: (e: unknown) =>
            toast.error(e instanceof Error ? e.message : 'Failed to add column'),
    })

    const remove = useMutation({
        mutationFn: (k: string) => deleteCustomColumn(k),
        onSuccess: invalidate,
        onError: (e: unknown) =>
            toast.error(e instanceof Error ? e.message : 'Failed to remove column'),
    })

    const keyValid = KEY_PATTERN.test(key)
    const canAdd = keyValid && label.trim() !== '' && sourceRef.trim() !== ''

    const onAdd = () => {
        if (!canAdd) return
        upsert.mutate({
            key,
            label: label.trim(),
            valueType,
            source,
            sourceRef: sourceRef.trim(),
            position: columns.length,
        })
    }

    return (
        <>
            <Button
                variant='outline'
                size='sm'
                className='h-7 gap-1.5 text-xs'
                onClick={() => setOpen(true)}
            >
                <Settings2 className='size-3.5' />
                Columns
                {columns.length > 0 && (
                    <span className='text-brand-main-50 light:text-black'>({columns.length})</span>
                )}
            </Button>

            <Dialog open={open} onOpenChange={setOpen}>
                <DialogContent className={DIALOG_CONTENT_CLASS}>
                    <div className={DIALOG_HEADER_CLASS}>
                        <DialogTitle>Custom columns</DialogTitle>
                        <DialogDescription className='mt-1 max-w-xl'>
                            Show the dimensions you care about. Each column reads from trace
                            metadata, span attributes, or a score.
                        </DialogDescription>
                    </div>

                    <div className={DIALOG_BODY_CLASS}>
                        <div className={LIST_CLASS}>
                            {columns.length === 0 ? (
                                <div className='rounded border border-dashed border-brand-main-600 bg-brand-main-900/35 px-4 py-5 text-center text-xs text-brand-main-50 light:border-border light:bg-black/[0.02] light:text-black'>
                                    No custom columns yet. Add one below.
                                </div>
                            ) : (
                                columns.map((c) => (
                                    <div
                                        key={c.key}
                                        className='grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded border border-brand-main-600 bg-brand-main-800/60 px-3 py-2 light:border-border light:bg-black/[0.02]'
                                    >
                                        <div className='grid min-w-0 gap-0.5'>
                                            <span className='truncate text-xs text-brand-main-50 light:text-black'>
                                                {c.label}
                                            </span>
                                            <span className='truncate text-[11px] text-brand-main-50 light:text-black'>
                                                {c.sourceRef} · {c.valueType}
                                            </span>
                                        </div>
                                        <Button
                                            variant='ghost'
                                            size='icon'
                                            className='size-7 shrink-0'
                                            disabled={remove.isPending}
                                            onClick={() => remove.mutate(c.key)}
                                        >
                                            <Trash2 className='size-3.5 text-brand-main-50 light:text-black' />
                                        </Button>
                                    </div>
                                ))
                            )}
                        </div>

                        <div className={FORM_CLASS}>
                            <div className='grid gap-3 sm:grid-cols-2'>
                                <div className={FIELD_CLASS}>
                                    <Label className={LABEL_CLASS}>Key</Label>
                                    <Input
                                        value={key}
                                        onChange={(e) => setKey(e.target.value)}
                                        placeholder='customer_tier'
                                        className={INPUT_CLASS}
                                    />
                                    <p className={key !== '' && !keyValid ? 'min-h-4 text-[11px] leading-snug text-red-400' : HELP_CLASS}>
                                        {key !== '' && !keyValid
                                            ? 'Letters, numbers, underscore only.'
                                            : 'Stable id used for the table column.'}
                                    </p>
                                </div>
                                <div className={FIELD_CLASS}>
                                    <Label className={LABEL_CLASS}>Label</Label>
                                    <Input
                                        value={label}
                                        onChange={(e) => setLabel(e.target.value)}
                                        placeholder='Customer tier'
                                        className={INPUT_CLASS}
                                    />
                                    <p className={HELP_CLASS}>Visible column header.</p>
                                </div>
                            </div>

                            <div className='mt-3 grid gap-3 sm:grid-cols-2'>
                                <div className={FIELD_CLASS}>
                                    <Label className={LABEL_CLASS}>Source</Label>
                                    <Select value={source} onValueChange={setSource}>
                                        <SelectTrigger className={SELECT_TRIGGER_CLASS}>
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className={SELECT_CONTENT_CLASS} align='start'>
                                            {SOURCES.map((s) => (
                                                <SelectItem key={s.value} value={s.value} className='text-xs'>
                                                    {s.label}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    <p className={HELP_CLASS}>{sourceMeta.refLabel} determines the value.</p>
                                </div>
                                <div className={FIELD_CLASS}>
                                    <Label className={LABEL_CLASS}>Type</Label>
                                    <Select value={valueType} onValueChange={setValueType}>
                                        <SelectTrigger className={SELECT_TRIGGER_CLASS}>
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className={SELECT_CONTENT_CLASS} align='start'>
                                            {VALUE_TYPES.map((t) => (
                                                <SelectItem key={t} value={t} className='text-xs capitalize'>
                                                    {t}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    <p className={HELP_CLASS}>Controls sorting and display formatting.</p>
                                </div>
                            </div>

                            <div className={`${FIELD_CLASS} mt-3`}>
                                <Label className={LABEL_CLASS}>{sourceMeta.refLabel}</Label>
                                <Input
                                    value={sourceRef}
                                    onChange={(e) => setSourceRef(e.target.value)}
                                    placeholder={sourceMeta.refHint}
                                    className={INPUT_CLASS}
                                />
                                <p className={HELP_CLASS}>Reference the exact metadata path, span attribute, or score name.</p>
                            </div>

                            <div className='mt-4 flex justify-end'>
                                <Button
                                    size='sm'
                                    className='h-8 gap-1.5 text-xs'
                                    disabled={!canAdd || upsert.isPending}
                                    onClick={onAdd}
                                >
                                    <Plus className='size-3.5' />
                                    Add column
                                </Button>
                            </div>
                        </div>
                    </div>
                </DialogContent>
            </Dialog>
        </>
    )
}
