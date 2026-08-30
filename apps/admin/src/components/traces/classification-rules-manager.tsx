import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Tags, Trash2, Plus } from 'lucide-react'
import {
    listClassificationRules,
    addClassificationRule,
    deleteClassificationRule,
} from '@/server/traces'
import type { ClassificationRule } from '@everstack/proto/everstack/traces/v1/traces_service_pb'

const { Dialog, DialogContent, DialogTitle, DialogDescription, Button, Input, Label, Badge } = ui

export const CLASSIFICATION_RULES_QUERY_KEY = ['trace-classification-rules'] as const

const DIALOG_CONTENT_CLASS = 'max-w-2xl gap-0 overflow-hidden p-0'
const DIALOG_HEADER_CLASS = 'border-b border-brand-main-600 px-5 py-4 pr-10 light:border-border'
const DIALOG_BODY_CLASS = 'flex max-h-[min(640px,calc(100vh-10rem))] flex-col overflow-hidden'
const LIST_CLASS = 'flex max-h-72 flex-col gap-2 overflow-auto px-5 py-4 scrollbar-macos'
const FORM_CLASS = 'border-t border-brand-main-600 bg-brand-main-950/45 px-5 py-4 light:border-border light:bg-black/[0.02]'
const FIELD_CLASS = 'space-y-1.5'
const INPUT_CLASS = 'h-9 w-full rounded border-brand-main-600 bg-brand-main-950/60 text-xs text-brand-main-50 placeholder:text-brand-main-50 light:bg-white light:text-black light:placeholder:text-black'
const LABEL_CLASS = 'text-[11px] font-medium uppercase tracking-wide text-brand-main-50 light:text-black'
const HELP_CLASS = 'min-h-4 text-[11px] leading-snug text-brand-main-50 light:text-black'

const EMPTY_RULES: ClassificationRule[] = []

const PATTERN_PATTERN = /^[a-zA-Z0-9_.:/%-]{1,128}$/
const KIND_PATTERN = /^[a-zA-Z0-9_ -]{1,40}$/

// ClassificationRulesManager lets a tenant extend the built-in trace-kind
// classifier with their own SpanName patterns, e.g. "retriever.% -> retrieval",
// so the Type column shows kinds that matter to them.
export function ClassificationRulesManager() {
    const queryClient = useQueryClient()
    const [open, setOpen] = useState(false)
    const [pattern, setPattern] = useState('')
    const [kind, setKind] = useState('')

    const { data: rules = EMPTY_RULES } = useQuery({
        queryKey: CLASSIFICATION_RULES_QUERY_KEY,
        queryFn: () => listClassificationRules(),
        staleTime: 60_000,
    })

    const invalidate = () =>
        queryClient.invalidateQueries({ queryKey: CLASSIFICATION_RULES_QUERY_KEY })

    const add = useMutation({
        mutationFn: () => addClassificationRule(pattern.trim(), kind.trim()),
        onSuccess: () => {
            invalidate()
            setPattern('')
            setKind('')
        },
        onError: (e: unknown) =>
            toast.error(e instanceof Error ? e.message : 'Failed to add rule'),
    })

    const remove = useMutation({
        mutationFn: (r: { pattern: string; kind: string }) =>
            deleteClassificationRule(r.pattern, r.kind),
        onSuccess: invalidate,
        onError: (e: unknown) =>
            toast.error(e instanceof Error ? e.message : 'Failed to remove rule'),
    })

    const patternValid = PATTERN_PATTERN.test(pattern)
    const kindValid = KIND_PATTERN.test(kind)
    const canAdd = patternValid && kindValid

    return (
        <>
            <Button
                variant="outline"
                size="sm"
                className="h-7 gap-1.5 text-xs"
                onClick={() => setOpen(true)}
            >
                <Tags className="size-3.5" />
                Rules
                {rules.length > 0 && <span className="text-brand-main-50 light:text-black">({rules.length})</span>}
            </Button>

            <Dialog open={open} onOpenChange={setOpen}>
                <DialogContent className={DIALOG_CONTENT_CLASS}>
                    <div className={DIALOG_HEADER_CLASS}>
                        <DialogTitle>Classification rules</DialogTitle>
                        <DialogDescription className="mt-1 max-w-xl">
                            Tag traces by span name. Matching spans add a kind to the Type column
                            alongside the built-in agent, workflow, and sandbox kinds.
                        </DialogDescription>
                    </div>

                    <div className={DIALOG_BODY_CLASS}>
                        <div className={LIST_CLASS}>
                            {rules.length === 0 ? (
                                <div className="rounded border border-dashed border-brand-main-600 bg-brand-main-900/35 px-4 py-5 text-center text-xs text-brand-main-50 light:border-border light:bg-black/[0.02] light:text-black">
                                    No rules yet. Add one below.
                                </div>
                            ) : (
                                rules.map((r) => (
                                    <div
                                        key={`${r.pattern}|${r.kind}`}
                                        className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-3 rounded border border-brand-main-600 bg-brand-main-800/60 px-3 py-2 light:border-border light:bg-black/[0.02]"
                                    >
                                        <span className="min-w-0 truncate text-xs text-brand-main-50 light:text-black">{r.pattern}</span>
                                        <Badge
                                            variant="outline"
                                            className="min-w-0 max-w-36 truncate border-brand-main-500 bg-brand-main-700/50 px-2 py-0.5 text-xs capitalize text-brand-main-50 light:text-black"
                                        >
                                            {r.kind}
                                        </Badge>
                                        <Button
                                            variant="ghost"
                                            size="icon"
                                            className="size-7 shrink-0"
                                            disabled={remove.isPending}
                                            onClick={() => remove.mutate({ pattern: r.pattern, kind: r.kind })}
                                        >
                                            <Trash2 className="size-3.5 text-brand-main-50 light:text-black" />
                                        </Button>
                                    </div>
                                ))
                            )}
                        </div>

                        <div className={FORM_CLASS}>
                            <div className="grid gap-3 sm:grid-cols-[minmax(0,1.25fr)_minmax(160px,0.75fr)]">
                                <div className={FIELD_CLASS}>
                                    <Label className={LABEL_CLASS}>Span name pattern</Label>
                                    <Input
                                        value={pattern}
                                        onChange={(e) => setPattern(e.target.value)}
                                        placeholder="retriever.%"
                                        className={INPUT_CLASS}
                                    />
                                    <p className={pattern !== '' && !patternValid ? 'min-h-4 text-[11px] leading-snug text-red-400' : HELP_CLASS}>
                                        {pattern !== '' && !patternValid
                                            ? 'Use % as the wildcard; letters, numbers, . _ : / - only.'
                                            : 'Use % for wildcard matches.'}
                                    </p>
                                </div>
                                <div className={FIELD_CLASS}>
                                    <Label className={LABEL_CLASS}>Kind</Label>
                                    <Input
                                        value={kind}
                                        onChange={(e) => setKind(e.target.value)}
                                        placeholder="retrieval"
                                        className={INPUT_CLASS}
                                    />
                                    <p className={kind !== '' && !kindValid ? 'min-h-4 text-[11px] leading-snug text-red-400' : HELP_CLASS}>
                                        {kind !== '' && !kindValid
                                            ? 'Letters, numbers, space, _ and - only.'
                                            : 'Shown as a Type badge.'}
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
                                    Add rule
                                </Button>
                            </div>
                        </div>
                    </div>
                </DialogContent>
            </Dialog>
        </>
    )
}
