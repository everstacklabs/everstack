import { type Icon } from '@everstack/ui/icons'
import { type Permission } from '@everstack/admin-core'

// Base interface for all topbar actions
export interface TopbarActionBase {
    key: string
    label: string
    className?: string
    requiredPermission?: Permission
}

// Button action - simple onClick handlers
export interface ButtonAction extends TopbarActionBase {
    type: 'button'
    icon?: Icon | React.ReactNode
    variant: string
    onClick: (setDialogOpen: (open: boolean) => void) => () => void
}

// Search action - integrated with URL search params
export interface SearchAction extends TopbarActionBase {
    type: 'search'
    placeholder?: string
    searchParam: string // URL search param key
    debounceMs?: number
}

// Filter action - managed by Zustand store
export interface FilterAction extends TopbarActionBase {
    type: 'filter'
    filterType: 'select' | 'date-range' | 'multi-select'
    options?: Array<{ value: string; label: string }>
    storeKey: string | number // Key in the Zustand store
    storeAction: string | number // Action name in the store
}

// Custom action - accepts any React component
export interface CustomAction extends TopbarActionBase {
    type: 'custom'
    component: React.ComponentType<any>
    props?: Record<string, any>
}

// Discriminated union of all action types
export type TopbarAction = ButtonAction | SearchAction | FilterAction | CustomAction

// Action group for smart grouping
export interface ActionGroup {
    title?: React.ReactNode
    actions?: TopbarAction[] | null
}

// Route configuration with grouped actions
export interface RouteActions {
    [route: string]: Record<string, ActionGroup | ActionGroup[]>
}

// Legacy interface for backward compatibility
export interface LegacyTopbarAction {
    pageTitle: string
    key: string
    label: string
    icon: Icon
    variant: string
    className?: string
    children?: React.ReactNode
    onClick: (setDialogOpen: (open: boolean) => void) => () => void
}

export interface TopbarActionConfig {
    actions: RouteActions
    globalDialogs?: {
        [route: string]: React.ComponentType<any>
    }
}
