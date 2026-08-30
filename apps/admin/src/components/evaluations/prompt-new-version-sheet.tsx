import { useState } from 'react'
import { ui } from '@everstack/ui'
import { Button, toast } from '@everstack/ui/components'
import { Plus, X } from 'lucide-react'
import { useCreatePromptVersion } from '@/hooks/evaluations/use-prompts'
import {
  versionConfig,
  type PromptMessageInput,
  type PromptVersion,
} from '@/server/prompts'

const {
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
  Textarea,
} = ui

export type NewVersionSheetProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  promptId: string
  baseVersion: PromptVersion | null
  nextVersion: number
}

/** Author a new immutable prompt version, seeded from the selected version. */
export function NewVersionSheet({
  open,
  onOpenChange,
  promptId,
  baseVersion,
  nextVersion,
}: NewVersionSheetProps) {
  const createVersionMutation = useCreatePromptVersion()
  const [messages, setMessages] = useState<PromptMessageInput[] | null>(null)
  const [commitMessage, setCommitMessage] = useState('')

  // Seed the editor from the selected version when the sheet opens.
  const effective: PromptMessageInput[] = messages ??
    baseVersion?.messages.map((m) => ({
      role: (m.role as PromptMessageInput['role']) || 'user',
      content: m.content,
    })) ?? [
      { role: 'system', content: '' },
      { role: 'user', content: '' },
    ]

  const update = (i: number, patch: Partial<PromptMessageInput>) =>
    setMessages(effective.map((m, idx) => (idx === i ? { ...m, ...patch } : m)))
  const remove = (i: number) =>
    setMessages(effective.filter((_, idx) => idx !== i))
  const add = () => setMessages([...effective, { role: 'user', content: '' }])

  const reset = () => {
    setMessages(null)
    setCommitMessage('')
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const nonEmpty = effective.filter((m) => m.content.trim())
    if (nonEmpty.length === 0) {
      toast.error('Add at least one message')
      return
    }
    try {
      await createVersionMutation.mutateAsync({
        promptId,
        messages: nonEmpty,
        config: baseVersion ? versionConfig(baseVersion) : undefined,
        commitMessage: commitMessage || undefined,
      })
      toast.success(`Version ${nextVersion} created`)
      reset()
      onOpenChange(false)
    } catch (err) {
      toast.error((err as Error)?.message ?? 'Failed to create version')
    }
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(o) => {
        if (!o) reset()
        onOpenChange(o)
      }}
    >
      <SheetContent side="right" className="min-w-[480px]">
        <SheetHeader>
          <SheetTitle>New Version (v{nextVersion})</SheetTitle>
          <SheetDescription className="text-white/60 mt-1 text-xs light:text-black/60">
            {baseVersion
              ? `Starting from v${baseVersion.version}. Versions are immutable once created.`
              : 'Versions are immutable once created.'}
          </SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form onSubmit={submit} className="space-y-4">
            <div className="space-y-2">
              {effective.map((m, i) => (
                <div
                  key={i}
                  className="rounded border border-brand-main-600 bg-brand-main-800/40 p-2 space-y-1.5"
                >
                  <div className="flex items-center justify-between">
                    <Select
                      value={m.role}
                      onValueChange={(role) =>
                        update(i, { role: role as PromptMessageInput['role'] })
                      }
                    >
                      <SelectTrigger className="h-6 w-28 bg-brand-main-700/60 border-brand-main-500 text-[11px]">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent className="bg-brand-main-900 border-brand-main-500">
                        {(['system', 'user', 'assistant'] as const).map((r) => (
                          <SelectItem
                            key={r}
                            value={r}
                            className="text-xs text-white/80 light:text-black/80"
                          >
                            {r}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {effective.length > 1 && (
                      <button
                        type="button"
                        onClick={() => remove(i)}
                        className="p-0.5 rounded text-white/30 hover:text-rose-400 transition-colors light:text-black/30"
                        title="Remove message"
                      >
                        <X className="h-3 w-3" />
                      </button>
                    )}
                  </div>
                  <Textarea
                    value={m.content}
                    onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
                      update(i, { content: e.target.value })
                    }
                    rows={4}
                    className="bg-brand-main-900 border-brand-main-600 text-white text-xs font-mono light:text-brand-main-50"
                  />
                </div>
              ))}
              <Button type="button" variant="ghost" onClick={add}>
                <Plus className="h-3 w-3 mr-1" /> Message
              </Button>
            </div>
            <div className="space-y-2">
              <Label
                htmlFor="commit-message"
                className="text-white font-medium light:text-brand-main-50"
              >
                Commit message
              </Label>
              <Input
                id="commit-message"
                placeholder="What changed?"
                value={commitMessage}
                onChange={(e) => setCommitMessage(e.target.value)}
                className="bg-brand-main-900 border-brand-main-600 text-white h-8 text-sm light:text-brand-main-50"
              />
            </div>
            <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
              <Button
                type="button"
                variant="outline"
                onClick={() => onOpenChange(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={createVersionMutation.isPending}>
                {createVersionMutation.isPending
                  ? 'Creating...'
                  : 'Create Version'}
              </Button>
            </div>
          </form>
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}
