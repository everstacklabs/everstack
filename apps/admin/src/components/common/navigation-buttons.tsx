import { ArrowUp, ArrowDown } from 'lucide-react'
import { ui } from '@everstack/ui'

const { Button, Tooltip, TooltipProvider } = ui

interface NavigationButtonsProps {
    canGoPrevious: boolean
    canGoNext: boolean
    onPrevious: () => void
    onNext: () => void
    previousLabel: string
    nextLabel: string
    iconClassName?: string
}

export function NavigationButtons({
    canGoPrevious,
    canGoNext,
    onPrevious,
    onNext,
    previousLabel,
    nextLabel,
    iconClassName = 'size-3',
}: NavigationButtonsProps) {
    return (
        <TooltipProvider>
            <div className='flex items-center gap-2'>
                <Tooltip content={
                    <div className='flex items-center gap-2 px-2 py-1'>
                        <span className='text-white/90 light:text-black/90 text-xs'>{previousLabel}</span>
                        <kbd className='px-1.5 py-0.5 text-xs font-semibold bg-white/10 light:bg-black/10 border border-white/20 light:border-black/20 rounded'>↑</kbd>
                    </div>
                }>
                    <Button
                        variant='secondary'
                        disabled={!canGoPrevious}
                        onClick={onPrevious}
                        tabIndex={-1}
                        className='p-1.5'
                    >
                        <ArrowUp className={iconClassName} />
                    </Button>
                </Tooltip>
                <Tooltip content={
                    <div className='flex items-center gap-2 px-2 py-1'>
                        <span className='text-white/90 light:text-black/90 text-xs'>{nextLabel}</span>
                        <kbd className='px-1.5 py-0.5 text-xs font-semibold bg-white/10 light:bg-black/10 border border-white/20 light:border-black/20 rounded'>↓</kbd>
                    </div>
                }>
                    <Button
                        variant='secondary'
                        disabled={!canGoNext}
                        onClick={onNext}
                        tabIndex={-1}
                        className='p-1.5'
                    >
                        <ArrowDown className={iconClassName} />
                    </Button>
                </Tooltip>
            </div>
        </TooltipProvider>
    )
}

