import { useState, useRef, useEffect } from 'react'
import { cn } from '@/lib/utils'
import { ChevronDown } from 'lucide-react'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import { getProviderAsset } from '@/components/providers/provider-icon'
import { Iconify } from '@everstack/ui/icons'

interface ModelSelectorProps {
  value: string
  onChange: (value: string) => void
}

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

function getProviderForModel(model: string, providers: { provider: string; models: string[] }[]): string {
  for (const p of providers) {
    if (p.models.includes(model)) return p.provider
  }
  return ''
}

export function ModelSelector({ value, onChange }: ModelSelectorProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const { data: providers } = useGatewayModels()

  const currentProvider = providers ? getProviderForModel(value, providers) : ''

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  // Truncate model name for display
  const displayName = value.length > 24 ? value.slice(0, 22) + '...' : value

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-[12px] font-medium border border-brand-main-600 text-white/50 light:text-black/50 transition-colors hover:text-white/70 light:hover:text-black/70 hover:bg-brand-main-800/50"
      >
        {currentProvider && <ProviderLogo provider={currentProvider} className="size-3.5" />}
        {displayName || 'Select model'}
        <ChevronDown className={cn('size-3 transition-transform', open && 'rotate-180')} />
      </button>

      {open && (
        <div className="absolute bottom-full right-0 mb-1 w-72 max-h-80 overflow-y-auto rounded-lg border border-brand-main-600 bg-brand-main-800 shadow-xl z-50 scrollbar-macos">
          {!providers?.length && (
            <p className="px-3 py-4 text-center text-xs text-white/30 light:text-black/30">No models configured</p>
          )}
          {providers?.map((group) => (
            <div key={group.provider}>
              {/* Provider header */}
              <div className="flex items-center gap-2 px-3 py-1.5 text-[10px] font-medium uppercase tracking-wider text-white/30 light:text-black/30 sticky top-0 bg-brand-main-800">
                <ProviderLogo provider={group.provider} className="size-3.5 opacity-60" />
                {group.provider}
              </div>
              {/* Models */}
              {group.models.map((model) => (
                <button
                  key={model}
                  onClick={() => {
                    onChange(model)
                    setOpen(false)
                  }}
                  className={cn(
                    'flex w-full items-center gap-2.5 px-3 py-1.5 text-left transition-colors',
                    model === value
                      ? 'bg-brand-secondary-600/15 text-brand-secondary-300'
                      : 'text-white/60 light:text-black/60 hover:bg-brand-main-700 hover:text-white/80 light:hover:text-black/80',
                  )}
                >
                  <ProviderLogo provider={group.provider} className="size-3.5 shrink-0" />
                  <span className="text-xs truncate">{model}</span>
                </button>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
