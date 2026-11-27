/**
 * Time-bucket labelling for bar charts and sparklines whose x values are
 * fixed-width buckets (e.g. 1h, 6h or 1d starts sent by the server).
 *
 * Bucket starts are usually UTC multiples of the width, so in most viewer
 * time zones a 6h or 1d bucket does not start at local midnight. Labelling
 * every bucket with its date (and letting the chart thin the labels) repeats
 * dates and puts them at arbitrary positions. These helpers pick the ticks
 * instead: one per local day boundary for sub-day buckets over several days,
 * hour ticks for short windows, and day labels under the bars for day
 * buckets. Every tick sits on a bucket start (or a day bucket's centre), so
 * labels line up with bars, and none sits so close to an end of the axis
 * that its label would be cut off. Pure: the time zone is a parameter.
 */

const HOUR = 3_600_000
const DAY = 24 * HOUR

export interface BucketFormatOptions {
    /** IANA zone the labels are rendered in. Default: the viewer's zone. */
    timeZone?: string
    /** Upper bound on labels; the step grows until the count fits. Default 7. */
    maxLabels?: number
    /**
     * No tick within this fraction of the axis span from either end, so a
     * centred label never runs off the plot. Default 0.06.
     */
    edgeMargin?: number
}

export interface BucketTick {
    /** Bucket index the tick belongs to. */
    index: number
    /** Axis position (epoch ms): the bucket start, or its centre for day buckets. */
    value: number
    label: string
}

interface LocalParts {
    /** yyyy-mm-dd in the target zone, for day comparisons. */
    day: string
    hour: number
    minute: number
}

const partsFormatters = new Map<string, Intl.DateTimeFormat>()
const dayFormatters = new Map<string, Intl.DateTimeFormat>()
const timeFormatters = new Map<string, Intl.DateTimeFormat>()

function zoneKey(timeZone: string | undefined): string {
    return timeZone ?? ''
}

function cached(
    map: Map<string, Intl.DateTimeFormat>,
    timeZone: string | undefined,
    make: () => Intl.DateTimeFormat,
): Intl.DateTimeFormat {
    const k = zoneKey(timeZone)
    let f = map.get(k)
    if (!f) {
        f = make()
        map.set(k, f)
    }
    return f
}

function localParts(ms: number, timeZone?: string): LocalParts {
    const f = cached(partsFormatters, timeZone, () =>
        new Intl.DateTimeFormat('en-US', {
            timeZone,
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            hourCycle: 'h23',
        }),
    )
    const p: Record<string, string> = {}
    for (const part of f.formatToParts(new Date(ms))) p[part.type] = part.value
    return {
        day: `${p.year}-${p.month}-${p.day}`,
        hour: Number(p.hour) % 24,
        minute: Number(p.minute),
    }
}

/** "Oct 7" in the target zone. */
export function formatBucketDay(ms: number, timeZone?: string): string {
    return cached(dayFormatters, timeZone, () =>
        new Intl.DateTimeFormat('en-US', { timeZone, month: 'short', day: 'numeric' }),
    ).format(new Date(ms))
}

/** "07:00" (24-hour) in the target zone. */
export function formatBucketTime(ms: number, timeZone?: string): string {
    return cached(timeFormatters, timeZone, () =>
        new Intl.DateTimeFormat('en-US', {
            timeZone,
            hour: '2-digit',
            minute: '2-digit',
            hourCycle: 'h23',
        }),
    ).format(new Date(ms))
}

/**
 * The bucket a tooltip is about, as a range: "Oct 7, 01:00–07:00",
 * "Oct 6, 17:00 – Oct 7, 17:00", or "Oct 7" for a day bucket that is
 * exactly one local day.
 */
export function formatBucketRange(
    start: number,
    bucketMs: number,
    { timeZone }: Pick<BucketFormatOptions, 'timeZone'> = {},
): string {
    const end = start + bucketMs
    const a = localParts(start, timeZone)
    const b = localParts(end, timeZone)
    if (bucketMs === DAY && a.hour === 0 && a.minute === 0) {
        return formatBucketDay(start, timeZone)
    }
    const startText = `${formatBucketDay(start, timeZone)}, ${formatBucketTime(start, timeZone)}`
    if (a.day === b.day) return `${startText}–${formatBucketTime(end, timeZone)}`
    return `${startText} – ${formatBucketDay(end, timeZone)}, ${formatBucketTime(end, timeZone)}`
}

/** Smallest step from `steps` that keeps `count / step` within `max`. */
function pickStep(count: number, max: number, steps: readonly number[]): number {
    for (const s of steps) if (Math.ceil(count / s) <= max) return s
    return steps[steps.length - 1] ?? 1
}

/** Keep every `step`-th item, counting back from the newest. */
function thinFromEnd<T>(items: T[], step: number): T[] {
    if (step <= 1) return items
    return items.filter((_, i) => (items.length - 1 - i) % step === 0)
}

/**
 * Which buckets get an x label, and what it says.
 *
 * - Day (or longer) buckets: the bar is the day, so the label is the local
 *   date of the bucket's centre, placed under the bar, every Nth day.
 * - Sub-day buckets over a window longer than 36h: one label per local day
 *   boundary, on the first bucket that starts in the new day.
 * - Sub-day buckets over a short window: hour labels every N hours on the
 *   bucket that starts each N-hour slot; local midnight reads as the date.
 */
export function bucketTicks(
    starts: readonly number[],
    bucketMs: number,
    { timeZone, maxLabels = 7, edgeMargin = 0.06 }: BucketFormatOptions = {},
): BucketTick[] {
    const n = starts.length
    if (n === 0 || bucketMs <= 0) return []
    const max = Math.max(1, maxLabels)
    const lo = starts[0]! + n * bucketMs * edgeMargin
    const hi = starts[0]! + n * bucketMs * (1 - edgeMargin)
    const inside = (t: BucketTick) => t.value >= lo && t.value <= hi

    if (bucketMs >= DAY) {
        const all = starts
            .map((s, index) => {
                const centre = s + bucketMs / 2
                return { index, value: centre, label: formatBucketDay(centre, timeZone) }
            })
            .filter(inside)
        return thinFromEnd(all, pickStep(all.length, max, [1, 2, 3, 5, 7, 14, 28]))
    }

    const parts = starts.map((s) => localParts(s, timeZone))
    const spanMs = n * bucketMs

    if (spanMs > 36 * HOUR) {
        const boundaries: BucketTick[] = []
        parts.forEach((p, index) => {
            const prev = parts[index - 1]
            const crossed = prev ? prev.day !== p.day : p.hour === 0 && p.minute === 0
            if (crossed) {
                const s = starts[index]!
                boundaries.push({ index, value: s, label: formatBucketDay(s, timeZone) })
            }
        })
        const kept = boundaries.filter(inside)
        return thinFromEnd(kept, pickStep(kept.length, max, [1, 2, 7, 14]))
    }

    const bucketHours = Math.max(1, Math.round(bucketMs / HOUR))
    const stepHours = pickStep(
        (spanMs * (1 - 2 * edgeMargin)) / HOUR,
        max,
        [1, 2, 3, 4, 6, 12, 24].filter((h) => h >= bucketHours),
    )
    const ticks: BucketTick[] = []
    parts.forEach((p, index) => {
        const slot = `${p.day}/${Math.floor(p.hour / stepHours)}`
        const prev = parts[index - 1]
        const prevSlot = prev ? `${prev.day}/${Math.floor(prev.hour / stepHours)}` : null
        const opens = prev
            ? slot !== prevSlot
            : p.minute === 0 && p.hour % stepHours === 0
        if (!opens) return
        const s = starts[index]!
        const midnight = p.hour === 0 && p.minute === 0
        ticks.push({
            index,
            value: s,
            label: midnight ? formatBucketDay(s, timeZone) : formatBucketTime(s, timeZone),
        })
    })
    return ticks.filter(inside)
}

/** Index of the bucket holding `ms`, clamped to the series. */
export function bucketIndexAt(starts: readonly number[], bucketMs: number, ms: number): number {
    const first = starts[0]
    if (first === undefined || bucketMs <= 0) return -1
    const i = Math.floor((ms - first) / bucketMs)
    return Math.min(starts.length - 1, Math.max(0, i))
}
