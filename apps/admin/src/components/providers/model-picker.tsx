import { useState, useRef, useEffect, useMemo } from 'react'
import { cn } from '@/lib/utils'
import { ChevronDown, Search, Check } from 'lucide-react'
import { Iconify } from '@everstack/ui/icons'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import { getProviderAsset, formatProviderName } from './provider-icon'

function ProviderLogo({ provider, className }: { provider: string; className?: string }) {
  const asset = getProviderAsset(provider)
  if (asset.type === 'image') {
    return (
      <img
        src={asset.value}
        alt={provider}
        className={cn('object-contain', asset.light && 'brightness-0 invert', className)}
      />
    )
  }
  return <Iconify.Icon icon={asset.value} className={className} />
}

interface ModelPickerProps {
  value: string
  onChange: (model: string) => void
  variant?: 'default' | 'compact'
  className?: string
  placeholder?: string
}

export function ModelPicker({
  value,
  onChange,
  variant = 'default',
  className,
  placeholder = 'Select model',
}: ModelPickerProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const { data: providers, isLoading } = useGatewayModels()

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
        setSearch('')
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  useEffect(() => {
    if (open) searchRef.current?.focus()
  }, [open])

  const currentProvider = useMemo(() => {
    if (!providers || !value) return ''
    for (const p of providers) {
      if (p.models.includes(value)) return p.provider
    }
    return ''
  }, [providers, value])

  const filteredProviders = useMemo(() => {
    if (!providers) return []
    if (!search) return providers
    const q = search.toLowerCase()
    return providers
      .map((group) => ({
        ...group,
        models: group.models.filter(
          (m) => m.toLowerCase().includes(q) || group.provider.toLowerCase().includes(q),
        ),
      }))
      .filter((group) => group.models.length > 0)
  }, [providers, search])

  const displayName = value
    ? value.length > 24 ? value.slice(0, 22) + '...' : value
    : placeholder

  const isCompact = variant === 'compact'

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        className={cn(
          'flex items-center gap-1.5 font-medium transition-colors',
          isCompact
            ? 'h-7 rounded px-2 text-[11px] border border-brand-main-700/70 bg-brand-main-900/55 text-white/70 light:text-black/70 hover:text-white/90 light:hover:text-black/90 hover:border-brand-main-600'
            : 'rounded px-3 py-1.5 text-xs border border-brand-main-600 bg-brand-main-900/50 text-white/70 light:text-black/70 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-800',
          className,
        )}
      >
        {currentProvider && <ProviderLogo provider={currentProvider} className="size-3.5 shrink-0" />}
        <span className="truncate max-w-[120px]">{displayName}</span>
        <ChevronDown className={cn('size-3 shrink-0 transition-transform', open && 'rotate-180')} />
      </button>

      {open && (
        <div className="absolute bottom-full right-0 mb-1 w-auto max-h-80 rounded border border-brand-main-600 bg-brand-main-800 shadow-xl z-50 flex flex-col">
          {/* Search */}
          <div className="flex items-center gap-2 px-2.5 py-2 border-b border-brand-main-700">
            <Search className="size-3.5 text-white/30 light:text-black/30 shrink-0" />
            <input
              ref={searchRef}
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search models..."
              className="flex-1 bg-transparent text-xs text-white light:text-brand-main-50 placeholder:text-white/25 light:placeholder:text-black/25 focus:outline-none"
            />
          </div>

          {/* Model list */}
          <div className="flex-1 overflow-y-auto overflow-x-hidden [&::-webkit-scrollbar]:w-[3px] [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-transparent hover:[&::-webkit-scrollbar-thumb]:bg-white/15 [&::-webkit-scrollbar-track]:bg-transparent">
            {isLoading && (
              <p className="px-3 py-4 text-center text-xs text-white/30 light:text-black/30">Loading models...</p>
            )}

            {!isLoading && filteredProviders.length === 0 && (
              <div className="px-3 py-4 text-center space-y-1">
                <p className="text-xs text-white/30 light:text-black/30">
                  {providers?.length === 0 ? 'No providers configured' : 'No models match'}
                </p>
                {providers?.length === 0 && (
                  <a href="/vault/llm-providers" className="text-[11px] text-brand-secondary-400 hover:text-brand-secondary-300">
                    Connect a provider
                  </a>
                )}
              </div>
            )}

            {filteredProviders.map((group) => (
              <div key={group.provider}>
                <div className="flex items-center gap-2 px-2 py-1 text-[10px] font-medium uppercase tracking-wider text-white/30 light:text-black/30 sticky top-0 bg-brand-main-800">
                  <ProviderLogo provider={group.provider} className="size-3.5 opacity-60" />
                  {formatProviderName(group.provider)}
                </div>
                {group.models.map((model) => (
                  <button
                    key={model}
                    onClick={() => {
                      onChange(model)
                      setOpen(false)
                      setSearch('')
                    }}
                    className={cn(
                      'flex w-full items-center gap-2 px-2 py-1.5 rounded text-left transition-colors',
                      model === value
                        ? 'bg-brand-secondary-600/15 text-brand-secondary-300'
                        : 'text-white/60 light:text-black/60 hover:bg-brand-main-700 hover:text-white/80 light:hover:text-black/80',
                    )}
                  >
                    <ProviderLogo provider={group.provider} className="size-3.5 shrink-0" />
                    <span className="text-xs truncate flex-1 min-w-0">{model}</span>
                    {model === value && <Check className="size-3 shrink-0 text-brand-secondary-400" />}
                  </button>
                ))}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
