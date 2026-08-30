/**
 * Shiki-highlighted `code` renderer for react-markdown.
 *
 * The trace sheet renders markdown through react-markdown (keeping its `prose`
 * styling). react-markdown emits plain `<pre><code>`; this component upgrades
 * fenced code blocks with a language to Shiki syntax highlighting, sharing the
 * one highlighter singleton in `@/lib/shiki`. Highlighting runs in an effect on
 * mount (the content here is static, never streamed), so the plain code shows
 * for one frame and is then replaced by the colored spans.
 *
 * Inline code and language-less fences are left as plain `<code>`.
 */

import { useEffect, useState, type ComponentPropsWithoutRef } from 'react'
import type { Components } from 'react-markdown'
import { highlightCode } from '@/lib/shiki'

function MarkdownCode({
  className,
  children,
  ...props
}: ComponentPropsWithoutRef<'code'>) {
  const language = /language-(\w+)/.exec(className ?? '')?.[1]
  const code = String(children ?? '')
  const [highlighted, setHighlighted] = useState<{
    innerHtml: string
    codeClass: string
  } | null>(null)

  useEffect(() => {
    if (!language) return
    let cancelled = false
    void highlightCode(code, language).then((res) => {
      if (!cancelled) setHighlighted(res)
    })
    return () => {
      cancelled = true
    }
  }, [code, language])

  // Inline code / no language → nothing to highlight.
  if (!language) {
    return (
      <code className={className} {...props}>
        {children}
      </code>
    )
  }

  // Highlighted: inject Shiki's colored spans, keep the prose <pre> chrome.
  if (highlighted) {
    return (
      <code
        className={highlighted.codeClass}
        dangerouslySetInnerHTML={{ __html: highlighted.innerHtml }}
      />
    )
  }

  // Pre-highlight frame: plain code.
  return (
    <code className={className} {...props}>
      {children}
    </code>
  )
}

/**
 * Stable `components` map for react-markdown. Module-level so the reference does
 * not change across renders.
 */
export const MARKDOWN_COMPONENTS: Components = {
  code: MarkdownCode,
}
