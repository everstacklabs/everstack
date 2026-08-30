import { useRef, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// Mustache-highlighting textarea. A transparent <textarea> sits over a
// highlight layer that re-renders the same text with {{vars}} colored in the
// catppuccin-mocha palette (the same theme lib/shiki.ts uses), so template
// variables read the way they do in code blocks. Native textarea behavior
// (caret, selection, undo) is preserved because the real input is the textarea.

// catppuccin-mocha: mauve (var name) + lavender (braces), text (plain).
const MAUVE = '#cba6f7'
const LAVENDER = '#b4befe'

const MUSTACHE_RE = /(\{\{\s*[\w.]+\s*\}\})/g

const SHARED_TEXT =
  'w-full font-mono text-xs leading-[1.6] whitespace-pre-wrap break-words'

const DEFAULT_VARS = ['input', 'output', 'expected_output', 'context', 'metadata']

function renderHighlighted(value: string): ReactNode[] {
  const out: ReactNode[] = []
  const parts = value.split(MUSTACHE_RE)
  parts.forEach((part, i) => {
    if (i % 2 === 1) {
      // token like {{ input }}
      const inner = part.replace(/^\{\{\s*|\s*\}\}$/g, '')
      out.push(
        <span key={i}>
          <span style={{ color: LAVENDER }}>{'{{'}</span>
          <span style={{ color: MAUVE }}>{inner}</span>
          <span style={{ color: LAVENDER }}>{'}}'}</span>
        </span>,
      )
    } else if (part) {
      out.push(<span key={i}>{part}</span>)
    }
  })
  // trailing newline so the highlight layer keeps height on a final empty line
  out.push(<span key="tail">{'\n'}</span>)
  return out
}

export function MustacheTextarea({
  value,
  onChange,
  placeholder,
  rows = 8,
  vars = DEFAULT_VARS,
  showVarChips = true,
  className,
}: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  rows?: number
  vars?: string[]
  showVarChips?: boolean
  className?: string
}) {
  const taRef = useRef<HTMLTextAreaElement>(null)
  const highlightRef = useRef<HTMLDivElement>(null)

  const syncScroll = () => {
    if (highlightRef.current && taRef.current) {
      highlightRef.current.scrollTop = taRef.current.scrollTop
      highlightRef.current.scrollLeft = taRef.current.scrollLeft
    }
  }

  const insertVar = (name: string) => {
    const ta = taRef.current
    const token = `{{${name}}}`
    if (!ta) {
      onChange(value + token)
      return
    }
    const start = ta.selectionStart ?? value.length
    const end = ta.selectionEnd ?? value.length
    const next = value.slice(0, start) + token + value.slice(end)
    onChange(next)
    requestAnimationFrame(() => {
      ta.focus()
      const pos = start + token.length
      ta.setSelectionRange(pos, pos)
    })
  }

  return (
    <div>
      <div
        className={cn(
          'relative rounded border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-white',
          className,
        )}
      >
        <div
          ref={highlightRef}
          aria-hidden
          className={cn(
            SHARED_TEXT,
            'pointer-events-none absolute inset-0 overflow-hidden px-3 py-2 text-white/85 light:text-black/85',
          )}
        >
          {renderHighlighted(value)}
        </div>
        <textarea
          ref={taRef}
          value={value}
          placeholder={placeholder}
          rows={rows}
          spellCheck={false}
          onScroll={syncScroll}
          onChange={(e) => onChange(e.target.value)}
          className={cn(
            SHARED_TEXT,
            'relative resize-y bg-transparent px-3 py-2 text-transparent caret-white outline-none placeholder:text-white/25 light:caret-black light:placeholder:text-black/30',
          )}
        />
      </div>
      {showVarChips && (
        <div className="mt-1.5 flex flex-wrap items-center gap-1">
          <span className="text-[10px] uppercase tracking-wide text-white/30 light:text-black/35">
            Insert
          </span>
          {vars.map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => insertVar(v)}
              className="rounded border border-brand-main-700 bg-brand-main-900 px-1.5 py-0.5 font-mono text-[10px] text-brand-secondary-300 transition-colors hover:border-brand-secondary-500/60 light:border-brand-main-200 light:bg-white light:text-brand-secondary-700"
            >
              {`{{${v}}}`}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
