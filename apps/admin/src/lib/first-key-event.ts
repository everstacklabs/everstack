import { captureEvent } from './posthog'

/**
 * Reports `first_key_created`, the last step of Cloud's instance-create
 * funnel (apps/cloud/src/lib/events.ts names it), when the key just minted
 * is the instance's first. `keysBefore` is the list as loaded before the
 * create; undefined (not loaded yet) reports nothing rather than guessing.
 * No key, name or id rides along.
 */
export function reportIfFirstKey(
  keysBefore: readonly unknown[] | undefined,
  capture: (event: string, props: Record<string, unknown>) => void = captureEvent,
): boolean {
  if (!keysBefore || keysBefore.length > 0) return false
  try {
    capture('first_key_created', {})
  } catch {
    // Analytics must never break key creation.
  }
  return true
}
