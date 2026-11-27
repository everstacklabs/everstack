import { useEffect, useRef, type ReactNode, type Ref } from 'react'
import { ui } from '@everstack/ui'
import { BotLogo } from './bot-logo'

const { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } = ui

// A little longer than the sheet's 300ms close animation.
const CLOSE_MS = 400
const EMPTY_VIEW = { kind: undefined, title: '', description: undefined, footer: null, children: null }

/** The side panel every bot flow opens in: the bot's mark and a title that
 *  stay put, one scrolling body, and the actions pinned at the bottom. */
export function BotSheet({
    open,
    onOpenChange,
    kind,
    title,
    description,
    bodyRef,
    footer,
    children,
}: {
    open: boolean
    onOpenChange: (open: boolean) => void
    /** Shows this kind's mark beside the title. */
    kind?: string
    title: string
    description?: string
    /** The scrolling element, for callers that need to scroll it. */
    bodyRef?: Ref<HTMLDivElement>
    footer: ReactNode
    children: ReactNode
}) {
    // Callers clear their state, a new key included, the moment they close. The
    // sheet takes 300ms to slide shut, so until then it keeps showing what it
    // showed while open, instead of going blank or flipping back a step.
    const view = { kind, title, description, footer, children }
    const held = useRef(view)
    if (open) held.current = view
    const shown = open ? view : held.current
    // Then let go of it, so a key in the held view does not outlive the panel.
    useEffect(() => {
        if (open) return
        const timer = setTimeout(() => {
            held.current = EMPTY_VIEW
        }, CLOSE_MS)
        return () => clearTimeout(timer)
    }, [open])

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent className="flex w-full flex-col gap-0 border-l-brand-main-500 bg-brand-main-900 sm:max-w-2xl">
                <SheetHeader className="min-w-0 items-center gap-3 py-3 pl-2">
                    {shown.kind && <BotLogo kind={shown.kind} size="md" />}
                    <div className="min-w-0">
                        <SheetTitle className="truncate text-base font-semibold text-white light:text-brand-main-50">
                            {shown.title}
                        </SheetTitle>
                        {shown.description && (
                            <SheetDescription className="mt-0.5 text-xs leading-5 text-white/60 light:text-black/60">
                                {shown.description}
                            </SheetDescription>
                        )}
                    </div>
                </SheetHeader>
                <SheetBody ref={bodyRef} className="scrollbar-macos min-w-0 overflow-x-hidden px-5 py-5">
                    {shown.children}
                </SheetBody>
                <SheetFooter className="px-5 py-3">{shown.footer}</SheetFooter>
            </SheetContent>
        </Sheet>
    )
}

/** The row of actions at the bottom of a bot sheet, with an optional error
 *  above it so a failure is visible wherever the body is scrolled. */
export function BotSheetActions({ error, children }: { error?: ReactNode; children: ReactNode }) {
    return (
        <>
            {error && (
                <p role="alert" className="text-sm text-danger">
                    {error}
                </p>
            )}
            <div className="flex justify-end gap-2">{children}</div>
        </>
    )
}
