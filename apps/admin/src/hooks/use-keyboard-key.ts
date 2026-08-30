import { useEffect } from 'react'

export interface UseKeyboardKeyOptions {
    /**
     * Whether the keyboard listener is enabled
     * @default true
     */
    enabled?: boolean
    /**
     * Whether to prevent default behavior
     * @default false
     */
    preventDefault?: boolean
    /**
     * Target element to attach the listener to
     * @default window
     */
    target?: HTMLElement | Window | null
}

/**
 * Hook to listen for specific keyboard key presses
 * @param key - The keyboard key to listen for (e.g., 'ArrowUp', 'ArrowDown', 'Enter')
 * @param callback - Function to call when the key is pressed
 * @param options - Optional configuration
 */
export function useKeyboardKey(
    key: string,
    callback: (event: KeyboardEvent) => void,
    options: UseKeyboardKeyOptions = {}
) {
    const { enabled = true, preventDefault = false, target = window } = options

    useEffect(() => {
        if (!enabled || !target) return

        const handleKeyPress = (event: KeyboardEvent) => {
            if (event.key === key) {
                if (preventDefault) {
                    event.preventDefault()
                }
                callback(event)
            }
        }

        target.addEventListener('keydown', handleKeyPress as EventListener)

        return () => {
            target.removeEventListener('keydown', handleKeyPress as EventListener)
        }
    }, [key, callback, enabled, preventDefault, target])
}

/**
 * Hook to listen for multiple keyboard keys
 * @param keys - Array of keyboard keys to listen for
 * @param callback - Function to call when any of the keys is pressed
 * @param options - Optional configuration
 */
export function useKeyboardKeys(
    keys: string[],
    callback: (key: string, event: KeyboardEvent) => void,
    options: UseKeyboardKeyOptions = {}
) {
    const { enabled = true, preventDefault = false, target = window } = options

    useEffect(() => {
        if (!enabled || !target) return

        const handleKeyPress = (event: KeyboardEvent) => {
            if (keys.includes(event.key)) {
                if (preventDefault) {
                    event.preventDefault()
                }
                callback(event.key, event)
            }
        }

        target.addEventListener('keydown', handleKeyPress as EventListener)

        return () => {
            target.removeEventListener('keydown', handleKeyPress as EventListener)
        }
    }, [keys, callback, enabled, preventDefault, target])
}

