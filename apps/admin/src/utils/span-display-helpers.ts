/**
 * Category-based styling helpers for trace visualization
 * Provides consistent icons and colors for each span category
 */

import { categoryFromSpanName, type SpanCategory } from './traces-common'

/**
 * Iconify icon name for each span category. We use Phosphor's **duotone** weight
 * (`ph:*-duotone`) for one consistent, premium look: each glyph is a clean,
 * widely-loved icon with a subtle two-tone fill that reads richer than a flat
 * outline, and because both tones derive from `currentColor` it still tints with
 * the per-category colour. The glyph is picked to be on-the-nose for each kind of
 * span so the icon itself says what the span did. Render with
 * `<Iconify.Icon icon={categoryIcons[c]} className="size-... <colour>" />`.
 */
export const categoryIcons: Record<SpanCategory, string> = {
    // Gateway-centric (legacy) categories
    gateway: 'ph:sign-in-duotone',
    provider: 'ph:brain-duotone',
    internal: 'ph:cube-duotone',
    cache: 'ph:database-duotone',
    function: 'ph:function-duotone',
    tool_loop: 'ph:arrows-clockwise-duotone',
    // Semantic taxonomy
    agent: 'octicon:ai-model-24',
    tool: 'ph:wrench-duotone',
    retriever: 'ph:magnifying-glass-duotone',
    embedding: 'ph:vector-three-duotone',
    sandbox: 'ph:terminal-window-duotone',
    browser: 'ph:browser-duotone',
    computer: 'ph:desktop-duotone',
    guardrail: 'ph:shield-check-duotone',
    scorer: 'ph:gauge-duotone',
    workflow: 'ph:flow-arrow-duotone',
    control: 'ph:git-branch-duotone',
    http: 'ph:swap-duotone',
    integration: 'ph:plugs-connected-duotone',
    harness: 'ph:package-duotone',
    media: 'ph:waveform-duotone',
}

/**
 * Solid "filled chip" styling for the trace-tree badges: the box is the
 * category's full colour and the glyph is a dark on-hue tone, so the chip reads
 * like a saturated tag (the inverse of the faint-tint `categoryColors` used for
 * inline badges elsewhere). Keyed to the same per-category hue families.
 */
export const categoryChipSolid: Record<SpanCategory, { bg: string; icon: string; border: string }> = {
    gateway: { bg: 'bg-sky-500', icon: 'text-sky-950', border: 'border-sky-600' },
    provider: { bg: 'bg-indigo-500', icon: 'text-indigo-950', border: 'border-indigo-600' },
    internal: { bg: 'bg-brand-secondary-500', icon: 'text-brand-secondary-900', border: 'border-brand-secondary-600' },
    cache: { bg: 'bg-amber-500', icon: 'text-amber-950', border: 'border-amber-600' },
    function: { bg: 'bg-purple-500', icon: 'text-purple-950', border: 'border-purple-600' },
    tool_loop: { bg: 'bg-blue-500', icon: 'text-blue-950', border: 'border-blue-600' },
    agent: { bg: 'bg-emerald-500', icon: 'text-emerald-950', border: 'border-emerald-600' },
    tool: { bg: 'bg-violet-500', icon: 'text-violet-950', border: 'border-violet-600' },
    retriever: { bg: 'bg-yellow-500', icon: 'text-yellow-950', border: 'border-yellow-600' },
    embedding: { bg: 'bg-fuchsia-500', icon: 'text-fuchsia-950', border: 'border-fuchsia-600' },
    sandbox: { bg: 'bg-orange-500', icon: 'text-orange-950', border: 'border-orange-600' },
    browser: { bg: 'bg-pink-500', icon: 'text-pink-950', border: 'border-pink-600' },
    computer: { bg: 'bg-lime-500', icon: 'text-lime-950', border: 'border-lime-600' },
    guardrail: { bg: 'bg-rose-500', icon: 'text-rose-950', border: 'border-rose-600' },
    scorer: { bg: 'bg-green-500', icon: 'text-green-950', border: 'border-green-600' },
    workflow: { bg: 'bg-teal-500', icon: 'text-teal-950', border: 'border-teal-600' },
    control: { bg: 'bg-zinc-500', icon: 'text-zinc-950', border: 'border-zinc-600' },
    http: { bg: 'bg-cyan-500', icon: 'text-cyan-950', border: 'border-cyan-600' },
    integration: { bg: 'bg-red-500', icon: 'text-red-950', border: 'border-red-600' },
    harness: { bg: 'bg-stone-500', icon: 'text-stone-950', border: 'border-stone-600' },
    media: { bg: 'bg-gray-500', icon: 'text-gray-950', border: 'border-gray-600' },
}

const categoryNameBadgeColors: Record<SpanCategory, { bg: string; icon: string; border: string }> = {
    gateway: { bg: 'bg-sky-500/10', icon: 'text-sky-300/85 light:text-sky-700/80', border: 'border-sky-500/20' },
    provider: { bg: 'bg-indigo-500/10', icon: 'text-indigo-300/85 light:text-indigo-700/80', border: 'border-indigo-500/20' },
    internal: { bg: 'bg-brand-secondary-500/10', icon: 'text-brand-secondary-200/85', border: 'border-brand-secondary-500/20' },
    cache: { bg: 'bg-amber-500/10', icon: 'text-amber-300/85 light:text-amber-700/80', border: 'border-amber-500/20' },
    function: { bg: 'bg-purple-500/10', icon: 'text-purple-300/85 light:text-purple-700/80', border: 'border-purple-500/20' },
    tool_loop: { bg: 'bg-blue-500/10', icon: 'text-blue-300/85 light:text-blue-700/80', border: 'border-blue-500/20' },
    agent: { bg: 'bg-emerald-500/10', icon: 'text-emerald-300/85 light:text-emerald-700/80', border: 'border-emerald-500/20' },
    tool: { bg: 'bg-violet-500/10', icon: 'text-violet-300/85 light:text-violet-700/80', border: 'border-violet-500/20' },
    retriever: { bg: 'bg-yellow-500/10', icon: 'text-yellow-300/85 light:text-yellow-700/80', border: 'border-yellow-500/20' },
    embedding: { bg: 'bg-fuchsia-500/10', icon: 'text-fuchsia-300/85 light:text-fuchsia-700/80', border: 'border-fuchsia-500/20' },
    sandbox: { bg: 'bg-orange-500/10', icon: 'text-orange-300/85 light:text-orange-700/80', border: 'border-orange-500/20' },
    browser: { bg: 'bg-pink-500/10', icon: 'text-pink-300/85 light:text-pink-700/80', border: 'border-pink-500/20' },
    computer: { bg: 'bg-lime-500/10', icon: 'text-lime-300/85 light:text-lime-700/80', border: 'border-lime-500/20' },
    guardrail: { bg: 'bg-rose-500/10', icon: 'text-rose-300/85 light:text-rose-700/80', border: 'border-rose-500/20' },
    scorer: { bg: 'bg-green-500/10', icon: 'text-green-300/85 light:text-green-700/80', border: 'border-green-500/20' },
    workflow: { bg: 'bg-teal-500/10', icon: 'text-teal-300/85 light:text-teal-700/80', border: 'border-teal-500/20' },
    control: { bg: 'bg-zinc-500/10', icon: 'text-zinc-300/85 light:text-zinc-700/80', border: 'border-zinc-500/20' },
    http: { bg: 'bg-cyan-500/10', icon: 'text-cyan-300/85 light:text-cyan-700/80', border: 'border-cyan-500/20' },
    integration: { bg: 'bg-red-500/10', icon: 'text-red-300/85 light:text-red-700/80', border: 'border-red-500/20' },
    harness: { bg: 'bg-stone-500/10', icon: 'text-stone-300/85 light:text-stone-700/80', border: 'border-stone-500/20' },
    media: { bg: 'bg-gray-500/10', icon: 'text-gray-300/85 light:text-gray-700/80', border: 'border-gray-500/20' },
}

/**
 * Color scheme for each span category (tree badges).
 *
 * Every category gets its own hue so a trace tree reads like a legend at a
 * glance: the icon says *what kind* of span, the colour reinforces it. Hues are
 * assigned so the categories that commonly co-occur inside one trace (agent /
 * provider / tool / retriever / http / sandbox / workflow) sit far apart on the
 * wheel; the rarer plumbing categories (internal / control / harness) stay in
 * the neutral grays so they recede. Tints follow the same `/15 bg, -300 text,
 * /25 border` formula as the rest of the trace UI. Mirrors the timeline-bar hex
 * map below.
 */
export const categoryColors: Record<SpanCategory, { bg: string; text: string; border: string }> = {
    // Gateway-centric (legacy) categories
    gateway: { bg: 'bg-sky-500/15', text: 'text-sky-300 light:text-sky-700', border: 'border-sky-500/25' },
    provider: { bg: 'bg-indigo-500/15', text: 'text-indigo-300 light:text-indigo-600', border: 'border-indigo-500/25' },
    internal: { bg: 'bg-brand-secondary-500/15', text: 'text-brand-secondary-200', border: 'border-brand-secondary-500/25' },
    cache: { bg: 'bg-amber-500/15', text: 'text-amber-300 light:text-amber-700', border: 'border-amber-500/25' },
    function: { bg: 'bg-purple-500/15', text: 'text-purple-300 light:text-purple-600', border: 'border-purple-500/25' },
    tool_loop: { bg: 'bg-blue-500/15', text: 'text-blue-300 light:text-blue-600', border: 'border-blue-500/25' },
    // Semantic taxonomy — one distinct hue each.
    agent: { bg: 'bg-emerald-500/15', text: 'text-emerald-300 light:text-emerald-600', border: 'border-emerald-500/25' },
    tool: { bg: 'bg-violet-500/15', text: 'text-violet-300 light:text-violet-600', border: 'border-violet-500/25' },
    retriever: { bg: 'bg-yellow-500/15', text: 'text-yellow-300 light:text-yellow-700', border: 'border-yellow-500/25' },
    embedding: { bg: 'bg-fuchsia-500/15', text: 'text-fuchsia-300 light:text-fuchsia-600', border: 'border-fuchsia-500/25' },
    sandbox: { bg: 'bg-orange-500/15', text: 'text-orange-300 light:text-orange-600', border: 'border-orange-500/25' },
    browser: { bg: 'bg-pink-500/15', text: 'text-pink-300 light:text-pink-600', border: 'border-pink-500/25' },
    computer: { bg: 'bg-lime-500/15', text: 'text-lime-300 light:text-lime-700', border: 'border-lime-500/25' },
    guardrail: { bg: 'bg-rose-500/15', text: 'text-rose-300 light:text-rose-600', border: 'border-rose-500/25' },
    scorer: { bg: 'bg-green-500/15', text: 'text-green-300 light:text-green-600', border: 'border-green-500/25' },
    workflow: { bg: 'bg-teal-500/15', text: 'text-teal-300 light:text-teal-600', border: 'border-teal-500/25' },
    control: { bg: 'bg-zinc-500/15', text: 'text-zinc-300 light:text-zinc-600', border: 'border-zinc-500/25' },
    http: { bg: 'bg-cyan-500/15', text: 'text-cyan-300 light:text-cyan-700', border: 'border-cyan-500/25' },
    integration: { bg: 'bg-red-500/15', text: 'text-red-300 light:text-red-600', border: 'border-red-500/25' },
    harness: { bg: 'bg-stone-500/15', text: 'text-stone-300 light:text-stone-600', border: 'border-stone-500/25' },
    media: { bg: 'bg-gray-500/15', text: 'text-gray-300 light:text-gray-600', border: 'border-gray-500/25' },
}

/**
 * Get a human-readable label for each category
 */
export const categoryLabels: Record<SpanCategory, string> = {
    gateway: 'Gateway',
    provider: 'Provider',
    internal: 'Internal',
    cache: 'Cache',
    function: 'Function',
    tool_loop: 'Tool Loop',
    agent: 'Agent',
    tool: 'Tool',
    retriever: 'Retriever',
    embedding: 'Embedding',
    sandbox: 'Sandbox',
    browser: 'Browser',
    computer: 'Computer',
    guardrail: 'Guardrail',
    scorer: 'Scorer',
    workflow: 'Workflow',
    control: 'Control',
    http: 'HTTP',
    integration: 'Integration',
    harness: 'Harness',
    media: 'Media',
}

/**
 * Timeline (gantt) bar colors per category — muted hex for inline styles, one
 * desaturated tone per category that lines up with the per-hue badges above so
 * a bar and its tree badge read as the same category.
 */
export const categoryTimelineColors: Record<SpanCategory, { fill: string; border: string }> = {
    gateway: { fill: '#3b82a8', border: '#5bb0d6' },     // sky
    provider: { fill: '#6c70c9', border: '#8e92dd' },    // indigo (primary span)
    internal: { fill: 'var(--color-brand-secondary-600)', border: 'var(--color-brand-secondary-400)' }, // brand accent
    cache: { fill: '#bf9350', border: '#d6ad68' },       // amber
    function: { fill: '#9b59c9', border: '#b87fe0' },    // purple
    tool_loop: { fill: '#4f7fd1', border: '#6f9ce6' },   // blue
    agent: { fill: '#3aa884', border: '#5bc7a3' },       // emerald
    tool: { fill: '#8f73d1', border: '#ab93e1' },        // violet
    retriever: { fill: '#c2a83e', border: '#d9c25c' },   // yellow
    embedding: { fill: '#b14fb1', border: '#d06fd0' },   // fuchsia
    sandbox: { fill: '#c47f3e', border: '#db9c5c' },     // orange
    browser: { fill: '#c45a8a', border: '#db7faa' },     // pink
    computer: { fill: '#84a83e', border: '#a3c75c' },    // lime
    guardrail: { fill: '#c44f6a', border: '#db6f8a' },   // rose
    scorer: { fill: '#4a9d5e', border: '#6cbd80' },      // green
    workflow: { fill: '#3a9d92', border: '#5bbdb0' },    // teal
    control: { fill: '#6b6b76', border: '#9595a3' },     // zinc
    http: { fill: '#3a9da8', border: '#5bbdc7' },        // cyan
    integration: { fill: '#c45050', border: '#db7070' }, // red
    harness: { fill: '#79716b', border: '#a8a29e' },     // stone
    media: { fill: '#6b7280', border: '#9ca3af' },       // gray
}

/**
 * A colourful, boxed badge for a trace/root **name** (the Trace Name column and
 * anywhere else a trace is listed by name). The chip keeps a category hue, but
 * uses a restrained tint so the icon reads as metadata instead of dominating the
 * trace row.
 *
 * We derive a semantic {@link SpanCategory} from the name via
 * `categoryFromSpanName` and reuse the category glyph. When the name matches no
 * category we still return a colour by hashing the name to a stable hue from a
 * diverse subset, paired with an honest generic trace glyph rather than a
 * misleading category one.
 */
// Identicon-style badges for trace names that match no semantic category: each
// pairs a distinct decorative glyph with a distinct muted hue, so two
// different trace names get two visibly different badges (varied icon AND colour)
// instead of collapsing to one look. The glyphs are intentionally abstract
// (shapes / cosmos / spark) rather than category icons so we never imply a false
// semantic — a hashed name has no known kind. Keyed by a stable hash of the name.
const FALLBACK_BADGES: Array<{ icon: string; chip: { bg: string; icon: string; border: string } }> = [
    { icon: 'ph:sparkle-duotone', chip: categoryNameBadgeColors.provider },
    { icon: 'ph:hexagon-duotone', chip: categoryNameBadgeColors.agent },
    { icon: 'ph:diamond-duotone', chip: categoryNameBadgeColors.embedding },
    { icon: 'ph:circles-three-duotone', chip: categoryNameBadgeColors.http },
    { icon: 'ph:shapes-duotone', chip: categoryNameBadgeColors.tool },
    { icon: 'ph:atom-duotone', chip: categoryNameBadgeColors.workflow },
    { icon: 'ph:planet-duotone', chip: categoryNameBadgeColors.sandbox },
    { icon: 'ph:compass-duotone', chip: categoryNameBadgeColors.retriever },
    { icon: 'ph:cube-duotone', chip: categoryNameBadgeColors.browser },
    { icon: 'ph:flower-lotus-duotone', chip: categoryNameBadgeColors.scorer },
    { icon: 'ph:lightning-duotone', chip: categoryNameBadgeColors.cache },
    { icon: 'ph:crosshair-duotone', chip: categoryNameBadgeColors.guardrail },
]

function hashName(name: string): number {
    let h = 0
    for (let i = 0; i < name.length; i++) {
        h = (Math.imul(h, 31) + name.charCodeAt(i)) | 0
    }
    return Math.abs(h)
}

export function getTraceNameBadge(name: string): {
    icon: string
    bg: string
    iconColor: string
    border: string
} {
    const category = categoryFromSpanName(name)
    if (category) {
        const chip = categoryNameBadgeColors[category]
        return { icon: categoryIcons[category], bg: chip.bg, iconColor: chip.icon, border: chip.border }
    }
    // No semantic match: stable hashed (glyph + hue) pair so each name looks unique.
    const fallback = FALLBACK_BADGES[hashName(name) % FALLBACK_BADGES.length]
    return { icon: fallback.icon, bg: fallback.chip.bg, iconColor: fallback.chip.icon, border: fallback.chip.border }
}
