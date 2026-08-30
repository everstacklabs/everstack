import { useState } from 'react'
import { Input, Label, Switch, Button, Badge } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import type { CORSConfig } from './types'

interface CORSFormProps {
  config: CORSConfig
  onChange: (config: CORSConfig) => void
}

const COMMON_METHODS = ['GET', 'POST', 'PUT', 'DELETE', 'OPTIONS', 'PATCH', 'HEAD']
const COMMON_HEADERS = ['*', 'Content-Type', 'Authorization', 'X-Request-ID', 'X-API-Key']

function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col">
      <Label className="text-brand-main-300 text-xs mb-1.5">{label}</Label>
      {children}
      {hint ? (
        <p className="text-[11px] text-brand-main-300 mt-1.5">{hint}</p>
      ) : (
        <div className="h-[18px]" />
      )}
    </div>
  )
}

function TagInput({
  label,
  hint,
  values,
  onChange,
  suggestions,
}: {
  label: string
  hint?: string
  values: string[]
  onChange: (values: string[]) => void
  suggestions?: string[]
}) {
  const [inputValue, setInputValue] = useState('')

  const addValue = (value: string) => {
    const trimmed = value.trim()
    if (trimmed && !values.includes(trimmed)) {
      onChange([...values, trimmed])
    }
    setInputValue('')
  }

  const removeValue = (value: string) => {
    onChange(values.filter((v) => v !== value))
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault()
      addValue(inputValue)
    }
  }

  return (
    <div className="flex flex-col">
      <Label className="text-brand-main-300 text-xs mb-1.5">{label}</Label>
      <div className="flex flex-wrap gap-1.5 min-h-[36px] p-2 bg-brand-main-700/50 border border-brand-main-500 rounded-md">
        {values.map((value) => (
          <Badge
            key={value}
            variant="secondary"
            className="flex items-center gap-1 bg-brand-main-600 text-brand-main-50"
          >
            {value}
            <button
              type="button"
              onClick={() => removeValue(value)}
              className="hover:text-red-400 light:hover:text-red-600 transition-colors"
            >
              <Iconify.Icon icon="mdi:close" className="h-3 w-3" />
            </button>
          </Badge>
        ))}
        <Input
          value={inputValue}
          onChange={(e) => setInputValue(e.target.value)}
          onKeyDown={handleKeyDown}
          onBlur={() => inputValue && addValue(inputValue)}
          placeholder="Type and press Enter..."
          className="flex-1 min-w-[120px] border-0 bg-transparent p-0 h-6 text-sm focus-visible:ring-0"
        />
      </div>
      {suggestions && suggestions.length > 0 && (
        <div className="flex flex-wrap gap-1 mt-1">
          {suggestions
            .filter((s) => !values.includes(s))
            .slice(0, 5)
            .map((suggestion) => (
              <Button
                key={suggestion}
                type="button"
                variant="ghost"
                size="sm"
                className="h-5 px-1.5 text-[10px] text-brand-main-300 hover:text-white light:hover:text-brand-main-50"
                onClick={() => addValue(suggestion)}
              >
                + {suggestion}
              </Button>
            ))}
        </div>
      )}
      {hint && (
        <p className="text-[11px] text-brand-main-300 mt-1.5">{hint}</p>
      )}
    </div>
  )
}

export function CORSForm({ config, onChange }: CORSFormProps) {
  const updateConfig = (updates: Partial<CORSConfig>) => {
    onChange({ ...config, ...updates })
  }

  return (
    <div>
      {/* Enable Toggle */}
      <div className="flex items-center justify-between py-2.5">
        <div>
          <Label className="text-white light:text-brand-main-50 text-sm">Enable CORS</Label>
          <p className="text-xs text-brand-main-200 mt-0.5">
            Allow cross-origin requests
          </p>
        </div>
        <Switch
          checked={config.enabled}
          onCheckedChange={(enabled) => updateConfig({ enabled })}
        />
      </div>

      {config.enabled && (
        <div className="space-y-4 pt-2">
          <TagInput
            label="Allowed Origins"
            hint='Use "*" to allow all origins'
            values={config.allowedOrigins ?? []}
            onChange={(allowedOrigins) => updateConfig({ allowedOrigins })}
            suggestions={['*', 'http://localhost:3000', 'https://example.com']}
          />
          <TagInput
            label="Allowed Methods"
            values={config.allowedMethods ?? []}
            onChange={(allowedMethods) => updateConfig({ allowedMethods })}
            suggestions={COMMON_METHODS}
          />
          <TagInput
            label="Allowed Headers"
            hint='Use "*" to allow all headers'
            values={config.allowedHeaders ?? []}
            onChange={(allowedHeaders) => updateConfig({ allowedHeaders })}
            suggestions={COMMON_HEADERS}
          />
          <TagInput
            label="Exposed Headers"
            hint="Headers accessible by the client"
            values={config.exposedHeaders ?? []}
            onChange={(exposedHeaders) => updateConfig({ exposedHeaders })}
            suggestions={['X-Request-ID', 'X-RateLimit-Remaining']}
          />
          <div className="grid grid-cols-2 gap-4">
            <Field label="Preflight Max Age" hint="Seconds to cache preflight responses">
              <Input
                value={config.maxAge ?? ''}
                onChange={(e) => updateConfig({ maxAge: e.target.value })}
                placeholder="3600"
                className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
              />
            </Field>
          </div>
          <div className="flex items-center justify-between py-2.5 border-t border-brand-main-600/30">
            <div>
              <Label className="text-white light:text-brand-main-50 text-sm">Allow Credentials</Label>
              <p className="text-xs text-brand-main-200 mt-0.5">
                Allow cookies and auth headers
              </p>
            </div>
            <Switch
              checked={config.allowCredentials}
              onCheckedChange={(allowCredentials) =>
                updateConfig({ allowCredentials })
              }
            />
          </div>
        </div>
      )}
    </div>
  )
}
