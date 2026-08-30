import type { Span } from "@everstack/proto/everstack/traces/v1/traces_pb"
import { getAttr } from "./traces-common"

// Get provider name from span
export function getProviderName(span: Span): string | undefined {
    // Try provider attribute first
    const provider = getAttr(span, 'provider') || getAttr(span, 'model.provider')
    if (provider) return provider

    // Extract from span name for provider spans
    if (span.spanName.startsWith('provider.')) {
        return span.spanName.split('.')[1]
    }

    return undefined
}
