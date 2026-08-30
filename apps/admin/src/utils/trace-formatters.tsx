/**
 * Core formatting utilities for traces observability UI
 * Ensures consistent formatting of durations, costs, tokens, and percentages
 */

import type { ReactNode } from 'react'
import { Iconify } from '@everstack/ui/icons'

/**
 * Safely convert BigInt to Number, clamping to safe integer range
 * @param value - BigInt value to convert
 * @returns Number value, clamped to safe integer range if necessary
 */
export function safeBigIntToNumber(value: bigint): number {
  const MAX_SAFE = BigInt(Number.MAX_SAFE_INTEGER)
  const MIN_SAFE = BigInt(Number.MIN_SAFE_INTEGER)

  if (value > MAX_SAFE) {
    return Number(MAX_SAFE)
  }
  if (value < MIN_SAFE) {
    return Number(MIN_SAFE)
  }
  return Number(value)
}

/**
 * Format duration with automatic unit selection
 * @param durationNs - Duration in nanoseconds (can be number, bigint, null, or undefined)
 * @returns Formatted string with appropriate unit (µs, ms, or s)
 */
export function formatDuration(
  durationNs: number | bigint | null | undefined,
): string {
  if (durationNs == null) {
    return '0ms'
  }

  const durationNum =
    typeof durationNs === 'bigint' ? safeBigIntToNumber(durationNs) : durationNs

  if (isNaN(durationNum)) {
    return '0ms'
  }

  const durationMs = durationNum / 1_000_000

  // < 1ms: show in microseconds
  if (durationMs < 1) {
    const durationUs = durationNum / 1_000
    return durationUs < 0.01 ? '0µs' : `${durationUs.toFixed(2)}µs`
  }

  // < 1s: show in milliseconds
  if (durationMs < 1000) {
    return `${durationMs.toFixed(2)}ms`
  }

  // >= 1s: show in seconds
  const durationS = durationMs / 1000
  return `${durationS.toFixed(2)}s`
}

/**
 * Format cost with consistent 5-decimal precision
 * @param cost - Cost value (can be number, string, or null)
 * @returns Formatted cost string with $ prefix
 */
export function formatCost(cost: number | string | null | undefined): string {
  if (cost == null) {
    return '$0.00000'
  }

  const numericCost = typeof cost === 'string' ? parseFloat(cost) : cost

  if (isNaN(numericCost)) {
    return '$0.00000'
  }

  if (numericCost === 0) {
    return '$0.00000'
  }

  // For very small costs, show as "< $0.00001"
  if (numericCost > 0 && numericCost < 0.00001) {
    return '<$0.00001'
  }

  return `$${numericCost.toFixed(5)}`
}

/**
 * Safely parse token count from various input types
 * @param value - Value to parse (can be string, number, bigint, or null)
 * @returns Parsed integer or 0 if invalid
 */
export function parseTokenCount(value: unknown): number {
  if (value == null) {
    return 0
  }

  if (typeof value === 'number') {
    return isNaN(value) ? 0 : Math.floor(value)
  }

  if (typeof value === 'bigint') {
    return safeBigIntToNumber(value)
  }

  if (typeof value === 'string') {
    const parsed = parseInt(value, 10)
    return isNaN(parsed) ? 0 : parsed
  }

  return 0
}

/**
 * Format token count with locale-specific thousand separators
 * @param count - Token count
 * @returns Formatted string (e.g., "1,000 tokens")
 */
export function formatTokens(count: number | null | undefined): string {
  const tokens = parseTokenCount(count)

  if (tokens === 0) {
    return '0 tokens'
  }

  if (tokens === 1) {
    return '1 token'
  }

  return `${tokens.toLocaleString()} tokens`
}

/**
 * Calculate percentage with division by zero protection
 * @param part - Partial value (can be number or bigint)
 * @param total - Total value (can be number or bigint)
 * @param decimals - Number of decimal places (default: 1)
 * @returns Formatted percentage string
 */
export function calculatePercentage(
  part: number | bigint | null | undefined,
  total: number | bigint | null | undefined,
  decimals: number = 1,
): string {
  if (part == null || total == null) {
    return '0%'
  }

  // Convert BigInt to number safely
  const partNum = typeof part === 'bigint' ? safeBigIntToNumber(part) : part
  const totalNum = typeof total === 'bigint' ? safeBigIntToNumber(total) : total

  if (isNaN(partNum) || isNaN(totalNum)) {
    return '0%'
  }

  if (totalNum === 0) {
    return '0%'
  }

  const percentage = (partNum / totalNum) * 100

  if (isNaN(percentage)) {
    return '0%'
  }

  return `${percentage.toFixed(decimals)}%`
}

/**
 * Safely parse JSON with error handling and circular reference protection
 * @param value - String to parse
 * @param fallback - Fallback value if parsing fails
 * @returns Parsed object or fallback
 */
export function safeJsonParse<T = unknown>(
  value: string | null | undefined,
  fallback: T,
): T {
  if (!value || typeof value !== 'string') {
    return fallback
  }

  try {
    const parsed = JSON.parse(value)
    return parsed as T
  } catch (error) {
    // If parsing fails, return fallback
    return fallback
  }
}

/**
 * Safely stringify JSON with circular reference handling
 * @param value - Value to stringify
 * @param pretty - Whether to format with indentation
 * @returns JSON string or error message
 */
export function safeJsonStringify(
  value: unknown,
  pretty: boolean = false,
): string {
  try {
    // Handle circular references
    const seen = new WeakSet()
    const replacer = (_key: string, val: unknown) => {
      if (typeof val === 'object' && val !== null) {
        if (seen.has(val)) {
          return '[Circular]'
        }
        seen.add(val)
      }
      return val
    }

    return JSON.stringify(value, replacer, pretty ? 2 : 0)
  } catch (error) {
    return '[Error stringifying value]'
  }
}

/**
 * Format a timestamp to relative time from trace start
 * @param timestamp - ISO timestamp or epoch time
 * @param traceStartTime - Trace start timestamp
 * @returns Formatted relative time (e.g., "+125ms")
 */
export function formatRelativeTime(
  timestamp: string | number | null | undefined,
  traceStartTime: string | number | null | undefined,
): string {
  if (!timestamp || !traceStartTime) {
    return '0ms'
  }

  try {
    const eventTime =
      typeof timestamp === 'string' ? new Date(timestamp).getTime() : timestamp
    const startTime =
      typeof traceStartTime === 'string'
        ? new Date(traceStartTime).getTime()
        : traceStartTime

    if (isNaN(eventTime) || isNaN(startTime)) {
      return '0ms'
    }

    const diffMs = eventTime - startTime

    if (diffMs < 0) {
      return '0ms'
    }

    if (diffMs < 1000) {
      return `+${diffMs.toFixed(0)}ms`
    }

    const diffS = diffMs / 1000
    return `+${diffS.toFixed(2)}s`
  } catch (error) {
    return '0ms'
  }
}

/**
 * Format bytes to human-readable size
 * @param bytes - Size in bytes
 * @returns Formatted string (e.g., "1.5 MB")
 */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null || isNaN(bytes) || bytes === 0) {
    return '0 B'
  }

  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const k = 1024
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(k))
  const unitIndex = Math.min(i, units.length - 1)

  return `${(bytes / Math.pow(k, unitIndex)).toFixed(2)} ${units[unitIndex]}`
}

/**
 * Truncate text with ellipsis
 * @param text - Text to truncate
 * @param maxLength - Maximum length before truncation
 * @returns Truncated text with ellipsis if needed
 */
export function truncateText(
  text: string | null | undefined,
  maxLength: number,
): string {
  if (!text) {
    return ''
  }

  if (text.length <= maxLength) {
    return text
  }

  return `${text.substring(0, maxLength)}...`
}

/**
 * Detect if a string is valid JSON
 * @param value - String to check
 * @returns True if valid JSON
 */
export function isJsonString(value: string | null | undefined): boolean {
  if (!value || typeof value !== 'string') {
    return false
  }

  // Quick check for JSON-like structure
  const trimmed = value.trim()
  if (
    !(trimmed.startsWith('{') && trimmed.endsWith('}')) &&
    !(trimmed.startsWith('[') && trimmed.endsWith(']'))
  ) {
    return false
  }

  try {
    JSON.parse(value)
    return true
  } catch {
    return false
  }
}

/**
 * Format a number with locale-specific formatting
 * @param value - Number to format
 * @param decimals - Number of decimal places (default: 2)
 * @returns Formatted number string
 */
export function formatNumber(
  value: number | null | undefined,
  decimals: number = 2,
): string {
  if (value == null || isNaN(value)) {
    return '0'
  }

  return value.toLocaleString(undefined, {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  })
}

/**
 * Format tokens in compact form for console display
 * @param tokens - Token count (can be number or bigint)
 * @returns Compact formatted string (e.g., "1.2k tok" or "145 tok")
 */
export function formatTokensCompact(
  tokens: number | bigint | null | undefined,
): string {
  const num =
    typeof tokens === 'bigint' ? safeBigIntToNumber(tokens) : (tokens ?? 0)
  if (num === 0) return '-'
  if (num < 1000) return `${num}`
  if (num < 1000000) return `${(num / 1000).toFixed(1)}k`
  return `${(num / 1000000).toFixed(1)}M`
}

/**
 * Format token breakdown for console display (input/output)
 * @param inputTokens - Input token count
 * @param outputTokens - Output token count
 * @returns Formatted string (e.g., "1.2k↑ / 500↓")
 */
export function formatTokenBreakdown(
  inputTokens: number | bigint | null | undefined,
  outputTokens: number | bigint | null | undefined,
): string {
  const input =
    typeof inputTokens === 'bigint'
      ? safeBigIntToNumber(inputTokens)
      : (inputTokens ?? 0)
  const output =
    typeof outputTokens === 'bigint'
      ? safeBigIntToNumber(outputTokens)
      : (outputTokens ?? 0)

  if (input === 0 && output === 0) return '-'

  const formatNum = (num: number): string => {
    if (num === 0) return '-'
    if (num < 1000) return `${num}`
    if (num < 1000000) return `${(num / 1000).toFixed(1)}k`
    return `${(num / 1000000).toFixed(1)}M`
  }

  return `${formatNum(input)}↑ / ${formatNum(output)}↓`
}

/**
 * Get provider abbreviation (3 characters)
 * @param provider - Provider name
 * @returns 3-character abbreviation
 */
export function getProviderAbbrev(provider: string | null | undefined): string {
  if (!provider) return 'UNK'

  const abbrevs: Record<string, string> = {
    openai: 'OAI',
    anthropic: 'ATH',
    'azure-openai': 'AZR',
    'aws-bedrock': 'BDR',
    'vertex-ai': 'VTX',
    cohere: 'CLD',
    google: 'GGL',
    groq: 'GRQ',
    mistral: 'MST',
    together: 'TGD',
    fireworks: 'FWK',
    xai: 'XAI',
    perplexity: 'PPX',
    cerebras: 'CRB',
    'nvidia-nim': 'NIM',
  }

  return (
    abbrevs[provider.toLowerCase()] || provider.substring(0, 3).toUpperCase()
  )
}

export function getStatusIcon(status: string | null | undefined): ReactNode {
  if (!status)
    return (
      <Iconify.Icon
        icon="lucide:question-mark-circle"
        className="size-4 text-yellow-400 light:text-yellow-700"
      />
    )

  if (status === 'success')
    return (
      <Iconify.Icon
        icon="mi:circle-check"
        className="size-4 text-emerald-400/50 light:text-emerald-600/60"
      />
    )
  if (status === 'error')
    return (
      <Iconify.Icon
        icon="lucide:x-circle"
        className="size-4 text-rose-400/50 light:text-rose-600/60"
      />
    )
  if (status === 'pending')
    return (
      <Iconify.Icon
        icon="lucide:question-mark-circle"
        className="size-4 text-yellow-400 light:text-yellow-700"
      />
    )
  return (
    <Iconify.Icon
      icon="lucide:question-mark-circle"
      className="size-4 text-yellow-400 light:text-yellow-700"
    />
  )
}

/**
 * Format timestamp as full date-time for console display
 * @param timestamp - Timestamp object with seconds field
 * @returns Formatted date-time string (YYYY-MM-DD HH:MM:SS)
 */
export function formatTimeHHMMSS(
  timestamp: { seconds?: number | bigint } | null | undefined,
): string {
  if (!timestamp || !timestamp.seconds) return '----------- --:--:--'

  const seconds =
    typeof timestamp.seconds === 'bigint'
      ? safeBigIntToNumber(timestamp.seconds)
      : timestamp.seconds

  const date = new Date(seconds * 1000)

  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  const secs = String(date.getSeconds()).padStart(2, '0')

  return `${year}-${month}-${day} ${hours}:${minutes}:${secs}`
}

/**
 * Format latency in compact form for console display
 * @param ms - Latency in milliseconds (can be number or bigint)
 * @returns Formatted latency string (e.g., "123ms" or "1.23s")
 */
export function formatLatencyCompact(
  ms: number | bigint | null | undefined,
): string {
  if (ms == null) return 'ERR'

  const num = typeof ms === 'bigint' ? safeBigIntToNumber(ms) : ms

  if (num === 0) return '-'
  if (num < 1000) return `${num}ms`
  return `${(num / 1000).toFixed(2)}s`
}

/**
 * Format cost in compact form for console display
 * @param cost - Cost value
 * @returns Formatted cost string
 */
export function formatCostCompact(cost: number | null | undefined): string {
  if (cost == null || cost === 0) return '$0.0000'

  if (cost < 0.0001) return '<$0.0001'

  return `$${cost.toFixed(4)}`
}
