import { useState, useCallback, useRef } from 'react'
import { ArrowRight } from 'lucide-react'
import { Button } from '@everstack/ui/components'
import { ContextSelector } from './context-selector'

interface PromptBarProps {
  onSubmit: (text: string, context: string) => void
  placeholder?: string
  disabled?: boolean
}

export function PromptBar({ onSubmit, placeholder, disabled }: PromptBarProps) {
  const [value, setValue] = useState('')
  const [context, setContext] = useState('auto')
  const inputRef = useRef<HTMLTextAreaElement>(null)

  const handleSubmit = useCallback(() => {
    const text = value.trim()
    if (!text || disabled) return
    onSubmit(text, context)
    setValue('')
  }, [value, context, disabled, onSubmit])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        handleSubmit()
      }
    },
    [handleSubmit],
  )

  return (
    <div className="w-full rounded-lg border border-brand-main-600 bg-brand-main-800/60 transition-colors focus-within:border-brand-main-500 focus-within:ring-1 focus-within:ring-brand-secondary-500/30">
      <textarea
        ref={inputRef}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={placeholder ?? 'What can I help you with?'}
        disabled={disabled}
        rows={1}
        className="w-full resize-none bg-transparent px-4 pt-3 pb-1 text-sm text-white light:text-brand-main-50 placeholder:text-white/30 light:placeholder:text-black/30 focus:outline-none"
      />
      <div className="flex items-center justify-between px-2 pb-2">
        <ContextSelector value={context} onChange={setContext} />
        <Button
          size="xs"
          variant={value.trim() ? 'default' : 'ghost'}
          onClick={handleSubmit}
          disabled={!value.trim() || disabled}
          className="rounded-md"
        >
          <ArrowRight className="size-3.5" />
        </Button>
      </div>
    </div>
  )
}
