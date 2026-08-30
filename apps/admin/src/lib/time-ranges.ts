import type { TimeRangePreset, CustomTimeRange } from '@/stores/logs-store'

export interface TimeRangeResult {
    from: string
    to: string
}

export const TIME_RANGE_LABELS: Record<TimeRangePreset, string> = {
    '15m': 'Last 15 minutes',
    '6h': 'Last 6 hours',
    '12h': 'Last 12 hours',
    '24h': 'Last 24 hours',
    '3d': 'Last 3 days',
    '7d': 'Last 7 days',
    '14d': 'Last 14 days',
    '30d': 'Last 30 days',
    '90d': 'Last 90 days',
    'custom': 'Custom range',
}

const TIME_RANGE_MS: Record<Exclude<TimeRangePreset, 'custom'>, number> = {
    '15m': 15 * 60 * 1000,
    '6h': 6 * 60 * 60 * 1000,
    '12h': 12 * 60 * 60 * 1000,
    '24h': 24 * 60 * 60 * 1000,
    '3d': 3 * 24 * 60 * 60 * 1000,
    '7d': 7 * 24 * 60 * 60 * 1000,
    '14d': 14 * 24 * 60 * 60 * 1000,
    '30d': 30 * 24 * 60 * 60 * 1000,
    '90d': 90 * 24 * 60 * 60 * 1000,
}

export function calculateTimeRange(
    preset: TimeRangePreset,
    custom?: CustomTimeRange | null
): TimeRangeResult {
    if (preset === 'custom' && custom) {
        return {
            from: custom.start.toISOString(),
            to: custom.end.toISOString(),
        }
    }

    // If preset is 'custom' but no custom range provided, fall back to default
    const safePreset = (preset === 'custom' ? '15m' : preset) as Exclude<TimeRangePreset, 'custom'>

    const now = new Date()
    const ms = TIME_RANGE_MS[safePreset]
    const from = new Date(now.getTime() - ms)

    return {
        from: from.toISOString(),
        to: now.toISOString(),
    }
}

/**
 * Determines if a given time range should be in live mode or paused mode.
 * 
 * @param preset - The time range preset (e.g., '15m', '6h', 'custom')
 * @param custom - Optional custom time range with start and end dates
 * @returns true if the range should be in live mode, false if it should be paused
 * 
 * Logic:
 * - Preset ranges (15m, 6h, etc.) are always live since they're relative to "now"
 * - Custom ranges are live only if the end time is at or after the current time
 * - Custom ranges with end time in the past should be paused (historical data)
 */
export function shouldBeLiveMode(
    preset: TimeRangePreset,
    custom?: CustomTimeRange | null
): boolean {
    // Preset ranges are always live (they're relative to "now")
    if (preset !== 'custom') {
        return true
    }

    // Custom range without valid dates defaults to live
    if (!custom?.start || !custom?.end) {
        return true
    }

    // If custom range end is before current time, it's historical data (paused mode)
    const now = new Date()
    const endTime = new Date(custom.end)

    // Add a small buffer (1 second) to handle edge cases where end time is very close to now
    return endTime.getTime() >= (now.getTime() - 1000)
}
