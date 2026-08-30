import {
    forwardRef,
    useCallback,
    useEffect,
    useImperativeHandle,
    useLayoutEffect,
    useRef,
} from 'react'
import { cn } from '@/lib/utils'

interface MentionComposerInputProps {
    value: string
    placeholder?: string
    className?: string
    onValueCursorChange: (value: string, cursor: number) => void
    onKeyDown?: (e: React.KeyboardEvent<HTMLDivElement>) => void
}

export interface MentionComposerInputHandle {
    focus: () => void
    replaceRange: (start: number, end: number, replacement: string, caretOffset: number) => void
}

interface Segment {
    type: 'text' | 'mention'
    value: string
}

const RAW_MENTION_RE = /@(\S+)/g
const TRAILING_PUNCTUATION_RE = /[.,!?;:)\]}>"'`]+$/
const COMPOSER_TOOLTIP_ID = 'mention-composer-tooltip'

function isMentionBoundary(prevChar: string | undefined): boolean {
    if (!prevChar) return true
    return /\s|[([{"'`]/.test(prevChar)
}

function trimTrailingPunctuation(value: string): string {
    return value.replace(TRAILING_PUNCTUATION_RE, '')
}

function getPathTail(path: string): string {
    const normalized = path.replace(/\/+$/, '')
    if (!normalized) return path
    const parts = normalized.split('/')
    return parts[parts.length - 1] || normalized
}

function looksLikeDirectory(path: string): boolean {
    return path.endsWith('/') || !path.includes('.')
}

function buildIconNode(doc: Document, isDir: boolean): HTMLElement {
    const wrapper = doc.createElement('span')
    wrapper.setAttribute('aria-hidden', 'true')
    wrapper.className = 'inline-flex items-center justify-center shrink-0 opacity-75'

    const svg = doc.createElementNS('http://www.w3.org/2000/svg', 'svg')
    svg.setAttribute('viewBox', '0 0 24 24')
    svg.setAttribute('fill', 'none')
    svg.setAttribute('stroke', 'currentColor')
    svg.setAttribute('stroke-width', '2')
    svg.setAttribute('stroke-linecap', 'round')
    svg.setAttribute('stroke-linejoin', 'round')
    svg.setAttribute('class', 'w-3 h-3')

    const paths = isDir
        ? [
            'M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9l-.81-1.2A2 2 0 0 0 7.9 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z',
        ]
        : [
            'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z',
            'M14 2v6h6',
            'm10 12.5-2 2.5 2 2.5',
            'm14 12.5 2 2.5-2 2.5',
        ]

    for (const d of paths) {
        const path = doc.createElementNS('http://www.w3.org/2000/svg', 'path')
        path.setAttribute('d', d)
        svg.appendChild(path)
    }

    wrapper.appendChild(svg)
    return wrapper
}

function getOrCreateComposerTooltip(doc: Document): HTMLElement {
    let tooltip = doc.getElementById(COMPOSER_TOOLTIP_ID)
    if (tooltip) return tooltip

    tooltip = doc.createElement('div')
    tooltip.id = COMPOSER_TOOLTIP_ID
    tooltip.setAttribute('aria-hidden', 'true')
    tooltip.className = 'pointer-events-none fixed z-[9999] hidden whitespace-nowrap rounded border border-white/10 bg-brand-main-600 px-2 py-1 text-[11px] leading-none text-white shadow-sm light:border-black/10 light:text-brand-main-50'
    doc.body.appendChild(tooltip)

    return tooltip
}

function hideComposerTooltip(doc: Document) {
    const tooltip = doc.getElementById(COMPOSER_TOOLTIP_ID)
    if (!tooltip) return
    tooltip.classList.add('hidden')
    tooltip.textContent = ''
}

function positionComposerTooltip(tooltip: HTMLElement, anchorRect: DOMRect) {
    const margin = 8
    const vw = window.innerWidth
    const vh = window.innerHeight

    const width = tooltip.offsetWidth
    const height = tooltip.offsetHeight

    let left = anchorRect.left + anchorRect.width / 2 - width / 2
    left = Math.max(margin, Math.min(left, vw - width - margin))

    let top = anchorRect.top - height - margin
    if (top < margin) {
        top = Math.min(vh - height - margin, anchorRect.bottom + margin)
    }

    tooltip.style.left = `${Math.round(left)}px`
    tooltip.style.top = `${Math.round(top)}px`
}

function showComposerTooltip(doc: Document, mentionValue: string, anchorRect: DOMRect) {
    const tooltip = getOrCreateComposerTooltip(doc)
    tooltip.textContent = mentionValue
    tooltip.classList.remove('hidden')
    positionComposerTooltip(tooltip, anchorRect)
}

function parseSegments(text: string): Segment[] {
    const segments: Segment[] = []
    let lastIndex = 0

    for (const match of text.matchAll(RAW_MENTION_RE)) {
        const start = match.index ?? 0
        const prevChar = start > 0 ? text[start - 1] : undefined
        if (!isMentionBoundary(prevChar)) continue

        const rawMention = match[1]
        const mention = trimTrailingPunctuation(rawMention)
        if (!mention) continue

        const rawEnd = start + match[0].length
        const mentionEnd = start + 1 + mention.length

        if (start > lastIndex) {
            segments.push({ type: 'text', value: text.slice(lastIndex, start) })
        }
        segments.push({ type: 'mention', value: mention })
        if (mentionEnd < rawEnd) {
            segments.push({ type: 'text', value: text.slice(mentionEnd, rawEnd) })
        }

        lastIndex = rawEnd
    }

    if (lastIndex < text.length) {
        segments.push({ type: 'text', value: text.slice(lastIndex) })
    }

    return segments
}

function isMentionElement(node: Node): node is HTMLElement {
    return node instanceof HTMLElement && node.dataset.mention === '1'
}

function mentionLength(el: HTMLElement): number {
    return 1 + (el.dataset.value?.length ?? 0)
}

function nodePlainLength(node: Node): number {
    if (node.nodeType === Node.TEXT_NODE) {
        return node.nodeValue?.length ?? 0
    }
    if (isMentionElement(node)) {
        return mentionLength(node)
    }
    if (node instanceof HTMLBRElement) {
        return 1
    }
    let total = 0
    for (const child of Array.from(node.childNodes)) {
        total += nodePlainLength(child)
    }
    return total
}

function serializeNode(node: Node): string {
    if (node.nodeType === Node.TEXT_NODE) return node.nodeValue ?? ''
    if (isMentionElement(node)) return `@${node.dataset.value ?? ''}`
    if (node instanceof HTMLBRElement) return '\n'
    let out = ''
    for (const child of Array.from(node.childNodes)) out += serializeNode(child)
    return out
}

function serializePlainText(root: HTMLElement): string {
    let out = ''
    for (const child of Array.from(root.childNodes)) {
        out += serializeNode(child)
    }
    return out
}

function domPointToOffset(root: HTMLElement, targetNode: Node, targetOffset: number): number {
    let total = 0
    let found = false

    const walk = (node: Node) => {
        if (found) return

        if (node === targetNode) {
            if (node.nodeType === Node.TEXT_NODE) {
                total += Math.min(targetOffset, node.nodeValue?.length ?? 0)
            } else if (isMentionElement(node)) {
                if (targetOffset > 0) total += mentionLength(node)
            } else {
                const children = Array.from(node.childNodes)
                const upto = Math.min(targetOffset, children.length)
                for (let i = 0; i < upto; i++) total += nodePlainLength(children[i]!)
            }
            found = true
            return
        }

        if (node.nodeType === Node.TEXT_NODE) {
            total += node.nodeValue?.length ?? 0
            return
        }

        if (isMentionElement(node)) {
            total += mentionLength(node)
            return
        }

        for (const child of Array.from(node.childNodes)) {
            walk(child)
            if (found) return
        }
    }

    walk(root)
    return total
}

function getCaretOffset(root: HTMLElement): number {
    const selection = window.getSelection()
    if (!selection || selection.rangeCount === 0 || !selection.anchorNode) {
        return serializePlainText(root).length
    }
    return domPointToOffset(root, selection.anchorNode, selection.anchorOffset)
}

function setCaretFromOffset(root: HTMLElement, offset: number) {
    const selection = window.getSelection()
    if (!selection) return

    const totalLength = serializePlainText(root).length
    let remaining = Math.max(0, Math.min(offset, totalLength))

    const range = document.createRange()

    const placeAt = (container: Node, pointOffset: number) => {
        range.setStart(container, pointOffset)
        range.collapse(true)
        selection.removeAllRanges()
        selection.addRange(range)
    }

    const children = Array.from(root.childNodes)
    for (let i = 0; i < children.length; i++) {
        const child = children[i]!

        if (remaining === 0) {
            placeAt(root, i)
            return
        }

        if (isMentionElement(child)) {
            const len = mentionLength(child)
            if (remaining <= len) {
                // Mention tokens are atomic: place caret after the token.
                placeAt(root, i + 1)
                return
            }
            remaining -= len
            continue
        }

        if (child.nodeType === Node.TEXT_NODE) {
            const textLen = child.nodeValue?.length ?? 0
            if (remaining <= textLen) {
                placeAt(child, remaining)
                return
            }
            remaining -= textLen
            continue
        }

        const len = nodePlainLength(child)
        if (remaining <= len) {
            placeAt(root, i + 1)
            return
        }
        remaining -= len
    }

    placeAt(root, children.length)
}

function buildMentionNode(doc: Document, mentionValue: string): HTMLElement {
    const isDir = looksLikeDirectory(mentionValue)
    const mention = doc.createElement('span')
    mention.dataset.mention = '1'
    mention.dataset.value = mentionValue
    mention.contentEditable = 'false'
    mention.title = mentionValue
    mention.className = 'relative inline-flex items-center gap-1 align-baseline z-0 px-0.5 text-brand-secondary-200 leading-[inherit]'

    mention.appendChild(buildIconNode(doc, isDir))

    mention.appendChild(doc.createTextNode(getPathTail(mentionValue)))

    const chrome = doc.createElement('span')
    chrome.setAttribute('aria-hidden', 'true')
    chrome.className = 'pointer-events-none absolute inset-y-px inset-x-0 rounded bg-brand-secondary-500/22 [box-shadow:inset_0_0_0_1px_rgba(167,139,250,0.38),inset_0_1px_0_rgba(255,255,255,0.08)] -z-10'
    mention.appendChild(chrome)

    mention.addEventListener('mouseenter', () => {
        showComposerTooltip(doc, mentionValue, mention.getBoundingClientRect())
    })
    mention.addEventListener('mousemove', () => {
        const tooltip = doc.getElementById(COMPOSER_TOOLTIP_ID)
        if (!tooltip || tooltip.classList.contains('hidden')) return
        positionComposerTooltip(tooltip, mention.getBoundingClientRect())
    })
    mention.addEventListener('mouseleave', () => {
        hideComposerTooltip(doc)
    })

    return mention
}

function renderFromText(root: HTMLElement, text: string) {
    const doc = root.ownerDocument
    hideComposerTooltip(doc)
    const fragment = doc.createDocumentFragment()
    const segments = parseSegments(text)

    for (const segment of segments) {
        if (segment.type === 'text') {
            fragment.appendChild(doc.createTextNode(segment.value))
            continue
        }
        fragment.appendChild(buildMentionNode(doc, segment.value))
    }

    root.replaceChildren(fragment)
}

export const MentionComposerInput = forwardRef<MentionComposerInputHandle, MentionComposerInputProps>(
    function MentionComposerInput(
        { value, placeholder, className, onValueCursorChange, onKeyDown },
        ref,
    ) {
        const editorRef = useRef<HTMLDivElement>(null)
        const syncingRef = useRef(false)
        const composingRef = useRef(false)

        const syncDomFromValue = useCallback((nextValue: string, caretOffset?: number) => {
            const editor = editorRef.current
            if (!editor) return

            syncingRef.current = true
            renderFromText(editor, nextValue)
            if (typeof caretOffset === 'number') {
                setCaretFromOffset(editor, caretOffset)
            }
            syncingRef.current = false
        }, [])

        const normalizeAndEmit = useCallback(() => {
            const editor = editorRef.current
            if (!editor) return

            const nextValue = serializePlainText(editor)
            const caret = getCaretOffset(editor)
            syncDomFromValue(nextValue, caret)
            onValueCursorChange(nextValue, caret)
        }, [onValueCursorChange, syncDomFromValue])

        const emitValueAndCursor = useCallback(() => {
            const editor = editorRef.current
            if (!editor) return
            onValueCursorChange(serializePlainText(editor), getCaretOffset(editor))
        }, [onValueCursorChange])

        const insertTextAtCursor = useCallback((text: string) => {
            const editor = editorRef.current
            if (!editor) return
            const selection = window.getSelection()
            if (!selection || selection.rangeCount === 0) return

            const range = selection.getRangeAt(0)
            range.deleteContents()

            const node = document.createTextNode(text)
            range.insertNode(node)
            range.setStart(node, node.nodeValue?.length ?? 0)
            range.collapse(true)

            selection.removeAllRanges()
            selection.addRange(range)

            normalizeAndEmit()
        }, [normalizeAndEmit])

        useImperativeHandle(ref, () => ({
            focus() {
                editorRef.current?.focus()
            },
            replaceRange(start: number, end: number, replacement: string, caretOffset: number) {
                const current = editorRef.current ? serializePlainText(editorRef.current) : value
                const nextValue = `${current.slice(0, start)}${replacement}${current.slice(end)}`
                syncDomFromValue(nextValue, caretOffset)
                onValueCursorChange(nextValue, caretOffset)
                editorRef.current?.focus()
            },
        }), [onValueCursorChange, syncDomFromValue, value])

        useLayoutEffect(() => {
            const editor = editorRef.current
            if (!editor || syncingRef.current) return

            const current = serializePlainText(editor)
            if (current !== value) {
                syncDomFromValue(value)
            }
        }, [syncDomFromValue, value])

        useEffect(() => {
            return () => {
                const doc = editorRef.current?.ownerDocument ?? document
                hideComposerTooltip(doc)
            }
        }, [])

        const handleInput = useCallback(() => {
            if (syncingRef.current) return
            if (composingRef.current) return
            normalizeAndEmit()
        }, [normalizeAndEmit])

        const handlePaste = useCallback((e: React.ClipboardEvent<HTMLDivElement>) => {
            e.preventDefault()
            const text = e.clipboardData.getData('text/plain')
            insertTextAtCursor(text)
        }, [insertTextAtCursor])

        const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
            onKeyDown?.(e)
            if (e.defaultPrevented) return

            if (e.key === 'Enter') {
                if (e.shiftKey) {
                    e.preventDefault()
                    insertTextAtCursor('\n')
                    return
                }
                e.preventDefault()
            }
        }, [insertTextAtCursor, onKeyDown])

        return (
            <div className="relative">
                {!value && placeholder && (
                    <span className="pointer-events-none absolute left-4 top-3 text-sm text-white/25 light:text-black/25">
                        {placeholder}
                    </span>
                )}
                <div
                    ref={editorRef}
                    contentEditable
                    suppressContentEditableWarning
                    spellCheck={false}
                    className={cn(
                        'w-full h-14 max-h-[200px] overflow-y-auto whitespace-pre-wrap wrap-break-words bg-transparent px-4 pt-3 pb-1 text-sm text-white focus:outline-none light:text-brand-main-50',
                        className,
                    )}
                    onInput={handleInput}
                    onPaste={handlePaste}
                    onKeyDown={handleKeyDown}
                    onKeyUp={emitValueAndCursor}
                    onMouseUp={emitValueAndCursor}
                    onCompositionStart={() => { composingRef.current = true }}
                    onCompositionEnd={() => {
                        composingRef.current = false
                        normalizeAndEmit()
                    }}
                />
            </div>
        )
    },
)
