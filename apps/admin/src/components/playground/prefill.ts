import type { PlaygroundRole } from '@/stores/playground-store'

export type PrefillMessage = { role: PlaygroundRole; text: string }

const VALID_ROLES = new Set<PlaygroundRole>(['system', 'user', 'assistant'])

function partText(content: unknown): string {
    if (typeof content === 'string') return content
    if (Array.isArray(content)) {
        return content
            .map((p) => {
                if (typeof p === 'string') return p
                if (p && typeof p === 'object') {
                    const part = p as Record<string, unknown>
                    if (typeof part.text === 'string') return part.text
                    if (typeof part.content === 'string') return part.content
                    if (
                        part.data &&
                        typeof part.data === 'object' &&
                        typeof (part.data as Record<string, unknown>).value === 'string'
                    ) {
                        return (part.data as Record<string, unknown>).value as string
                    }
                }
                return ''
            })
            .join('')
    }
    return ''
}

/**
 * Decode a span's request-messages attribute (OpenAI-style JSON array, a
 * single message object, or plain text) into composer messages. Trace
 * attributes are stringified in several shapes depending on the SDK that
 * produced them, so this is deliberately forgiving.
 */
export function parseMessagesAttribute(raw: string): PrefillMessage[] {
    const trimmed = raw.trim()
    if (!trimmed) return []
    if (trimmed.startsWith('[') || trimmed.startsWith('{')) {
        try {
            const parsed: unknown = JSON.parse(trimmed)
            const list = Array.isArray(parsed) ? parsed : [parsed]
            const messages: PrefillMessage[] = []
            for (const entry of list) {
                if (!entry || typeof entry !== 'object') continue
                const msg = entry as Record<string, unknown>
                const role =
                    typeof msg.role === 'string' ? msg.role.toLowerCase() : 'user'
                const text = partText(msg.content ?? msg.text)
                if (!text) continue
                messages.push({
                    role: VALID_ROLES.has(role as PlaygroundRole)
                        ? (role as PlaygroundRole)
                        : 'user',
                    text,
                })
            }
            if (messages.length) return messages
        } catch {
            // fall through to plain text
        }
    }
    return [{ role: 'user', text: raw }]
}
