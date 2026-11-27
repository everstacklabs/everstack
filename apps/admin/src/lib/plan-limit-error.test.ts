import { describe, expect, it } from 'vitest'
import {
  decodeErrorInfo,
  isPlanLimitError,
  planLimitFromError,
} from './plan-limit-error'

// Encodes google.rpc.ErrorInfo the way the gateway sends it (reason,
// domain, metadata map entries), so the decoder is tested against real
// wire bytes rather than a hand-built object.
function encodeErrorInfo(
  reason: string,
  metadata: Record<string, string>,
): Uint8Array {
  const enc = new TextEncoder()
  const out: number[] = []
  const field = (num: number, payload: Uint8Array | number[]) => {
    out.push((num << 3) | 2)
    let len = payload.length
    while (len > 0x7f) {
      out.push((len & 0x7f) | 0x80)
      len >>>= 7
    }
    out.push(len)
    out.push(...payload)
  }
  const sub = (pairs: [number, string][]) => {
    const inner: number[] = []
    for (const [n, s] of pairs) {
      const b = enc.encode(s)
      inner.push((n << 3) | 2, b.length, ...b)
    }
    return inner
  }
  field(1, enc.encode(reason))
  field(2, enc.encode('everstack.ai'))
  for (const [k, v] of Object.entries(metadata))
    field(
      3,
      sub([
        [1, k],
        [2, v],
      ]),
    )
  return new Uint8Array(out)
}

const agentCap =
  'Agent limit reached: your plan includes 3 and you have 7. Upgrade your plan to add more agents: https://everstack.ai/pricing'

describe('planLimitFromError', () => {
  it('reads the plan_limit_reached ErrorInfo detail', () => {
    const err = {
      code: 8,
      rawMessage: agentCap,
      details: [
        {
          type: 'google.rpc.ErrorInfo',
          value: encodeErrorInfo('plan_limit_reached', {
            limit_type: 'AGENTS',
            limit: '3',
            used: '7',
          }),
        },
      ],
    }
    expect(planLimitFromError(err)).toEqual({
      message:
        'Agent limit reached: your plan includes 3 and you have 7. Upgrade your plan to add more agents.',
      limitType: 'AGENTS',
      limit: 3,
      used: 7,
    })
  })

  it('falls back to the message shape for gateways without the detail', () => {
    const err = {
      code: 8,
      rawMessage:
        'concurrent sandbox limit reached: 10 of 10 slots are allocated; stop or sleep a sandbox before starting another',
    }
    expect(planLimitFromError(err)?.message).toBe(
      'Concurrent sandbox limit reached: 10 of 10 slots are allocated; stop or sleep a sandbox before starting another.',
    )
  })

  it('never treats rate limiting or other codes as a plan cap', () => {
    expect(
      isPlanLimitError({
        code: 8,
        rawMessage: 'rate limit exceeded, try again in 30s',
      }),
    ).toBe(false)
    expect(isPlanLimitError({ code: 13, rawMessage: agentCap })).toBe(false)
    expect(isPlanLimitError(new Error(agentCap))).toBe(false)
    expect(isPlanLimitError(null)).toBe(false)
  })

  it('rejects malformed detail bytes without throwing', () => {
    expect(decodeErrorInfo(new Uint8Array([0x0a, 0x09, 0x61]))).toBeNull()
  })
})
