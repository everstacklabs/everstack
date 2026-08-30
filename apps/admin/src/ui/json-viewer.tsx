import { useMemo, useState } from 'react'
import JsonView from 'react18-json-view'
import 'react18-json-view/src/style.css'
import 'react18-json-view/src/dark.css'
import '@/styles/styles.css'
import { ui } from '@everstack/ui'
import { Copy, Check, UnfoldVertical, FoldVertical } from 'lucide-react'
import { cn } from '@everstack/utils/functions/cn'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'

const { Button } = ui

// Deep parse JSON strings nested in objects
function deepParseJson(value: unknown): unknown {
    if (typeof value === 'string') {
        try {
            const parsed = JSON.parse(value)
            return deepParseJson(parsed)
        } catch {
            return value
        }
    }
    if (Array.isArray(value)) {
        return value.map(item => deepParseJson(item))
    }
    if (value !== null && typeof value === 'object') {
        const result: Record<string, unknown> = {}
        for (const [key, val] of Object.entries(value)) {
            result[key] = deepParseJson(val)
        }
        return result
    }
    return value
}

// Stringify JSON node for copying
function stringifyJsonNode(node: unknown): string {
    if (typeof node === 'string') {
        return node
    }

    try {
        return JSON.stringify(
            node,
            (_key, value) => {
                switch (typeof value) {
                    case 'bigint':
                        return String(value) + 'n'
                    case 'number':
                    case 'boolean':
                    case 'object':
                    case 'string':
                        return value as string
                    default:
                        return String(value)
                }
            },
            2
        )
    } catch (error) {
        console.error('JSON stringify error', error)
        return 'Error: JSON.stringify failed'
    }
}

export function JsonViewer({
    data,
    className,
    collapsed = false,
    collapseStringsAfterLength = 500,
    showControls = true,
}: {
    data: Record<string, unknown> | unknown
    className?: string
    collapsed?: boolean
    collapseStringsAfterLength?: number
    showControls?: boolean
}) {
    const parsedJson = useMemo(() => deepParseJson(data), [data])
    const [isCollapsed, setIsCollapsed] = useState(collapsed)
    const [isCopied, setIsCopied] = useState(false)

    const handleCopy = async (event?: React.MouseEvent<HTMLButtonElement>) => {
        if (event) {
            event.preventDefault()
        }
        const textToCopy = stringifyJsonNode(parsedJson)
        await copyToClipboard(textToCopy)
        setIsCopied(true)
        setTimeout(() => setIsCopied(false), 2000)

        if (event) {
            event.currentTarget.focus()
        }
    }

    const handleToggleCollapse = () => {
        setIsCollapsed(!isCollapsed)
    }

    return (
        <div className={cn('relative flex flex-col gap-2', className)}>
            {showControls && (
                <div className="absolute top-0 right-0 flex items-center justify-end gap-2">
                    <Button
                        variant="ghost"
                        size="icon"
                        onClick={handleToggleCollapse}
                        className="h-4 w-4"
                        title={isCollapsed ? 'Expand all' : 'Collapse all'}
                    >
                        {isCollapsed ? (
                            <UnfoldVertical className="size-3" />
                        ) : (
                            <FoldVertical className="size-3" />
                        )}
                    </Button>
                    <Button
                        variant="ghost"
                        size="icon"
                        onClick={handleCopy}
                        className="h-4 w-4"
                        title="Copy JSON"
                    >
                        {isCopied ? (
                            <Check className="size-3 text-emerald-500" />
                        ) : (
                            <Copy className="size-3" />
                        )}
                    </Button>
                </div>
            )}
            <div className="w-full [&_.json-view]:text-xs [&_.json-view]:leading-relaxed [&_.json-view_.json-view--string]:text-xs [&_.json-view_.json-view--number]:text-xs [&_.json-view_.json-view--boolean]:text-xs [&_.json-view_.json-view--null]:text-xs [&_.json-view_.json-view--property]:text-xs">
                <JsonView
                    src={parsedJson}
                    theme="github"
                    className="bg-transparent"
                    dark={true}
                    collapsed={isCollapsed ? 1 : false}
                    collapseObjectsAfterLength={isCollapsed ? 0 : 20}
                    collapseStringsAfterLength={collapseStringsAfterLength}
                    collapseStringMode="word"
                    customizeCollapseStringUI={(fullString, truncated) =>
                        truncated ? (
                            <div className="opacity-50 text-[10px]">
                                {`\n...expand (${Math.max(fullString.length - collapseStringsAfterLength, 0)} more characters)`}
                            </div>
                        ) : (
                            ''
                        )
                    }
                    displaySize={isCollapsed ? 'collapsed' : 'expanded'}
                    matchesURL={true}
                    displayArrayIndex={true}
                    editable={false}
                    enableClipboard={false}
                />
            </div>
        </div>
    )
}
