import { useState } from 'react'
import { Link, useLocation, useNavigate, useSearch } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { Play, GitCompare, Plus } from 'lucide-react'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { usePrompt, usePromptVersions } from '@/hooks/evaluations/use-prompts'
import { versionConfig, type PromptVersion } from '@/server/prompts'
import { usePlaygroundStore, type PlaygroundRole } from '@/stores/playground-store'
import { NewVersionSheet } from '@/components/evaluations/prompt-new-version-sheet'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

function usePromptIdFromPath(): string {
    const { pathname } = useLocation()
    const segments = pathname.split('/').filter(Boolean)
    // /evaluations/prompts-library/<id>
    const idx = segments.indexOf('prompts-library')
    return idx >= 0 && segments.length > idx + 1 ? segments[idx + 1] : ''
}

/** The version the detail page currently has selected (mirrored in ?v). */
function useSelectedVersion(versions?: PromptVersion[]): PromptVersion | null {
    const search = useSearch({ strict: false }) as { v?: number }
    if (!versions || versions.length === 0) return null
    return versions.find((x) => x.version === search.v) ?? versions[0]
}

function PromptBreadcrumb() {
    const promptId = usePromptIdFromPath()
    const { data: prompt, isLoading } = usePrompt(promptId)
    return (
        <div className="flex items-center gap-1.5">
            <Link
                to="/evaluations/prompts-library"
                className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors"
            >
                Prompts
            </Link>
            {promptId && (
                <>
                    <span className="text-brand-main-400 text-sm">/</span>
                    {isLoading ? (
                        <span className="inline-block h-4 w-24 rounded bg-white/10 light:bg-black/10 animate-pulse" />
                    ) : (
                        <span className="text-sm text-white light:text-brand-main-50 font-normal">
                            {prompt?.name ?? promptId.substring(0, 12) + '...'}
                        </span>
                    )}
                </>
            )}
        </div>
    )
}

function OpenInPlaygroundButton() {
    const gate = useFeatureGate(FeatureKey.PROMPT_MANAGEMENT)
    const navigate = useNavigate()
    const promptId = usePromptIdFromPath()
    const { data: prompt } = usePrompt(promptId)
    const { data: versions } = usePromptVersions(promptId)
    const current = useSelectedVersion(versions)
    const loadConversation = usePlaygroundStore((s) => s.loadConversation)

    if (gate.isBlocked || !current) return null

    const open = () => {
        const config = versionConfig(current)
        loadConversation({
            messages: current.messages.map((m) => ({
                role: (m.role as PlaygroundRole) || 'user',
                text: m.content,
            })),
            model: config.model,
            temperature: config.temperature,
        })
        void navigate({ to: '/evaluations/playground' })
        toast.success(`Loaded ${prompt?.name ?? 'prompt'} v${current.version} into the playground`)
    }

    return (
        <Button variant="outline" onClick={open}>
            <Play className="h-3.5 w-3.5 mr-1.5" /> Open in playground
        </Button>
    )
}

function CompareVersionsButton() {
    const gate = useFeatureGate(FeatureKey.PROMPT_MANAGEMENT)
    const navigate = useNavigate()
    const promptId = usePromptIdFromPath()
    const { data: versions } = usePromptVersions(promptId)
    const current = useSelectedVersion(versions)

    if (gate.isBlocked || !versions || versions.length === 0) return null

    const previous = current
        ? versions.find((v) => v.version < current.version) ?? null
        : null

    return (
        <Button
            variant="outline"
            onClick={() =>
                void navigate({
                    to: '/evaluations/prompts-library/compare',
                    search: {
                        lp: promptId,
                        lv: previous?.version,
                        rp: promptId,
                        rv: current?.version,
                    },
                })
            }
        >
            <GitCompare className="h-3.5 w-3.5 mr-1.5" /> Compare versions
        </Button>
    )
}

function NewVersionButton() {
    const gate = useFeatureGate(FeatureKey.PROMPT_MANAGEMENT)
    const promptId = usePromptIdFromPath()
    const { data: prompt } = usePrompt(promptId)
    const { data: versions } = usePromptVersions(promptId)
    const current = useSelectedVersion(versions)
    const [open, setOpen] = useState(false)

    if (gate.isBlocked) return null

    return (
        <>
            <Button variant="default" onClick={() => setOpen(true)}>
                <Plus className="h-3.5 w-3.5 mr-1.5" /> New version
            </Button>
            <NewVersionSheet
                open={open}
                onOpenChange={setOpen}
                promptId={promptId}
                baseVersion={current}
                nextVersion={(prompt?.latestVersion ?? 0) + 1}
            />
        </>
    )
}

export const EvaluationsPromptsLibraryDetailActions: ActionGroup[] = [
    {
        title: <PromptBreadcrumb />,
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'prompt-open-playground',
                label: 'Open in playground',
                component: OpenInPlaygroundButton,
            },
            {
                type: 'custom',
                key: 'prompt-compare-versions',
                label: 'Compare versions',
                component: CompareVersionsButton,
            },
            {
                type: 'custom',
                key: 'prompt-new-version',
                label: 'New version',
                component: NewVersionButton,
            },
        ],
    },
]
