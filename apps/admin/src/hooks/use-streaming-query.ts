import { useMemo, useState, useEffect, useCallback } from 'react'
import { useQuery, useQueryClient, keepPreviousData } from '@tanstack/react-query'

export interface UseStreamingQueryOptions<T> {
    /**
     * Base query key prefix (e.g., 'logs' or 'traces')
     */
    queryKeyPrefix: string

    /**
     * Start time (ISO string)
     */
    from: string

    /**
     * End time (ISO string)
     */
    to: string

    /**
     * Whether in live mode (streaming) or paused mode (historical)
     */
    isLiveMode: boolean

    /**
     * Streaming function that yields items
     */
    streamFn: (signal?: AbortSignal, offset?: number, limit?: number) => AsyncGenerator<T, void, unknown>

    /**
     * Function to extract unique ID from item
     */
    getItemId: (item: T) => string

    /**
     * Function to extract timestamp from item (in milliseconds)
     */
    getItemTimestamp: (item: T) => number

    /**
     * Enable infinite scroll pagination
     */
    enableInfiniteScroll?: boolean

    /**
     * Page size for pagination (default: 500)
     */
    pageSize?: number

    /**
     * Extra values to include in query key (triggers re-fetch on change)
     */
    queryKeyExtra?: Record<string, unknown>

    /**
     * Additional query options
     */
    queryOptions?: {
        staleTime?: number
        gcTime?: number
    }
}

export function useStreamingQuery<T>({
    queryKeyPrefix,
    from,
    to,
    isLiveMode,
    streamFn,
    getItemId,
    getItemTimestamp,
    enableInfiniteScroll = false,
    pageSize = 500,
    queryKeyExtra,
    queryOptions = {},
}: UseStreamingQueryOptions<T>) {
    const qc = useQueryClient()

    // Stable serialization of extra key for memoization
    const extraKeyStr = queryKeyExtra ? JSON.stringify(queryKeyExtra) : ''

    // Build query key directly from props (no debouncing to avoid cache misses)
    // Include isLiveMode to prevent cache reuse when switching between live/paused modes
    const queryKey = useMemo(
        () => [queryKeyPrefix, { from, to, live: isLiveMode, ...(queryKeyExtra || {}) }],
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [queryKeyPrefix, from, to, isLiveMode, extraKeyStr]
    )

    // Track current offset for pagination
    const [currentOffset, setCurrentOffset] = useState(0)
    const [hasMore, setHasMore] = useState(true)
    const [isFetchingMore, setIsFetchingMore] = useState(false)

    // Reset offset and fetching state when query key changes
    useEffect(() => {
        setCurrentOffset(0)
        setHasMore(true)
        setIsFetchingMore(false) // Reset fetching state on query key change
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [queryKeyPrefix, from, to, isLiveMode, extraKeyStr])

    // Streaming query
    const query = useQuery<T[]>({
        queryKey,
        queryFn: async ({ signal }) => {
            // Seed the accumulator from whatever is already cached for THIS key.
            // On a refetch (same key — e.g. live tail re-run, window focus,
            // reconnect) this prevents the list from momentarily collapsing to a
            // single row: the first streamed item would otherwise overwrite the
            // full list with a fresh [item] via setQueryData. On a new key the
            // cache is empty (placeholder data is not stored), so this is [].
            const acc: T[] = [...(qc.getQueryData<T[]>(queryKey) ?? [])]
            const seen = new Set<string>(
                acc.map(getItemId).filter(Boolean) as string[],
            )
            const toTime = new Date(to).getTime()

            // Determine limit based on infinite scroll setting
            const limit = enableInfiniteScroll ? pageSize : undefined

            try {
                for await (const item of streamFn(signal, 0, limit)) {
                    const itemId = getItemId(item)
                    if (!itemId) continue

                    // In paused mode, stop when we've received items beyond the 'to' time
                    if (!isLiveMode) {
                        const itemTime = getItemTimestamp(item)
                        if (itemTime > toTime) {
                            break
                        }
                    }

                    // Check if this item already exists (status update)
                    const existingIndex = acc.findIndex(
                        (i) => getItemId(i) === itemId
                    )
                    if (existingIndex >= 0) {
                        // Update existing item
                        acc[existingIndex] = item
                    } else {
                        // New item
                        seen.add(itemId)
                        acc.unshift(item)
                        // Only limit if infinite scroll is disabled
                        if (!enableInfiniteScroll && acc.length > 500) {
                            acc.pop()
                        }
                    }

                    // Update cache immediately for real-time UI
                    qc.setQueryData(queryKey, acc.slice())
                }

                // Check if we got less than requested - means no more data
                if (enableInfiniteScroll && acc.length < pageSize) {
                    setHasMore(false)
                }
            } catch (err) {
                // AbortError is expected when React Query cancels the request
                if (err instanceof Error && err.name !== 'AbortError') {
                    throw err
                }
            }

            return acc
        },
        refetchOnMount: false, // Don't refetch on mount - rely on cache
        enabled: true,
        // Data never goes stale - only refetch on manual refresh or query key change
        // This prevents expensive re-queries when switching routes
        staleTime: queryOptions.staleTime ?? Infinity,
        // Use global gcTime (30 minutes) to persist data across route switches
        gcTime: queryOptions.gcTime ?? 30 * 60 * 1000,
        // Keep the previous key's rows on screen while the new time range loads.
        // Previously this was `undefined`, which blanked the list on every range
        // change until the (streaming, sometimes slow) new query painted its
        // first row — read by users as "the data flashed then disappeared". The
        // client-side time-window filter re-scopes the carried-over rows to the
        // new range, and they are replaced as soon as fresh data streams in.
        placeholderData: keepPreviousData,
    })

    // Reset isFetchingMore when main query is loading (prevents stuck state)
    useEffect(() => {
        if (query.isLoading || query.isFetching) {
            setIsFetchingMore(false)
        }
    }, [query.isLoading, query.isFetching])

    // Function to fetch next page
    const fetchNextPage = useCallback(async () => {
        if (!enableInfiniteScroll || !hasMore || isFetchingMore || !query.data) {
            return
        }

        setIsFetchingMore(true)
        const nextOffset = currentOffset + pageSize

        try {
            const acc: T[] = [...query.data]
            const seen = new Set<string>(acc.map(getItemId))
            const toTime = new Date(to).getTime()
            let newItemsCount = 0

            // Create a new AbortController for this fetch with timeout
            const abortController = new AbortController()
            const timeoutId = setTimeout(() => {
                abortController.abort()
            }, 30000) // 30 second timeout

            try {
                for await (const item of streamFn(abortController.signal, nextOffset, pageSize)) {
                    const itemId = getItemId(item)
                    if (!itemId) continue

                    // In paused mode, stop when we've received items beyond the 'to' time
                    if (!isLiveMode) {
                        const itemTime = getItemTimestamp(item)
                        if (itemTime > toTime) {
                            break
                        }
                    }

                    // Skip duplicates
                    if (seen.has(itemId)) continue

                    seen.add(itemId)
                    acc.push(item) // Append to end since we're paginating
                    newItemsCount++
                }
            } finally {
                // Clear timeout and ensure we abort the stream when done
                clearTimeout(timeoutId)
                abortController.abort()
            }

            // Update cache with new data
            qc.setQueryData(queryKey, acc)

            // Update offset and check if there's more data
            setCurrentOffset(nextOffset)
            if (newItemsCount < pageSize) {
                setHasMore(false)
            }
        } catch (err) {
            console.error('Error fetching next page:', err)
        } finally {
            setIsFetchingMore(false)
        }
    }, [enableInfiniteScroll, hasMore, isFetchingMore, query.data, currentOffset, pageSize, streamFn, getItemId, getItemTimestamp, to, isLiveMode, qc, queryKey])

    return {
        ...query,
        fetchNextPage,
        hasMore,
        isFetchingMore,
    }
}

