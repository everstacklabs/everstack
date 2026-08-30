/**
 * Shared Shiki highlighter singleton.
 *
 * One highlighter for the whole admin app (Shiki is heavy — creating it more
 * than once loads the engine + theme repeatedly). Languages are loaded lazily
 * on first use and cached. Used by both the agent chat renderer
 * (`agent-markdown.tsx`) and the trace-sheet markdown code blocks.
 */

import type { BundledLanguage } from 'shiki'

type Highlighter = Awaited<
  ReturnType<(typeof import('shiki'))['createHighlighter']>
>

export const SHIKI_THEME = 'catppuccin-mocha'

let highlighterPromise: Promise<Highlighter> | null = null
const loadedLangs = new Set<string>()

function getHighlighter(): Promise<Highlighter> {
  if (!highlighterPromise) {
    highlighterPromise = import('shiki').then((mod) =>
      mod.createHighlighter({ themes: [SHIKI_THEME], langs: [] }),
    )
  }
  return highlighterPromise
}

/** Ensure a language grammar is loaded; falls back to plaintext on failure. */
async function ensureLanguage(
  highlighter: Highlighter,
  language: string,
): Promise<string> {
  const lang = language.toLowerCase()
  if (loadedLangs.has(lang)) return lang
  try {
    await highlighter.loadLanguage(lang as BundledLanguage)
    loadedLangs.add(lang)
    return lang
  } catch {
    if (!loadedLangs.has('plaintext')) {
      try {
        await highlighter.loadLanguage('plaintext' as BundledLanguage)
        loadedLangs.add('plaintext')
      } catch {
        /* noop */
      }
    }
    return 'plaintext'
  }
}

/**
 * Highlight `code` and return the inner HTML of Shiki's `<code>` (the colored
 * `<span>`s only) plus its className, so callers can inject it into their own
 * `<code>` element and keep their own container styling. Returns null on
 * failure so callers can leave the plain code in place.
 */
export async function highlightCode(
  code: string,
  language: string,
): Promise<{ innerHtml: string; codeClass: string } | null> {
  try {
    const highlighter = await getHighlighter()
    const lang = await ensureLanguage(highlighter, language)
    const html = highlighter.codeToHtml(code, { lang, theme: SHIKI_THEME })
    const temp = document.createElement('div')
    temp.innerHTML = html
    const shikiCode = temp.querySelector('code')
    if (!shikiCode) return null
    return { innerHtml: shikiCode.innerHTML, codeClass: shikiCode.className }
  } catch {
    return null
  }
}
