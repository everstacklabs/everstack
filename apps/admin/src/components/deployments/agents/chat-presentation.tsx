import type { KeyboardEvent, ReactNode } from 'react'
import { ArrowUp, Loader2 } from 'lucide-react'
import { cn } from '@/lib/utils'
import { ui } from '@everstack/ui'

const { Button, Textarea } = ui

export function ChatTranscript({ children }: { children: ReactNode }) {
  return (
    <div className="mx-auto max-w-3xl space-y-6 pl-6 pr-3 py-6">{children}</div>
  )
}

export function ChatUserMessage({ children }: { children: ReactNode }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[80%] rounded-md bg-brand-secondary-600/20 px-4 pb-2 pt-1.5 text-brand-secondary-200">
        {children}
      </div>
    </div>
  )
}

export function ChatAssistantMessage({ children }: { children: ReactNode }) {
  return <div className="min-w-0">{children}</div>
}

export function ChatComposerFrame({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'relative rounded border border-brand-main-600 bg-brand-main-950 transition-colors focus-within:ring-1 focus-within:ring-brand-secondary-500',
        className,
      )}
    >
      {children}
    </div>
  )
}

export function shouldSubmitChatKey(key: string, shiftKey: boolean) {
  return key === 'Enter' && !shiftKey
}

export function ChatComposer({
  id,
  value,
  onChange,
  onSubmit,
  placeholder,
  disabled,
  submitting,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  onSubmit: () => void
  placeholder: string
  disabled?: boolean
  submitting?: boolean
}) {
  const canSend = !!value.trim() && !disabled && !submitting
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (
      !shouldSubmitChatKey(event.key, event.shiftKey) ||
      event.nativeEvent.isComposing
    )
      return
    event.preventDefault()
    if (canSend) onSubmit()
  }
  return (
    <ChatComposerFrame>
      <Textarea
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={onKeyDown}
        disabled={disabled}
        placeholder={placeholder}
        rows={3}
        aria-label={placeholder}
        className="min-h-20 resize-none border-0 bg-transparent px-3 py-3 shadow-none focus-visible:ring-0"
      />
      <div className="flex items-center justify-end px-2 pb-2">
        <Button
          size="xs"
          type="button"
          onClick={onSubmit}
          disabled={!canSend}
          aria-label="Send message"
          title="Send message"
        >
          {submitting ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <ArrowUp className="size-4" />
          )}
        </Button>
      </div>
    </ChatComposerFrame>
  )
}
