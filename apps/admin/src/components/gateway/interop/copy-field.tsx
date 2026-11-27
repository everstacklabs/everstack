import { useState } from 'react'
import { ui } from '@everstack/ui'

const { Button, CodeBlock, CodeBlockBody, CodeBlockContent, CodeBlockCopyButton, CodeBlockItem } = ui

async function copyText(value: string): Promise<boolean> {
    try {
        await navigator.clipboard.writeText(value)
        return true
    } catch {
        return false
    }
}

/** A single-line value with a copy button. */
export function CopyField({
    value,
    label,
    onCopy,
    ariaLabel,
    wrap,
}: {
    value: string
    label?: string
    onCopy?: () => void
    ariaLabel?: string
    /** Show the whole value on several lines instead of truncating it. */
    wrap?: boolean
}) {
    const [copied, setCopied] = useState(false)
    return (
        <div className="flex items-center gap-2">
            <code className={`flex-1 ${wrap ? 'wrap-anywhere' : 'truncate'} rounded bg-brand-main-800/40 px-3 py-1.5 text-xs border border-brand-main-600/40 text-brand-main-100`}>
                {value}
            </code>
            <Button
                variant="outline"
                aria-label={ariaLabel}
                onClick={async () => {
                    if (await copyText(value)) {
                        onCopy?.()
                        setCopied(true)
                        setTimeout(() => setCopied(false), 1500)
                    }
                }}
            >
                {copied ? 'Copied' : (label ?? 'Copy')}
            </Button>
        </div>
    )
}

/** A multi-line, Shiki-highlighted code block with a copy button - matches the
 *  deployment detail / onboarding code blocks. */
export function CodeBox({
    code,
    language = 'bash',
    onCopy,
}: {
    code: string
    language?: string
    onCopy?: () => void
}) {
    const data = [{ language, filename: `snippet.${language === 'bash' ? 'sh' : language}`, code }]
    return (
        <CodeBlock data={data} defaultValue={language} className="rounded border border-brand-main-600/40 bg-brand-main-950">
            <CodeBlockBody>
                {(item) => (
                    <CodeBlockItem key={item.language} value={item.language} className="relative">
                        <CodeBlockContent
                            language={language}
                            className="!bg-brand-main-950 [&_.shiki]:!bg-transparent [&_pre]:!bg-transparent"
                        >
                            {item.code}
                        </CodeBlockContent>
                        <div className="absolute right-2 top-2">
                            <CodeBlockCopyButton size="sm" onCopy={onCopy} />
                        </div>
                    </CodeBlockItem>
                )}
            </CodeBlockBody>
        </CodeBlock>
    )
}
