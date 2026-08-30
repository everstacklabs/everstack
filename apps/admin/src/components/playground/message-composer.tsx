import { Plus, X } from 'lucide-react'
import { cn } from '@everstack/utils/functions/cn'
import {
    usePlaygroundStore,
    type ComposerMessage,
    type PlaygroundRole,
} from '@/stores/playground-store'
import { ui } from '@everstack/ui'

const { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } = ui

const ROLES: PlaygroundRole[] = ['system', 'user', 'assistant']

const roleStyles: Record<PlaygroundRole, string> = {
    system: 'text-amber-300/90 border-amber-400/30',
    user: 'text-brand-secondary-300 border-brand-secondary-500/35',
    assistant: 'text-emerald-300/90 border-emerald-400/30',
}

const rolePlaceholders: Record<PlaygroundRole, string> = {
    system: 'Set the behavior of the model (optional)',
    user: 'What should the model do? Use {{input}} to insert each row.',
    assistant: 'Prior assistant turn (for multi-turn context)',
}

function MessageRow({ variantId, message }: { variantId: string; message: ComposerMessage }) {
    const setMessageText = usePlaygroundStore((s) => s.setMessageText)
    const setMessageRole = usePlaygroundStore((s) => s.setMessageRole)
    const removeMessage = usePlaygroundStore((s) => s.removeMessage)

    return (
        <div className="group border-b border-brand-main-800/80 py-3 last:border-b-0 focus-within:border-brand-secondary-500/50 transition-colors">
            <div className="mb-2 flex items-center justify-between">
                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <button
                            type="button"
                            className={cn(
                                'rounded border px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide transition-colors hover:border-brand-secondary-500/50',
                                roleStyles[message.role],
                            )}
                        >
                            {message.role}
                        </button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent
                        align="start"
                        className="bg-brand-main-900 border-brand-main-500 min-w-[8rem]"
                    >
                        {ROLES.map((role) => (
                            <DropdownMenuItem
                                key={role}
                                className="text-xs text-white/80 capitalize light:text-black/80"
                                onSelect={() => setMessageRole(variantId, message.id, role)}
                            >
                                {role}
                            </DropdownMenuItem>
                        ))}
                    </DropdownMenuContent>
                </DropdownMenu>
                <button
                    type="button"
                    onClick={() => removeMessage(variantId, message.id)}
                    className="p-0.5 rounded text-white/0 group-hover:text-white/40 hover:!text-rose-400 transition-colors light:text-black/0 light:group-hover:text-black/40"
                    title="Remove message"
                >
                    <X className="h-3 w-3" />
                </button>
            </div>
            <textarea
                value={message.text}
                onChange={(e) => setMessageText(variantId, message.id, e.target.value)}
                placeholder={rolePlaceholders[message.role]}
                rows={message.role === 'system' ? 3 : 4}
                className="w-full resize-y bg-transparent text-sm leading-relaxed text-zinc-100 outline-none placeholder:text-white/25 light:text-brand-main-50 light:placeholder:text-black/25"
            />
        </div>
    )
}

/**
 * Editable conversation for a single task column: a list of role-tagged
 * messages owned by that variant. Lives in the persisted playground store.
 */
export function MessageComposer({ variantId }: { variantId: string }) {
    const messages = usePlaygroundStore(
        (s) => s.variants.find((v) => v.id === variantId)?.messages ?? EMPTY_MESSAGES,
    )
    const addMessage = usePlaygroundStore((s) => s.addMessage)

    return (
        <div className="flex flex-col gap-2">
            {messages.map((m) => (
                <MessageRow key={m.id} variantId={variantId} message={m} />
            ))}
            <button
                type="button"
                onClick={() => addMessage(variantId)}
                className="inline-flex items-center gap-1 self-start py-1 text-xs text-white/50 transition-colors hover:text-white light:text-black/50 light:hover:text-brand-main-50"
            >
                <Plus className="h-3 w-3" /> Message
            </button>
        </div>
    )
}

const EMPTY_MESSAGES: ComposerMessage[] = []
