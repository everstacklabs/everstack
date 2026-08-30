/**
 * trace-messages: parse the normalized conversation we already store on spans
 * (`llm.request.messages` for input, `llm.response.choices` for output) into a
 * typed message model the ConversationView can render structurally.
 *
 * Today the trace UI flattens everything to `{role, text}` and drops tool calls,
 * tool results, and multimodal content. This parser preserves all of it while
 * staying defensive: anything it can't recognize falls back to text/JSON so the
 * UI never renders less than before.
 *
 * Shapes handled:
 *  - OpenAI chat messages: { role, content: string | ContentPart[], tool_calls,
 *    tool_call_id, name }
 *  - OpenAI tool_calls: [{ id, type:'function', function:{ name, arguments } }]
 *  - OpenAI response: { choices: [{ message, finish_reason }] }
 *  - Anthropic content blocks: { type:'text'|'image'|'tool_use'|'tool_result' }
 *  - Nested wrappers: { index, message } / { message }
 */

export type ContentPart =
  | { type: 'text'; text: string }
  | { type: 'image'; url: string; detail?: string }
  | { type: 'audio'; url: string; format?: string }
  | { type: 'json'; value: unknown }

export type ToolCall = { id: string; name: string; args: unknown }

export type ChatRole = 'system' | 'developer' | 'user' | 'assistant' | 'tool'

export interface ChatMessage {
  role: ChatRole
  content: ContentPart[]
  /** assistant tool calls (OpenAI tool_calls / Anthropic tool_use blocks) */
  toolCalls?: ToolCall[]
  /** set on a tool-result message; links back to the originating tool call */
  toolCallId?: string
  /** tool name on tool-result messages, when available */
  name?: string
  /** assistant final message finish reason */
  finishReason?: string
  /**
   * Model reasoning / extended-thinking text, when the provider returns it
   * inline. OpenAI-compatible (DeepSeek, Qwen, etc.) put it on
   * `message.reasoning_content` / `message.reasoning`; Anthropic emits
   * `{type:'thinking'|'reasoning'}` content blocks. We surface it distinctly
   * from the final answer.
   */
  reasoning?: string
}

function tryParse(value: unknown): unknown {
  if (typeof value !== 'string') return value
  const trimmed = value.trim()
  if (!trimmed || (trimmed[0] !== '{' && trimmed[0] !== '[')) return value
  try {
    return JSON.parse(trimmed)
  } catch {
    return value
  }
}

function normalizeRole(role: unknown): ChatRole {
  const r = typeof role === 'string' ? role.toLowerCase() : ''
  if (r === 'system' || r === 'developer' || r === 'user' || r === 'assistant' || r === 'tool') {
    return r as ChatRole
  }
  // Anthropic uses 'assistant'/'user' only; some SDKs use 'model' for assistant.
  if (r === 'model' || r === 'ai') return 'assistant'
  if (r === 'human') return 'user'
  return 'user'
}

function imageUrlFromPart(part: any): string | null {
  // OpenAI: { type:'image_url', image_url:{ url } } or { image_url:'...' }
  if (part.image_url) {
    if (typeof part.image_url === 'string') return part.image_url
    if (typeof part.image_url.url === 'string') return part.image_url.url
  }
  // Anthropic: { type:'image', source:{ type:'base64', media_type, data } | { type:'url', url } }
  if (part.source) {
    if (typeof part.source.url === 'string') return part.source.url
    if (part.source.type === 'base64' && part.source.data) {
      const media = part.source.media_type || 'image/png'
      return `data:${media};base64,${part.source.data}`
    }
  }
  if (part.type === 'image' && typeof part.url === 'string') return part.url
  return null
}

/** Convert a single message's `content` (string | array) into ContentParts. */
function parseContent(content: unknown): ContentPart[] {
  if (content == null) return []
  if (typeof content === 'string') {
    return content ? [{ type: 'text', text: content }] : []
  }
  if (!Array.isArray(content)) {
    // Some providers nest a single object; pretty-print as JSON.
    return [{ type: 'json', value: content }]
  }
  const parts: ContentPart[] = []
  for (const raw of content) {
    if (typeof raw === 'string') {
      if (raw) parts.push({ type: 'text', text: raw })
      continue
    }
    if (!raw || typeof raw !== 'object') continue
    const part: any = raw
    const t = part.type

    if (t === 'text' || part.text || (t == null && typeof part.content === 'string')) {
      // OpenAI: {type:'text', text}; OTel gen_ai: {type:'text', content}.
      const text = typeof part.text === 'string' ? part.text : typeof part.content === 'string' ? part.content : ''
      if (text) parts.push({ type: 'text', text })
      continue
    }
    if (t === 'image' || t === 'image_url' || part.image_url || part.source) {
      const url = imageUrlFromPart(part)
      if (url) parts.push({ type: 'image', url, detail: part.image_url?.detail })
      continue
    }
    if (t === 'input_audio' || t === 'audio' || part.input_audio || part.audio) {
      // OpenAI: {type:'input_audio', input_audio:{data, format}} / output audio;
      // also accept a url or a bare data URI.
      const au = part.input_audio ?? part.audio ?? part
      let url: string | undefined
      if (typeof au === 'string') url = au
      else if (typeof au?.url === 'string') url = au.url
      else if (typeof au?.data === 'string')
        url = au.data.startsWith('data:') ? au.data : `data:audio/${au.format || 'mp3'};base64,${au.data}`
      if (url) parts.push({ type: 'audio', url, format: au?.format })
      // A transcript may accompany the audio — surface it as text too.
      if (typeof au?.transcript === 'string' && au.transcript) parts.push({ type: 'text', text: au.transcript })
      continue
    }
    if (t === 'tool_result' || t === 'tool_call_response') {
      // Anthropic tool_result / OTel gen_ai tool_call_response carried in content.
      const payload = part.content ?? part.response
      const inner = parseContent(payload)
      if (inner.length) parts.push(...inner)
      else if (payload !== undefined) parts.push({ type: 'json', value: payload })
      continue
    }
    if (t === 'tool_use' || t === 'tool_call' || t === 'thinking' || t === 'reasoning' || t === 'redacted_thinking') {
      // Handled separately (tool calls / reasoning); skip here.
      continue
    }
    // Unknown block: surface as JSON rather than dropping it.
    parts.push({ type: 'json', value: part })
  }
  return parts
}

/** Extract tool calls from an OpenAI message or Anthropic content blocks. */
function parseToolCalls(msg: any): ToolCall[] | undefined {
  const calls: ToolCall[] = []
  // OpenAI: msg.tool_calls = [{ id, function:{ name, arguments } }]
  if (Array.isArray(msg.tool_calls)) {
    for (const tc of msg.tool_calls) {
      if (!tc) continue
      const fn = tc.function || tc
      calls.push({
        id: String(tc.id ?? fn.id ?? ''),
        name: String(fn.name ?? tc.name ?? 'tool'),
        args: tryParse(fn.arguments ?? tc.arguments ?? tc.input ?? {}),
      })
    }
  }
  // Anthropic: tool_use blocks inside content array
  if (Array.isArray(msg.content)) {
    for (const block of msg.content) {
      if (block && typeof block === 'object' && block.type === 'tool_use') {
        calls.push({
          id: String(block.id ?? ''),
          name: String(block.name ?? 'tool'),
          args: tryParse(block.input ?? {}),
        })
      }
    }
  }
  // OTel gen_ai: tool_call parts inside `parts`.
  if (Array.isArray(msg.parts)) {
    for (const p of msg.parts) {
      if (p && typeof p === 'object' && p.type === 'tool_call') {
        calls.push({
          id: String(p.id ?? ''),
          name: String(p.name ?? 'tool'),
          args: tryParse(p.arguments ?? p.input ?? {}),
        })
      }
    }
  }
  return calls.length ? calls : undefined
}

function toolResultIdFromContent(msg: any): string | undefined {
  if (msg.tool_call_id) return String(msg.tool_call_id)
  if (Array.isArray(msg.content)) {
    const block = msg.content.find(
      (b: any) => b && typeof b === 'object' && b.type === 'tool_result',
    )
    if (block?.tool_use_id) return String(block.tool_use_id)
  }
  if (Array.isArray(msg.parts)) {
    const p = msg.parts.find((x: any) => x && typeof x === 'object' && x.type === 'tool_call_response')
    if (p?.id) return String(p.id)
  }
  return undefined
}

/** Extract reasoning / extended-thinking text from a message, when inline. */
function extractReasoning(raw: any): string | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const direct =
    (typeof raw.reasoning_content === 'string' && raw.reasoning_content) ||
    (typeof raw.reasoning === 'string' && raw.reasoning) ||
    (typeof raw.thinking === 'string' && raw.thinking)
  if (direct) return direct
  if (Array.isArray(raw.content)) {
    const parts: string[] = []
    for (const block of raw.content) {
      if (!block || typeof block !== 'object') continue
      if (block.type === 'thinking' && typeof block.thinking === 'string') {
        parts.push(block.thinking)
      } else if (block.type === 'reasoning') {
        if (typeof block.text === 'string') parts.push(block.text)
        else if (Array.isArray(block.summary)) {
          for (const s of block.summary) if (typeof s?.text === 'string') parts.push(s.text)
        }
      }
    }
    if (parts.length) return parts.join('\n')
  }
  return undefined
}

/** Parse one message-like object into a ChatMessage. */
function parseMessage(raw: any): ChatMessage | null {
  if (raw == null) return null

  // Unwrap nested { index, message } / { message } / OpenAI choice.
  if (raw.message && typeof raw.message === 'object') {
    const wrapped = parseMessage(raw.message)
    if (wrapped && raw.finish_reason) wrapped.finishReason = String(raw.finish_reason)
    return wrapped
  }

  if (typeof raw === 'string') {
    return raw ? { role: 'user', content: [{ type: 'text', text: raw }] } : null
  }
  if (typeof raw !== 'object') return null

  const role = normalizeRole(raw.role)
  // OpenAI/Anthropic use `content`; OTel gen_ai messages carry `parts`.
  const content = parseContent(raw.content !== undefined ? raw.content : raw.parts)
  const msg: ChatMessage = { role, content }

  const toolCalls = role === 'assistant' ? parseToolCalls(raw) : undefined
  if (toolCalls) msg.toolCalls = toolCalls

  if (role === 'assistant') {
    const reasoning = extractReasoning(raw)
    if (reasoning) msg.reasoning = reasoning
  }

  if (role === 'tool') {
    msg.toolCallId = toolResultIdFromContent(raw)
    if (raw.name) msg.name = String(raw.name)
  }
  if (raw.finish_reason) msg.finishReason = String(raw.finish_reason)

  // Drop genuinely empty messages (no text, no images, no tool calls, no reasoning).
  if (!msg.content.length && !msg.toolCalls && !msg.reasoning) return null
  return msg
}

/**
 * Parse stored request/response payloads into a ChatMessage[].
 * Returns [] when the data isn't message-shaped, so callers can fall back to
 * the existing text/JSON rendering.
 */
export function parseConversation(raw: unknown): ChatMessage[] {
  const data = tryParse(raw)
  if (data == null) return []

  // A bare string that didn't parse to structured JSON is not a conversation;
  // let the caller fall back to the legacy text renderer rather than guessing
  // a role (which would mislabel a plain-text completion as a user message).
  if (typeof data === 'string') return []

  // OpenAI response: { choices: [{ message, finish_reason }] }
  if (!Array.isArray(data) && typeof data === 'object' && Array.isArray((data as any).choices)) {
    return (data as any).choices
      .map((c: any) => parseMessage(c))
      .filter((m: ChatMessage | null): m is ChatMessage => m != null)
  }

  // Array of messages (request) or content parts.
  if (Array.isArray(data)) {
    // If it's an array of content parts (no roles), treat as one user message.
    const looksLikeParts = data.every(
      (x) => x && typeof x === 'object' && (x.type || x.text) && x.role === undefined && x.content === undefined,
    )
    if (looksLikeParts) {
      const parts = parseContent(data)
      return parts.length ? [{ role: 'user', content: parts }] : []
    }
    return data
      .map((m) => parseMessage(m))
      .filter((m): m is ChatMessage => m != null)
  }

  // Single message object.
  const single = parseMessage(data)
  return single ? [single] : []
}

/** True when the raw payload parses to at least one structured message. */
export function hasStructuredConversation(raw: unknown): boolean {
  return parseConversation(raw).length > 0
}

/** Flatten a ChatMessage's text parts (used for tool-result string content). */
export function messageText(msg: ChatMessage): string {
  return msg.content
    .map((p) => (p.type === 'text' ? p.text : p.type === 'json' ? JSON.stringify(p.value, null, 2) : ''))
    .filter(Boolean)
    .join('\n')
}
