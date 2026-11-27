/**
 * Recognises a plan cap refusal from the gateway so create flows can show a
 * quiet inline upgrade notice instead of a red error toast.
 *
 * The gateway answers a capped create (agent, channel, dataset item, eval
 * run, annotation queue, hosted site, sandbox, ...) with Connect code
 * ResourceExhausted. Current gateways also attach a google.rpc.ErrorInfo
 * detail with reason "plan_limit_reached"; older ones only carry the message,
 * so the message shape is the fallback.
 */

const RESOURCE_EXHAUSTED = 8
const ERROR_INFO_TYPE = 'google.rpc.ErrorInfo'
export const PLAN_LIMIT_REASON = 'plan_limit_reached'

const PLAN_LIMIT_MESSAGE =
  /(limit reached|not included in your current plan|limit exceeded)/i
// Rate limiting also uses ResourceExhausted; it must never read as a plan cap.
const RATE_LIMIT_MESSAGE = /(rate limit|too many requests|try again in)/i

export interface PlanLimit {
  /** Customer-facing sentence, without the trailing pricing URL. */
  message: string
  limitType?: string
  limit?: number
  used?: number
}

interface ErrorInfo {
  reason: string
  metadata: Record<string, string>
}

interface DetailLike {
  type?: unknown
  value?: unknown
  debug?: unknown
}

/** Returns the plan cap behind err, or null when err is anything else. */
export function planLimitFromError(err: unknown): PlanLimit | null {
  if (!err || typeof err !== 'object') return null
  const e = err as {
    code?: unknown
    rawMessage?: unknown
    message?: unknown
    details?: unknown
  }
  if (e.code !== RESOURCE_EXHAUSTED && e.code !== 'resource_exhausted') {
    return null
  }
  const raw =
    typeof e.rawMessage === 'string'
      ? e.rawMessage
      : typeof e.message === 'string'
        ? e.message.replace(/^\[[a-z_]+\]\s*/i, '')
        : ''

  const info = findErrorInfo(e.details)
  if (info?.reason === PLAN_LIMIT_REASON) {
    return {
      message: readableMessage(raw),
      limitType: info.metadata.limit_type,
      limit: toNumber(info.metadata.limit),
      used: toNumber(info.metadata.used),
    }
  }
  if (PLAN_LIMIT_MESSAGE.test(raw) && !RATE_LIMIT_MESSAGE.test(raw)) {
    return { message: readableMessage(raw) }
  }
  return null
}

export function isPlanLimitError(err: unknown): boolean {
  return planLimitFromError(err) !== null
}

/** Drops the trailing pricing URL (the notice renders its own link). */
function readableMessage(raw: string): string {
  const trimmed = raw
    .replace(/[:\s]*https?:\/\/\S+\s*$/i, '')
    .replace(/^browserpool:\s*/i, '')
    .trim()
  if (!trimmed) return 'You have reached a limit on your current plan.'
  const sentence = trimmed.charAt(0).toUpperCase() + trimmed.slice(1)
  return /[.!?]$/.test(sentence) ? sentence : `${sentence}.`
}

function toNumber(v: string | undefined): number | undefined {
  if (v === undefined || v === '') return undefined
  const n = Number(v)
  return Number.isFinite(n) ? n : undefined
}

function findErrorInfo(details: unknown): ErrorInfo | null {
  if (!Array.isArray(details)) return null
  for (const d of details as DetailLike[]) {
    if (!d || d.type !== ERROR_INFO_TYPE) continue
    if (d.value instanceof Uint8Array) {
      const decoded = decodeErrorInfo(d.value)
      if (decoded) return decoded
    }
    const debug = d.debug as
      | { reason?: unknown; metadata?: Record<string, string> }
      | undefined
    if (debug && typeof debug.reason === 'string') {
      return { reason: debug.reason, metadata: debug.metadata ?? {} }
    }
  }
  return null
}

/**
 * Minimal protobuf decode of google.rpc.ErrorInfo
 * (1: reason, 2: domain, 3: map<string, string> metadata). Kept local so the
 * admin app does not depend on generated googleapis types for one message.
 */
export function decodeErrorInfo(bytes: Uint8Array): ErrorInfo | null {
  const text = new TextDecoder()
  const info: ErrorInfo = { reason: '', metadata: {} }
  try {
    readFields(bytes, (field, value) => {
      if (field === 1) info.reason = text.decode(value)
      if (field === 3) {
        let key = ''
        let val = ''
        readFields(value, (f, v) => {
          if (f === 1) key = text.decode(v)
          if (f === 2) val = text.decode(v)
        })
        if (key) info.metadata[key] = val
      }
    })
  } catch {
    return null
  }
  return info.reason ? info : null
}

function readFields(
  bytes: Uint8Array,
  onLengthDelimited: (field: number, value: Uint8Array) => void,
): void {
  let pos = 0
  const varint = (): number => {
    let result = 0
    let shift = 0
    for (;;) {
      if (pos >= bytes.length) throw new Error('truncated varint')
      const b = bytes[pos++]
      result += (b & 0x7f) * 2 ** shift
      if ((b & 0x80) === 0) return result
      shift += 7
      if (shift > 49) throw new Error('varint too long')
    }
  }
  while (pos < bytes.length) {
    const tag = varint()
    const field = Math.floor(tag / 8)
    const wire = tag & 7
    if (wire === 2) {
      const len = varint()
      if (pos + len > bytes.length) throw new Error('truncated field')
      onLengthDelimited(field, bytes.subarray(pos, pos + len))
      pos += len
    } else if (wire === 0) {
      varint()
    } else if (wire === 1) {
      pos += 8
    } else if (wire === 5) {
      pos += 4
    } else {
      throw new Error('unsupported wire type')
    }
  }
}
