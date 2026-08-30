import { useState, useEffect, useCallback, useRef } from 'react'
import { useSearch } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import { Search } from '@everstack/ui/icons'
import { type SearchAction as SearchActionType } from '../types'

const { InputWithIcon } = ui

interface SearchActionProps {
    action: SearchActionType
}

// Move debounce utility outside component to prevent recreation
function debounce<T extends (...args: any[]) => any>(
    func: T,
    wait: number
): (...args: Parameters<T>) => void {
    let timeout: NodeJS.Timeout | null = null

    return (...args: Parameters<T>) => {
        if (timeout) clearTimeout(timeout)
        timeout = setTimeout(() => func(...args), wait)
    }
}

export function SearchAction({ action }: SearchActionProps) {
    const [localValue, setLocalValue] = useState('')
    const search = useSearch({ strict: false })
    const debouncedUpdateRef = useRef<((value: string) => void) | null>(null)
    const ref = useRef<HTMLDivElement>(null)

    // Initialize local value from URL search params on mount
    useEffect(() => {
        const urlValue = (search as any)?.[action.searchParam] || ''
        setLocalValue(urlValue)
        if (ref.current) {
            ref.current.focus()
        }
    }, []) // Only run on mount

    // Initialize debounced function once
    useEffect(() => {
        debouncedUpdateRef.current = debounce((value: string) => {
            const currentSearch = new URLSearchParams(window.location.search)
            if (value) {
                currentSearch.set(action.searchParam, value)
            } else {
                currentSearch.delete(action.searchParam)
            }
            const newUrl = `${window.location.pathname}?${currentSearch.toString()}`
            window.history.replaceState(null, '', newUrl)
        }, action.debounceMs || 300)
    }, [action.searchParam, action.debounceMs])


    const handleChange = useCallback((value: string) => {
        setLocalValue(value)
        if (debouncedUpdateRef.current) {
            debouncedUpdateRef.current(value)
        }
    }, [])

    return (
        <InputWithIcon
            ref={ref}
            key={action.key}
            type="text"
            icon={<Search className='text-white/50 light:text-black/50' />}
            iconPosition="left"
            iconSize={16}
            placeholder={action.placeholder || `Search ${action.label.toLowerCase()}...`}
            value={localValue}
            onChange={(e) => handleChange(e.target.value)}
            className={`bg-brand-main-800 w-60 ${action.className || ''}`}
        />
    )
}
