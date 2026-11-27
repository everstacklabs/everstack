// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import {
  REPLAY_BLOCK_SELECTOR,
  REPLAY_MASK_TEXT_SELECTOR,
  stripNetworkPayload,
} from './replay-privacy'

function masked(html: string, target: string): boolean {
  document.body.innerHTML = html
  const el = document.querySelector(target)
  if (!el) throw new Error(`no element for ${target}`)
  // rrweb checks ancestors for text masking.
  return el.closest(REPLAY_MASK_TEXT_SELECTOR) !== null
}

describe('REPLAY_MASK_TEXT_SELECTOR', () => {
  it('masks anything inside a data-ph-mask container', () => {
    expect(masked('<div data-ph-mask><p><span id="t">sk-live</span></p></div>', '#t')).toBe(true)
  })

  it('masks the element types code, logs, prompts and terminals render through', () => {
    for (const html of [
      '<pre><span id="t">x</span></pre>',
      '<code id="t">x</code>',
      '<textarea id="t">x</textarea>',
      '<div contenteditable="true"><p id="t">x</p></div>',
      '<div class="xterm"><div id="t">$ ls</div></div>',
      '<div class="cm-editor"><div id="t">x</div></div>',
    ]) {
      expect(masked(html, '#t'), html).toBe(true)
    }
  })

  it('leaves navigation and page chrome readable', () => {
    expect(masked('<nav><a id="t" href="/traces">Traces</a></nav>', '#t')).toBe(false)
    expect(masked('<main><h1 id="t">API keys</h1></main>', '#t')).toBe(false)
  })
})

describe('REPLAY_BLOCK_SELECTOR', () => {
  // rrweb records attributes verbatim, so a link's href would keep tokens
  // and query strings even when its text is masked. Blocked elements are
  // recorded as a placeholder with no attributes.
  it('blocks links that can carry tokens or free-text queries', () => {
    const blocked = (html: string) => {
      document.body.innerHTML = html
      return document.getElementById('t')!.matches(REPLAY_BLOCK_SELECTOR)
    }
    expect(blocked('<a id="t" data-ph-block href="https://p-3000.sbx.example/app">port</a>')).toBe(true)
    expect(blocked('<a id="t" href="/observability/traces?q=password">x</a>')).toBe(true)
    expect(blocked('<div data-ph-mask><a id="t" href="https://files.example/doc">x</a></div>')).toBe(true)
    expect(blocked('<div class="prose"><a id="t" href="https://x.example/a">x</a></div>')).toBe(true)
    expect(blocked('<nav><a id="t" href="/observability/traces">Traces</a></nav>')).toBe(false)
  })

  it('blocks canvases and iframes', () => {
    document.body.innerHTML = '<canvas id="c"></canvas><iframe id="f"></iframe><div id="d"></div>'
    expect(document.getElementById('c')!.matches(REPLAY_BLOCK_SELECTOR)).toBe(true)
    expect(document.getElementById('f')!.matches(REPLAY_BLOCK_SELECTOR)).toBe(true)
    expect(document.getElementById('d')!.matches(REPLAY_BLOCK_SELECTOR)).toBe(false)
  })
})

describe('stripNetworkPayload', () => {
  it('keeps timing and drops headers and bodies', () => {
    const out = stripNetworkPayload({
      name: 'https://i.example/everstack.keys.v1.KeysService/Create',
      method: 'POST',
      status: 200,
      startTime: 1,
      endTime: 2,
      requestHeaders: { cookie: 'es_everstack_session=secret' },
      requestBody: '{"prompt":"hi"}',
      responseHeaders: { 'set-cookie': 'x' },
      responseBody: '{"key":"sk-live"}',
    })
    expect(out).toMatchObject({ method: 'POST', status: 200, startTime: 1, endTime: 2 })
    expect(JSON.stringify(out)).not.toMatch(/secret|prompt|sk-live|set-cookie/)
  })

  // posthog-js runs the same function over replay page URLs
  // ($url_changed, $pageview) as well as captured requests.
  it('drops query strings and hashes from request and page URLs', () => {
    expect(stripNetworkPayload({ name: 'https://i.example/observability/traces?q=prompt#x' }).name).toBe(
      'https://i.example/observability/traces',
    )
    expect(stripNetworkPayload({ url: '/v1/logs?query=password' }).url).toBe('/v1/logs')
  })
})
