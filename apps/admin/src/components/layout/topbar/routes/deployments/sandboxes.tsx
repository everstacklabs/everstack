import { type ActionGroup } from '@/components/layout/topbar/types'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { useQueryClient } from '@tanstack/react-query'
import {
    SANDBOX_INSTANCES_KEY,
    useSandboxInstances,
    useStopSandbox,
    useReviveSandbox,
    useTerminateSandbox,
    useRecoverSandbox,
    useArchiveSandbox,
} from '@/hooks/deployments/use-sandbox'
import {
    isSandboxRunning,
    isSandboxStopped,
    isSandboxError,
    sandboxStatusLabel,
} from '@/components/deployments/sandbox/lifecycle'

// Topbar actions for the sandboxes routes. All navigation/action
// buttons live here (not inside the page content), matching every
// other route in the app.

export const DeploymentsSandboxesNewActions: ActionGroup[] = [
    {
        title: (
            <span className="flex items-center gap-1.5">
                <Link
                    to="/deployments/sandboxes"
                    search={{ tab: 'instances' }}
                    className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors"
                >
                    Sandboxes
                </Link>
                <span className="text-brand-main-400 text-sm">/</span>
                <span className="text-sm text-white light:text-brand-main-50 font-normal">New</span>
            </span>
        ),
        actions: [],
    },
]

export const DeploymentsSandboxesActions: ActionGroup[] = [
    {
        title: 'Sandboxes',
        actions: [],
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'refresh-sandboxes',
                label: 'Refresh',
                component: RefreshSandboxesButton,
            },
            {
                type: 'custom',
                key: 'create-sandbox',
                requiredPermission: 'resource:create',
                label: 'Create Sandbox',
                component: CreateSandboxButton,
            },
        ],
    },
]

// Detail page (/deployments/sandboxes/{id}): breadcrumb + the single
// home of the sandbox's lifecycle actions.
export const DeploymentsSandboxesDetailActions: ActionGroup[] = [
    {
        title: <SandboxDetailBreadcrumb />,
        actions: [],
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'sandbox-lifecycle-actions',
                label: 'Lifecycle',
                component: SandboxLifecycleActions,
            },
        ],
    },
]

function RefreshSandboxesButton() {
    const queryClient = useQueryClient()
    return (
        <Button
            variant="outline"
            size="default"
            onClick={() => queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })}
        >
            <Iconify.Icon icon="heroicons:arrow-path" className="size-4" />
            Refresh
        </Button>
    )
}

function CreateSandboxButton() {
    const navigate = useNavigate()
    return (
        <Button
            variant="default"
            size="default"
            onClick={() => navigate({ to: '/deployments/sandboxes/new' })}
        >
            Create Sandbox
        </Button>
    )
}

function useDetailSandboxId(): string {
    // The detail route is /deployments/sandboxes/$sandboxId; read the
    // param without binding to a specific route id so this component
    // can mount from the topbar (outside the route subtree).
    const params = useParams({ strict: false }) as { sandboxId?: string }
    return params.sandboxId ?? ''
}

function SandboxDetailBreadcrumb() {
    const sandboxId = useDetailSandboxId()
    const { data } = useSandboxInstances()
    const inst = data?.instances.find((i) => i.id === sandboxId)
    return (
        <div className="flex items-center gap-1.5">
            <Link
                to="/deployments/sandboxes"
                search={{ tab: 'instances' }}
                className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors"
            >
                Sandboxes
            </Link>
            <span className="text-brand-main-400 text-sm">/</span>
            <span className="text-sm text-white light:text-brand-main-50 font-normal truncate max-w-[280px]">
                {inst?.name?.trim() || sandboxId}
            </span>
            {inst && (
                <span className="ml-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-brand-main-700/50 text-white/70 light:text-black/70 border border-brand-main-600">
                    {sandboxStatusLabel(inst)}
                </span>
            )}
        </div>
    )
}

function SandboxLifecycleActions() {
    const sandboxId = useDetailSandboxId()
    const navigate = useNavigate()
    const { data } = useSandboxInstances()
    const inst = data?.instances.find((i) => i.id === sandboxId)

    const stopMutation = useStopSandbox()
    const startMutation = useReviveSandbox()
    const archiveMutation = useArchiveSandbox()
    const recoverMutation = useRecoverSandbox()
    const terminateMutation = useTerminateSandbox()

    const pending =
        stopMutation.isPending ||
        startMutation.isPending ||
        archiveMutation.isPending ||
        recoverMutation.isPending ||
        terminateMutation.isPending

    if (!inst) return null

    const running = isSandboxRunning(inst)
    const stopped = isSandboxStopped(inst)
    const errored = isSandboxError(inst)
    const archived = inst.lifecycleState === 'archived'
    const destroyed =
        inst.lifecycleState === 'terminated' || inst.lifecycleState === 'deleted'

    return (
        <div className="flex items-center gap-1.5">
            {(stopped || archived) && (
                <Button
                    variant="outline"
                    size="default"
                    disabled={pending}
                    onClick={() =>
                        startMutation.mutate(sandboxId, {
                            onSuccess: () => toast.success('Start requested'),
                            onError: (e) => toast.error(`Start failed: ${e.message}`),
                        })
                    }
                >
                    <Iconify.Icon icon="heroicons:play" className="size-4" />
                    Start
                </Button>
            )}
            {running && (
                <Button
                    variant="outline"
                    size="default"
                    disabled={pending}
                    onClick={() =>
                        stopMutation.mutate(sandboxId, {
                            onSuccess: () => toast.success('Stop requested'),
                            onError: (e) => toast.error(`Stop failed: ${e.message}`),
                        })
                    }
                >
                    <Iconify.Icon icon="heroicons:pause" className="size-4" />
                    Stop
                </Button>
            )}
            {stopped && !archived && (
                <Button
                    variant="outline"
                    size="default"
                    disabled={pending}
                    onClick={() =>
                        archiveMutation.mutate(sandboxId, {
                            onSuccess: () => toast.success('Archive requested'),
                            onError: (e) => toast.error(`Archive failed: ${e.message}`),
                        })
                    }
                >
                    <Iconify.Icon icon="heroicons:archive-box" className="size-4" />
                    Archive
                </Button>
            )}
            {errored && (
                <Button
                    variant="outline"
                    size="default"
                    disabled={pending}
                    onClick={() =>
                        recoverMutation.mutate(sandboxId, {
                            onSuccess: () => toast.success('Recovery started'),
                            onError: (e) => toast.error(`Recover failed: ${e.message}`),
                        })
                    }
                >
                    <Iconify.Icon icon="heroicons:arrow-path" className="size-4" />
                    Recover
                </Button>
            )}
            {!destroyed && (
                <Button
                    variant="outline"
                    size="default"
                    disabled={pending}
                    className="text-white/60 light:text-black/60 hover:text-red-300 light:hover:text-red-600 hover:border-red-500/40"
                    onClick={() => {
                        if (
                            !window.confirm(
                                'Destroy this sandbox? The workspace and its data will be removed.',
                            )
                        )
                            return
                        terminateMutation.mutate(sandboxId, {
                            onSuccess: () => {
                                toast.success('Delete requested')
                                navigate({
                                    to: '/deployments/sandboxes',
                                    search: { tab: 'instances' },
                                })
                            },
                            onError: (e) => toast.error(`Delete failed: ${e.message}`),
                        })
                    }}
                >
                    <Iconify.Icon icon="heroicons:trash" className="size-4" />
                    Delete
                </Button>
            )}
        </div>
    )
}
