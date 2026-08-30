import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { EventsService } from '@everstack/proto/everstack/events/v1/events_service_pb'
import type {
    ListEventsRequest,
    ListEventsResponse,
    GetEventRequest,
    GetEventResponse,
    GetEventPayloadRequest,
    GetEventPayloadResponse,
    Event,
} from '@everstack/proto/everstack/events/v1/events_service_pb'
import {
    ListEventsRequestSchema,
    ListEventsResponseSchema,
    GetEventRequestSchema,
    GetEventPayloadRequestSchema,
} from '@everstack/proto/everstack/events/v1/events_service_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''
const API_KEY = env.VITE_API_KEY as string | undefined

const apiKeyInterceptor = (next: (req: any) => Promise<any>) => async (req: any) => {
    if (API_KEY) {
        req.header.set('x-evs-api-key', API_KEY)
    }
    return next(req)
}

const transport = createServerTransport(env.VITE_API_KEY, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [apiKeyInterceptor],
})
const eventsClient = createClientFor(EventsService)(transport)

export type ListEventsParams = {
    type?: string
    apiKeyHash?: string
    from?: string
    to?: string
    pageSize?: number
    pageToken?: string
}

export async function listEvents(params: ListEventsParams = {}): Promise<ListEventsResponse> {
    const { type, apiKeyHash, from, to, pageSize, pageToken } = params
    const req: ListEventsRequest = create(ListEventsRequestSchema, {
        type: type ?? '',
        apiKeyHash: apiKeyHash ?? '',
        from: from ?? '',
        to: to ?? '',
        pageSize: pageSize ?? 50,
        pageToken: pageToken ?? '',
    })
    const stream = eventsClient.listEvents(req)
    const events: Event[] = []
    let nextPageToken = ''
    for await (const msg of stream) {
        if (Array.isArray(msg.events)) {
            events.push(...msg.events)
        }
        if (msg.nextPageToken) {
            nextPageToken = msg.nextPageToken
        }
    }
    return create(ListEventsResponseSchema, { events, nextPageToken })
}

// Stream events from the server-streaming ListEvents RPC.
// Yields each Event as it arrives across response chunks.
export async function* streamEvents(
    params: ListEventsParams = {},
    opts?: { signal?: AbortSignal },
): AsyncGenerator<Event, void, unknown> {
    const { type, apiKeyHash, from, to, pageSize, pageToken } = params
    const req: ListEventsRequest = create(ListEventsRequestSchema, {
        type: type ?? '',
        apiKeyHash: apiKeyHash ?? '',
        from: from ?? '',
        to: to ?? '',
        pageSize: pageSize ?? 50,
        pageToken: pageToken ?? '',
    })
    const stream = eventsClient.listEvents(req, { signal: opts?.signal })
    for await (const msg of stream) {
        if (Array.isArray(msg.events)) {
            for (const ev of msg.events) {
                yield ev as Event
            }
        }
    }
}

export async function onEvents(
    params: ListEventsParams,
    onEvent: (event: Event) => void,
    opts?: { signal?: AbortSignal },
): Promise<void> {
    for await (const ev of streamEvents(params, opts)) {
        onEvent(ev)
    }
}

export async function getEvent(id: string): Promise<GetEventResponse> {
    const req: GetEventRequest = create(GetEventRequestSchema, { id })
    return eventsClient.getEvent(req)
}

export async function getEventPayload(id: string): Promise<GetEventPayloadResponse> {
    const req: GetEventPayloadRequest = create(GetEventPayloadRequestSchema, { id })
    return eventsClient.getEventPayload(req)
}
