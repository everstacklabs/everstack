import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
    getRuntimeConfig,
    getRuntimeConfigSection,
    updateRuntimeConfigSection,
    updateRuntimeConfigSectionYAML,
    resetRuntimeConfigSection,
    type ConfigSectionName,
} from '@/server/gateway-config'

// Query keys for runtime config
export const runtimeConfigKeys = {
    all: ['runtime-config'] as const,
    full: () => [...runtimeConfigKeys.all, 'full'] as const,
    section: (section: ConfigSectionName) => [...runtimeConfigKeys.all, 'section', section] as const,
}

/**
 * Hook to fetch the full runtime configuration
 */
export function useRuntimeConfig() {
    return useQuery({
        queryKey: runtimeConfigKeys.full(),
        queryFn: () => getRuntimeConfig(),
        staleTime: 30_000, // 30 seconds
    })
}

/**
 * Hook to fetch a specific configuration section
 */
export function useRuntimeConfigSection(section: ConfigSectionName) {
    return useQuery({
        queryKey: runtimeConfigKeys.section(section),
        queryFn: () => getRuntimeConfigSection(section),
        staleTime: 30_000,
    })
}

/**
 * Hook to update a configuration section with structured data
 */
export function useUpdateRuntimeConfigSection() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({
            section,
            config,
        }: {
            section: ConfigSectionName
            config: Record<string, unknown>
        }) => updateRuntimeConfigSection(section, config),
        onSuccess: (_, { section }) => {
            // Invalidate the specific section and full config
            queryClient.invalidateQueries({ queryKey: runtimeConfigKeys.section(section) })
            queryClient.invalidateQueries({ queryKey: runtimeConfigKeys.full() })
        },
    })
}

/**
 * Hook to update a configuration section with YAML content
 */
export function useUpdateRuntimeConfigSectionYAML() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({
            section,
            yamlContent,
        }: {
            section: ConfigSectionName
            yamlContent: string
        }) => updateRuntimeConfigSectionYAML(section, yamlContent),
        onSuccess: (_, { section }) => {
            queryClient.invalidateQueries({ queryKey: runtimeConfigKeys.section(section) })
            queryClient.invalidateQueries({ queryKey: runtimeConfigKeys.full() })
        },
    })
}

/**
 * Hook to reset a configuration section to defaults
 */
export function useResetRuntimeConfigSection() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: (section: ConfigSectionName) => resetRuntimeConfigSection(section),
        onSuccess: (_, section) => {
            queryClient.invalidateQueries({ queryKey: runtimeConfigKeys.section(section) })
            queryClient.invalidateQueries({ queryKey: runtimeConfigKeys.full() })
        },
    })
}
