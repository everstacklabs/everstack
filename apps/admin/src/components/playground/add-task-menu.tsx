import { ui } from '@everstack/ui'
import { Plus, MessageSquare, Workflow, Globe, Triangle } from 'lucide-react'
import { usePlaygroundStore, type TaskType } from '@/stores/playground-store'

const { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } = ui

const TYPES: {
    type: TaskType
    label: string
    hint: string
    icon: typeof Plus
    color: string
    disabled?: boolean
}[] = [
    { type: 'prompt', label: 'Prompt', hint: 'Messages + model', icon: MessageSquare, color: 'text-brand-secondary-300' },
    { type: 'workflow', label: 'Workflow', hint: 'Runs through workflow execution', icon: Workflow, color: 'text-amber-300' },
    { type: 'remote', label: 'Remote eval', hint: 'Backend proxy needed', icon: Globe, color: 'text-sky-300', disabled: true },
    { type: 'scorer', label: 'Scorer', hint: 'Column runner coming later', icon: Triangle, color: 'text-emerald-300', disabled: true },
]

/**
 * Adds a comparison task of any type. A task is any row→output transform the
 * grid can compare — not just a model variant — so the "+" opens a typed menu.
 */
export function AddTaskMenu() {
    const addTask = usePlaygroundStore((s) => s.addTask)
    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <button
                    type="button"
                    className="flex w-full items-center gap-2 border-t border-brand-main-800 px-3 py-2 text-xs text-white/45 transition-colors hover:text-white light:text-black/45 light:hover:text-brand-main-50"
                    title="Add a comparison task"
                >
                    <Plus className="h-4 w-4" />
                    <span>Add task</span>
                </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-60 bg-brand-main-900 border-brand-main-500">
                {TYPES.map(({ type, label, hint, icon: Icon, color, disabled }) => (
                    <DropdownMenuItem
                        key={type}
                        disabled={disabled}
                        onSelect={() => {
                            if (!disabled) addTask(type)
                        }}
                        className="flex items-start gap-2.5 py-2"
                    >
                        <Icon className={`h-4 w-4 mt-0.5 shrink-0 ${color}`} />
                        <span className="flex flex-col">
                            <span className="text-sm text-white/90 light:text-black/90">{label}</span>
                            <span className="text-[11px] text-white/40 light:text-black/40">{hint}</span>
                        </span>
                    </DropdownMenuItem>
                ))}
            </DropdownMenuContent>
        </DropdownMenu>
    )
}
