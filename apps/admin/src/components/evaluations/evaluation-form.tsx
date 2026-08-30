import type { ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { ui } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'

const { Collapsible, CollapsibleContent, CollapsibleTrigger, Label } = ui

export const evaluationSheetContentClass =
  'flex h-[100vh] w-full flex-col overflow-hidden'

export const evaluationSheetBodyClass =
  'flex-1 space-y-5 overflow-y-auto px-6 py-5 scrollbar-macos'

export const evaluationSheetFooterClass =
  'flex-row items-center justify-end gap-2 px-6'

export const evaluationSheetSplitFooterClass =
  'flex-row items-center justify-between gap-2 px-6'

export const evaluationInputClass =
  'h-9 border-brand-main-600 bg-brand-main-900/60 text-sm text-white placeholder:text-white/35 focus-visible:border-brand-secondary-500 focus-visible:ring-brand-secondary-500/40 focus-visible:ring-[1px] light:bg-white light:text-brand-main-50 light:placeholder:text-black/35'

export const evaluationTextareaClass =
  'min-h-24 border-brand-main-600 bg-brand-main-900/60 text-sm text-white placeholder:text-white/35 focus-visible:border-brand-secondary-500 focus-visible:ring-brand-secondary-500/40 focus-visible:ring-[1px] light:bg-white light:text-brand-main-50 light:placeholder:text-black/35'

export const evaluationSelectTriggerClass =
  'h-9 w-full border-brand-main-600 bg-brand-main-900/60 text-sm text-zinc-200 light:bg-white light:text-zinc-800'

export const evaluationSelectContentClass =
  'border-brand-main-600 bg-brand-main-900 text-zinc-200 light:bg-white light:text-zinc-800'

export const evaluationErrorClass =
  'rounded border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600'

export const evaluationPanelClass =
  'rounded border border-brand-main-700/70 bg-brand-main-900/40 light:border-black/10 light:bg-white'

type EvaluationFieldProps = {
  label: ReactNode
  htmlFor?: string
  action?: ReactNode
  children: ReactNode
  className?: string
}

export function EvaluationField({
  label,
  htmlFor,
  action,
  children,
  className,
}: EvaluationFieldProps) {
  return (
    <div className={cn('space-y-2', className)}>
      <div className="flex min-h-5 items-center justify-between gap-3">
        <Label
          htmlFor={htmlFor}
          className="text-sm font-normal text-white light:text-brand-main-50"
        >
          {label}
        </Label>
        {action}
      </div>
      {children}
    </div>
  )
}

type EvaluationInlineActionProps = {
  children: ReactNode
  onClick?: () => void
}

export function EvaluationInlineAction({
  children,
  onClick,
}: EvaluationInlineActionProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="shrink-0 text-xs text-brand-secondary-300 transition-colors hover:text-brand-secondary-200 light:text-brand-secondary-700 light:hover:text-brand-secondary-800"
    >
      {children}
    </button>
  )
}

type EvaluationDisclosureProps = {
  label: ReactNode
  open: boolean
  onOpenChange: (open: boolean) => void
  children: ReactNode
  className?: string
}

export function EvaluationDisclosure({
  label,
  open,
  onOpenChange,
  children,
  className,
}: EvaluationDisclosureProps) {
  return (
    <Collapsible open={open} onOpenChange={onOpenChange} className={className}>
      <CollapsibleTrigger className="flex h-8 w-full items-center justify-between rounded border border-brand-main-700/50 bg-brand-main-800/70 px-3 text-left text-xs text-white/70 transition-colors hover:bg-brand-main-800 light:border-black/10 light:bg-black/[0.04] light:text-black/70 light:hover:bg-black/[0.06]">
        <span>{label}</span>
        <ChevronRight
          className={cn(
            'h-3.5 w-3.5 text-white/35 transition-transform light:text-black/35',
            open && 'rotate-90',
          )}
        />
      </CollapsibleTrigger>
      <CollapsibleContent className="pt-4">
        <div className="space-y-4 border-l border-brand-main-700/70 pl-4 light:border-black/10">
          {children}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}
