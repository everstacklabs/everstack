/**
 * Single source of truth for the trace-detail data-viz palette.
 *
 * The token/cost panels used to scatter bright one-off utilities
 * (sky-500 / emerald-500 / amber-500 / violet-500 / a 10-colour hex treemap).
 * This consolidates them into a small set of muted, brand-leaning tints so the
 * categories stay distinguishable without reading like a rainbow. Categories
 * carry meaning through their label first; colour is a secondary cue.
 *
 * Keep this list short. New series should reuse an existing tint before adding
 * one. Tints are tuned for the dark theme (low-opacity fills over a neutral
 * `vizTrack`) and carry `light:` overrides so the same maps read correctly on
 * the light theme.
 */

export type VizTint = {
    /** Proportional-bar fill. */
    bar: string
    /** Legend swatch. */
    dot: string
    /** Value text. */
    text: string
}

export const tokenTint = {
    /** Prompt / fresh input — cool, closest to the brand accent. */
    input: { bar: 'bg-indigo-400/50 light:bg-indigo-500/50', dot: 'bg-indigo-400/70 light:bg-indigo-500/70', text: 'text-indigo-300 light:text-indigo-700' },
    /** Completion / output. */
    output: { bar: 'bg-teal-400/50 light:bg-teal-500/50', dot: 'bg-teal-400/70 light:bg-teal-500/70', text: 'text-teal-300 light:text-teal-700' },
    /** Provider-cached prompt tokens (the saving). */
    cached: { bar: 'bg-amber-400/45 light:bg-amber-500/45', dot: 'bg-amber-300/70 light:bg-amber-500/70', text: 'text-amber-200/90 light:text-amber-700' },
    /** Reasoning / thinking tokens. */
    reasoning: { bar: 'bg-violet-400/50 light:bg-violet-500/50', dot: 'bg-violet-400/70 light:bg-violet-500/70', text: 'text-violet-300 light:text-violet-700' },
    /** Audio tokens (rare). */
    audio: { bar: 'bg-sky-400/45 light:bg-sky-500/45', dot: 'bg-sky-400/70 light:bg-sky-500/70', text: 'text-sky-300 light:text-sky-700' },
} as const satisfies Record<string, VizTint>

/** Neutral track behind every proportional bar. */
export const vizTrack = 'bg-brand-main-700/70'

/**
 * Cost is a single magnitude metric — length already encodes the value, so one
 * restrained accent is enough. The top row gets a slightly brighter fill.
 */
export const costBar = 'bg-indigo-400/45 light:bg-indigo-500/45'
export const costBarTop = 'bg-indigo-300/70 light:bg-indigo-500/70'

/** Muted indigo badge for per-span cost chips (cost is one accent, not a rainbow). */
export const costBadgeCls = 'text-indigo-300 light:text-indigo-700 bg-indigo-500/10 border-indigo-500/25'

/**
 * Muted status colours for the timeline waterfall bars. Raw hex because they are
 * applied as inline fills (not Tailwind classes); kept here so the trace
 * data-viz palette lives in one place rather than inline in the component.
 */
export const timelineStatusColors = {
    OK: { fill: '#3f9e7f', border: '#5cb89a' },
    ERROR: { fill: '#d4566a', border: '#e27d8d' },
    UNSET: { fill: '#647488', border: '#93a3b6' },
} as const

/**
 * Semantic status tints. Colour here means something (error / warning / pass)
 * and earns its place; plain metadata labels should use `neutral` so colour
 * stays a signal, not decoration. All muted for the dark theme.
 */
export type StatusTint = {
    text: string
    bg: string
    border: string
    /** Solid-ish fill for a status bar. */
    bar: string
    /** Legend / inline swatch. */
    dot: string
}

export const statusTint = {
    error: {
        text: 'text-rose-300 light:text-rose-700',
        bg: 'bg-rose-500/10',
        border: 'border-rose-500/25',
        bar: 'bg-rose-500/70',
        dot: 'bg-rose-400/70 light:bg-rose-500/70',
    },
    warning: {
        text: 'text-amber-300 light:text-amber-700',
        bg: 'bg-amber-500/10',
        border: 'border-amber-500/25',
        bar: 'bg-amber-500/70',
        dot: 'bg-amber-400/70 light:bg-amber-500/70',
    },
    success: {
        text: 'text-emerald-300 light:text-emerald-700',
        bg: 'bg-emerald-500/10',
        border: 'border-emerald-500/25',
        bar: 'bg-emerald-500/70',
        dot: 'bg-emerald-400/70 light:bg-emerald-500/70',
    },
    info: {
        text: 'text-indigo-300 light:text-indigo-700',
        bg: 'bg-indigo-500/10',
        border: 'border-indigo-500/25',
        bar: 'bg-indigo-500/70',
        dot: 'bg-indigo-400/70 light:bg-indigo-500/70',
    },
    neutral: {
        text: 'text-zinc-300 light:text-zinc-700',
        bg: 'bg-brand-main-600/30',
        border: 'border-brand-main-500',
        bar: 'bg-brand-main-400',
        dot: 'bg-zinc-400/60 light:bg-zinc-500/60',
    },
} as const satisfies Record<string, StatusTint>

export type StatusKey = keyof typeof statusTint

/** `text bg border` class string for a status badge. */
export function statusBadge(s: StatusKey): string {
    const t = statusTint[s]
    return `${t.text} ${t.bg} ${t.border}`
}

/**
 * Message-role tints — input-ish indigo for user, output-ish teal for
 * assistant, amber for system, neutral for tools. Colour-coding roles genuinely
 * helps reading a conversation, so it earns its place.
 */
export const roleTint = {
    system: { text: 'text-amber-200/90 light:text-amber-700', bg: 'bg-amber-400/5', border: 'border-amber-400/25' },
    user: { text: 'text-indigo-300 light:text-indigo-700', bg: 'bg-indigo-500/5', border: 'border-indigo-500/25' },
    assistant: { text: 'text-teal-300 light:text-teal-700', bg: 'bg-teal-500/5', border: 'border-teal-500/25' },
    tool: { text: 'text-zinc-300 light:text-zinc-700', bg: 'bg-brand-main-600/30', border: 'border-brand-main-500' },
} as const

/** `text bg border` class string for a message-role badge. */
export function roleBadge(role?: string): string {
    const key = (role ?? '').toLowerCase() as keyof typeof roleTint
    const t = roleTint[key] ?? roleTint.tool
    return `${t.text} ${t.bg} ${t.border}`
}

/** Quality colour for a 0–1 score bar: pass / borderline / fail. */
export function scoreBarClass(value01: number): string {
    if (value01 >= 0.7) return statusTint.success.bar
    if (value01 >= 0.4) return statusTint.warning.bar
    return statusTint.error.bar
}
