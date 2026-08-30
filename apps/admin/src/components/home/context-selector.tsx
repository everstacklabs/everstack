import { useState, useRef, useEffect } from 'react'
import { cn } from '@/lib/utils'
import { ChevronDown } from 'lucide-react'
import { Iconify } from '@everstack/ui/icons'

export interface ContextOption {
  id: string
  label: string
  icon: string
  description: string
}

const CONTEXT_OPTIONS: ContextOption[] = [
  { id: 'auto', label: 'Auto', icon: 'lucide:sparkles', description: 'AI decides which tools to use' },
  { id: 'agents', label: 'Agents', icon: 'lucide:bot', description: 'Create, list, configure agents' },
  { id: 'observability', label: 'Observability', icon: 'hugeicons:telescope-02', description: 'Query traces, logs, metrics' },
  { id: 'gateway', label: 'Gateway', icon: 'mingcute:route-line', description: 'Routing config, rate limits' },
  { id: 'evaluations', label: 'Evaluations', icon: 'lucide:flask-conical', description: 'Datasets, runs, scores' },
  { id: 'vault', label: 'Vault', icon: 'stash:vault', description: 'API keys, providers' },
]

interface ContextSelectorProps {
  value: string
  onChange: (value: string) => void
}

export function ContextSelector({ value, onChange }: ContextSelectorProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  const selected = CONTEXT_OPTIONS.find((o) => o.id === value) ?? CONTEXT_OPTIONS[0]

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 h-7 rounded px-2 text-[11px] font-medium border border-brand-main-700/70 bg-brand-main-900/55 text-white/70 light:text-black/70 transition-colors hover:text-white/90 light:hover:text-black/90 hover:border-brand-main-600"
      >
        <Iconify.Icon icon={selected.icon} className="size-3.5" />
        {selected.label}
        <ChevronDown className={cn('size-3 transition-transform', open && 'rotate-180')} />
      </button>

      {open && (
        <div className="absolute bottom-full left-0 mb-1 w-40 rounded border border-brand-main-600 bg-brand-main-800 py-1 shadow-xl z-50 p-1">
          {CONTEXT_OPTIONS.map((option) => (
            <button
              key={option.id}
              onClick={() => {
                onChange(option.id)
                setOpen(false)
              }}
              className={cn(
                'flex w-full items-center rounded gap-2.5 px-3 py-2 text-left transition-colors',
                option.id === value
                  ? 'bg-brand-secondary-600/15 text-brand-secondary-300'
                  : 'text-white/60 light:text-black/60 hover:bg-brand-main-700/50 hover:text-white/80 light:hover:text-black/80',
              )}
            >
              <Iconify.Icon icon={option.icon} className="size-4 shrink-0" />
              <div className="min-w-0">
                <p className="text-xs font-medium">{option.label}</p>
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
