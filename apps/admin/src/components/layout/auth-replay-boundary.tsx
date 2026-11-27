import type { ReactNode } from 'react'
import { isReplayReadableRoute, replayMask } from '@/lib/replay-privacy'

/**
 * Sign-in pages render outside the dashboard's <main>, so they get the same
 * default-deny replay mask here: readable only when the route is on the
 * replay allowlist (login and register, to see the handoff), masked
 * otherwise (invite and device pages carry tokens and codes).
 */
export function AuthReplayBoundary({ pathname, children }: { pathname: string; children: ReactNode }) {
  const mask = isReplayReadableRoute(pathname) ? {} : replayMask
  return (
    <div {...mask} className="contents">
      {children}
    </div>
  )
}
