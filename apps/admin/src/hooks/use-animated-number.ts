import { useState, useEffect, useRef } from 'react'

/**
 * Smoothly animates a number from its previous value to the target
 * using requestAnimationFrame with ease-out-quad easing.
 *
 * @param target  The number to animate towards
 * @param immediate  When true, snaps to target without animation
 * @returns The current animated (rounded) integer value
 */
export function useAnimatedNumber(target: number, immediate: boolean): number {
    const [displayed, setDisplayed] = useState(target)
    const rafRef = useRef(0)
    const startRef = useRef(target)
    const startTimeRef = useRef(0)

    useEffect(() => {
        cancelAnimationFrame(rafRef.current)

        if (immediate || target === displayed) {
            setDisplayed(target)
            return
        }

        const from = displayed
        const to = target
        const duration = 400 // ms
        startRef.current = from
        startTimeRef.current = performance.now()

        const step = (now: number) => {
            const elapsed = now - startTimeRef.current
            const t = Math.min(elapsed / duration, 1)
            // ease-out-quad: t * (2 - t)
            const eased = t * (2 - t)
            const current = Math.round(from + (to - from) * eased)

            setDisplayed(current)

            if (t < 1) {
                rafRef.current = requestAnimationFrame(step)
            }
        }

        rafRef.current = requestAnimationFrame(step)

        return () => cancelAnimationFrame(rafRef.current)
        // eslint-disable-next-line react-hooks/exhaustive-deps -- animate from displayed to target
    }, [target, immediate])

    return displayed
}
