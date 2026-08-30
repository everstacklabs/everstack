import { useState, useCallback, useEffect } from 'react'
import { useUpdateAgent } from '@/hooks/deployments/use-agents'
import { toast } from '@everstack/ui/components'
import Editor, { type Monaco } from '@monaco-editor/react'
import { type editor } from 'monaco-editor'
import { ui } from '@everstack/ui'
import { AgentMarkdown } from './agent-markdown'

export const IDENTITY_FILES = [
    { key: 'soulMd', label: 'SOUL.md', placeholder: '# Soul\n\nDefine the core personality, values, and behavioral principles for this agent...' },
    { key: 'identityMd', label: 'IDENTITY.md', placeholder: '# Identity\n\nDescribe who this agent is, its background, and how it presents itself...' },
    { key: 'userMd', label: 'USER.md', placeholder: '# User\n\nSpecify user preferences, context, and interaction guidelines...' },
    { key: 'roleMd', label: 'ROLE.md', placeholder: '# Role\n\nDefine role-specific instructions, responsibilities, and constraints...' },
] as const

const materialOceanTheme: editor.IStandaloneThemeData = {
    base: 'vs-dark',
    inherit: true,
    rules: [
        { token: 'comment', foreground: '546E7A', fontStyle: 'italic' },
        { token: 'keyword', foreground: 'C792EA' },
        { token: 'string', foreground: 'C3E88D' },
        { token: 'number', foreground: 'F78C6C' },
        { token: 'type', foreground: 'FFCB6B' },
        { token: 'delimiter', foreground: '89DDFF' },
    ],
    colors: {
        'editor.background': '#0F111A',
        'editor.foreground': '#BABED8',
        'editor.lineHighlightBackground': '#1F2233',
        'editor.selectionBackground': '#3C435E',
        'editorCursor.foreground': '#FFCC00',
        'editorWhitespace.foreground': '#4B5263',
        'editorLineNumber.foreground': '#4B5263',
        'editorLineNumber.activeForeground': '#A6ACCD',
        'editorIndentGuide.background': '#4B526340',
        'editorIndentGuide.activeBackground': '#4B5263',
    },
}

export type IdentityKey = typeof IDENTITY_FILES[number]['key']
type IdentityState = Record<IdentityKey, string>

interface IdentityEditorProps {
    agentId: string
    identity?: { soulMd?: string; identityMd?: string; userMd?: string; roleMd?: string }
    activeFile: IdentityKey
    onFileChange: (file: IdentityKey) => void
}

export function IdentityEditor({ agentId, identity, activeFile, onFileChange }: IdentityEditorProps) {
    const { Tabs, TabsList, TabsTrigger } = ui
    const chartMode = ui.useChartMode()
    const updateMutation = useUpdateAgent()
    const [viewMode, setViewMode] = useState<'edit' | 'preview'>('edit')
    const [values, setValues] = useState<IdentityState>({
        soulMd: identity?.soulMd ?? '',
        identityMd: identity?.identityMd ?? '',
        userMd: identity?.userMd ?? '',
        roleMd: identity?.roleMd ?? '',
    })

    const handleEditorWillMount = useCallback((monaco: Monaco) => {
        monaco.editor.defineTheme('material-ocean', materialOceanTheme)
    }, [])

    const handleChange = useCallback((key: IdentityKey) => (newValue: string | undefined) => {
        if (newValue !== undefined) {
            setValues((prev) => ({ ...prev, [key]: newValue }))
        }
    }, [])

    const handleSave = useCallback(() => {
        if (updateMutation.isPending) return
        updateMutation.mutate(
            { id: agentId, identity: values },
            {
                onSuccess: () => toast.success('Identity files updated'),
                onError: (e) => toast.error(`Failed: ${e.message}`),
            },
        )
    }, [agentId, values, updateMutation])

    useEffect(() => {
        const onKeyDown = (event: KeyboardEvent) => {
            const isSaveShortcut = (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's'
            if (!isSaveShortcut) return
            event.preventDefault()
            handleSave()
        }

        window.addEventListener('keydown', onKeyDown)
        return () => window.removeEventListener('keydown', onKeyDown)
    }, [handleSave])

    const activeFileMeta = IDENTITY_FILES.find((f) => f.key === activeFile)
    const previewContent = values[activeFile].trim()
        ? values[activeFile]
        : activeFileMeta?.placeholder ?? ''

    return (
        <div className="h-full flex gap-0">
            <div className="flex w-36 shrink-0 bg-brand-main-800/50 border border-brand-main-600 rounded border-r-0">
                <div className="flex flex-col gap-0.5 p-1.5 flex-1">
                    {IDENTITY_FILES.map((file) => (
                        <button
                            key={file.key}
                            onClick={() => onFileChange(file.key)}
                            className={`text-left px-2.5 py-1.5 rounded text-xs font-mono transition-colors ${activeFile === file.key ? 'bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500/30' : 'text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 border border-transparent'}`}
                        >
                            {file.label}
                        </button>
                    ))}
                </div>
            </div>
            <div className="flex-1 min-h-0 min-w-0 rounded-r-lg overflow-hidden border border-brand-main-600 flex flex-col bg-brand-main-950/40">
                <div className="shrink-0 border-b border-brand-main-600 px-2 py-1.5 flex items-center justify-between bg-brand-main-900/65">
                    <span className='pl-2 text-xs font-semibold text-brand-brand-300'>
                        {activeFileMeta?.label}
                    </span>
                    <Tabs value={viewMode} onValueChange={(value) => setViewMode(value as 'edit' | 'preview')}>
                        <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-0.5 h-auto gap-1">
                            <TabsTrigger className="text-xs px-1.5 py-0.5 relative flex items-center data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors" value="edit">Edit</TabsTrigger>
                            <TabsTrigger className="text-xs px-1.5 py-0.5 relative flex items-center data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors" value="preview">Preview</TabsTrigger>
                        </TabsList>
                    </Tabs>
                    <span className="text-[10px] text-white/40 light:text-black/40 font-mono pr-1">Cmd/Ctrl+S</span>
                </div>
                <div className="flex-1 min-h-0">
                    {viewMode === 'edit' ? (
                        <Editor
                            key={activeFile}
                            defaultLanguage="markdown"
                            language="markdown"
                            value={values[activeFile]}
                            onChange={handleChange(activeFile)}
                            beforeMount={handleEditorWillMount}
                            theme={chartMode === 'light' ? 'vs' : 'material-ocean'}
                            options={{
                                automaticLayout: true,
                                minimap: { enabled: false },
                                scrollBeyondLastLine: false,
                                fontSize: 13,
                                lineNumbers: 'on',
                                renderLineHighlight: 'all',
                                wordWrap: 'on',
                                wrappingIndent: 'same',
                                scrollbar: { vertical: 'auto', horizontal: 'auto', useShadows: false },
                                folding: true,
                                quickSuggestions: false,
                                suggestOnTriggerCharacters: false,
                                padding: { top: 12, bottom: 12 },
                            }}
                        />
                    ) : (
                        <div className="h-full overflow-y-auto p-4 scrollbar-macos">
                            <AgentMarkdown variant="chat">{previewContent}</AgentMarkdown>
                        </div>
                    )}
                </div>
            </div>
        </div>
    )
}
