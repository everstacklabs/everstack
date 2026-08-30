import { type CustomAction as CustomActionType } from '../types'

interface CustomActionProps {
    action: CustomActionType
}

export function CustomAction({ action }: CustomActionProps) {
    const Component = action.component

    return (
        <div className={action.className}>
            <Component {...(action.props || {})} />
        </div>
    )
}
