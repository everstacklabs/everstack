import { stripUrl } from './analytics-sanitize'

/**
 * What session replay on the instance console may and may not see.
 *
 * The console renders secrets (API keys, tokens, provider credentials) and
 * customer content (prompts, completions, messages, terminal output, memory
 * records, eval datasets). Replay keeps the page structure, navigation,
 * clicks and layout, but the text of anything that can carry that content is
 * masked before it leaves the browser. rrweb checks ancestors, so masking a
 * container masks everything inside it.
 *
 * Three central layers, no per-page edits:
 * 1. Default deny for page content. The shell's <main> is masked on every
 *    route except the short allowlist below, so a new page is masked until
 *    someone decides otherwise.
 * 2. Shared renderers of secrets and customer content (chat transcripts,
 *    agent markdown, tool call cards, table cells, trace input/output, the
 *    editors' highlight mirrors, revealed keys) carry `data-ph-mask`, so they
 *    stay masked even on an allowlisted page.
 * 3. The selector also matches what those renderers are built from (code,
 *    pre, editors, terminals, tables, prose) and everything rendered in a
 *    portal outside <main> (dialogs, sheets, popovers, tooltips, menus,
 *    toasts).
 */

/** Spread onto the root element of a component whose text must never be recorded. */
export const replayMask = { 'data-ph-mask': '' } as const

/**
 * Spread onto an element replay must not record at all, attributes
 * included: links whose href comes from customer data (sandbox port URLs,
 * presigned storage links, attachments, sources, served sites). The user
 * sees the real link; the replay shows an empty box of the same size.
 */
export const replayBlock = { 'data-ph-block': '' } as const

export const REPLAY_MASK_TEXT_SELECTOR = [
  '[data-ph-mask]',
  '.ph-mask',
  // Code, JSON, logs and terminal output.
  'pre',
  'code',
  'kbd',
  'samp',
  // Prompt editors and composers.
  'textarea',
  '[contenteditable]',
  // Editor and terminal libraries.
  '.xterm',
  '.cm-editor',
  '.monaco-editor',
  // Markdown (completions, messages, logs).
  '.prose',
  '.sd-prose',
  // Table data: trace lists, logs, datasets, eval runs, keys.
  'td',
  '[role="cell"]',
  '[role="gridcell"]',
  // Portaled overlays render outside <main>.
  '[role="dialog"]',
  '[role="alertdialog"]',
  '[role="tooltip"]',
  '[role="menu"]',
  '[role="listbox"]',
  '[data-radix-popper-content-wrapper]',
  '[data-sonner-toaster]',
].join(', ')

/**
 * Elements replay does not record at all (an empty placeholder of the same
 * size, no attributes). Canvases back the sandbox browser stream and charts; iframes,
 * embeds and video can show sandbox or customer pages; images inside masked
 * regions and inline images are screenshots and attachments, whose URLs or
 * data would otherwise be recorded verbatim.
 */
export const REPLAY_BLOCK_SELECTOR = [
  'canvas',
  'iframe',
  'video',
  'embed',
  'object',
  '[data-ph-block]',
  // rrweb records attributes verbatim, so link hrefs keep tokens and query
  // strings even where the text is masked: block links with a query, and
  // every link in masked content or rendered markdown.
  'a[href*="?"]',
  '[data-ph-mask] a[href]',
  '.prose a[href]',
  '.sd-prose a[href]',
  '[data-ph-mask] img',
  'main img',
  'img[src^="data:"]',
  'img[src^="blob:"]',
].join(', ')

/**
 * Routes whose page content is safe to record as readable text: no secrets
 * and no customer content. Everything else is masked by default.
 */
export const REPLAY_READABLE_ROUTES = [
  // Sign-in pages, recorded for signed-out visitors of managed instances.
  // Inputs, errors and the magic-link address stay masked.
  '/login',
  '/register',
  '/license',
  '/settings/billing',
  '/settings/catalog',
  '/settings/domain',
  '/settings/general',
] as const

export function isReplayReadableRoute(pathname: string): boolean {
  const path = pathname.replace(/\/+$/, '') || '/'
  return REPLAY_READABLE_ROUTES.some((route) => path === route || path.startsWith(`${route}/`))
}

type NetworkRequest = {
  name?: string
  url?: string
  requestHeaders?: unknown
  requestBody?: unknown
  responseHeaders?: unknown
  responseBody?: unknown
}

/**
 * Network timing may be recorded, but never headers or bodies (they carry
 * session cookies, API keys, prompts and completions) and never a query
 * string or hash (free-text search and ESQL). Applied even if the project's
 * remote settings turn payload capture on. posthog-js also passes replay
 * page URLs ($url_changed, $pageview) through this function.
 */
export function stripNetworkPayload<T extends NetworkRequest>(request: T): T {
  return {
    ...request,
    ...(typeof request.name === 'string' ? { name: stripUrl(request.name) } : {}),
    ...(typeof request.url === 'string' ? { url: stripUrl(request.url) } : {}),
    requestHeaders: undefined,
    requestBody: undefined,
    responseHeaders: undefined,
    responseBody: undefined,
  }
}
