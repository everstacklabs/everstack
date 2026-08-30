import { useState } from 'react'
import { Icon } from '@iconify/react'
import { ui } from '@everstack/ui'
import { useStudioStore } from '@/stores/studio-store'

const { Sheet, SheetContent, SheetHeader, SheetTitle, Input } = ui

interface VariablesSheetProps {
    open: boolean
    onOpenChange: (open: boolean) => void
}

function VariableRow({
    entryKey,
    value,
    onKeyChange,
    onValueChange,
    onRemove,
    onBlur,
}: {
    entryKey: string
    value: string
    onKeyChange: (newKey: string) => void
    onValueChange: (newValue: string) => void
    onRemove: () => void
    onBlur?: () => void
}) {
    return (
        <div className="flex items-center gap-2">
            <Input
                value={entryKey}
                onChange={(e) => onKeyChange(e.target.value)}
                onBlur={onBlur}
                placeholder="Key"
                className="flex-1 py-1.5 bg-brand-main-800 border-brand-main-600 rounded text-white light:text-brand-main-50 text-sm"
            />
            <Input
                value={value}
                onChange={(e) => onValueChange(e.target.value)}
                onBlur={onBlur}
                placeholder="Value"
                className="flex-1 py-1.5 bg-brand-main-800 border-brand-main-600 rounded text-white light:text-brand-main-50 text-sm"
            />
            <button
                onClick={onRemove}
                className="rounded p-1 text-brand-main-400 hover:bg-brand-main-800 hover:text-red-400 light:hover:text-red-600 transition-colors flex-shrink-0"
                title="Remove variable"
            >
                <Icon icon="lucide:x" className="h-4 w-4" />
            </button>
        </div>
    )
}

export function VariablesSheet({ open, onOpenChange }: VariablesSheetProps) {
    const variables = useStudioStore((s) => s.variables)
    const sessionVariables = useStudioStore((s) => s.sessionVariables)
    const setVariable = useStudioStore((s) => s.setVariable)
    const removeVariable = useStudioStore((s) => s.removeVariable)
    const setSessionVariable = useStudioStore((s) => s.setSessionVariable)
    const removeSessionVariable = useStudioStore((s) => s.removeSessionVariable)
    const clearSessionVariables = useStudioStore((s) => s.clearSessionVariables)

    // Track draft rows that haven't been committed to the store yet
    const [draftWorkflowRows, setDraftWorkflowRows] = useState<{ key: string; value: string }[]>([])
    const [draftSessionRows, setDraftSessionRows] = useState<{ key: string; value: string }[]>([])

    const workflowEntries = Object.entries(variables)
    const sessionEntries = Object.entries(sessionVariables)

    const commitWorkflowDraft = (idx: number) => {
        const draft = draftWorkflowRows[idx]
        if (!draft) return
        if (draft.key.trim()) {
            setVariable(draft.key.trim(), draft.value)
            setDraftWorkflowRows((prev) => prev.filter((_, i) => i !== idx))
        }
    }

    const commitSessionDraft = (idx: number) => {
        const draft = draftSessionRows[idx]
        if (!draft) return
        if (draft.key.trim()) {
            setSessionVariable(draft.key.trim(), draft.value)
            setDraftSessionRows((prev) => prev.filter((_, i) => i !== idx))
        }
    }

    const handleAddWorkflowRow = () => {
        setDraftWorkflowRows((prev) => [...prev, { key: '', value: '' }])
    }

    const handleAddSessionRow = () => {
        setDraftSessionRows((prev) => [...prev, { key: '', value: '' }])
    }

    const handleWorkflowKeyChange = (oldKey: string, newKey: string) => {
        const val = variables[oldKey] ?? ''
        removeVariable(oldKey)
        if (newKey.trim()) {
            setVariable(newKey.trim(), val)
        }
    }

    const handleSessionKeyChange = (oldKey: string, newKey: string) => {
        const val = sessionVariables[oldKey] ?? ''
        removeSessionVariable(oldKey)
        if (newKey.trim()) {
            setSessionVariable(newKey.trim(), val)
        }
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent className="bg-brand-main-900 border-l-brand-main-500 w-full sm:max-w-sm flex flex-col">
                <SheetHeader className="flex items-center space-x-2">
                    <SheetTitle className="text-white light:text-brand-main-50 text-base font-semibold flex items-center gap-2">
                        <Icon icon="lucide:braces" className="h-4 w-4 text-brand-main-300" />
                        Variables
                    </SheetTitle>
                </SheetHeader>

                <div className="flex-1 overflow-y-auto px-4 py-3 space-y-6">
                    {/* Workflow Variables Section */}
                    <div>
                        <h3 className="text-sm font-medium text-brand-main-200 mb-3">
                            Workflow Variables
                        </h3>
                        <p className="text-xs text-brand-main-400 mb-3">
                            Persisted with the workflow and available across sessions.
                        </p>

                        {workflowEntries.length === 0 && draftWorkflowRows.length === 0 && (
                            <div className="text-xs text-brand-main-500 py-4 text-center border border-dashed border-brand-main-700 rounded">
                                No workflow variables defined
                            </div>
                        )}

                        <div className="space-y-2">
                            {workflowEntries.map(([key, value]) => (
                                <VariableRow
                                    key={key}
                                    entryKey={key}
                                    value={value}
                                    onKeyChange={(newKey) => handleWorkflowKeyChange(key, newKey)}
                                    onValueChange={(newValue) => setVariable(key, newValue)}
                                    onRemove={() => removeVariable(key)}
                                />
                            ))}

                            {draftWorkflowRows.map((draft, idx) => (
                                <VariableRow
                                    key={`draft-wf-${idx}`}
                                    entryKey={draft.key}
                                    value={draft.value}
                                    onKeyChange={(newKey) => {
                                        setDraftWorkflowRows((prev) =>
                                            prev.map((r, i) => (i === idx ? { ...r, key: newKey } : r))
                                        )
                                    }}
                                    onValueChange={(newValue) => {
                                        setDraftWorkflowRows((prev) =>
                                            prev.map((r, i) => (i === idx ? { ...r, value: newValue } : r))
                                        )
                                    }}
                                    onRemove={() => {
                                        setDraftWorkflowRows((prev) => prev.filter((_, i) => i !== idx))
                                    }}
                                    onBlur={() => commitWorkflowDraft(idx)}
                                />
                            ))}
                        </div>

                        <button
                            onClick={handleAddWorkflowRow}
                            className="mt-2 flex items-center gap-1.5 text-xs text-brand-main-400 hover:text-white light:hover:text-brand-main-50 transition-colors"
                        >
                            <Icon icon="lucide:plus" className="h-3.5 w-3.5" />
                            Add variable
                        </button>
                    </div>

                    {/* Divider */}
                    <div className="border-t border-brand-main-700" />

                    {/* Session Overrides Section */}
                    <div>
                        <div className="flex items-center justify-between mb-3">
                            <h3 className="text-sm font-medium text-brand-main-200">
                                Session Overrides
                            </h3>
                            {sessionEntries.length > 0 && (
                                <button
                                    onClick={clearSessionVariables}
                                    className="text-xs text-brand-main-400 hover:text-red-400 light:hover:text-red-600 transition-colors"
                                >
                                    Clear all
                                </button>
                            )}
                        </div>
                        <p className="text-xs text-brand-main-400 mb-3">
                            Temporary overrides for this session only. Cleared on reload.
                        </p>

                        {sessionEntries.length === 0 && draftSessionRows.length === 0 && (
                            <div className="text-xs text-brand-main-500 py-4 text-center border border-dashed border-brand-main-700 rounded">
                                No session overrides defined
                            </div>
                        )}

                        <div className="space-y-2">
                            {sessionEntries.map(([key, value]) => (
                                <VariableRow
                                    key={key}
                                    entryKey={key}
                                    value={value}
                                    onKeyChange={(newKey) => handleSessionKeyChange(key, newKey)}
                                    onValueChange={(newValue) => setSessionVariable(key, newValue)}
                                    onRemove={() => removeSessionVariable(key)}
                                />
                            ))}

                            {draftSessionRows.map((draft, idx) => (
                                <VariableRow
                                    key={`draft-sess-${idx}`}
                                    entryKey={draft.key}
                                    value={draft.value}
                                    onKeyChange={(newKey) => {
                                        setDraftSessionRows((prev) =>
                                            prev.map((r, i) => (i === idx ? { ...r, key: newKey } : r))
                                        )
                                    }}
                                    onValueChange={(newValue) => {
                                        setDraftSessionRows((prev) =>
                                            prev.map((r, i) => (i === idx ? { ...r, value: newValue } : r))
                                        )
                                    }}
                                    onRemove={() => {
                                        setDraftSessionRows((prev) => prev.filter((_, i) => i !== idx))
                                    }}
                                    onBlur={() => commitSessionDraft(idx)}
                                />
                            ))}
                        </div>

                        <button
                            onClick={handleAddSessionRow}
                            className="mt-2 flex items-center gap-1.5 text-xs text-brand-main-400 hover:text-white light:hover:text-brand-main-50 transition-colors"
                        >
                            <Icon icon="lucide:plus" className="h-3.5 w-3.5" />
                            Add override
                        </button>
                    </div>
                </div>
            </SheetContent>
        </Sheet>
    )
}
