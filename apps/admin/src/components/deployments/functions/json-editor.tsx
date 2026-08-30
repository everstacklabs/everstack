import { useRef, useEffect } from 'react'
import Editor, { type Monaco } from '@monaco-editor/react'
import { type editor } from 'monaco-editor'
import { cn } from '@/lib/utils'

interface JSONEditorProps {
    value: string
    onChange: (value: string) => void
    className?: string
    readOnly?: boolean
    height?: string
    label?: string
    placeholder?: string
}

// Material Ocean High Contrast theme matching brand colors
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
        { token: 'string.key.json', foreground: '82AAFF' },
        { token: 'string.value.json', foreground: 'C3E88D' },
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
        'editor.selectionHighlightBackground': '#3C435E80',
        'editorBracketMatch.background': '#3C435E',
        'editorBracketMatch.border': '#82AAFF',
    },
}

export function JSONEditor({
    value,
    onChange,
    className,
    readOnly = false,
    height = '140px',
    label,
    placeholder,
}: JSONEditorProps) {
    const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null)

    const handleEditorWillMount = (monaco: Monaco) => {
        monaco.editor.defineTheme('material-ocean', materialOceanTheme)
    }

    const handleEditorDidMount = (editorInstance: editor.IStandaloneCodeEditor, monaco: Monaco) => {
        editorRef.current = editorInstance
        monaco.editor.setTheme('material-ocean')

        // Configure editor options
        editorInstance.updateOptions({
            minimap: { enabled: false },
            scrollBeyondLastLine: false,
            fontSize: 13,
            lineNumbers: 'on',
            renderLineHighlight: 'all',
            scrollbar: {
                vertical: 'auto',
                horizontal: 'auto',
                useShadows: false,
            },
            folding: true,
            readOnly,
            quickSuggestions: {
                other: true,
                comments: false,
                strings: true,
            },
            suggestOnTriggerCharacters: true,
            acceptSuggestionOnEnter: 'on',
            tabCompletion: 'on',
            wordBasedSuggestions: 'off',
        })

        // Show placeholder when empty
        if (placeholder && !value) {
            const model = editorInstance.getModel()
            if (model) {
                model.setValue(placeholder)
                editorInstance.setSelection({
                    startLineNumber: 1,
                    startColumn: 1,
                    endLineNumber: model.getLineCount(),
                    endColumn: model.getLineMaxColumn(model.getLineCount()),
                })
            }
        }
    }

    const handleEditorChange = (newValue: string | undefined) => {
        if (newValue !== undefined) {
            onChange(newValue)
        }
    }

    useEffect(() => {
        if (editorRef.current) {
            editorRef.current.updateOptions({ readOnly })
        }
    }, [readOnly])

    // Update the model value when value prop changes externally
    useEffect(() => {
        if (editorRef.current) {
            const model = editorRef.current.getModel()
            if (model && model.getValue() !== value) {
                model.setValue(value)
            }
        }
    }, [value])

    return (
        <div className={cn('relative', className)}>
            <div className="bg-brand-main-900/50 border border-brand-main-600 rounded-lg overflow-hidden">
                {label && (
                    <div className="bg-brand-main-800/50 px-3 py-1.5 border-b border-brand-main-600 flex items-center gap-2">
                        <span className="text-xs text-brand-main-200 font-mono">
                            {label}
                        </span>
                        {readOnly && (
                            <span className="text-xs text-brand-main-400 ml-auto">Read-only</span>
                        )}
                    </div>
                )}
                <Editor
                    height={height}
                    defaultLanguage="json"
                    value={value}
                    onChange={handleEditorChange}
                    beforeMount={handleEditorWillMount}
                    onMount={handleEditorDidMount}
                    theme="material-ocean"
                    options={{
                        automaticLayout: true,
                        formatOnPaste: true,
                        formatOnType: true,
                    }}
                    loading={
                        <div className="flex items-center justify-center h-full bg-brand-main-900/50">
                            <span className="text-brand-main-400 text-sm">Loading editor...</span>
                        </div>
                    }
                />
            </div>
        </div>
    )
}
