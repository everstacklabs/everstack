import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import { Button, toast } from '@everstack/ui/components'
import { GitCompare, Plus } from 'lucide-react'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { useCreatePrompt } from '@/hooks/evaluations/use-prompts'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

const {
    Input,
    Label,
    Sheet,
    SheetBody,
    SheetContent,
    SheetDescription,
    SheetHeader,
    SheetTitle,
    Textarea,
} = ui

function ComparePromptsButton() {
    const gate = useFeatureGate(FeatureKey.PROMPT_MANAGEMENT)
    const navigate = useNavigate()
    if (gate.isBlocked) return null
    return (
        <Button
            variant="outline"
            onClick={() => void navigate({ to: '/evaluations/prompts-library/compare' })}
        >
            <GitCompare className="h-3.5 w-3.5 mr-1.5" />
            Compare
        </Button>
    )
}

function CreatePromptButton() {
    const gate = useFeatureGate(FeatureKey.PROMPT_MANAGEMENT)
    const createMutation = useCreatePrompt()
    const [open, setOpen] = useState(false)
    const [name, setName] = useState('')
    const [description, setDescription] = useState('')
    const [tags, setTags] = useState('')

    if (gate.isBlocked) return null

    const handleCreate = async (e: React.FormEvent) => {
        e.preventDefault()
        try {
            await createMutation.mutateAsync({
                name: name.trim(),
                description: description || undefined,
                tags: tags.split(',').map((t) => t.trim()).filter(Boolean),
            })
            setName('')
            setDescription('')
            setTags('')
            setOpen(false)
            toast.success('Prompt created successfully')
        } catch (err) {
            toast.error((err as Error)?.message ?? 'Failed to create prompt')
        }
    }

    return (
        <>
            <Button variant="default" onClick={() => setOpen(true)}>
                <Plus className="h-3.5 w-3.5 mr-1.5" />
                Create Prompt
            </Button>
            <Sheet open={open} onOpenChange={setOpen}>
                <SheetContent side="right" className="min-w-[400px]">
                    <SheetHeader>
                        <SheetTitle>Create Prompt</SheetTitle>
                        <SheetDescription className="text-white/60 light:text-black/60 mt-1 text-xs">
                            Name the prompt now; add content as version 1 from the prompt
                            page or by saving from the playground.
                        </SheetDescription>
                    </SheetHeader>
                    <SheetBody>
                        <form onSubmit={handleCreate} className="space-y-4">
                            <div className="space-y-2">
                                <Label htmlFor="topbar-prompt-name" className="text-white light:text-brand-main-50 font-medium">
                                    Name
                                </Label>
                                <Input
                                    id="topbar-prompt-name"
                                    placeholder="support-triage"
                                    value={name}
                                    onChange={(e) => setName(e.target.value)}
                                    required
                                    className="bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50 h-8 text-sm"
                                />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor="topbar-prompt-description" className="text-white light:text-brand-main-50 font-medium">
                                    Description
                                </Label>
                                <Textarea
                                    id="topbar-prompt-description"
                                    placeholder="What is this prompt for?"
                                    value={description}
                                    onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
                                        setDescription(e.target.value)
                                    }
                                    rows={3}
                                    className="bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50 text-sm"
                                />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor="topbar-prompt-tags" className="text-white light:text-brand-main-50 font-medium">
                                    Tags
                                </Label>
                                <Input
                                    id="topbar-prompt-tags"
                                    placeholder="support, triage (comma separated)"
                                    value={tags}
                                    onChange={(e) => setTags(e.target.value)}
                                    className="bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50 h-8 text-sm"
                                />
                            </div>
                            <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
                                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                                    Cancel
                                </Button>
                                <Button type="submit" disabled={createMutation.isPending}>
                                    {createMutation.isPending ? 'Creating...' : 'Create Prompt'}
                                </Button>
                            </div>
                        </form>
                    </SheetBody>
                </SheetContent>
            </Sheet>
        </>
    )
}

export const EvaluationsPromptsLibraryActions: ActionGroup[] = [
    {
        title: 'Prompts',
        actions: [
            {
                type: 'custom',
                key: 'compare-prompts',
                label: 'Compare',
                component: ComparePromptsButton,
            },
            {
                type: 'custom',
                key: 'create-prompt',
                label: 'Create Prompt',
                component: CreatePromptButton,
            },
        ],
    },
]
