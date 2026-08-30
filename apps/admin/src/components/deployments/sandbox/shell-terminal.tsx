import { useEffect, useRef, useCallback } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { useSandboxShell } from '@/hooks/deployments/use-sandbox'
import { ShellStatusPanel } from './shell-status-panel'
import '@xterm/xterm/css/xterm.css'

interface ShellTerminalProps {
    image: string
    sandboxId: string
    sessionId: string
    // Optional: bubble the actually-attached shell session id up to a
    // parent (used by the multi-tab strip to highlight the correct
    // tab after the gateway assigns an id on a fresh "+ New shell"
    // attach). Don't use this to drive the terminal's key — that
    // would cause a remount loop. It's a one-way notification.
    onShellSessionResolved?: (shellSessionId: string) => void
}

export function ShellTerminal({ image, sandboxId, sessionId, onShellSessionResolved }: ShellTerminalProps) {
    const termRef = useRef<HTMLDivElement>(null)
    const terminalRef = useRef<Terminal | null>(null)
    const fitAddonRef = useRef<FitAddon | null>(null)
    const {
        connect,
        send,
        resize,
        disconnect,
        isConnected,
        isReconnecting,
        isGone,
        isRecovering,
        // Needed by the onShellSessionResolved bubble-up effect below.
        // PR #223 trimmed this destructure for the slim status panel;
        // PR #228 (rebased on top of #223) introduced the effect but
        // the rebase didn't re-add the field — textual clean merge,
        // semantic break, broken build on master.
        shellSessionId,
    } = useSandboxShell(sandboxId)

    // The hook auto-reconnects on drop with exponential backoff and
    // preserves the persistent shell session, so the manual button
    // mostly exists as an escape hatch when the user explicitly
    // wants a fresh attempt without waiting out the backoff. We keep
    // the xterm buffer untouched so reconnect feels seamless: the
    // server replays scrollback via tmux when the new attach lands.
    const handleReconnect = useCallback(() => {
        disconnect()
        // No inline marker — the status panel shows Reconnecting /
        // Connected as it transitions. See the connect() block below
        // for the rationale on keeping xterm purely user-content.
        setTimeout(() => {
            connect((data) => terminalRef.current?.write(data))
            terminalRef.current?.focus()
        }, 200)
    }, [connect, disconnect])

    // Bubble the connected session id up to a parent on every change.
    // Skip when empty — that means "not yet resolved" and the parent
    // already knows that (it's how the empty-string sentinel works).
    // Stash the callback in a ref so a parent that passes an inline
    // lambda doesn't retrigger the effect on every render.
    const onShellSessionResolvedRef = useRef(onShellSessionResolved)
    useEffect(() => {
        onShellSessionResolvedRef.current = onShellSessionResolved
    })
    useEffect(() => {
        if (shellSessionId) {
            onShellSessionResolvedRef.current?.(shellSessionId)
        }
    }, [shellSessionId])

    useEffect(() => {
        if (!termRef.current) return

        const terminal = new Terminal({
            cursorBlink: true,
            fontSize: 13,
            fontFamily: 'Menlo, Monaco, "Courier New", monospace',
            theme: {
                background: '#0d1117',
                foreground: '#c9d1d9',
                cursor: '#c9d1d9',
            },
            scrollback: 5000,
        })

        const fitAddon = new FitAddon()
        const webLinksAddon = new WebLinksAddon()
        terminal.loadAddon(fitAddon)
        terminal.loadAddon(webLinksAddon)

        terminal.open(termRef.current)

        terminalRef.current = terminal
        fitAddonRef.current = fitAddon

        // Startup banner — Everstack wordmark in the figlet "standard" font.
        const banner = [
            '  _______     _______ ____  ____ _____  _    ____ _  __',
            ' | ____\\ \\   / / ____|  _ \\/ ___|_   _|/ \\  / ___| |/ /',
            ' |  _|  \\ \\ / /|  _| | |_) \\___ \\ | | / _ \\| |   | \' / ',
            ' | |___  \\ V / | |___|  _ < ___) || |/ ___ \\ |___| . \\ ',
            ' |_____|  \\_/  |_____|_| \\_\\____/ |_/_/   \\_\\____|_|\\_\\',
        ]
        terminal.write('\x1b[38;5;141m' + banner.join('\r\n') + '\x1b[0m\r\n')
        terminal.write('\r\n')
        terminal.write(' \x1b[38;5;245mSandbox Shell \x1b[38;5;141m' + image + '\x1b[38;5;240m ·\x1b[38;5;245m Session \x1b[38;5;141m' + sessionId + '\x1b[0m\r\n')
        terminal.write(' \x1b[38;5;240m═════════════════════════════════════════════════════════════════════════════════\x1b[0m\r\n')
        terminal.write('\r\n')

        // Delay the initial fit until after the browser has computed layout.
        // Use double-rAF: the first frame may still have pending flex/grid
        // layout calculations; the second frame ensures the container has its
        // final dimensions so fit() computes correct rows/cols and the hidden
        // textarea is properly sized for keyboard focus.
        requestAnimationFrame(() => {
            requestAnimationFrame(() => {
                try {
                    fitAddon.fit()
                } catch {
                    // container may still have 0 dimensions; ResizeObserver will retry
                }
                terminal.focus()
            })
        })

        // Forward terminal input to WebSocket
        terminal.onData((data) => send(data))
        terminal.onResize(({ rows, cols }) => resize(rows, cols))

        // Connect WebSocket. We deliberately do NOT write a
        // "[connection dropped]" line into the xterm buffer on close
        // anymore — the status panel above the terminal already shows
        // Connected / Reconnecting… / Disconnected as the source of
        // truth, and an inline write that doesn't get cleared on
        // reconnect produces the worst-of-both-worlds confusion where
        // the panel says "Connected" but the terminal body still
        // displays stale "[connection dropped]" text.
        //
        // The actual "send my geometry so tmux redraws on attach"
        // handshake is driven by a separate effect below, keyed on
        // isConnected.
        connect((data) => terminal.write(data))

        // Handle container resize
        const observer = new ResizeObserver(() => {
            try {
                fitAddon.fit()
            } catch {
                // ignore fit errors during unmount
            }
        })
        observer.observe(termRef.current)

        return () => {
            observer.disconnect()
            terminal.dispose()
            disconnect()
            terminalRef.current = null
            fitAddonRef.current = null
        }
    }, [sandboxId]) // eslint-disable-line react-hooks/exhaustive-deps

    // Every time the WebSocket transitions to connected — initial
    // attach, auto-reconnect after a drop, or a fresh attach after
    // remount — push the current terminal geometry to the server.
    // Without this, a reattach to an idle tmux pane sits blank
    // because tmux is waiting for the new client's size before
    // redrawing, and the pane has no output of its own to send. The
    // previous "resize on first data" path silently failed in that
    // exact case (the most common one for reattach!).
    //
    // The resize itself drives the redraw: tmux receives SIGWINCH,
    // redraws its pane buffer at the new size, and broadcasts the
    // ANSI sequence to the just-attached client. No synthetic input
    // needed — sending Ctrl-L or similar would be intrusive for
    // users mid-vim / mid-cat / mid-anything-interactive.
    useEffect(() => {
        if (!isConnected) return
        const term = terminalRef.current
        if (!term) return
        if (term.rows <= 0 || term.cols <= 0) return
        resize(term.rows, term.cols)
    }, [isConnected, resize])

    return (
        <div className="flex flex-col h-full">
            <ShellStatusPanel
                isConnected={isConnected}
                isReconnecting={isReconnecting}
                isGone={isGone}
                isRecovering={isRecovering}
                onReconnect={handleReconnect}
            />

            {/* Terminal container. The padding lives on this WRAPPER, not
                on the xterm target: FitAddon measures its target element
                to compute rows/cols, and padding on that element made it
                overcount, clipping the bottom lines and breaking
                scroll-to-bottom. The inner div is a clean, full-size
                surface xterm owns; min-h-0 lets the flex child shrink so
                the xterm viewport gets a real height (and thus a working
                scrollbar / wheel scroll through the 5000-line scrollback).
                The click handler re-focuses the terminal anywhere in the
                area, including the padding gutter. */}
            <div
                className="flex-1 min-h-0 bg-[#0d1117] p-2 overflow-hidden"
                onClick={() => terminalRef.current?.focus()}
            >
                <div ref={termRef} className="h-full w-full" />
            </div>
        </div>
    )
}
