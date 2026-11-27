import { ui } from '@everstack/ui'
import type { RemoteAgent } from '@/hooks/gateway/use-interop'
import { BACKEND_SURFACES, SURFACE_INFO, SURFACES, offeredSurfaces, kindEntry } from './bot-catalog'
import { inputClass, mutedClass, selectContentClass, selectTriggerClass } from './bot-setup'

const { Checkbox, Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } = ui

const NONE = '__none__'

/** Checkboxes for the surfaces a kind can use. A surface already on the bot
 *  stays listed so it can be turned off, as long as the gateway allows it. */
export function SurfaceChecklist({
    kind,
    value,
    onChange,
    idPrefix,
}: {
    kind: string
    value: string[]
    onChange: (value: string[]) => void
    idPrefix: string
}) {
    const entry = kindEntry(kind)
    const backend = entry ? BACKEND_SURFACES[entry.id] : []
    const offered = SURFACES.filter((s) => offeredSurfaces(kind).includes(s) || (value.includes(s) && backend.includes(s)))
    const toggle = (surface: string, on: boolean) =>
        onChange(SURFACES.filter((s) => (s === surface ? on : value.includes(s))))
    return (
        <fieldset className="min-w-0 space-y-2">
            <legend className="mb-2 text-sm font-medium">What it may use</legend>
            <div className="divide-y divide-brand-main-600/40 rounded border border-brand-main-600/40">
                {offered.map((surface) => {
                    const id = `${idPrefix}-surface-${surface}`
                    return (
                        <div key={surface} className="flex items-start gap-2.5 px-3 py-2.5">
                            <Checkbox
                                id={id}
                                className="mt-0.5"
                                checked={value.includes(surface)}
                                onCheckedChange={(checked) => toggle(surface, checked === true)}
                            />
                            <Label htmlFor={id} className="flex flex-1 cursor-pointer flex-col items-start gap-0.5 font-normal">
                                <span className="text-sm">{SURFACE_INFO[surface].label}</span>
                                <span className="text-[11px] leading-4 text-brand-main-300">
                                    {SURFACE_INFO[surface].description}
                                </span>
                            </Label>
                        </div>
                    )
                })}
            </div>
            {entry?.hosted && (
                <p className={`text-[11px] ${mutedClass}`}>
                    {entry.name} is hosted by {entry.hosted}, so it can only use tools from this gateway.
                </p>
            )}
            {value.length === 0 && <p className="text-[11px] text-warning">Choose at least one.</p>}
        </fieldset>
    )
}

/** Links a saved A2A remote agent: the address where Everstack agents can
 *  call this bot. */
export function RemoteSelect({
    remotes,
    value,
    onChange,
}: {
    remotes: RemoteAgent[]
    value: string
    onChange: (value: string) => void
}) {
    return (
        <div className="space-y-2">
            <Label htmlFor="bot-remote">Remote agent (optional)</Label>
            <Select
                value={value || NONE}
                onValueChange={(next) => {
                    // Ignore the hidden native select's '' (see external-runs).
                    if (next === NONE) onChange('')
                    else if (remotes.some((r) => r.name === next)) onChange(next)
                }}
            >
                <SelectTrigger id="bot-remote" className={selectTriggerClass}>
                    <SelectValue />
                </SelectTrigger>
                <SelectContent className={selectContentClass}>
                    <SelectItem value={NONE}>None</SelectItem>
                    {remotes.map((r) => (
                        <SelectItem key={r.id} value={r.name}>
                            {r.name}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
            <p className="text-xs text-brand-main-200">
                The saved A2A connection where Everstack agents can call this bot.
            </p>
        </div>
    )
}

export const RATE_LIMIT_MAX = 100000

/** Reads the rate limit field: empty means no bot-specific limit (null);
 *  anything else must be a whole number from 1 to 100,000. */
export function parseRateLimitInput(text: string): { ok: true; value: number | null } | { ok: false } {
    const trimmed = text.trim()
    if (trimmed === '') return { ok: true, value: null }
    if (!/^\d+$/.test(trimmed)) return { ok: false }
    const value = Number(trimmed)
    if (value < 1 || value > RATE_LIMIT_MAX) return { ok: false }
    return { ok: true, value }
}

/** Optional per-bot rate limit, in requests per minute. */
export function RateLimitField({
    value,
    onChange,
    idPrefix,
}: {
    value: string
    onChange: (value: string) => void
    idPrefix: string
}) {
    const id = `${idPrefix}-rate-limit`
    const invalid = !parseRateLimitInput(value).ok
    return (
        <div className="space-y-2">
            <Label htmlFor={id}>Rate limit (optional)</Label>
            <div className="flex items-center gap-2">
                <Input
                    id={id}
                    inputMode="numeric"
                    autoComplete="off"
                    value={value}
                    onChange={(event) => onChange(event.target.value)}
                    placeholder="No limit"
                    aria-invalid={invalid}
                    aria-describedby={`${id}-hint`}
                    className={`w-32 ${inputClass}`}
                />
                <span className="text-xs text-brand-main-300">requests per minute</span>
            </div>
            <p id={`${id}-hint`} className={`text-[11px] leading-4 ${invalid ? 'text-danger' : mutedClass}`}>
                {invalid
                    ? 'Use a whole number from 1 to 100,000, or leave it empty.'
                    : 'Counts every request the bot key makes. Leave empty for no bot-specific limit.'}
            </p>
        </div>
    )
}
