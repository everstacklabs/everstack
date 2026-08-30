import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import { type ConfigSectionName } from '@/server/gateway-config'

interface SectionState {
    config: Record<string, unknown> | null
    yaml: string
    hasChanges: boolean
    isInitialized: boolean
}

interface GatewayConfigState {
    activeSection: ConfigSectionName
    sectionStates: Record<ConfigSectionName, SectionState>
    isSaving: boolean
    isResetting: boolean

    // Actions
    setActiveSection: (section: ConfigSectionName) => void
    setSectionConfig: (section: ConfigSectionName, config: Record<string, unknown>) => void
    setSectionYAML: (section: ConfigSectionName, yaml: string) => void
    setSectionHasChanges: (section: ConfigSectionName, hasChanges: boolean) => void
    setSectionInitialized: (section: ConfigSectionName, isInitialized: boolean) => void
    setIsSaving: (isSaving: boolean) => void
    setIsResetting: (isResetting: boolean) => void
    resetSectionState: (section: ConfigSectionName) => void
}

const defaultSectionState: SectionState = {
    config: null,
    yaml: '',
    hasChanges: false,
    isInitialized: false,
}

const defaultSectionStates: Record<ConfigSectionName, SectionState> = {
    rate_limit: { ...defaultSectionState },
    load_balancer: { ...defaultSectionState },
    features: { ...defaultSectionState },
    agents: { ...defaultSectionState },
    functions: { ...defaultSectionState },
    mcp_gateway: { ...defaultSectionState },
    sandbox: { ...defaultSectionState },
    cache: { ...defaultSectionState },
    fastpath: { ...defaultSectionState },
    telemetry: { ...defaultSectionState },
    cors: { ...defaultSectionState },
}

export const useGatewayConfigStore = create<GatewayConfigState>()(
    devtools(
        (set) => ({
            activeSection: 'rate_limit',
            sectionStates: defaultSectionStates,
            isSaving: false,
            isResetting: false,

            setActiveSection: (section) =>
                set({ activeSection: section }, false, 'setActiveSection'),

            setSectionConfig: (section, config) =>
                set(
                    (state) => ({
                        sectionStates: {
                            ...state.sectionStates,
                            [section]: { ...state.sectionStates[section], config },
                        },
                    }),
                    false,
                    'setSectionConfig'
                ),

            setSectionYAML: (section, yaml) =>
                set(
                    (state) => ({
                        sectionStates: {
                            ...state.sectionStates,
                            [section]: { ...state.sectionStates[section], yaml },
                        },
                    }),
                    false,
                    'setSectionYAML'
                ),

            setSectionHasChanges: (section, hasChanges) =>
                set(
                    (state) => ({
                        sectionStates: {
                            ...state.sectionStates,
                            [section]: { ...state.sectionStates[section], hasChanges },
                        },
                    }),
                    false,
                    'setSectionHasChanges'
                ),

            setSectionInitialized: (section, isInitialized) =>
                set(
                    (state) => ({
                        sectionStates: {
                            ...state.sectionStates,
                            [section]: { ...state.sectionStates[section], isInitialized },
                        },
                    }),
                    false,
                    'setSectionInitialized'
                ),

            setIsSaving: (isSaving) => set({ isSaving }, false, 'setIsSaving'),

            setIsResetting: (isResetting) => set({ isResetting }, false, 'setIsResetting'),

            resetSectionState: (section) =>
                set(
                    (state) => ({
                        sectionStates: {
                            ...state.sectionStates,
                            [section]: { ...defaultSectionState },
                        },
                    }),
                    false,
                    'resetSectionState'
                ),
        }),
        {
            name: 'gateway-config',
        }
    )
)

// Selectors
export const useActiveSection = () => useGatewayConfigStore((state) => state.activeSection)
export const useSectionStates = () => useGatewayConfigStore((state) => state.sectionStates)
export const useIsSaving = () => useGatewayConfigStore((state) => state.isSaving)
export const useIsResetting = () => useGatewayConfigStore((state) => state.isResetting)

export const useCurrentSectionState = () => {
    const activeSection = useGatewayConfigStore((state) => state.activeSection)
    const sectionStates = useGatewayConfigStore((state) => state.sectionStates)
    return sectionStates[activeSection]
}

export const useHasAnyChanges = () => {
    const sectionStates = useGatewayConfigStore((state) => state.sectionStates)
    return Object.values(sectionStates).some((s) => s.hasChanges)
}

export const useGatewayConfigActions = () => {
    const setActiveSection = useGatewayConfigStore((state) => state.setActiveSection)
    const setSectionConfig = useGatewayConfigStore((state) => state.setSectionConfig)
    const setSectionYAML = useGatewayConfigStore((state) => state.setSectionYAML)
    const setSectionHasChanges = useGatewayConfigStore((state) => state.setSectionHasChanges)
    const setSectionInitialized = useGatewayConfigStore((state) => state.setSectionInitialized)
    const setIsSaving = useGatewayConfigStore((state) => state.setIsSaving)
    const setIsResetting = useGatewayConfigStore((state) => state.setIsResetting)
    const resetSectionState = useGatewayConfigStore((state) => state.resetSectionState)

    return {
        setActiveSection,
        setSectionConfig,
        setSectionYAML,
        setSectionHasChanges,
        setSectionInitialized,
        setIsSaving,
        setIsResetting,
        resetSectionState,
    }
}
