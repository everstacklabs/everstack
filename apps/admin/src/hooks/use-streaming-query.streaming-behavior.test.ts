import { describe, it, expect } from 'vitest'
import {
  QueryClient,
  QueryObserver,
  keepPreviousData,
} from '@tanstack/react-query'

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

// Mirrors the FIXED use-streaming-query.ts queryFn: seeds the accumulator from
// the cache for this key so incremental setQueryData paints never collapse the
// list, then paints during the fetch and tails until aborted.
function makeLiveQueryFn(
  qc: QueryClient,
  key: unknown[],
  batch: string[],
  opts: { paintDelayMs?: number } = {},
) {
  return async ({ signal }: { signal: AbortSignal }) => {
    const acc: string[] = [...((qc.getQueryData(key) as string[]) ?? [])]
    try {
      for (const item of batch) {
        if (opts.paintDelayMs) await sleep(opts.paintDelayMs)
        if (!acc.includes(item)) acc.unshift(item)
        qc.setQueryData(key, acc.slice())
      }
      await new Promise<void>((_, reject) => {
        if (signal.aborted)
          return reject(new DOMException('aborted', 'AbortError') as unknown as Error)
        signal.addEventListener('abort', () =>
          reject(new DOMException('aborted', 'AbortError') as unknown as Error),
        )
      })
    } catch (err) {
      if (err instanceof Error && err.name !== 'AbortError') throw err
    }
    return acc
  }
}

describe('useStreamingQuery streaming fixes', () => {
  it('REFETCH no longer collapses the list to a single row', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const key = ['logs', { live: true }]
    const observer = new QueryObserver(qc, {
      queryKey: key,
      queryFn: makeLiveQueryFn(qc, key, ['a', 'b', 'c'], { paintDelayMs: 15 }),
      staleTime: 0,
      placeholderData: keepPreviousData,
    })
    const sizes: number[] = []
    const unsub = observer.subscribe((r) => {
      if (Array.isArray(r.data)) sizes.push(r.data.length)
    })
    await sleep(80)
    expect([...(observer.getCurrentResult().data as string[])].sort()).toEqual(['a', 'b', 'c'])

    // Only measure list sizes emitted DURING the refetch, not the initial paint.
    sizes.length = 0
    const p = observer.refetch()
    await sleep(80)
    void p

    // The list never drops below the 3 rows already shown.
    const minSizeDuringRefetch = Math.min(...sizes.filter((n) => n > 0))
    // eslint-disable-next-line no-console
    console.log('[fix-refetch] sizes seen:', sizes, 'min:', minSizeDuringRefetch)
    expect(minSizeDuringRefetch).toBe(3)
    expect([...(observer.getCurrentResult().data as string[])].sort()).toEqual(['a', 'b', 'c'])
    unsub()
  })

  it('KEY CHANGE keeps previous rows on screen while the new range loads', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const keyA = ['logs', { from: 'A' }]
    const keyB = ['logs', { from: 'B' }]
    const observer = new QueryObserver(qc, {
      queryKey: keyA,
      queryFn: makeLiveQueryFn(qc, keyA, ['a', 'b', 'c']),
      staleTime: 0,
      placeholderData: keepPreviousData,
    })
    const unsub = observer.subscribe(() => {})
    await sleep(20)
    expect([...(observer.getCurrentResult().data as string[])].sort()).toEqual(['a', 'b', 'c'])

    observer.setOptions({
      queryKey: keyB,
      queryFn: makeLiveQueryFn(qc, keyB, ['x', 'y'], { paintDelayMs: 40 }),
      staleTime: 0,
      placeholderData: keepPreviousData,
    })
    await sleep(5)
    const gapData = observer.getCurrentResult().data
    // eslint-disable-next-line no-console
    console.log('[fix-keychange] during gap, displayed:', gapData)
    // No blank screen: previous rows remain visible while keyB loads.
    expect([...(gapData as string[])].sort()).toEqual(['a', 'b', 'c'])

    await sleep(120)
    expect([...(observer.getCurrentResult().data as string[])].sort()).toEqual(['x', 'y'])
    unsub()
  })
})
