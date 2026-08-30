import { useMemo } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { Loader } from '@everstack/ui/components'
import { GitCompare } from 'lucide-react'
import { cn } from '@everstack/utils/functions/cn'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { usePrompts, usePromptVersions } from '@/hooks/evaluations/use-prompts'
import { versionConfig, type PromptVersion } from '@/server/prompts'
import { diffWords, type DiffOp } from '@/lib/diff'

const {
    Card,
    CardContent,
    CardHeader,
    CardTitle,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} = ui

const compareSearchSchema = z.object({
    lp: z.string().optional(), // left prompt id
    lv: z.coerce.number().optional(), // left version
    rp: z.string().optional(), // right prompt id
    rv: z.coerce.number().optional(), // right version
})

export const Route = createFileRoute('/evaluations/prompts-library_/compare')({
    component: PromptComparePage,
    validateSearch: compareSearchSchema,
})

const roleStyles: Record<string, string> = {
    system: 'text-amber-300/90 light:text-amber-700/90 border-amber-400/30 bg-amber-400/5',
    user: 'text-brand-secondary-300 border-brand-secondary-500/30 bg-brand-secondary-500/5',
    assistant: 'text-emerald-300/90 light:text-emerald-600/90 border-emerald-400/30 bg-emerald-400/5',
}

function PromptComparePage() {
    const gate = useFeatureGate(FeatureKey.PROMPT_MANAGEMENT)

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Prompt Management"
                description="Version and manage prompt templates for consistent evaluation workflows."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return <PromptCompareContent />
}

function PromptCompareContent() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    const { data: prompts } = usePrompts()

    const { data: leftVersions } = usePromptVersions(search.lp ?? '')
    const { data: rightVersions } = usePromptVersions(search.rp ?? '')

    const left = pickVersion(leftVersions, search.lv)
    const right = pickVersion(rightVersions, search.rv)

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            {/* Selectors */}
            <div className="shrink-0 px-4 py-2 border-b border-brand-main-700 grid grid-cols-2 gap-3">
                <SideSelector
                    label="A"
                    accent="text-brand-secondary-300"
                    prompts={prompts}
                    promptId={search.lp}
                    versions={leftVersions}
                    version={left?.version}
                    onPrompt={(lp) => navigate({ search: (p) => ({ ...p, lp, lv: undefined }), replace: true })}
                    onVersion={(lv) => navigate({ search: (p) => ({ ...p, lv }), replace: true })}
                />
                <SideSelector
                    label="B"
                    accent="text-amber-300 light:text-amber-700"
                    prompts={prompts}
                    promptId={search.rp}
                    versions={rightVersions}
                    version={right?.version}
                    onPrompt={(rp) => navigate({ search: (p) => ({ ...p, rp, rv: undefined }), replace: true })}
                    onVersion={(rv) => navigate({ search: (p) => ({ ...p, rv }), replace: true })}
                />
            </div>

            {!search.lp || !search.rp ? (
                <div className="flex-1 flex flex-col items-center justify-center text-white/40 light:text-black/40 gap-2">
                    <GitCompare className="size-8 opacity-30" />
                    <div className="text-sm">Pick a prompt version on each side to compare</div>
                    <div className="text-xs text-white/30 light:text-black/30">Message-by-message word diff · config diff</div>
                </div>
            ) : !leftVersions || !rightVersions ? (
                <div className="flex-1 flex items-center justify-center text-white/40 light:text-black/40 text-xs">
                    <Loader loaderText="Loading versions..." />
                </div>
            ) : !left || !right ? (
                <div className="flex-1 flex items-center justify-center text-white/40 light:text-black/40 text-xs">
                    One side has no versions yet.
                </div>
            ) : (
                <div className="flex-1 min-h-0 overflow-auto p-3 space-y-3">
                    <ConfigDiff left={left} right={right} />
                    <MessagesDiff left={left} right={right} />
                </div>
            )}
        </div>
    )
}

function pickVersion(
    versions: PromptVersion[] | undefined,
    version?: number,
): PromptVersion | null {
    if (!versions || versions.length === 0) return null
    if (version !== undefined) {
        return versions.find((v) => v.version === version) ?? versions[0]
    }
    return versions[0]
}

type SideSelectorProps = {
    label: string
    accent: string
    prompts: ReturnType<typeof usePrompts>['data']
    promptId?: string
    versions: PromptVersion[] | undefined
    version?: number
    onPrompt: (id: string) => void
    onVersion: (version: number) => void
}

function SideSelector({
    label,
    accent,
    prompts,
    promptId,
    versions,
    version,
    onPrompt,
    onVersion,
}: SideSelectorProps) {
    return (
        <div className="flex items-center gap-2">
            <span className={cn('text-xs font-mono shrink-0', accent)}>{label}</span>
            <Select value={promptId ?? ''} onValueChange={onPrompt}>
                <SelectTrigger className="h-7 flex-1 bg-brand-main-700/60 border-brand-main-500 text-xs">
                    <SelectValue placeholder="Select a prompt" />
                </SelectTrigger>
                <SelectContent className="bg-brand-main-900 border-brand-main-500">
                    {(prompts ?? []).map((p) => (
                        <SelectItem key={p.id} value={p.id} className="text-xs text-white/80 light:text-black/80">
                            {p.name}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
            <Select
                value={version !== undefined ? String(version) : ''}
                onValueChange={(v) => onVersion(Number(v))}
                disabled={!versions || versions.length === 0}
            >
                <SelectTrigger className="h-7 w-24 bg-brand-main-700/60 border-brand-main-500 text-xs">
                    <SelectValue placeholder="version" />
                </SelectTrigger>
                <SelectContent className="bg-brand-main-900 border-brand-main-500">
                    {(versions ?? []).map((v) => (
                        <SelectItem key={v.id} value={String(v.version)} className="text-xs text-white/80 light:text-black/80">
                            v{v.version}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        </div>
    )
}

function ConfigDiff({ left, right }: { left: PromptVersion; right: PromptVersion }) {
    const a = versionConfig(left)
    const b = versionConfig(right)
    const rows: Array<{ k: string; av: string; bv: string }> = [
        { k: 'Model', av: a.model ?? '—', bv: b.model ?? '—' },
        { k: 'Temperature', av: fmt(a.temperature), bv: fmt(b.temperature) },
        { k: 'Top P', av: fmt(a.topP), bv: fmt(b.topP) },
        { k: 'Max tokens', av: fmt(a.maxTokens), bv: fmt(b.maxTokens) },
    ]
    return (
        <Card className="border-brand-main-500 bg-brand-main-900/40">
            <CardHeader className="!pb-1.5">
                <CardTitle className="text-white light:text-brand-main-50 text-sm">Config</CardTitle>
            </CardHeader>
            <CardContent className="!pt-0">
                <table className="w-full text-xs">
                    <tbody>
                        {rows.map((r) => {
                            const differs = r.av !== r.bv
                            return (
                                <tr key={r.k} className="border-b border-brand-main-700/30 last:border-0">
                                    <td className="py-1.5 pr-3 text-white/40 light:text-black/40 w-32">{r.k}</td>
                                    <td className={cn('py-1.5 px-3 font-mono', differs ? 'text-rose-300 light:text-rose-600' : 'text-white/80 light:text-black/80')}>
                                        {r.av}
                                    </td>
                                    <td className={cn('py-1.5 px-3 font-mono', differs ? 'text-emerald-300 light:text-emerald-600' : 'text-white/80 light:text-black/80')}>
                                        {r.bv}
                                    </td>
                                </tr>
                            )
                        })}
                    </tbody>
                </table>
            </CardContent>
        </Card>
    )
}

function fmt(v?: number): string {
    return v === undefined ? '—' : String(v)
}

function MessagesDiff({ left, right }: { left: PromptVersion; right: PromptVersion }) {
    const count = Math.max(left.messages.length, right.messages.length)
    const rows = Array.from({ length: count }, (_, i) => i)
    return (
        <Card className="border-brand-main-500 bg-brand-main-900/40">
            <CardHeader className="!pb-1.5 flex flex-row items-center justify-between">
                <CardTitle className="text-white light:text-brand-main-50 text-sm">Messages</CardTitle>
                <span className="text-[10px] text-white/30 light:text-black/30">
                    <span className="text-rose-300 light:text-rose-600">removed</span> ·{' '}
                    <span className="text-emerald-300 light:text-emerald-600">added</span>
                </span>
            </CardHeader>
            <CardContent className="!pt-0 space-y-2">
                {rows.map((i) => (
                    <MessageRow key={i} left={left.messages[i]} right={right.messages[i]} />
                ))}
            </CardContent>
        </Card>
    )
}

function MessageRow({
    left,
    right,
}: {
    left?: { role: string; content: string }
    right?: { role: string; content: string }
}) {
    const ops = useMemo(
        () => diffWords(left?.content ?? '', right?.content ?? ''),
        [left?.content, right?.content],
    )
    return (
        <div className="grid grid-cols-2 gap-2">
            <DiffPane role={left?.role} ops={ops} side="left" missing={!left} />
            <DiffPane role={right?.role} ops={ops} side="right" missing={!right} />
        </div>
    )
}

function DiffPane({
    role,
    ops,
    side,
    missing,
}: {
    role?: string
    ops: DiffOp[]
    side: 'left' | 'right'
    missing: boolean
}) {
    return (
        <div className="rounded border border-brand-main-600 bg-brand-main-800/40">
            <div className="px-2 pt-1.5">
                <span
                    className={cn(
                        'rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide',
                        roleStyles[role ?? ''] ?? 'text-white/40 light:text-black/40 border-brand-main-500',
                    )}
                >
                    {role ?? '(none)'}
                </span>
            </div>
            <div className="px-2 py-1.5 text-xs font-mono whitespace-pre-wrap leading-relaxed">
                {missing ? (
                    <span className="text-white/25 light:text-black/25">(no message)</span>
                ) : (
                    ops.map((op, i) => {
                        if (side === 'left' && op.type === 'insert') return null
                        if (side === 'right' && op.type === 'delete') return null
                        return (
                            <span
                                key={i}
                                className={cn(
                                    op.type === 'delete' && 'bg-rose-500/20 text-rose-300 light:text-rose-600 line-through',
                                    op.type === 'insert' && 'bg-emerald-500/20 text-emerald-200 light:text-emerald-600',
                                    op.type === 'equal' && 'text-zinc-200 light:text-zinc-800',
                                )}
                            >
                                {op.value}
                            </span>
                        )
                    })
                )}
            </div>
        </div>
    )
}
