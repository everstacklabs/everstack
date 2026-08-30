import { useState, useRef, useEffect } from 'react'
import { Icon } from '@iconify/react'
import Markdown from 'react-markdown'
import { useExecutionStore, type StudioTab } from '@/stores/execution-store'
import { useAnimatedText } from '@/hooks/use-animated-text'
import { ExecutionTimeline } from './execution-timeline'
import { ExecutionHistory } from './execution-history'
import { ExecutionDetail } from './execution-detail'
import { NodeExecutionDetails } from './node-execution-details'
import { BrowserRunPanel } from './browser-run-panel'
import { useStudioStore } from '@/stores/studio-store'

interface ChatPanelContentProps {
  layout: 'right' | 'bottom'
}

// In light mode, re-point the prose-invert palette vars at theme-aware tokens so
// inverted prose renders dark text (dark mode untouched: the vars keep their defaults).
const PROSE_CLASSES =
  'prose prose-sm prose-invert max-w-none prose-p:my-1 prose-headings:my-2 prose-ul:my-1 prose-ol:my-1 prose-li:my-0 prose-pre:my-2 prose-pre:bg-brand-main-950 prose-pre:border prose-pre:border-brand-main-700 prose-code:text-brand-secondary-300 prose-code:before:content-none prose-code:after:content-none prose-strong:text-white light:prose-strong:text-brand-main-50 prose-a:text-brand-secondary-400 light:[--tw-prose-invert-body:var(--color-brand-main-100)] light:[--tw-prose-invert-headings:var(--color-brand-main-50)] light:[--tw-prose-invert-bold:var(--color-brand-main-50)] light:[--tw-prose-invert-bullets:var(--color-brand-main-300)] light:[--tw-prose-invert-counters:var(--color-brand-main-300)] light:[--tw-prose-invert-quotes:var(--color-brand-main-100)] light:[--tw-prose-invert-hr:var(--color-brand-main-700)]'

const TAB_ICONS: Record<StudioTab, string> = {
  chat: 'lucide:message-square',
  browser: 'lucide:mouse-pointer-click',
  trace: 'lucide:activity',
  nodes: 'lucide:layers',
  runs: 'lucide:history',
}

const TAB_LABELS: Record<StudioTab, string> = {
  chat: 'Chat',
  browser: 'Browser',
  trace: 'Trace',
  nodes: 'Nodes',
  runs: 'Runs',
}

function ChatAudioPlayer({
  audio,
  contentType,
}: {
  audio: string
  contentType?: string
}) {
  const [playing, setPlaying] = useState(false)
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const blobUrlRef = useRef<string | null>(null)

  const ensureAudio = () => {
    if (audioRef.current) return audioRef.current
    const mime = contentType || 'audio/wav'
    const blob = new Blob(
      [Uint8Array.from(atob(audio), (c) => c.charCodeAt(0))],
      { type: mime },
    )
    const url = URL.createObjectURL(blob)
    blobUrlRef.current = url
    const el = new Audio(url)
    audioRef.current = el
    el.onloadedmetadata = () => {
      if (Number.isFinite(el.duration)) setDuration(el.duration)
    }
    el.ontimeupdate = () => setCurrentTime(el.currentTime)
    el.onended = () => {
      setPlaying(false)
      setCurrentTime(0)
    }
    return el
  }

  const toggle = () => {
    const el = ensureAudio()
    if (playing) {
      el.pause()
      setPlaying(false)
    } else {
      el.play().then(() => setPlaying(true))
    }
  }

  const handleSeek = (e: React.ChangeEvent<HTMLInputElement>) => {
    const time = parseFloat(e.target.value)
    setCurrentTime(time)
    if (audioRef.current) audioRef.current.currentTime = time
  }

  const handleDownload = () => {
    const mime = contentType || 'audio/wav'
    const ext = mime.includes('wav')
      ? 'wav'
      : mime.includes('mpeg')
        ? 'mp3'
        : 'audio'
    const blob = new Blob(
      [Uint8Array.from(atob(audio), (c) => c.charCodeAt(0))],
      { type: mime },
    )
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `voice-output.${ext}`
    a.click()
    URL.revokeObjectURL(url)
  }

  const fmt = (s: number) => {
    const m = Math.floor(s / 60)
    const sec = Math.floor(s % 60)
    return `${m}:${sec.toString().padStart(2, '0')}`
  }

  return (
    <div className="flex items-center gap-2.5 py-1 min-w-[240px]">
      <button
        type="button"
        onClick={toggle}
        className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-brand-main-700 hover:bg-brand-main-600 transition-colors"
      >
        <Icon
          icon={playing ? 'lucide:pause' : 'lucide:play'}
          className="h-3.5 w-3.5 text-white light:text-brand-main-50"
        />
      </button>
      <div className="flex-1 min-w-0 space-y-1">
        <input
          type="range"
          min={0}
          max={duration || 1}
          step={0.1}
          value={currentTime}
          onChange={handleSeek}
          className="w-full h-1.5 rounded-full appearance-none cursor-pointer bg-brand-main-700 accent-emerald-500 [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:h-3 [&::-webkit-slider-thumb]:w-3 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-emerald-500"
        />
        <div className="flex items-center justify-between">
          <span className="text-[10px] text-white/40 light:text-black/40">
            {fmt(currentTime)}
            {duration > 0 ? ` / ${fmt(duration)}` : ''}
          </span>
          <span className="text-[10px] text-white/40 light:text-black/40">
            {contentType || 'audio/wav'}
          </span>
        </div>
      </div>
      <button
        type="button"
        onClick={handleDownload}
        className="flex h-7 w-7 shrink-0 items-center justify-center rounded hover:bg-brand-main-700 transition-colors"
        title="Download audio"
      >
        <Icon
          icon="lucide:download"
          className="h-3.5 w-3.5 text-white/50 light:text-black/50 hover:text-white light:hover:text-brand-main-50"
        />
      </button>
    </div>
  )
}

export function ChatPanelContent({ layout }: ChatPanelContentProps) {
  const [input, setInput] = useState('')
  const [showHeaders, setShowHeaders] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)

  const tab = useExecutionStore((s) => s.activeTab)
  const setTab = useExecutionStore((s) => s.setActiveTab)
  const messages = useExecutionStore((s) => s.messages)
  const events = useExecutionStore((s) => s.events)
  const tenantId = useStudioStore((s) => s.tenantId)
  const metadata = useExecutionStore((s) => s.metadata)
  const isExecuting = useExecutionStore((s) => s.isExecuting)
  const rawStreamingContent = useExecutionStore((s) => s.streamingContent)
  const streamingContent = useAnimatedText(rawStreamingContent, isExecuting)
  const executionError = useExecutionStore((s) => s.executionError)
  const closeTestPanel = useExecutionStore((s) => s.closeTestPanel)
  const sendMessage = useExecutionStore((s) => s.sendMessage)
  const setMetadata = useExecutionStore((s) => s.setMetadata)
  const clearMessages = useExecutionStore((s) => s.clearMessages)
  const selectedExecutionId = useExecutionStore((s) => s.selectedExecutionId)
  const togglePanelPosition = useExecutionStore((s) => s.togglePanelPosition)
  const panelPosition = useExecutionStore((s) => s.panelPosition)
  const cancelExecution = useExecutionStore((s) => s.cancelExecution)

  const headerCount = Object.keys(metadata).filter((k) => k.trim()).length

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, streamingContent, executionError])

  const handleSend = () => {
    const trimmed = input.trim()
    if (!trimmed) return
    sendMessage(trimmed)
    setInput('')
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const updateHeader = (oldKey: string, newKey: string, newValue: string) => {
    const updated = { ...metadata }
    if (oldKey !== newKey) {
      delete updated[oldKey]
    }
    updated[newKey] = newValue
    setMetadata(updated)
  }

  const addHeader = () => {
    let key = ''
    let i = 0
    while (key in metadata) {
      i++
      key = `header-${i}`
    }
    setMetadata({ ...metadata, [key]: '' })
  }

  const removeHeader = (key: string) => {
    const updated = { ...metadata }
    delete updated[key]
    setMetadata(updated)
  }

  const tabContent = (
    <>
      {tab === 'chat' ? (
        <div className="flex flex-col gap-3 p-4">
          {messages.length === 0 && !isExecuting && (
            <div className="flex flex-col items-center justify-center py-12 text-brand-main-400">
              <Icon
                icon="lucide:message-square"
                className="h-10 w-10 mb-3 opacity-40"
              />
              <p className="text-sm">Send a message to test your workflow</p>
              <p className="text-xs mt-1 opacity-60">
                Messages will be sent through the workflow entry point
              </p>
            </div>
          )}

          {messages.map((msg, idx) => (
            <div
              key={idx}
              className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}
            >
              <div
                className={`max-w-[85%] rounded-lg px-3 py-2 text-sm ${
                  msg.role === 'user'
                    ? 'bg-brand-secondary-600 text-white'
                    : 'bg-brand-main-800 text-brand-main-100'
                }`}
              >
                {msg.role === 'user' ? (
                  <pre className="whitespace-pre-wrap font-sans">
                    {msg.content}
                  </pre>
                ) : msg.audio ? (
                  <ChatAudioPlayer
                    audio={msg.audio}
                    contentType={msg.audioContentType}
                  />
                ) : (
                  <div className={PROSE_CLASSES}>
                    <Markdown>{msg.content}</Markdown>
                  </div>
                )}
              </div>
            </div>
          ))}

          {isExecuting && streamingContent && (
            <div className="flex justify-start">
              <div className="max-w-[85%] rounded-lg bg-brand-main-800 px-3 py-2 text-sm text-brand-main-100">
                <div className={PROSE_CLASSES}>
                  <Markdown>{streamingContent}</Markdown>
                </div>
                <span className="inline-block w-[1.5px] h-4 bg-brand-secondary-400 animate-pulse ml-0.5" />
              </div>
            </div>
          )}

          {isExecuting && !streamingContent && (
            <div className="flex justify-start">
              <div className="rounded-lg bg-brand-main-800 px-3 py-2">
                <div className="flex items-center gap-1.5">
                  <div
                    className="h-1.5 w-1.5 rounded-full bg-brand-main-400 animate-bounce"
                    style={{ animationDelay: '0ms' }}
                  />
                  <div
                    className="h-1.5 w-1.5 rounded-full bg-brand-main-400 animate-bounce"
                    style={{ animationDelay: '150ms' }}
                  />
                  <div
                    className="h-1.5 w-1.5 rounded-full bg-brand-main-400 animate-bounce"
                    style={{ animationDelay: '300ms' }}
                  />
                </div>
              </div>
            </div>
          )}

          {executionError && !isExecuting && (
            <div className="flex justify-start">
              <div className="max-w-[85%] rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400 light:text-red-600">
                <div className="flex items-start gap-2">
                  <Icon
                    icon="lucide:alert-circle"
                    className="mt-0.5 h-4 w-4 flex-shrink-0"
                  />
                  <pre className="whitespace-pre-wrap font-sans">
                    {executionError}
                  </pre>
                </div>
              </div>
            </div>
          )}

          <div ref={messagesEndRef} />
        </div>
      ) : tab === 'browser' ? (
        <BrowserRunPanel events={events} tenantId={tenantId} />
      ) : tab === 'trace' ? (
        <ExecutionTimeline />
      ) : tab === 'nodes' ? (
        <NodeExecutionDetails />
      ) : selectedExecutionId ? (
        <ExecutionDetail />
      ) : (
        <ExecutionHistory />
      )}
    </>
  )

  // Bottom layout: vertical icon sidebar + content
  if (layout === 'bottom') {
    return (
      <div className="flex h-full bg-brand-main-900">
        {/* Vertical icon sidebar */}
        <div className="flex flex-col items-center justify-between border-r border-brand-main-700 w-10 py-2">
          <div className="flex flex-col items-center gap-1">
            {(['chat', 'browser', 'trace', 'nodes', 'runs'] as StudioTab[]).map(
              (t) => (
                <button
                  key={t}
                  onClick={() => setTab(t)}
                  className={`flex h-8 w-8 items-center justify-center rounded transition-colors ${
                    tab === t
                      ? 'bg-brand-secondary-600/20 text-brand-secondary-400 border-l-2 border-brand-secondary-400'
                      : 'text-brand-main-400 hover:bg-brand-main-800 hover:text-brand-main-200'
                  }`}
                  title={TAB_LABELS[t]}
                >
                  <Icon icon={TAB_ICONS[t]} className="h-4 w-4" />
                </button>
              ),
            )}
          </div>
          <div className="flex flex-col items-center gap-1">
            <button
              onClick={togglePanelPosition}
              className="flex h-8 w-8 items-center justify-center rounded text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
              title="Move panel to right"
            >
              <Icon icon="lucide:panel-right" className="h-4 w-4" />
            </button>
            <button
              onClick={closeTestPanel}
              className="flex h-8 w-8 items-center justify-center rounded text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
              title="Close"
            >
              <Icon icon="lucide:x" className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* Content area */}
        <div className="flex flex-1 flex-col overflow-hidden">
          {/* Compact header */}
          <div className="flex items-center justify-between border-b border-brand-main-700 px-3 py-1.5">
            <span className="text-xs font-medium text-brand-main-300">
              {TAB_LABELS[tab]}
            </span>
            <button
              onClick={clearMessages}
              className="rounded p-1 text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
              title="Clear messages"
            >
              <Icon icon="lucide:trash-2" className="h-3 w-3" />
            </button>
          </div>

          {/* Tab content */}
          <div className="flex-1 overflow-y-auto">{tabContent}</div>

          {/* Input area */}
          {tab === 'chat' && (
            <>
              {/* Headers / metadata section */}
              <div className="border-t border-brand-main-700">
                <button
                  onClick={() => setShowHeaders(!showHeaders)}
                  className="flex w-full items-center justify-between px-3 py-1.5 text-xs text-brand-main-400 hover:text-brand-main-200 transition-colors"
                >
                  <div className="flex items-center gap-1.5">
                    <Icon icon="lucide:key" className="h-3 w-3" />
                    <span>Headers / Auth</span>
                    {headerCount > 0 && (
                      <span className="rounded-full bg-brand-secondary-600/30 px-1.5 py-0.5 text-[10px] text-brand-secondary-400">
                        {headerCount}
                      </span>
                    )}
                  </div>
                  <Icon
                    icon="lucide:chevron-up"
                    className={`h-3 w-3 transition-transform ${showHeaders ? '' : 'rotate-180'}`}
                  />
                </button>

                {showHeaders &&
                  renderHeaders(
                    metadata,
                    updateHeader,
                    addHeader,
                    removeHeader,
                  )}
              </div>

              <div className="border-t border-brand-main-700 p-3">
                <div className="flex items-end gap-2">
                  <textarea
                    value={input}
                    onChange={(e) => setInput(e.target.value)}
                    onKeyDown={handleKeyDown}
                    placeholder={
                      isExecuting ? 'Executing...' : 'Type a message...'
                    }
                    disabled={isExecuting}
                    rows={1}
                    className="flex-1 resize-none rounded-lg border border-brand-main-600 bg-brand-main-800 px-3 py-2 text-sm text-white light:text-brand-main-50 placeholder-brand-main-400 focus:border-brand-secondary-500 focus:outline-none focus:ring-1 focus:ring-brand-secondary-500 disabled:opacity-50"
                    style={{ maxHeight: '120px' }}
                  />
                  {isExecuting ? (
                    <button
                      onClick={cancelExecution}
                      className="flex h-9 w-9 items-center justify-center rounded-lg bg-red-600 text-white hover:bg-red-500 transition-colors flex-shrink-0"
                      title="Stop execution"
                    >
                      <Icon icon="lucide:square" className="h-4 w-4" />
                    </button>
                  ) : (
                    <button
                      onClick={handleSend}
                      disabled={!input.trim()}
                      className="flex h-9 w-9 items-center justify-center rounded-lg bg-brand-secondary-600 text-white hover:bg-brand-secondary-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors flex-shrink-0"
                    >
                      <Icon icon="lucide:send" className="h-4 w-4" />
                    </button>
                  )}
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    )
  }

  // Right layout (default)
  return (
    <div className="flex flex-col h-full bg-brand-main-900">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-brand-main-700 px-4 py-2.5">
        <div className="flex items-center gap-2">
          <Icon
            icon="lucide:play-circle"
            className="h-4 w-4 text-brand-secondary-400"
          />
          <span className="text-sm font-medium text-white light:text-brand-main-50">
            Test Workflow
          </span>
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={clearMessages}
            className="rounded p-1 text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
            title="Clear messages"
          >
            <Icon icon="lucide:trash-2" className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={togglePanelPosition}
            className="rounded p-1 text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
            title={
              panelPosition === 'right'
                ? 'Move panel to bottom'
                : 'Move panel to right'
            }
          >
            <Icon
              icon={
                panelPosition === 'right'
                  ? 'lucide:panel-bottom'
                  : 'lucide:panel-right'
              }
              className="h-3.5 w-3.5"
            />
          </button>
          <button
            onClick={closeTestPanel}
            className="rounded p-1 text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
            title="Close"
          >
            <Icon icon="lucide:x" className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Tab selector */}
      <div className="flex border-b border-brand-main-700">
        {(['chat', 'browser', 'trace', 'nodes', 'runs'] as StudioTab[]).map(
          (t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`flex-1 py-2 text-xs font-medium transition-colors ${
                tab === t
                  ? 'text-brand-secondary-400 border-b-2 border-brand-secondary-400'
                  : 'text-brand-main-400 hover:text-brand-main-200'
              }`}
            >
              {TAB_LABELS[t]}
            </button>
          ),
        )}
      </div>

      {/* Content area */}
      <div className="flex-1 overflow-y-auto">{tabContent}</div>

      {/* Headers / metadata section (only in chat tab) */}
      {tab === 'chat' && (
        <>
          <div className="border-t border-brand-main-700">
            <button
              onClick={() => setShowHeaders(!showHeaders)}
              className="flex w-full items-center justify-between px-3 py-2 text-xs text-brand-main-400 hover:text-brand-main-200 transition-colors"
            >
              <div className="flex items-center gap-1.5">
                <Icon icon="lucide:key" className="h-3 w-3" />
                <span>Headers / Auth</span>
                {headerCount > 0 && (
                  <span className="rounded-full bg-brand-secondary-600/30 px-1.5 py-0.5 text-[10px] text-brand-secondary-400">
                    {headerCount}
                  </span>
                )}
              </div>
              <Icon
                icon="lucide:chevron-up"
                className={`h-3 w-3 transition-transform ${showHeaders ? '' : 'rotate-180'}`}
              />
            </button>

            {showHeaders &&
              renderHeaders(metadata, updateHeader, addHeader, removeHeader)}
          </div>

          {/* Input area */}
          <div className="border-t border-brand-main-700 p-3">
            <div className="flex items-end gap-2">
              <textarea
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder={isExecuting ? 'Executing...' : 'Type a message...'}
                disabled={isExecuting}
                rows={1}
                className="flex-1 resize-none rounded-lg border border-brand-main-600 bg-brand-main-800 px-3 py-2 text-sm text-white light:text-brand-main-50 placeholder-brand-main-400 focus:border-brand-secondary-500 focus:outline-none focus:ring-1 focus:ring-brand-secondary-500 disabled:opacity-50"
                style={{ maxHeight: '120px' }}
              />
              {isExecuting ? (
                <button
                  onClick={cancelExecution}
                  className="flex h-9 w-9 items-center justify-center rounded-lg bg-red-600 text-white hover:bg-red-500 transition-colors flex-shrink-0"
                  title="Stop execution"
                >
                  <Icon icon="lucide:square" className="h-4 w-4" />
                </button>
              ) : (
                <button
                  onClick={handleSend}
                  disabled={!input.trim()}
                  className="flex h-9 w-9 items-center justify-center rounded-lg bg-brand-secondary-600 text-white hover:bg-brand-secondary-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors flex-shrink-0"
                >
                  <Icon icon="lucide:send" className="h-4 w-4" />
                </button>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

function renderHeaders(
  metadata: Record<string, string>,
  updateHeader: (oldKey: string, newKey: string, newValue: string) => void,
  addHeader: () => void,
  removeHeader: (key: string) => void,
) {
  return (
    <div className="space-y-2 px-3 pb-3">
      <p className="text-[10px] text-brand-main-500">
        Key-value pairs sent as request metadata. Use this to provide API keys
        or auth tokens for the Auth node.
      </p>
      {Object.entries(metadata).map(([key, value]) => (
        <div key={key} className="flex gap-1.5 items-center">
          <input
            value={key}
            onChange={(e) => updateHeader(key, e.target.value, value)}
            placeholder="Key"
            className="flex-1 rounded border border-brand-main-600 bg-brand-main-800 px-2 py-1 text-xs text-white light:text-brand-main-50 placeholder-brand-main-500 focus:border-brand-secondary-500 focus:outline-none"
          />
          <input
            value={value}
            onChange={(e) => updateHeader(key, key, e.target.value)}
            placeholder="Value"
            type={
              key.toLowerCase().includes('key') ||
              key.toLowerCase().includes('auth') ||
              key.toLowerCase().includes('token') ||
              key.toLowerCase().includes('secret')
                ? 'password'
                : 'text'
            }
            className="flex-1 rounded border border-brand-main-600 bg-brand-main-800 px-2 py-1 text-xs text-white light:text-brand-main-50 placeholder-brand-main-500 focus:border-brand-secondary-500 focus:outline-none"
          />
          <button
            onClick={() => removeHeader(key)}
            className="p-0.5 text-brand-main-500 hover:text-red-400 light:hover:text-red-600 transition-colors"
          >
            <Icon icon="lucide:x" className="h-3 w-3" />
          </button>
        </div>
      ))}
      <button
        onClick={addHeader}
        className="flex items-center gap-1 text-[11px] text-brand-secondary-400 hover:text-brand-secondary-300 transition-colors"
      >
        <Icon icon="lucide:plus" className="h-3 w-3" />
        Add header
      </button>
    </div>
  )
}
