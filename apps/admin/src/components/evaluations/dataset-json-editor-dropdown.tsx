import { Braces, ChevronDown, ChevronRight } from 'lucide-react'
import { JSONEditor } from '@/components/deployments/functions/json-editor'

type DatasetJsonEditorDropdownProps = {
  label: string
  value: string
  onChange: (value: string) => void
  open: boolean
  onOpenChange: (open: boolean) => void
  height?: string
}

function getJsonStatus(value: string): {
  label: string
  className: string
} {
  const trimmed = value.trim()
  if (!trimmed) {
    return {
      label: 'empty',
      className: 'text-white/35 light:text-black/35',
    }
  }

  try {
    JSON.parse(trimmed)
    return trimmed === '{}'
      ? {
          label: 'default',
          className: 'text-white/35 light:text-black/35',
        }
      : {
          label: 'draft',
          className: 'text-brand-secondary-300 light:text-brand-secondary-700',
        }
  } catch {
    return {
      label: 'invalid',
      className: 'text-red-400 light:text-red-600',
    }
  }
}

export function DatasetJsonEditorDropdown({
  label,
  value,
  onChange,
  open,
  onOpenChange,
  height = '220px',
}: DatasetJsonEditorDropdownProps) {
  const status = getJsonStatus(value)

  return (
    <div className="overflow-hidden rounded border border-brand-main-700/70 bg-brand-main-900/35 light:border-brand-main-200 light:bg-white">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => onOpenChange(!open)}
        className="flex h-9 w-full items-center justify-between gap-3 px-3 text-left transition-colors hover:bg-white/[0.03] light:hover:bg-black/[0.03]"
      >
        <span className="flex min-w-0 items-center gap-2">
          {open ? (
            <ChevronDown className="h-3.5 w-3.5 shrink-0 text-white/35 light:text-black/35" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 shrink-0 text-white/35 light:text-black/35" />
          )}
          <Braces className="h-3.5 w-3.5 shrink-0 text-brand-secondary-300 light:text-brand-secondary-700" />
          <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
            {label}
          </span>
        </span>
        <span className={`shrink-0 text-[10px] ${status.className}`}>
          {status.label}
        </span>
      </button>
      {open && (
        <div className="border-t border-brand-main-700/70 bg-brand-main-900/40 p-2 light:border-brand-main-200 light:bg-white">
          <JSONEditor value={value} onChange={onChange} height={height} />
        </div>
      )}
    </div>
  )
}
