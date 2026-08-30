export type InterfaceDensity = 'comfortable' | 'compact'

export const INTERFACE_DENSITY_STORAGE_KEY = 'everstack-density'
export const INTERFACE_DENSITY_CHANGED_EVENT = 'everstack-density-change'
export const DEFAULT_INTERFACE_DENSITY: InterfaceDensity = 'compact'

export function isInterfaceDensity(value: unknown): value is InterfaceDensity {
  return value === 'comfortable' || value === 'compact'
}

export function applyInterfaceDensity(density: InterfaceDensity) {
  if (typeof document === 'undefined') return
  document.documentElement.setAttribute('data-density', density)
}

export function readStoredInterfaceDensity(): InterfaceDensity | null {
  if (typeof window === 'undefined') return null
  try {
    const stored = window.localStorage.getItem(INTERFACE_DENSITY_STORAGE_KEY)
    return isInterfaceDensity(stored) ? stored : null
  } catch {
    return null
  }
}

export function persistInterfaceDensity(density: InterfaceDensity) {
  applyInterfaceDensity(density)
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(INTERFACE_DENSITY_STORAGE_KEY, density)
    window.dispatchEvent(
      new CustomEvent(INTERFACE_DENSITY_CHANGED_EVENT, { detail: density }),
    )
  } catch {
    // Ignore storage failures; the active document still receives the density.
  }
}
