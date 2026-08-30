import { Button, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import {
    useGatewayConfigStore,
    useSectionStates,
    useIsSaving,
    useIsResetting,
    useHasAnyChanges,
} from '@/stores/gateway-config-store'
import { CONFIG_SECTION_LABELS, type ConfigSectionName } from '@/server/gateway-config'

// Maps UI section names to their backend API section names.
const SECTION_API_MAP: Partial<Record<ConfigSectionName, ConfigSectionName>> = {
    agents: 'features',
    functions: 'features',
    mcp_gateway: 'features',
    sandbox: 'features',
    fastpath: 'features',
}
import {
    useUpdateRuntimeConfigSection,
    useResetRuntimeConfigSection,
} from '@/hooks/gateway/use-runtime-config'

export function GatewayConfigSaveActions() {
    const sectionStates = useSectionStates()
    const isSaving = useIsSaving()
    const isResetting = useIsResetting()
    const hasAnyChanges = useHasAnyChanges()

    const updateMutation = useUpdateRuntimeConfigSection()
    const resetMutation = useResetRuntimeConfigSection()

    const setIsSaving = useGatewayConfigStore((state) => state.setIsSaving)
    const setIsResetting = useGatewayConfigStore((state) => state.setIsResetting)
    const setSectionHasChanges = useGatewayConfigStore((state) => state.setSectionHasChanges)

    // Get sections that have changes
    const sectionsWithChanges = (Object.keys(sectionStates) as ConfigSectionName[]).filter(
        (section) => sectionStates[section].hasChanges
    )

    const handleSaveAll = async () => {
        if (sectionsWithChanges.length === 0) return

        setIsSaving(true)
        const errors: string[] = []
        const saved: string[] = []

        try {
            // Save all sections with changes
            for (const section of sectionsWithChanges) {
                const state = sectionStates[section]
                if (!state.config) continue

                try {
                    const apiSection = SECTION_API_MAP[section] ?? section
                    await updateMutation.mutateAsync({
                        section: apiSection,
                        config: state.config,
                    })
                    setSectionHasChanges(section, false)
                    saved.push(CONFIG_SECTION_LABELS[section])
                } catch (error) {
                    errors.push(CONFIG_SECTION_LABELS[section])
                }
            }

            if (saved.length > 0 && errors.length === 0) {
                toast.success(`All changes saved successfully`)
            } else if (saved.length > 0 && errors.length > 0) {
                toast.warning(`Saved ${saved.join(', ')} but failed to save ${errors.join(', ')}`)
            } else if (errors.length > 0) {
                toast.error(`Failed to save: ${errors.join(', ')}`)
            }
        } finally {
            setIsSaving(false)
        }
    }

    const handleResetAll = async () => {
        if (sectionsWithChanges.length === 0) return

        setIsResetting(true)
        const errors: string[] = []
        const reset: string[] = []

        try {
            // Reset all sections with changes
            for (const section of sectionsWithChanges) {
                try {
                    const apiSection = SECTION_API_MAP[section] ?? section
                    await resetMutation.mutateAsync(apiSection)
                    setSectionHasChanges(section, false)
                    reset.push(CONFIG_SECTION_LABELS[section])
                } catch (error) {
                    errors.push(CONFIG_SECTION_LABELS[section])
                }
            }

            if (reset.length > 0 && errors.length === 0) {
                toast.success(`All sections reset to defaults`)
            } else if (reset.length > 0 && errors.length > 0) {
                toast.warning(`Reset ${reset.join(', ')} but failed to reset ${errors.join(', ')}`)
            } else if (errors.length > 0) {
                toast.error(`Failed to reset: ${errors.join(', ')}`)
            }
        } finally {
            setIsResetting(false)
        }
    }

    return (
        <div className="flex items-center gap-2">
            {hasAnyChanges && (
                <span className="text-xs text-yellow-400 light:text-yellow-700 bg-yellow-400/10 px-2 py-1 rounded flex items-center gap-1">
                    <Iconify.Icon icon="mdi:circle" className="h-2 w-2" />
                    {sectionsWithChanges.length} modified
                </span>
            )}
            <Button
                variant="outline"
                onClick={handleResetAll}
                disabled={!hasAnyChanges || isResetting || isSaving}
            >
                {isResetting ? (
                    <Iconify.Icon icon="mdi:loading" className="h-4 w-4 animate-spin" />
                ) : (
                    <Iconify.Icon icon="mdi:refresh" className="h-4 w-4" />
                )}
                Reset
            </Button>
            <Button
                onClick={handleSaveAll}
                disabled={!hasAnyChanges || isSaving || isResetting}
            >
                {isSaving ? (
                    <Iconify.Icon icon="mdi:loading" className="h-4 w-4 animate-spin" />
                ) : (
                    null
                )}
                Save Config
            </Button>
        </div>
    )
}
