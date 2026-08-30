import { type ActionGroup } from '@/components/layout/topbar/types'
import { Button } from '@everstack/ui/components'
import { useNavigate } from '@tanstack/react-router'

export const DeploymentsStudioActions: ActionGroup[] = [
    {
        title: 'Studio',
        actions: [],
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'create-workflow',
                requiredPermission: 'resource:create',
                label: 'Create Workflow',
                component: CreateWorkflowButton,
            },
        ],
    },
]

export const DeploymentsStudioNewActions: ActionGroup[] = [
    {
        title: 'Studio',
        actions: [],
    },
]

export const DeploymentsStudioEditorActions: ActionGroup[] = [
    {
        actions: [
            {
                type: 'custom',
                key: 'studio-editor-title',
                label: 'Workflow',
                component: StudioEditorTitle,
            },
        ],
    },
]

function CreateWorkflowButton() {
    const navigate = useNavigate()
    return (
        <Button
            variant="default"
            onClick={() => navigate({ to: '/deployments/studio/new' })}
        >
            Create workflow
        </Button>
    )
}

function StudioEditorTitle() {
    // We import the store lazily to avoid circular dependency issues
    const { useStudioStore } = require('@/stores/studio-store')
    const name = useStudioStore((s: any) => s.name)
    const workflowId = useStudioStore((s: any) => s.workflowId)

    const handleCopy = () => {
        if (workflowId) {
            navigator.clipboard.writeText(`${window.location.origin}/deployments/studio/${workflowId}`)
        }
    }

    return (
        <div className="flex items-center gap-2 ml-2">
            <span className="text-sm font-semibold text-white light:text-brand-main-50">{name || 'Untitled Workflow'}</span>
            {workflowId && (
                <Button
                    size="xs"
                    onClick={handleCopy}
                    title="Copy workflow link"
                    className="items-center rounded bg-brand-main-600/50 border border-brand-main-500 text-[10px] font-mono text-brand-main-200 hover:bg-brand-main-500/50 hover:text-white light:hover:text-brand-main-50 transition-colors"
                >
                    {workflowId.slice(0, 8)}
                </Button>
            )}
        </div>
    )
}
