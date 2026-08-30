import { useState } from 'react'
import { ui } from '@everstack/ui'
import { Button, toast } from '@everstack/ui/components'
import {
    useCreatePrompt,
    useCreatePromptVersion,
    usePrompts,
} from '@/hooks/evaluations/use-prompts'
import { getPromptVersion, versionConfig, type PromptMessageInput } from '@/server/prompts'
import {
    usePlaygroundStore,
    type ComposerMessage,
    type PlaygroundRole,
    type PlaygroundVariant,
} from '@/stores/playground-store'

const EMPTY_SAVE_MESSAGES: ComposerMessage[] = []

const {
    Badge,
    Input,
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Sheet,
    SheetBody,
    SheetContent,
    SheetDescription,
    SheetHeader,
    SheetTitle,
} = ui

const NEW_PROMPT = '__new__'

type DialogProps = {
    open: boolean
    onOpenChange: (open: boolean) => void
}

/** Pick a prompt from the library and load its latest version into the composer. */
export function LoadPromptDialog({ open, onOpenChange }: DialogProps) {
    const { data: prompts, isLoading } = usePrompts()
    const loadConversation = usePlaygroundStore((s) => s.loadConversation)
    const [loadingId, setLoadingId] = useState<string | null>(null)

    const load = async (promptId: string, name: string) => {
        setLoadingId(promptId)
        try {
            const response = await getPromptVersion({ promptId })
            const version = response.version
            if (!version || version.messages.length === 0) {
                toast.error('This prompt has no versions yet')
                return
            }
            const config = versionConfig(version)
            loadConversation({
                messages: version.messages.map((m) => ({
                    role: (m.role as PlaygroundRole) || 'user',
                    text: m.content,
                })),
                model: config.model,
                temperature: config.temperature,
            })
            toast.success(`Loaded ${name} v${version.version}`)
            onOpenChange(false)
        } catch (err) {
            toast.error((err as Error)?.message ?? 'Failed to load prompt')
        } finally {
            setLoadingId(null)
        }
    }

    const list = prompts?.filter((p) => p.versionCount > 0) ?? []

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="min-w-[400px]">
                <SheetHeader>
                    <SheetTitle>Load Prompt</SheetTitle>
                    <SheetDescription className="text-white/60 mt-1 text-xs light:text-black/60">
                        Replace the composer with the latest version of a saved prompt.
                    </SheetDescription>
                </SheetHeader>
                <SheetBody>
                    {isLoading ? (
                        <p className="text-xs text-white/40 light:text-black/40">Loading prompts…</p>
                    ) : list.length === 0 ? (
                        <p className="text-xs text-white/50 light:text-black/50">
                            No prompts with content yet. Save the current conversation to
                            create one.
                        </p>
                    ) : (
                        <div className="space-y-1.5">
                            {list.map((p) => (
                                <button
                                    key={p.id}
                                    type="button"
                                    disabled={loadingId !== null}
                                    onClick={() => void load(p.id, p.name)}
                                    className="w-full text-left rounded border border-brand-main-600 bg-brand-main-800/40 px-3 py-2 hover:border-brand-secondary-500/50 transition-colors disabled:opacity-50"
                                >
                                    <div className="flex items-center gap-2">
                                        <span className="text-xs font-medium text-brand-secondary-100">
                                            {p.name}
                                        </span>
                                        <span className="text-[10px] font-mono text-white/40 light:text-black/40">
                                            v{p.latestVersion}
                                        </span>
                                        {Object.keys(p.labels ?? {}).map((label) => (
                                            <Badge
                                                key={label}
                                                variant="outline"
                                                className="text-[10px] border-emerald-400/30 text-emerald-300"
                                            >
                                                {label}
                                            </Badge>
                                        ))}
                                    </div>
                                    {p.description && (
                                        <div className="text-[11px] text-white/40 truncate mt-0.5 light:text-black/40">
                                            {p.description}
                                        </div>
                                    )}
                                </button>
                            ))}
                        </div>
                    )}
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}

type SavePromptDialogProps = DialogProps & {
    /** Variant whose model/params are captured as the version's config. */
    configVariant?: PlaygroundVariant
}

/** Save the composer as a new prompt, or as a new version of an existing one. */
export function SavePromptDialog({ open, onOpenChange, configVariant }: SavePromptDialogProps) {
    const { data: prompts } = usePrompts()
    // Save the target task's prompt (its own messages); fall back to the base task.
    const messages = usePlaygroundStore(
        (s) =>
            (configVariant
                ? s.variants.find((v) => v.id === configVariant.id)?.messages
                : s.variants[0]?.messages) ?? EMPTY_SAVE_MESSAGES,
    )
    const createPromptMutation = useCreatePrompt()
    const createVersionMutation = useCreatePromptVersion()

    const [target, setTarget] = useState(NEW_PROMPT)
    const [name, setName] = useState('')
    const [commitMessage, setCommitMessage] = useState('')
    const pending = createPromptMutation.isPending || createVersionMutation.isPending

    const submit = async (e: React.FormEvent) => {
        e.preventDefault()
        const content: PromptMessageInput[] = messages
            .filter((m) => m.text.trim())
            .map((m) => ({ role: m.role, content: m.text }))
        if (content.length === 0) {
            toast.error('The conversation is empty')
            return
        }
        const config = configVariant
            ? {
                  model: configVariant.model || undefined,
                  temperature: configVariant.temperature,
                  topP: configVariant.topP,
                  maxTokens: configVariant.maxTokens,
              }
            : undefined
        try {
            if (target === NEW_PROMPT) {
                if (!name.trim()) {
                    toast.error('Give the prompt a name')
                    return
                }
                await createPromptMutation.mutateAsync({
                    name: name.trim(),
                    messages: content,
                    config,
                    commitMessage: commitMessage || undefined,
                })
                toast.success(`Prompt "${name.trim()}" created`)
            } else {
                const prompt = prompts?.find((p) => p.id === target)
                await createVersionMutation.mutateAsync({
                    promptId: target,
                    messages: content,
                    config,
                    commitMessage: commitMessage || undefined,
                })
                toast.success(
                    `Saved as ${prompt?.name ?? 'prompt'} v${(prompt?.latestVersion ?? 0) + 1}`,
                )
            }
            setName('')
            setCommitMessage('')
            onOpenChange(false)
        } catch (err) {
            toast.error((err as Error)?.message ?? 'Failed to save prompt')
        }
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="min-w-[400px]">
                <SheetHeader>
                    <SheetTitle>Save to Prompt Library</SheetTitle>
                    <SheetDescription className="text-white/60 mt-1 text-xs light:text-black/60">
                        Snapshot the conversation{configVariant?.model ? ` and ${configVariant.model}` : ''}{' '}
                        as a versioned prompt.
                    </SheetDescription>
                </SheetHeader>
                <SheetBody>
                    <form onSubmit={submit} className="space-y-4">
                        <div className="space-y-2">
                            <Label className="text-white font-medium light:text-brand-main-50">Save as</Label>
                            <Select value={target} onValueChange={setTarget}>
                                <SelectTrigger className="h-8 bg-brand-main-900 border-brand-main-600 text-sm">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="bg-brand-main-900 border-brand-main-500">
                                    <SelectItem value={NEW_PROMPT} className="text-xs text-white/80 light:text-black/80">
                                        New prompt…
                                    </SelectItem>
                                    {(prompts ?? []).map((p) => (
                                        <SelectItem key={p.id} value={p.id} className="text-xs text-white/80 light:text-black/80">
                                            {p.name} (new version v{p.latestVersion + 1})
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        {target === NEW_PROMPT && (
                            <div className="space-y-2">
                                <Label htmlFor="save-prompt-name" className="text-white font-medium light:text-brand-main-50">
                                    Name
                                </Label>
                                <Input
                                    id="save-prompt-name"
                                    placeholder="support-triage"
                                    value={name}
                                    onChange={(e) => setName(e.target.value)}
                                    className="bg-brand-main-900 border-brand-main-600 text-white h-8 text-sm light:text-brand-main-50"
                                />
                            </div>
                        )}
                        <div className="space-y-2">
                            <Label htmlFor="save-commit-message" className="text-white font-medium light:text-brand-main-50">
                                Commit message
                            </Label>
                            <Input
                                id="save-commit-message"
                                placeholder="What changed?"
                                value={commitMessage}
                                onChange={(e) => setCommitMessage(e.target.value)}
                                className="bg-brand-main-900 border-brand-main-600 text-white h-8 text-sm light:text-brand-main-50"
                            />
                        </div>
                        <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
                            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                                Cancel
                            </Button>
                            <Button type="submit" disabled={pending}>
                                {pending ?'Saving...' : 'Save'}
                            </Button>
                        </div>
                    </form>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}
