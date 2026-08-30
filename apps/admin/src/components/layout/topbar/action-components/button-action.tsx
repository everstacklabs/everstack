import { useCallback, isValidElement } from 'react'
import { ui } from '@everstack/ui'
import { type ButtonAction as ButtonActionType } from '../types'

const { Button } = ui

interface ButtonActionProps {
    action: ButtonActionType
    onDialogOpen: (open: boolean) => void
}

export function ButtonAction({ action, onDialogOpen }: ButtonActionProps) {
    const handleClick = useCallback(() => {
        action.onClick(onDialogOpen)()
    }, [action, onDialogOpen])

    // Check if icon is already a React element or a component
    const iconElement = !action.icon
        ? null
        : isValidElement(action.icon)
            ? action.icon
            : (() => { const Icon = action.icon as any; return <Icon size={16} /> })()

    return (
        <Button
            variant={action.variant}
            onClick={handleClick}
            className={`flex items-center text-sm gap-1 ${action.className || ''}`}
        >
            {iconElement}
            {action.label}
        </Button>
    )
}
