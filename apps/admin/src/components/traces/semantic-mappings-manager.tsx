import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Shuffle, Trash2, Plus } from 'lucide-react'
import {
    listSemanticMappings,
    addSemanticMapping,
    deleteSemanticMapping,
} from '@/server/traces'
import type { SemanticMapping } from '@everstack/proto/everstack/traces/v1/traces_service_pb'

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

export const SEMANTIC_MAPPINGS_QUERY_KEY = ['trace-semantic-mappings'] as const

const DIALOG_CONTENT_CLASS = 'max-w-2xl gap-0 overflow-hidden p-0'
const DIALOG_HEADER_CLASS = 'border-b border-brand-main-600 px-5 py-4 pr-10 light:border-border'
const DIALOG_BODY_CLASS = 'flex max-h-[min(640px,calc(100vh-10rem))] flex-col overflow-hidden'
const LIST_CLASS = 'flex max-h-72 flex-col gap-3 overflow-auto px-5 py-4 scrollbar-macos'
const FORM_CLASS = 'border-t border-brand-main-600 bg-brand-main-950/45 px-5 py-4 light:border-border light:bg-black/[0.02]'
const FIELD_CLASS = 'space-y-1.5'
const INPUT_CLASS = 'h-9 w-full rounded border-brand-main-600 bg-brand-main-950/60 text-xs text-brand-main-50 placeholder:text-brand-main-50 light:bg-white light:text-black light:placeholder:text-black'
const SELECT_TRIGGER_CLASS = 'h-9 w-full rounded border-brand-main-600 bg-brand-main-950/60 text-xs text-zinc-200 light:bg-white light:text-black'
const SELECT_CONTENT_CLASS = 'bg-brand-main-900 border-brand-main-600 text-zinc-200 light:bg-popover light:text-popover-foreground'
const LABEL_CLASS = 'text-[11px] font-medium uppercase tracking-wide text-brand-main-50 light:text-black'
const HELP_CLASS = 'min-h-4 text-[11px] leading-snug text-brand-main-50 light:text-black'

const EMPTY_MAPPINGS: SemanticMapping[] = []

// Typed fields a tenant attribute can be aliased into. Keep in sync with the
// backend validFields set.
const FIELDS: { value: string; label: string }[] = [
    { value: 'model', label: 'Model' },
    { value: 'provider', label: 'Provider' },
    { value: 'session', label: 'Session' },
    { value: 'user', label: 'User' },
    { value: 'cost', label: 'Cost' },
    { value: 'input', label: 'Input' },
    { value: 'output', label: 'Output' },
    { value: 'input_tokens', label: 'Input tokens' },
    { value: 'output_tokens', label: 'Output tokens' },
    { value: 'total_tokens', label: 'Total tokens' },
]

const fieldLabel = (v: string) => FIELDS.find((f) => f.value === v)?.label ?? v

const ATTR_KEY_PATTERN = /^[a-zA-Z0-9_.:/-]{1,128}$/

// SemanticMappingsManager lets a tenant alias its own span-attribute names into
// our typed fields, so a non-standard SDK's attributes populate the built-in
// columns without us editing the hardcoded semconv lists.
export function SemanticMappingsManager() {
    const queryClient = useQueryClient()
    const [open, setOpen] = useState(false)
    const [field, setField] = useState('model')
    const [attrKey, setAttrKey] = useState('')

    const { data: mappings = EMPTY_MAPPINGS } = useQuery({
        queryKey: SEMANTIC_MAPPINGS_QUERY_KEY,
        queryFn: () => listSemanticMappings(),
        staleTime: 60_000,
    })

    const byField = useMemo(() => {
        const m = new Map<string, string[]>()
        for (const map of mappings) {
            const arr = m.get(map.field) ?? []
            arr.push(map.attrKey)
            m.set(map.field, arr)
        }
        return m
    }, [mappings])

    const invalidate = () =>
        queryClient.invalidateQueries({ queryKey: SEMANTIC_MAPPINGS_QUERY_KEY })

    const add = useMutation({
        mutationFn: () => addSemanticMapping(field, attrKey.trim()),
        onSuccess: () => {
            invalidate()
            setAttrKey('')
        },
        onError: (e: unknown) =>
            toast.error(e instanceof Error ? e.message : 'Failed to add mapping'),
    })

    const remove = useMutation({
        mutationFn: (m: { field: string; attrKey: string }) =>
            deleteSemanticMapping(m.field, m.attrKey),
        onSuccess: invalidate,
        onError: (e: unknown) =>
            toast.error(e instanceof Error ? e.message : 'Failed to remove mapping'),
    })

    const keyValid = ATTR_KEY_PATTERN.test(attrKey)
    const canAdd = keyValid && attrKey.trim() !== ''

    return (
        <>
            <Button
                variant="outline"
                size="sm"
                className="h-7 gap-1.5 text-xs"
                onClick={() => setOpen(true)}
            >
                <Shuffle className="size-3.5" />
                Mappings
                {mappings.length > 0 && <span className="text-brand-main-50 light:text-black">({mappings.length})</span>}
            </Button>

            <Dialog open={open} onOpenChange={setOpen}>
                <DialogContent className={DIALOG_CONTENT_CLASS}>
                    <div className={DIALOG_HEADER_CLASS}>
                        <DialogTitle>Attribute mappings</DialogTitle>
                        <DialogDescription className="mt-1 max-w-xl">
                            Map your own span-attribute names into built-in fields, so traces
                            from non-standard SDKs populate the standard columns.
                        </DialogDescription>
                    </div>

                    <div className={DIALOG_BODY_CLASS}>
                        <div className={LIST_CLASS}>
                            {mappings.length === 0 ? (
                                <div className="rounded border border-dashed border-brand-main-600 bg-brand-main-900/35 px-4 py-5 text-center text-xs text-brand-main-50 light:border-border light:bg-black/[0.02] light:text-black">
                                    No mappings yet. Add one below.
                                </div>
                            ) : (
                                FIELDS.filter((f) => byField.has(f.value)).map((f) => (
                                    <div key={f.value} className="space-y-1.5">
                                        <span className="text-[11px] font-medium uppercase tracking-wide text-brand-main-50 light:text-black">
                                            {f.label}
                                        </span>
                                        {(byField.get(f.value) ?? []).map((k) => (
                                            <div
                                                key={k}
                                                className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded border border-brand-main-600 bg-brand-main-800/60 px-3 py-2 light:border-border light:bg-black/[0.02]"
                                            >
                                                <span className="min-w-0 truncate text-xs text-brand-main-50 light:text-black">{k}</span>
                                                <Button
                                                    variant="ghost"
                                                    size="icon"
                                                    className="size-7 shrink-0"
                                                    disabled={remove.isPending}
                                                    onClick={() => remove.mutate({ field: f.value, attrKey: k })}
                                                >
                                                    <Trash2 className="size-3.5 text-brand-main-50 light:text-black" />
                                                </Button>
                                            </div>
                                        ))}
                                    </div>
                                ))
                            )}
                        </div>

                        <div className={FORM_CLASS}>
                            <div className="grid gap-3 sm:grid-cols-[180px_minmax(0,1fr)]">
                                <div className={FIELD_CLASS}>
                                    <Label className={LABEL_CLASS}>Field</Label>
                                    <Select value={field} onValueChange={setField}>
                                        <SelectTrigger className={SELECT_TRIGGER_CLASS}>
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className={SELECT_CONTENT_CLASS} align="start">
                                            {FIELDS.map((f) => (
                                                <SelectItem key={f.value} value={f.value} className="text-xs">
                                                    {f.label}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    <p className={HELP_CLASS}>Maps to {fieldLabel(field)}.</p>
                                </div>
                                <div className={FIELD_CLASS}>
                                    <Label className={LABEL_CLASS}>Attribute key</Label>
                                    <Input
                                        value={attrKey}
                                        onChange={(e) => setAttrKey(e.target.value)}
                                        placeholder="my_app.model_id"
                                        className={INPUT_CLASS}
                                    />
                                    <p className={attrKey !== '' && !keyValid ? 'min-h-4 text-[11px] leading-snug text-red-400' : HELP_CLASS}>
                                        {attrKey !== '' && !keyValid
                                            ? 'Letters, numbers, and . _ : / - only.'
                                            : 'Built-in keys keep priority; your key is used as a fallback.'}
                                    </p>
                                </div>
                            </div>
                            <div className="mt-4 flex justify-end">
                                <Button
                                    size="sm"
                                    className="h-8 gap-1.5 text-xs"
                                    disabled={!canAdd || add.isPending}
                                    onClick={() => canAdd && add.mutate()}
                                >
                                    <Plus className="size-3.5" />
                                    Add mapping
                                </Button>
                            </div>
                        </div>
                    </div>
                </DialogContent>
            </Dialog>
        </>
    )
}
