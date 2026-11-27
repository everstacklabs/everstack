// Customer-facing BillingService reads from the instance console. Only cloud
// instances have a billing service behind them, and the gateway must proxy
// the procedure; callers treat any failure as "not available" and fall back.
//
// This transport deliberately skips the auth-redirect interceptor that
// createServerTransport adds: a missing proxy or a denied read must never
// bounce the whole console to the login page.
import {
  createClientFor,
  createConnectTransport,
  create,
} from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { getActiveOrgId } from '@/lib/active-org'
import { BillingService } from '@everstack/proto/everstack/billing/v1/billing_service_pb'
import {
  GetModelTrialRequestSchema,
  type GetModelTrialResponse,
} from '@everstack/proto/everstack/billing/v1/billing_pb'

const env = ((typeof import.meta !== 'undefined'
  ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
  : undefined) ?? {}) as Record<string, string | undefined>

const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createConnectTransport({
  baseUrl: `${getApiBaseUrl()}${connectBase}`,
  useBinaryFormat: true,
  fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
  interceptors: [
    // Same tenant pin as createServerTransport's activeOrgInterceptor.
    (next) => async (req) => {
      const orgId = getActiveOrgId()
      if (orgId) req.header.set('x-org-id', orgId)
      return next(req)
    },
  ],
})

const billingClient = createClientFor(BillingService)(transport)

/**
 * Reads the organization's free model requests ("model trial"). The org comes
 * from the session; the id is sent when known so multi-org users are pinned.
 */
export async function getModelTrial(): Promise<GetModelTrialResponse> {
  const req = create(GetModelTrialRequestSchema, {
    organizationId: getActiveOrgId() ?? '',
  })
  return billingClient.getModelTrial(req)
}
