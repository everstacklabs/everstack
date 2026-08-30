import { Icon } from '@iconify/react'
import { Input, toast } from '@everstack/ui/components'
import { Check, ui } from '@everstack/ui'

import { Route } from '@/routes/deployments/studio/$workflowId'
import { useStudioStore } from '@/stores/studio-store'
import { useExecutionStore } from '@/stores/execution-store'
import { usePublishWorkflow, useUnpublishWorkflow } from '@/hooks/deployments/use-workflows'
import { useAutosave } from '@/hooks/deployments/use-autosave'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'
import { useState } from 'react'
import { VersionHistorySheet } from './version-history-sheet'
import { VariablesSheet } from './variables-sheet'

const { Badge, DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } = ui

export function StudioToolbar() {
    const navigate = Route.useNavigate()
    const name = useStudioStore((s) => s.name)
    const setName = useStudioStore((s) => s.setName)
    const enabled = useStudioStore((s) => s.enabled)
    const workflowId = useStudioStore((s) => s.workflowId)
    const publishedSnapshot = useStudioStore((s) => s.publishedSnapshot)
    const cancelDraftChanges = useStudioStore((s) => s.cancelDraftChanges)
    const past = useStudioStore((s) => s.past)
    const future = useStudioStore((s) => s.future)
    const undo = useStudioStore((s) => s.undo)
    const redo = useStudioStore((s) => s.redo)
    const [isCopied, setIsCopied] = useState(false)
    const [isVersionHistoryOpen, setIsVersionHistoryOpen] = useState(false)
    const isVariablesPanelOpen = useStudioStore((s) => s.isVariablesPanelOpen)
    const setVariablesPanelOpen = useStudioStore((s) => s.setVariablesPanelOpen)

    const publishWorkflow = usePublishWorkflow()
    const unpublishWorkflow = useUnpublishWorkflow()
    const { status: autosaveStatus, flush } = useAutosave()

    const handlePublish = async () => {
        if (!workflowId) return
        try {
            // Flush any pending draft saves before publishing
            flush()
            const res = await publishWorkflow.mutateAsync(workflowId)
            if (res.workflow) {
                const state = useStudioStore.getState()
                useStudioStore.getState().setWorkflowData({
                    id: res.workflow.id,
                    name: res.workflow.name || state.name,
                    description: res.workflow.description || state.description,
                    nodes: state.nodes,
                    edges: state.edges,
                    viewport: state.viewport,
                    enabled: res.workflow.enabled ?? true,
                    version: res.workflow.version ?? state.version,
                })
                // setWorkflowData already clears publishedSnapshot
            }
        } catch (error) {
            console.error('Failed to publish workflow:', error)
        }
    }

    const handleMoveToDraft = async () => {
        if (!workflowId) return
        try {
            const res = await unpublishWorkflow.mutateAsync(workflowId)
            if (res.workflow) {
                const state = useStudioStore.getState()
                useStudioStore.getState().setWorkflowData({
                    id: res.workflow.id,
                    name: res.workflow.name || state.name,
                    description: res.workflow.description || state.description,
                    nodes: state.nodes,
                    edges: state.edges,
                    viewport: state.viewport,
                    enabled: res.workflow.enabled ?? false,
                    version: res.workflow.version ?? state.version,
                })
            }
        } catch (error) {
            console.error('Failed to move workflow to draft:', error)
        }
    }

    const handleCopy = async (text: string) => {
        await copyToClipboard(text)
        toast.success(`Workflow link copied to clipboard`)
        setIsCopied(true)
        setTimeout(() => setIsCopied(false), 2000)
    }

    return (
        <div className="flex items-center gap-3 border-b border-brand-main-700 bg-brand-main-950 px-4 py-2 w-full h-auto">
            {/* Back to list */}
            <button
                onClick={() => navigate({ to: '/deployments/studio' })}
                className="rounded p-1.5 text-brand-main-300 border border-transparent hover:bg-brand-main-800 hover:border-brand-main-600 hover:text-white light:hover:text-brand-main-50 transition-colors"
                title="Back to workflows"
            >
                <Icon icon="lucide:arrow-left" className="h-4 w-4" />
            </button>
            <div className="h-5 w-px bg-brand-main-700" />

            {/* Workflow name */}
            <Input
                value={name}
                onChange={(e) => {
                    setName(e.target.value)
                    flush()
                }}
                className="py-1 w-56 bg-brand-main-800 border-brand-main-600 rounded text-white light:text-brand-main-50 text-sm"
                placeholder="Workflow name..."
            />

            {workflowId && (
                <>
                    <Badge variant='secondary' className='text-xs bg-brand-main-800 border border-brand-main-600 rounded py-1.5' onClick={() => handleCopy(`${window.location.origin}/deployments/studio/${workflowId}` || '')}>
                        <span className='text-white/90 light:text-black/90 text-xs'>{workflowId}</span>
                    </Badge>
                    {isCopied && (
                        <div>
                            <Check className='size-3 text-green-500' />
                        </div>
                    )}
                </>
            )}

            <div className="flex-1" />

            {/* Autosave status indicator */}
            <div className="flex items-center gap-1.5 text-xs">
                {autosaveStatus === 'saving' && (
                    <span className="flex items-center gap-1 text-brand-main-400">
                        <Icon icon="lucide:loader-2" className="h-3.5 w-3.5 animate-spin" />
                        Saving...
                    </span>
                )}
                {autosaveStatus === 'saved' && (
                    <span className="flex items-center gap-1 text-emerald-400 light:text-emerald-600">
                        <Icon icon="lucide:check" className="h-3.5 w-3.5" />
                        Saved
                    </span>
                )}
                {autosaveStatus === 'error' && (
                    <span className="flex items-center gap-1 text-red-400 light:text-red-600">
                        <Icon icon="lucide:alert-circle" className="h-3.5 w-3.5" />
                        Save failed
                    </span>
                )}

                <button
                    onClick={() => setIsVersionHistoryOpen(true)}
                    disabled={!workflowId}
                    className="rounded p-1.5 text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                    title="Version history"
                >
                    <Icon icon="fa7-solid:clock-rotate-left" className="h-4 w-4" />
                </button>
            </div>

            <div className="h-5 w-px bg-brand-main-700" />

            {/* Undo / Redo */}
            <div className="flex items-center gap-1">
                <button
                    onClick={undo}
                    disabled={past.length === 0}
                    className="rounded p-1.5 text-brand-main-300 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                    title="Undo (Ctrl+Z)"
                >
                    <Icon icon="lucide:undo-2" className="h-4 w-4" />
                </button>
                <button
                    onClick={redo}
                    disabled={future.length === 0}
                    className="rounded p-1.5 text-brand-main-300 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                    title="Redo (Ctrl+Shift+Z)"
                >
                    <Icon icon="lucide:redo-2" className="h-4 w-4" />
                </button>
            </div>

            {/* Separator */}
            <div className="h-5 w-px bg-brand-main-700" />

            {/* Settings dropdown */}
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <button
                        className="rounded p-1.5 text-brand-main-300 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 focus:outline-none focus-visible:ring-0 focus-visible:ring-transparent transition-colors"
                        title="Settings"
                    >
                        <Icon icon="lucide:settings" className="h-4 w-4" />
                    </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="bg-brand-main-800 border-brand-main-600 text-brand-main-100">
                    <DropdownMenuItem
                        onClick={() => setVariablesPanelOpen(true)}
                        className="flex items-center gap-2 cursor-pointer"
                    >
                        <Icon icon="lucide:braces" className="h-4 w-4" />
                        Variables
                    </DropdownMenuItem>
                </DropdownMenuContent>
            </DropdownMenu>

            <div className="h-5 w-px bg-brand-main-700" />

            {/* Test button */}
            <button
                onClick={() => useExecutionStore.getState().openTestPanel()}
                disabled={!workflowId}
                className="flex items-center gap-1.5 rounded border border-brand-secondary-600 bg-brand-secondary-600/10 px-3 py-1.5 text-sm font-medium text-brand-secondary-300 hover:bg-brand-secondary-600/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
                <Icon icon="lucide:play" className="h-4 w-4" />
                Test
            </button>

            {/* Publish / Cancel / Move to Draft */}
            {workflowId && publishedSnapshot !== null && (
                <>
                    <button
                        onClick={cancelDraftChanges}
                        disabled={publishWorkflow.isPending || unpublishWorkflow.isPending}
                        className="flex items-center gap-1.5 rounded border border-brand-main-600 bg-brand-main-800 px-3 py-1.5 text-sm font-medium text-brand-main-200 hover:bg-brand-main-700 disabled:opacity-50 transition-colors"
                    >
                        <Icon icon="lucide:x" className="h-4 w-4" />
                        Cancel
                    </button>
                    <button
                        onClick={handlePublish}
                        disabled={publishWorkflow.isPending || unpublishWorkflow.isPending}
                        className="flex items-center gap-1.5 rounded bg-brand-secondary-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-brand-secondary-500 disabled:opacity-50 transition-colors"
                    >
                        {publishWorkflow.isPending ? (
                            <Icon icon="lucide:loader-2" className="h-4 w-4 animate-spin" />
                        ) : (
                            <Icon icon="lucide:rocket" className="h-4 w-4" />
                        )}
                        Publish
                    </button>
                </>
            )}
            {workflowId && publishedSnapshot === null && enabled && (
                <button
                    onClick={handleMoveToDraft}
                    disabled={publishWorkflow.isPending || unpublishWorkflow.isPending}
                    className="flex items-center gap-1.5 rounded border border-amber-500/50 bg-amber-500/10 px-3 py-1.5 text-sm font-medium text-amber-300 light:text-amber-700 hover:bg-amber-500/20 disabled:opacity-50 transition-colors"
                >
                    {unpublishWorkflow.isPending ? (
                        <Icon icon="lucide:loader-2" className="h-4 w-4 animate-spin" />
                    ) : (
                        <Icon icon="lucide:archive" className="h-4 w-4" />
                    )}
                    Move to Draft
                </button>
            )}
            {workflowId && publishedSnapshot === null && !enabled && (
                <button
                    onClick={handlePublish}
                    disabled={publishWorkflow.isPending || unpublishWorkflow.isPending}
                    className="flex items-center gap-1.5 rounded bg-brand-secondary-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-brand-secondary-500 disabled:opacity-50 transition-colors"
                >
                    {publishWorkflow.isPending ? (
                        <Icon icon="lucide:loader-2" className="h-4 w-4 animate-spin" />
                    ) : (
                        <Icon icon="lucide:rocket" className="h-4 w-4" />
                    )}
                    Publish
                </button>
            )}

            <VersionHistorySheet
                open={isVersionHistoryOpen}
                onOpenChange={setIsVersionHistoryOpen}
            />
            <VariablesSheet open={isVariablesPanelOpen} onOpenChange={setVariablesPanelOpen} />
        </div>
    )
}
