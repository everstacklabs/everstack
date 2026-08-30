import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import {
    LogsService,
    ListLogCustomColumnsRequestSchema,
    UpsertLogCustomColumnRequestSchema,
    DeleteLogCustomColumnRequestSchema,
    LogCustomColumnDefSchema,
} from '@everstack/proto/everstack/logs/v1/logs_service_pb'
import type {
    LogCustomColumnDef,
} from '@everstack/proto/everstack/logs/v1/logs_service_pb'
import type {
    ListLogsRequest,
    RequestLog,
} from '@everstack/proto/everstack/logs/v1/logs_pb'
import {
    ListLogsRequestSchema,
} from '@everstack/proto/everstack/logs/v1/logs_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})
const logsClient = createClientFor(LogsService)(transport)

export type ListLogsParams = {
    from?: string
    to?: string
    pageSize?: number
    offset?: number
}

// Stream logs from the server-streaming ListLogs RPC.
// Yields each RequestLog as it arrives across response chunks.
export async function* streamLogs(
    params: ListLogsParams = {},
    opts?: { signal?: AbortSignal },
): AsyncGenerator<RequestLog, void, unknown> {
    const { from, to, pageSize, offset } = params
    const req: ListLogsRequest = create(ListLogsRequestSchema, {
        from: from ?? '',
        to: to ?? '',
        pageSize: pageSize ?? 100,
        offset: offset ?? 0,
    })
    const stream = logsClient.listLogs(req, { signal: opts?.signal })
    for await (const msg of stream) {
        if (Array.isArray(msg.logs)) {
            for (const log of msg.logs) {
                yield log as RequestLog
            }
        }
    }
}

export async function onLogs(
    params: ListLogsParams,
    onLog: (log: RequestLog) => void,
    opts?: { signal?: AbortSignal },
): Promise<void> {
    for await (const log of streamLogs(params, opts)) {
        onLog(log)
    }
}

// ---------------------------------------------------------------------------
// Custom log columns (user-defined columns sourced from a LogAttributes key)
// ---------------------------------------------------------------------------

export type LogColumnInput = {
    key: string
    label: string
    attrKey: string
    position?: number
}

export async function listLogColumns(opts?: {
    signal?: AbortSignal
}): Promise<LogCustomColumnDef[]> {
    const req = create(ListLogCustomColumnsRequestSchema, {})
    const response = await logsClient.listLogCustomColumns(req, {
        signal: opts?.signal,
    })
    return response.columns
}

export async function upsertLogColumn(
    input: LogColumnInput,
): Promise<LogCustomColumnDef | undefined> {
    const req = create(UpsertLogCustomColumnRequestSchema, {
        column: create(LogCustomColumnDefSchema, {
            key: input.key,
            label: input.label,
            attrKey: input.attrKey,
            position: input.position ?? 0,
        }),
    })
    const response = await logsClient.upsertLogCustomColumn(req)
    return response.column
}

export async function deleteLogColumn(key: string): Promise<void> {
    const req = create(DeleteLogCustomColumnRequestSchema, { key })
    await logsClient.deleteLogCustomColumn(req)
}

