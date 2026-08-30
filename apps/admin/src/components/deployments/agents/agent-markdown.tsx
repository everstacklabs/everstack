import { useEffect, useMemo, useRef } from 'react'
import { Marked } from 'marked'
import morphdom from 'morphdom'
import { highlightCode } from '@/lib/shiki'

// ── Marked instance ─────────────────────────────────────────────────────────

const markedInstance = new Marked({ gfm: true, breaks: false })

// ── Incomplete markdown closer (remend) ─────────────────────────────────────

function closeIncompleteMarkdown(text: string): string {
    if (!text) return text

    const lines = text.split('\n')
    let insideFence = false
    let fenceMarker = ''
    for (const line of lines) {
        const trimmed = line.trim()
        if (!insideFence) {
            const m = /^(`{3,}|~{3,})/.exec(trimmed)
            if (m) { insideFence = true; fenceMarker = m[1]![0]!.repeat(m[1]!.length) }
        } else {
            const m = /^(`{3,}|~{3,})\s*$/.exec(trimmed)
            if (m && m[1]![0] === fenceMarker[0] && m[1]!.length >= fenceMarker.length) {
                insideFence = false; fenceMarker = ''
            }
        }
    }
    if (insideFence) text = text + '\n' + fenceMarker

    const FENCE_RE = /(```[\s\S]*?```|~~~[\s\S]*?~~~)/g
    return text.split(FENCE_RE).map((part, i) => i % 2 === 1 ? part : closeInlineDelimiters(part)).join('')
}

function closeInlineDelimiters(text: string): string {
    let backtickCount = 0
    let inBacktick = false
    for (let i = 0; i < text.length; i++) {
        if (text[i] === '`') { if (!inBacktick) inBacktick = true; backtickCount++ }
        else if (inBacktick) { inBacktick = false }
    }
    if (backtickCount % 2 === 1) text += '`'

    const lastNewline = text.lastIndexOf('\n')
    const lastLine = text.slice(lastNewline + 1)

    let boldOpen = 0, italicOpen = 0, j = 0
    while (j < lastLine.length) {
        if (lastLine[j] === '*' || lastLine[j] === '_') {
            const ch = lastLine[j]!
            let runLen = 0
            while (j < lastLine.length && lastLine[j] === ch) { runLen++; j++ }
            if (runLen >= 3) { if (boldOpen && italicOpen) { boldOpen = 0; italicOpen = 0 } else { boldOpen++; italicOpen++ } }
            else if (runLen === 2) { boldOpen = boldOpen ? 0 : 1 }
            else { italicOpen = italicOpen ? 0 : 1 }
        } else { j++ }
    }
    if (boldOpen && italicOpen) text += '***'
    else if (boldOpen) text += '**'
    else if (italicOpen) text += '*'

    const tildeMatches = lastLine.match(/~~/g)
    if (tildeMatches && tildeMatches.length % 2 === 1) text += '~~'

    const linkMatch = /\[([^\]]*)\]\([^)]*$/.exec(text)
    if (linkMatch) text += ')'
    const bracketMatch = /\[[^\]]*$/.exec(text)
    if (bracketMatch && !linkMatch) text += ']()'

    return text
}

// ── Normalize chat markdown ─────────────────────────────────────────────────

const FENCED_CODE_BLOCK_REGEX = /(```[\s\S]*?```|~~~[\s\S]*?~~~)/g

function normalizeChatMarkdown(markdown: string): string {
    if (!markdown) return markdown
    const normalized = markdown.replace(/\r\n?/g, '\n')
    return normalized.split(FENCED_CODE_BLOCK_REGEX).map((seg, i) => {
        if (i % 2 === 1) return seg
        return seg
            .replace(/[ \t]+\n/g, '\n')
            .replace(/\n{3,}/g, '\n\n')
            .replace(/\n{2,}(?=\s*#{1,6}\s)/g, '\n')
            .replace(/(#{1,6}[^\n]*)\n{2,}/g, '$1\n')
            .replace(/\n{2,}(?=\s*(?:[-*+]|\d+\.)\s)/g, '\n')
            .replace(/((?:[-*+]|\d+\.)\s[^\n]*)\n{2,}/g, '$1\n')
            .replace(/\n{2,}(?=\s*(?:---+|\*\*\*+|___+)\s*$)/gm, '\n')
            .replace(/\n{3,}$/g, '\n\n')
    }).join('')
}

// ── Block-level element tags (animate on insert) ────────────────────────

const BLOCK_TAGS = new Set([
    'P', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6',
    'UL', 'OL', 'LI', 'BLOCKQUOTE', 'PRE', 'TABLE', 'HR',
])

// ── Post-processing: add copy buttons, syntax highlighting ──────────────────

function decorateDOM(container: HTMLElement, isStreaming: boolean) {
    // Open links in new tab
    container.querySelectorAll('a').forEach((a) => {
        a.setAttribute('target', '_blank')
        a.setAttribute('rel', 'noopener noreferrer')
    })

    // Decorate fenced code blocks with language label + copy button
    container.querySelectorAll('pre').forEach((pre) => {
        // Skip already-decorated blocks
        if (pre.getAttribute('data-decorated')) return

        const code = pre.querySelector('code')
        if (!code) return

        const langClass = code.className.match(/language-(\w+)/)
        const language = langClass ? langClass[1] : ''

        // Wrap in a container div
        const wrapper = document.createElement('div')
        wrapper.className = 'sd-code-block group'

        // Header
        const header = document.createElement('div')
        header.className = 'sd-code-header'
        header.innerHTML = `
            <span class="sd-code-lang">${language || 'text'}</span>
            <button class="sd-code-copy" title="Copy code">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
            </button>
        `

        const copyBtn = header.querySelector('.sd-code-copy')
        if (copyBtn) {
            copyBtn.addEventListener('click', () => {
                const text = code.textContent ?? ''
                navigator.clipboard.writeText(text).then(() => {
                    copyBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>'
                    copyBtn.classList.add('sd-code-copied')
                    setTimeout(() => {
                        copyBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>'
                        copyBtn.classList.remove('sd-code-copied')
                    }, 2000)
                })
            })
        }

        pre.setAttribute('data-decorated', 'true')
        pre.parentNode?.insertBefore(wrapper, pre)
        wrapper.appendChild(header)
        wrapper.appendChild(pre)

        // Queue Shiki highlighting for static mode
        if (!isStreaming && language) {
            highlightCodeBlock(code, language)
        }
    })
}

// ── Shiki highlighting (shared singleton in @/lib/shiki) ──────────────────────

async function highlightCodeBlock(codeEl: HTMLElement, language: string) {
    const res = await highlightCode(codeEl.textContent ?? '', language)
    if (res) {
        codeEl.innerHTML = res.innerHtml
        codeEl.className = res.codeClass
    }
}

// ── AgentMarkdown ────────────────────────────────────────────────────────────

interface AgentMarkdownProps {
    children: string
    isStreaming?: boolean
    className?: string
    variant?: 'compact' | 'chat'
}

export function AgentMarkdown({
    children,
    isStreaming = false,
    className,
    variant = 'compact',
}: AgentMarkdownProps) {
    const containerRef = useRef<HTMLDivElement>(null)
    const prevContentRef = useRef('')

    // Process content: close incomplete syntax during streaming, normalize.
    // Always apply closeIncompleteMarkdown — it's idempotent on complete text
    // and avoids a re-render flash when isStreaming flips to false.
    const processedContent = useMemo(() => {
        const content = closeIncompleteMarkdown(children)
        return variant === 'chat' ? normalizeChatMarkdown(content) : content
    }, [children, variant])

    // Parse markdown → HTML and patch DOM via morphdom.
    // Only re-runs when content actually changes — NOT when isStreaming flips.
    useEffect(() => {
        const container = containerRef.current
        if (!container) return

        // Skip if content hasn't changed
        if (processedContent === prevContentRef.current) return
        prevContentRef.current = processedContent

        if (!processedContent) {
            container.innerHTML = ''
            return
        }

        const html = markedInstance.parse(processedContent, { async: false }) as string
        const temp = document.createElement('div')
        temp.innerHTML = html

        morphdom(container, temp, {
            childrenOnly: true,
            onBeforeElUpdated: (fromEl, toEl) => {
                // Skip identical nodes for performance
                if (fromEl.isEqualNode(toEl)) return false
                // Preserve decorated code blocks
                if (fromEl.getAttribute('data-decorated')) return false
                return true
            },
            onNodeAdded: (node) => {
                // Animate new block-level elements sliding in
                if (node.nodeType === 1 && BLOCK_TAGS.has((node as HTMLElement).tagName)) {
                    (node as HTMLElement).setAttribute('data-sd-new', '')
                }
                return node
            },
        })

        decorateDOM(container, true) // always treat as streaming here — Shiki runs below
    }, [processedContent])

    // When streaming ends, apply Shiki syntax highlighting to code blocks.
    // This is the only thing that changes on the streaming→done transition —
    // no DOM teardown, no re-parse, just additive highlighting in place.
    const wasStreamingRef = useRef(isStreaming)
    useEffect(() => {
        if (wasStreamingRef.current && !isStreaming && containerRef.current) {
            containerRef.current.querySelectorAll('pre[data-decorated] code').forEach((code) => {
                const langClass = code.className.match(/language-(\w+)/)
                if (langClass?.[1]) {
                    highlightCodeBlock(code as HTMLElement, langClass[1])
                }
            })
        }
        wasStreamingRef.current = isStreaming
    }, [isStreaming])

    const variantClass = variant === 'chat' ? 'sd-prose sd-chat' : 'sd-prose sd-compact'

    return (
        <div
            ref={containerRef}
            className={`${variantClass}${isStreaming ? ' streaming-cursor' : ''}${className ? ` ${className}` : ''}`}
        />
    )
}
