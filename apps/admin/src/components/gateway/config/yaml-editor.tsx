import { useRef, useEffect, useMemo } from 'react'
import Editor, { type Monaco } from '@monaco-editor/react'
import { type editor } from 'monaco-editor'
import { configureMonacoYaml } from 'monaco-yaml'
import YamlWorker from 'monaco-yaml/yaml.worker.js?worker'
import JsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker'
import CssWorker from 'monaco-editor/esm/vs/language/css/css.worker?worker'
import HtmlWorker from 'monaco-editor/esm/vs/language/html/html.worker?worker'
import TsWorker from 'monaco-editor/esm/vs/language/typescript/ts.worker?worker'
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import { cn } from '@everstack/utils/functions/cn'
import { CONFIG_SCHEMAS } from './schemas'
import type { ConfigSectionName } from '@/server/gateway-config'

interface YAMLEditorProps {
    value: string
    onChange: (value: string) => void
    section?: ConfigSectionName
    className?: string
    readOnly?: boolean
    height?: string
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
        { token: 'tag', foreground: 'F07178' },
        { token: 'attribute.name', foreground: 'FFCB6B' },
        { token: 'attribute.value', foreground: 'C3E88D' },
        { token: 'delimiter', foreground: '89DDFF' },
        { token: 'key', foreground: '82AAFF' },
        { token: 'string.key.json', foreground: '82AAFF' },
        { token: 'string.value.json', foreground: 'C3E88D' },
        // YAML specific tokens
        { token: 'type.yaml', foreground: '82AAFF' },
        { token: 'string.yaml', foreground: 'C3E88D' },
        { token: 'number.yaml', foreground: 'F78C6C' },
        { token: 'keyword.yaml', foreground: 'C792EA' },
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

// Track if monaco-yaml has been configured
let monacoYamlConfigured = false

// Configure Monaco environment for web workers
// This needs to run before Monaco loads
if (typeof window !== 'undefined') {
    window.MonacoEnvironment = {
        getWorker(_, label) {
            switch (label) {
                case 'yaml':
                    return new YamlWorker()
                case 'json':
                    return new JsonWorker()
                case 'css':
                case 'scss':
                case 'less':
                    return new CssWorker()
                case 'html':
                case 'handlebars':
                case 'razor':
                    return new HtmlWorker()
                case 'typescript':
                case 'javascript':
                    return new TsWorker()
                default:
                    return new EditorWorker()
            }
        },
    }
}

export function YAMLEditor({
    value,
    onChange,
    section,
    className,
    readOnly = false,
    height = '400px'
}: YAMLEditorProps) {
    const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null)

    // Generate a unique URI for this editor based on section
    const modelUri = useMemo(() => {
        return section ? `file:///config/${section}.yaml` : 'file:///config/default.yaml'
    }, [section])

    const handleEditorWillMount = (monaco: Monaco) => {
        monaco.editor.defineTheme('material-ocean', materialOceanTheme)

        // Configure monaco-yaml only once
        if (!monacoYamlConfigured) {
            // Build schemas array from CONFIG_SCHEMAS
            // Use glob patterns that match the model URIs
            const schemas = Object.entries(CONFIG_SCHEMAS).map(([sectionName, schema]) => ({
                uri: `file:///schemas/${sectionName}.json`,
                fileMatch: [
                    `**/config/${sectionName}.yaml`,
                    `file:///config/${sectionName}.yaml`,
                    `${sectionName}.yaml`,
                ],
                schema: schema as Record<string, unknown>,
            }))

            configureMonacoYaml(monaco, {
                enableSchemaRequest: false,
                hover: true,
                completion: true,
                validate: true,
                format: true,
                schemas,
            })

            monacoYamlConfigured = true
        }
    }

    const handleEditorDidMount = (editorInstance: editor.IStandaloneCodeEditor, monaco: Monaco) => {
        editorRef.current = editorInstance
        monaco.editor.setTheme('material-ocean')

        // Set the model URI to match the schema
        if (section) {
            const uri = monaco.Uri.parse(modelUri)
            const existingModel = monaco.editor.getModel(uri)

            if (existingModel) {
                // Use existing model
                editorInstance.setModel(existingModel)
                if (existingModel.getValue() !== value) {
                    existingModel.setValue(value)
                }
            } else {
                // Create new model with correct URI
                const newModel = monaco.editor.createModel(value, 'yaml', uri)
                editorInstance.setModel(newModel)
            }
        }

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
            // Enable suggestions
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

    // Update the model value when value prop changes (but editor didn't trigger it)
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
                <div className="bg-brand-main-800/50 px-3 py-1.5 border-b border-brand-main-600 flex items-center gap-2">
                    <span className="text-xs text-brand-main-200 font-mono">
                        {section ? `${section}.yaml` : 'config.yaml'}
                    </span>
                    <span className="text-xs text-brand-main-400 ml-2">
                        Press Ctrl+Space for suggestions
                    </span>
                    {readOnly && (
                        <span className="text-xs text-brand-main-400 ml-auto">Read-only</span>
                    )}
                </div>
                <Editor
                    height={height}
                    defaultLanguage="yaml"
                    value={value}
                    onChange={handleEditorChange}
                    beforeMount={handleEditorWillMount}
                    onMount={handleEditorDidMount}
                    theme="material-ocean"
                    path={modelUri}
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
