import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { timestampDate } from '@everstack/client'
import {
  ModelTrialState as ProtoState,
  type GetModelTrialResponse,
} from '@everstack/proto/everstack/billing/v1/billing_pb'
import { getModelTrial } from '@/server/billing'
import { isCloudManaged } from '@/lib/cloud-mode'
import type { ModelTrial, ModelTrialState } from '@/lib/model-trial'

export const MODEL_TRIAL_QUERY_KEY = ['billing', 'model-trial'] as const

const STATES: Record<ProtoState, ModelTrialState | null> = {
  [ProtoState.UNSPECIFIED]: null,
  [ProtoState.ACTIVE]: 'active',
  [ProtoState.EXHAUSTED]: 'exhausted',
  [ProtoState.EXPIRED]: 'expired',
  [ProtoState.PAUSED]: 'paused',
  [ProtoState.NOT_ELIGIBLE]: 'not_eligible',
}

export function toModelTrial(res: GetModelTrialResponse): ModelTrial | null {
  const state = STATES[res.state] ?? null
  if (!state) return null
  return {
    state,
    eligible: res.eligible,
    granted: res.granted,
    used: res.used,
    remaining: res.remaining,
    model: res.model,
    maxOutputTokens: res.maxOutputTokens,
    expiresAt: res.expiresAt ? timestampDate(res.expiresAt) : null,
    resumesAt: res.resumesAt ? timestampDate(res.resumesAt) : null,
  }
}

/**
 * The organization's free model requests, or null when the feature is off,
 * the instance is not cloud-managed, or the billing read is unavailable. A
 * null answer makes the playground behave exactly as it does without a
 * trial, so nothing here can surface until the backend enables it.
 */
export function useModelTrial(): UseQueryResult<ModelTrial | null, Error> {
  return useQuery({
    queryKey: MODEL_TRIAL_QUERY_KEY,
    queryFn: async () => {
      try {
        return toModelTrial(await getModelTrial())
      } catch {
        return null
      }
    },
    enabled: isCloudManaged(),
    retry: false,
    staleTime: 30 * 1000,
    refetchOnWindowFocus: false,
  })
}
