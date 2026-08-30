import { createContext, useContext } from 'react'

interface VersionDiffContextValue {
    activeVersion: number | null
    nodeIds: Set<string>
    edgeIds: Set<string>
}

const defaultValue: VersionDiffContextValue = {
    activeVersion: null,
    nodeIds: new Set(),
    edgeIds: new Set(),
}

export const VersionDiffContext = createContext<VersionDiffContextValue>(defaultValue)

export function useVersionDiffContext(): VersionDiffContextValue {
    return useContext(VersionDiffContext)
}
