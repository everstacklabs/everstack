import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Check, Loader2 } from 'lucide-react'
import { Button } from '@everstack/ui/components'
import { AddKeyCard } from '@/components/onboarding/add-key-card'
import { NoModelProviderError } from '@/server/platform-agent'

const PROVIDERS_HREF = '/vault/llm-providers'
// A provider saved a moment ago can take a beat to become routable, so the
// assistant is re-resolved a few times before giving up.
const SETTLE_ATTEMPTS = 5
const SETTLE_GAP_MS = 1200

/**
 * The reason the assistant could not start, held while it is being resolved
 * again. A query with no data drops its error the moment a refetch begins,
 * which would unmount the setup card in the middle of its own retry; this
 * keeps the last error until the assistant exists.
 */
export function useStickyError(error: unknown, resolved: boolean): Error | null {
  const [last, setLast] = useState<Error | null>(null)
  if (resolved) {
    if (last !== null) setLast(null)
    return null
  }
  if (error instanceof Error && error !== last) setLast(error)
  return error instanceof Error ? error : last
}

// AssistantSetupCard takes the place of a send button that could only do
// nothing: it says why the assistant cannot answer yet and holds the fix.
// With no model provider connected (a new organization, or one that only
// used free requests) the fix is a key, added right here; any other failure
// shows its reason and a retry. Render it with a sticky error (see
// useStickyError) so it stays mounted while `retry` runs.
export function AssistantSetupCard({
  error,
  retry,
  settleGapMs = SETTLE_GAP_MS,
}: {
  error: Error
  // Re-resolves the assistant; resolves true once it exists.
  retry: () => Promise<boolean>
  // Pause between attempts to re-resolve the assistant.
  settleGapMs?: number
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [settling, setSettling] = useState(false)
  // A key was saved here. If the assistant is still not up after the
  // retries, the key form must not come back as if nothing was saved.
  const [keySaved, setKeySaved] = useState(false)

  const settle = async () => {
    setSettling(true)
    try {
      await queryClient.invalidateQueries({ queryKey: ['gateway-models'] })
      for (let attempt = 0; attempt < SETTLE_ATTEMPTS; attempt++) {
        if (await retry()) return
        if (attempt < SETTLE_ATTEMPTS - 1) {
          await new Promise((r) => setTimeout(r, settleGapMs))
        }
      }
    } finally {
      setSettling(false)
    }
  }

  if (settling) {
    return (
      <div
        role="status"
        className="flex items-center justify-center gap-2 rounded border border-brand-main-600 bg-brand-main-800/40 px-3 py-2 text-xs text-white/60 light:text-black/60"
      >
        <Loader2 className="size-3.5 animate-spin" />
        Starting the assistant
      </div>
    )
  }

  if (keySaved) {
    return (
      <div
        role="status"
        className="flex items-start gap-2 rounded border border-brand-main-600 bg-brand-main-800/40 px-3 py-2 text-xs"
      >
        <Check className="mt-0.5 size-3.5 shrink-0 text-success" />
        <div className="min-w-0 flex-1 space-y-1.5">
          <p className="font-medium text-white/80 light:text-black/80">
            Your key is saved. The assistant is still starting.
          </p>
          <p className="text-white/50 light:text-black/50">
            A new provider can take a few seconds to come online.
          </p>
          <Button size="xs" variant="outline" onClick={() => void settle()}>
            Try again
          </Button>
        </div>
      </div>
    )
  }

  if (error instanceof NoModelProviderError) {
    return (
      <AddKeyCard
        title="Add an OpenAI or Anthropic key to chat with the assistant."
        saveLabel="Add key"
        onSaved={() => {
          setKeySaved(true)
          void settle()
        }}
        onOtherProvider={() => navigate({ to: PROVIDERS_HREF })}
      />
    )
  }

  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded border border-warning-border bg-warning-surface/60 px-3 py-2 text-xs"
    >
      <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" />
      <div className="min-w-0 flex-1 space-y-1.5">
        <p className="font-medium text-warning">
          The assistant could not start.
        </p>
        <p className="break-words text-warning/70">{error.message}</p>
        <Button size="xs" variant="outline" onClick={() => void settle()}>
          Try again
        </Button>
      </div>
    </div>
  )
}
