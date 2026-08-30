import { useRef, useEffect, useCallback, useState } from 'react'
import { getApiBaseUrl } from '@/lib/api-url'

interface BrowserStreamViewerProps {
  sessionId: string
  /** Base64-encoded JPEG screenshot from browser.screenshot events — displayed as fallback when stream isn't delivering frames. */
  screenshotBase64?: string | null
}

/**
 * BrowserStreamViewer renders a live browser viewport via WebSocket.
 *
 * Architecture:
 *   Canvas <- binary JPEG frames <- WebSocket <- Go backend relay <- sidecar streamer <- CDP screencast
 *   Canvas -> JSON input events -> WebSocket -> Go backend relay -> sidecar streamer -> CDP Input.dispatch*
 *
 * Fallback: When the WebSocket stream isn't delivering frames, displays the latest
 * screenshot from browser.screenshot events (sent as base64 via SSE).
 *
 * The WebSocket endpoint is /v1/sandbox/{sessionId}/browser/stream
 */
export function BrowserStreamViewer({ sessionId, screenshotBase64 }: BrowserStreamViewerProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const [status, setStatus] = useState<'connecting' | 'connected' | 'error'>('connecting')
  const [statusMsg, setStatusMsg] = useState('Connecting...')
  const [frameCount, setFrameCount] = useState(0)
  const frameCountRef = useRef(0)
  const lastFrameAtRef = useRef(0) // timestamp of last received frame
  const [streamStale, setStreamStale] = useState(false) // true if no frames for 3+ seconds

  // Build WebSocket URL from the API base
  const wsUrl = useCallback(() => {
    const base = getApiBaseUrl()
    const url = new URL(`/v1/sandbox/${sessionId}/browser/stream`, base)
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    return url.toString()
  }, [sessionId])

  // Render a JPEG frame onto the canvas
  const renderFrame = useCallback((data: ArrayBuffer) => {
    const canvas = canvasRef.current
    if (!canvas) return

    const blob = new Blob([data], { type: 'image/jpeg' })
    const url = URL.createObjectURL(blob)
    const img = new Image()
    img.onload = () => {
      const ctx = canvas.getContext('2d')
      if (!ctx) return

      // Resize canvas to match frame dimensions on first frame or dimension change
      if (canvas.width !== img.width || canvas.height !== img.height) {
        canvas.width = img.width
        canvas.height = img.height
      }

      ctx.drawImage(img, 0, 0)
      URL.revokeObjectURL(url)
      frameCountRef.current++
      lastFrameAtRef.current = Date.now()
      setStreamStale(false)
      // Update React state every 10 frames to avoid excessive renders
      if (frameCountRef.current <= 1 || frameCountRef.current % 10 === 0) {
        setFrameCount(frameCountRef.current)
      }
    }
    img.onerror = () => {
      console.warn('[browser-stream] failed to decode frame, size:', data.byteLength)
      URL.revokeObjectURL(url)
    }
    img.src = url
  }, [])

  // Send input event to the backend
  const sendInput = useCallback((event: Record<string, unknown>) => {
    const ws = wsRef.current
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(JSON.stringify(event))
  }, [])

  // Get coordinates relative to the browser viewport (accounting for canvas scaling)
  const getCoords = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current
    if (!canvas) return { x: 0, y: 0 }
    const rect = canvas.getBoundingClientRect()
    const scaleX = canvas.width / rect.width
    const scaleY = canvas.height / rect.height
    return {
      x: (e.clientX - rect.left) * scaleX,
      y: (e.clientY - rect.top) * scaleY,
    }
  }, [])

  // Mouse event handlers
  const handleMouseMove = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    const { x, y } = getCoords(e)
    sendInput({ type: 'mouse', action: 'move', x, y, modifiers: getModifiers(e) })
  }, [getCoords, sendInput])

  const handleMouseDown = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    const { x, y } = getCoords(e)
    sendInput({
      type: 'mouse', action: 'down', x, y,
      button: mouseButtonName(e.button),
      clickCount: 1,
      modifiers: getModifiers(e),
    })
  }, [getCoords, sendInput])

  const handleMouseUp = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    const { x, y } = getCoords(e)
    sendInput({
      type: 'mouse', action: 'up', x, y,
      button: mouseButtonName(e.button),
      clickCount: 1,
      modifiers: getModifiers(e),
    })
  }, [getCoords, sendInput])

  const handleWheel = useCallback((e: React.WheelEvent<HTMLCanvasElement>) => {
    const { x, y } = getCoords(e)
    sendInput({
      type: 'scroll', x, y,
      deltaX: e.deltaX, deltaY: e.deltaY,
      modifiers: getModifiers(e),
    })
  }, [getCoords, sendInput])

  // Keyboard event handler — attach to the canvas container
  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLCanvasElement>) => {
    e.preventDefault()
    sendInput({
      type: 'keyboard', keyDown: true,
      key: e.key, code: e.code,
      text: e.key.length === 1 ? e.key : '',
      modifiers: getModifiers(e),
    })
  }, [sendInput])

  const handleKeyUp = useCallback((e: React.KeyboardEvent<HTMLCanvasElement>) => {
    e.preventDefault()
    sendInput({
      type: 'keyboard', keyDown: false,
      key: e.key, code: e.code,
      modifiers: getModifiers(e),
    })
  }, [sendInput])

  // Prevent context menu on the canvas
  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
  }, [])

  // Connect/reconnect WebSocket with exponential backoff
  useEffect(() => {
    let ws: WebSocket
    let reconnectTimer: ReturnType<typeof setTimeout>
    let closed = false
    let retries = 0
    const MAX_RETRIES = 10

    function connect() {
      if (closed) return
      if (retries >= MAX_RETRIES) {
        setStatus('error')
        setStatusMsg('Browser not reachable — sandbox may be stopped')
        return
      }

      const url = wsUrl()
      console.log('[browser-stream] connecting to', url, `(attempt ${retries + 1})`)
      setStatus('connecting')
      setStatusMsg(retries > 0 ? `Reconnecting (${retries}/${MAX_RETRIES})...` : 'Connecting...')

      ws = new WebSocket(url)
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws

      ws.onopen = () => {
        console.log('[browser-stream] connected, waiting for frames...')
        retries = 0
        setStatus('connected')
        setStatusMsg('Connected, waiting for frames...')
      }

      ws.onmessage = (e) => {
        if (e.data instanceof ArrayBuffer) {
          if (frameCountRef.current === 0) {
            console.log('[browser-stream] first frame received, size:', e.data.byteLength)
          }
          renderFrame(e.data)
        } else {
          console.log('[browser-stream] received non-binary message:', typeof e.data, e.data)
        }
      }

      ws.onclose = (e) => {
        console.log('[browser-stream] closed, code:', e.code, 'reason:', e.reason)
        setStatus('connecting')
        wsRef.current = null
        if (!closed) {
          retries++
          const delay = Math.min(2000 * Math.pow(1.5, retries - 1), 15000)
          setStatusMsg(`Connection lost (code ${e.code}). Retry ${retries}/${MAX_RETRIES} in ${(delay / 1000).toFixed(1)}s...`)
          reconnectTimer = setTimeout(connect, delay)
        }
      }

      ws.onerror = () => {
        // onerror doesn't provide useful info — the close event has the details
      }
    }

    connect()

    return () => {
      closed = true
      clearTimeout(reconnectTimer)
      if (ws) ws.close()
      wsRef.current = null
    }
  }, [wsUrl, renderFrame])

  // Detect stale stream — if connected but no frames for 3+ seconds, show fallback
  useEffect(() => {
    const interval = setInterval(() => {
      if (lastFrameAtRef.current > 0 && Date.now() - lastFrameAtRef.current > 3000) {
        setStreamStale(true)
      }
    }, 1000)
    return () => clearInterval(interval)
  }, [])

  const hasStreamFrames = frameCount > 0 && !streamStale
  const showOverlay = status !== 'connected' || !hasStreamFrames
  // Show screenshot fallback when stream isn't delivering frames or went stale
  const showScreenshotFallback = !hasStreamFrames && screenshotBase64

  return (
    <div className="relative bg-black w-full h-full">
      {/* Screenshot fallback — displayed when WebSocket stream isn't delivering frames */}
      {showScreenshotFallback && (
        <img
          src={`data:image/jpeg;base64,${screenshotBase64}`}
          alt="Browser screenshot"
          className="absolute inset-0 w-full h-full object-contain z-[5]"
        />
      )}

      {/* Connection status overlay — visible until first stream frame (hidden when screenshot fallback is shown) */}
      {showOverlay && !showScreenshotFallback && (
        <div className="absolute inset-0 flex items-center justify-center z-10 bg-zinc-900">
          <div className="text-center px-4">
            {status === 'error' ? (
              <div className="w-5 h-5 rounded-full bg-red-500/20 flex items-center justify-center mx-auto mb-2">
                <span className="text-red-400 text-xs">!</span>
              </div>
            ) : (
              <div className="w-5 h-5 border-2 border-zinc-600 border-t-zinc-300 rounded-full animate-spin mx-auto mb-2" />
            )}
            <p className="text-xs text-zinc-400">{statusMsg}</p>
            <p className="text-[10px] text-zinc-600 mt-1 font-mono">
              {sessionId.slice(0, 20)}... | frames: {frameCount}
            </p>
          </div>
        </div>
      )}

      {/* Status badge over screenshot fallback */}
      {showScreenshotFallback && (
        <div className="absolute top-2 right-2 z-10 bg-black/60 text-[10px] text-zinc-400 px-2 py-0.5 rounded font-mono">
          {streamStale ? 'stream stale' : status === 'connected' ? 'stream: no frames' : status} | screenshot
        </div>
      )}

      <canvas
        ref={canvasRef}
        className="w-full h-full object-contain cursor-default"
        tabIndex={0}
        onMouseMove={handleMouseMove}
        onMouseDown={handleMouseDown}
        onMouseUp={handleMouseUp}
        onWheel={handleWheel}
        onKeyDown={handleKeyDown}
        onKeyUp={handleKeyUp}
        onContextMenu={handleContextMenu}
        style={{ outline: 'none' }}
      />
    </div>
  )
}

// Helper: convert DOM mouse button number to CDP button name
function mouseButtonName(button: number): string {
  switch (button) {
    case 0: return 'left'
    case 1: return 'middle'
    case 2: return 'right'
    default: return 'left'
  }
}

// Helper: build CDP modifier bitmask from DOM event
function getModifiers(e: { altKey: boolean; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean }): number {
  let m = 0
  if (e.altKey) m |= 1
  if (e.ctrlKey) m |= 2
  if (e.metaKey) m |= 4
  if (e.shiftKey) m |= 8
  return m
}
