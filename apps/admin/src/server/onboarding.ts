import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { OnboardingService } from '@everstack/proto/everstack/onboarding/v1/onboarding_service_pb'
import { UpdateOnboardingStateRequestSchema } from '@everstack/proto/everstack/onboarding/v1/onboarding_service_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

// Same-origin: no API key interceptor needed. The active-org header is stamped
// by createServerTransport so the server resolves the right tenant.
const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})
const onboardingClient = createClientFor(OnboardingService)(transport)

// The durable, tenant-scoped slice of launch-center state. Mirrors the proto
// OnboardingState minus the server-stamped updated_at, which the client never
// sends. Completion is NOT tracked here — it is derived live from providers /
// agents / keys, so a persisted flag would go stale.
export interface OnboardingServerState {
    dismissed: boolean
    celebrationShown: boolean
    selectedPath: string
    sandboxSkipped: boolean
}

export async function getOnboardingState(): Promise<OnboardingServerState> {
    const res = await onboardingClient.getOnboardingState({})
    const s = res.state
    return {
        dismissed: s?.dismissed ?? false,
        celebrationShown: s?.celebrationShown ?? false,
        selectedPath: s?.selectedPath ?? '',
        sandboxSkipped: s?.sandboxSkipped ?? false,
    }
}

export async function updateOnboardingState(
    state: OnboardingServerState,
): Promise<OnboardingServerState> {
    const req = create(UpdateOnboardingStateRequestSchema, {
        dismissed: state.dismissed,
        celebrationShown: state.celebrationShown,
        selectedPath: state.selectedPath,
        sandboxSkipped: state.sandboxSkipped,
    })
    const res = await onboardingClient.updateOnboardingState(req)
    const s = res.state
    return {
        dismissed: s?.dismissed ?? false,
        celebrationShown: s?.celebrationShown ?? false,
        selectedPath: s?.selectedPath ?? '',
        sandboxSkipped: s?.sandboxSkipped ?? false,
    }
}
