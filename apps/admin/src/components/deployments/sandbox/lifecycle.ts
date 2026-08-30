import type { SandboxInstance } from '@/server/sandbox'

// Internal lifecycle_state values (DB vocabulary). 'sleeping' is the
// reconciler's stopped state; legacy rows used 'stopped' and the
// backfill migration folds them together, but both are handled here so
// the UI is correct mid-rollout.
const STOPPED_STATES = new Set(['stopped', 'sleeping'])
const ERROR_STATES = new Set(['error', 'failed'])

export function sandboxLifecycle(inst: SandboxInstance): string {
  return (
    inst.lifecycleState?.trim() ||
    (inst.status === 'running' ? 'running' : inst.status)
  )
}

export function isSandboxRunning(inst: SandboxInstance): boolean {
  const lifecycle = inst.lifecycleState?.trim()
  return lifecycle ? lifecycle === 'running' : inst.status === 'running'
}

export function isSandboxStopped(inst: SandboxInstance): boolean {
    return STOPPED_STATES.has(sandboxLifecycle(inst))
}

export function isSandboxError(inst: SandboxInstance): boolean {
    return ERROR_STATES.has(sandboxLifecycle(inst))
}

export function isSandboxIntermediate(inst: SandboxInstance): boolean {
    // Union of the reconciler vocabulary and the labels the control
    // plane surfaces ('restoring'/'deleting' arrived with the
    // control-plane hardening pass).
    return ['pending', 'provisioning', 'creating', 'stopping', 'reviving', 'restoring', 'archiving', 'terminating', 'deleting'].includes(sandboxLifecycle(inst))
}

// publicState maps the internal lifecycle vocabulary onto the
// Daytona-style labels users see. The DB keeps its names; the UI (and
// the public API) present these.
const PUBLIC_STATE: Record<string, string> = {
    pending: 'creating',
    provisioning: 'creating',
    creating: 'creating',
    running: 'started',
    stopping: 'stopping',
    stopped: 'stopped',
    sleeping: 'stopped',
    reviving: 'starting',
    restoring: 'starting',
    archiving: 'archiving',
    archived: 'archived',
    terminating: 'destroying',
    deleting: 'destroying',
    terminated: 'destroyed',
    failed: 'error',
    error: 'error',
}

export function sandboxStatusLabel(inst: SandboxInstance): string {
    // Prefer the server-derived public label when present (newer
    // gateways); fall back to the local mapping for older responses.
    if (inst.state?.trim()) {
        return inst.state.trim()
    }
    const internal = sandboxLifecycle(inst)
    return PUBLIC_STATE[internal] ?? internal
}
