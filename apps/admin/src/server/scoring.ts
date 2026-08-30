/**
 * Synchronous scoring driver for the playground grid. Calls the
 * EvalService.ScoreOutput RPC, which runs the same builtin/code/LLM-judge
 * scorers as the background eval runner but returns immediately, so a grid
 * cell can show its scores the moment its generation finishes.
 *
 * Tenant is resolved server-side from the authenticated context; the tenantId
 * passed here is ignored by the server (kept only to satisfy the wire shape).
 */

import { createServerTransport } from '@/server'
import { createClientFor, create, fromJson, ValueSchema } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { EvalService } from '@everstack/proto/everstack/datasets/v1/datasets_service_pb'
import {
    GenerateScorerRequestSchema,
    ScoreOutputRequestSchema,
} from '@everstack/proto/everstack/datasets/v1/datasets_pb'

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
const evalClient = createClientFor(EvalService)(transport)

/** A score name -> value map plus optional "<name>_reason" / "<name>_error" keys. */
export type ScoreMap = Record<string, unknown>

export type ScoreOutputParams = {
    /** The generation input (row input / prompt variables). */
    input?: unknown
    /** The generated output to score. */
    output: unknown
    /** Optional reference output for comparison scorers. */
    expectedOutput?: unknown
    /** Score configs to run (must belong to the caller's tenant). */
    scorerConfigIds: string[]
    metadata?: Record<string, unknown>
    signal?: AbortSignal
}

/**
 * Score a single output against the given scorer configs. Returns the scores
 * map, or an empty object if no scorers were requested.
 */
export async function scoreOutput(params: ScoreOutputParams): Promise<ScoreMap> {
    if (!params.scorerConfigIds.length) return {}

    const req = create(ScoreOutputRequestSchema, {
        input: params.input === undefined ? undefined : fromJson(ValueSchema, params.input as never),
        output: fromJson(ValueSchema, (params.output ?? null) as never),
        expectedOutput:
            params.expectedOutput === undefined
                ? undefined
                : fromJson(ValueSchema, params.expectedOutput as never),
        metadata: params.metadata as never,
        scorerConfigIds: params.scorerConfigIds,
    })

    const resp = await evalClient.scoreOutput(req, { signal: params.signal })
    return (resp.scores ?? {}) as ScoreMap
}

export type GenerateScorerParams = {
    /** Free-text description of what to evaluate. */
    intent: string
    /** "numeric" | "categorical" | "boolean" — defaults server-side to numeric. */
    dataType?: string
    signal?: AbortSignal
}

export type GenerateScorerResult = {
    suggestedName: string
    prompt: string
    dataType: string
    suggestedCategories: string[]
    notes: string
}

/**
 * Ask the scorer generator (EvalService.GenerateScorer) to draft an LLM-judge
 * prompt for the given intent. Used by the G-Eval criteria -> steps ramp.
 */
export async function generateScorer(params: GenerateScorerParams): Promise<GenerateScorerResult> {
    const req = create(GenerateScorerRequestSchema, {
        tenantId: '',
        intent: params.intent,
        dataType: params.dataType,
    })
    const resp = await evalClient.generateScorer(req, { signal: params.signal })
    return {
        suggestedName: resp.suggestedName,
        prompt: resp.prompt,
        dataType: resp.dataType,
        suggestedCategories: resp.suggestedCategories ?? [],
        notes: resp.notes,
    }
}
