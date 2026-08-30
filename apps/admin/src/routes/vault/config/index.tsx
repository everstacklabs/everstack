import { createFileRoute } from '@tanstack/react-router'
import { useState, useEffect } from 'react'
import { Button, Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { toast } from '@everstack/ui/components'
import { getConfigYAML, saveConfigYAML } from '@/server/providers'
import { useQueryClient } from '@tanstack/react-query'
import { providerKeys } from '@/hooks/vault/use-providers'

export const Route = createFileRoute('/vault/config/')({
    component: ConfigTab,
})

function ConfigTab() {
    const [yamlContent, setYamlContent] = useState('')
    const [hasChanges, setHasChanges] = useState(false)
    const [isLoading, setIsLoading] = useState(true)
    const [isSaving, setIsSaving] = useState(false)
    const [originalContent, setOriginalContent] = useState('')
    const queryClient = useQueryClient()

    const loadConfig = async () => {
        try {
            setIsLoading(true)
            const response = await getConfigYAML()
            const content = response.yamlContent
            setYamlContent(content)
            setOriginalContent(content)
            setHasChanges(false)
        } catch (error) {
            toast.error(`Failed to load config: ${error instanceof Error ? error.message : 'Unknown error'}`)
        } finally {
            setIsLoading(false)
        }
    }

    const saveConfig = async () => {
        try {
            setIsSaving(true)
            await saveConfigYAML(yamlContent)
            setOriginalContent(yamlContent)
            setHasChanges(false)

            // Invalidate provider queries to refresh data
            queryClient.invalidateQueries({ queryKey: providerKeys.all })

            toast.success('Config saved to YAML and database')
        } catch (error) {
            toast.error(`Failed to save config: ${error instanceof Error ? error.message : 'Unknown error'}`)
        } finally {
            setIsSaving(false)
        }
    }

    const handleContentChange = (newContent: string) => {
        setYamlContent(newContent)
        setHasChanges(newContent !== originalContent)
    }

    const handleReset = () => {
        setYamlContent(originalContent)
        setHasChanges(false)
    }

    useEffect(() => {
        loadConfig()
    }, [])

    if (isLoading) {
        return (
            <div className='flex flex-col w-full px-2'>
                <div className='flex-1 flex items-center justify-center'>
                    <Loader loaderText='Loading config...' />
                </div>
            </div>
        )
    }

    return (
        <div className='flex flex-col w-full px-2'>
            <div className='space-y-6'>
                {/* Header */}
                <div className='flex items-center justify-between'>
                    <div>
                        <h1 className='text-2xl font-bold text-white light:text-brand-main-50'>Configuration</h1>
                        <p className='text-white/60 light:text-black/60 mt-1'>
                            Edit your gateway configuration as YAML code
                        </p>
                    </div>
                    <div className='flex items-center gap-3'>
                        {hasChanges && (
                            <Button
                                variant='outline'
                                size='sm'
                                onClick={handleReset}
                                disabled={isSaving}
                            >
                                <Iconify.Icon icon='mdi:refresh' className='h-4 w-4 mr-2' />
                                Reset
                            </Button>
                        )}
                        <Button
                            onClick={saveConfig}
                            disabled={!hasChanges || isSaving}
                            size='sm'
                        >
                            {isSaving ? (
                                <>
                                    <Iconify.Icon icon='mdi:loading' className='h-4 w-4 mr-2 animate-spin' />
                                    Saving...
                                </>
                            ) : (
                                <>
                                    <Iconify.Icon icon='mdi:content-save' className='h-4 w-4 mr-2' />
                                    Save Config
                                </>
                            )}
                        </Button>
                    </div>
                </div>

                {/* Warning */}
                <div className='bg-blue-500/10 border border-blue-500/20 rounded-lg p-4'>
                    <div className='flex items-start gap-3'>
                        <Iconify.Icon icon='mdi:information' className='h-5 w-5 text-blue-400 light:text-blue-600 mt-0.5' />
                        <div className='flex-1'>
                            <h3 className='text-sm font-medium text-blue-400 light:text-blue-600'>Configuration Editor</h3>
                            <p className='text-sm text-blue-300/80 light:text-blue-600/80 mt-1'>
                                Changes made here will be saved to both the YAML file and the database.
                                Make sure to validate your YAML syntax before saving.
                            </p>
                        </div>
                    </div>
                </div>

                {/* Code Editor */}
                <div className='bg-gray-900/50 light:bg-gray-100/50 border border-gray-700 light:border-gray-300 rounded-lg overflow-hidden'>
                    <div className='bg-gray-800/50 light:bg-gray-200/50 px-4 py-2 border-b border-gray-700 light:border-gray-300'>
                        <div className='flex items-center gap-2'>
                            <Iconify.Icon icon='mdi:file-code' className='h-4 w-4 text-gray-400 light:text-gray-600' />
                            <span className='text-sm text-gray-300 light:text-gray-700'>gateway.yaml</span>
                            {hasChanges && (
                                <span className='text-xs text-yellow-400 light:text-yellow-700 bg-yellow-400/10 px-2 py-1 rounded'>
                                    Modified
                                </span>
                            )}
                        </div>
                    </div>
                    <div className='p-0'>
                        <textarea
                            value={yamlContent}
                            onChange={(e) => handleContentChange(e.target.value)}
                            className='w-full h-96 bg-transparent text-gray-100 light:text-gray-900 font-mono text-sm p-4 resize-none focus:outline-none'
                            placeholder='Loading configuration...'
                            spellCheck={false}
                        />
                    </div>
                </div>

                {/* Footer Info */}
                <div className='text-xs text-gray-400 light:text-gray-600 text-center'>
                    <p>
                        This editor allows you to modify the gateway configuration directly.
                        Changes are saved to both the YAML file and synchronized with the database.
                    </p>
                </div>
            </div>
        </div>
    )
}
