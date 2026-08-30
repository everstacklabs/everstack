import { useState, useEffect, useRef } from 'react'

// Base reveal rate: ~180 chars/sec (smooth typing feel)
const BASE_CHARS_PER_MS = 0.18

/**
 * Smoothly reveals text character-by-character using requestAnimationFrame.
 * Adapts speed to prevent falling too far behind incoming text.
 * When streaming stops, immediately reveals all remaining text.
 */
export function useAnimatedText(targetText: string, isStreaming: boolean): string {
    const [displayed, setDisplayed] = useState('')
    const idxRef = useRef(0)
    const rafRef = useRef(0)
    const targetRef = useRef('')
    const animatingRef = useRef(false)

    // Always keep targetRef current so the rAF loop reads the latest text
    targetRef.current = targetText

    // Flush immediately when streaming stops
    useEffect(() => {
        if (!isStreaming && targetText) {
            cancelAnimationFrame(rafRef.current)
            animatingRef.current = false
            idxRef.current = targetText.length
            setDisplayed(targetText)
        }
    }, [isStreaming, targetText])

    // Reset when text is cleared
    useEffect(() => {
        if (!targetText) {
            cancelAnimationFrame(rafRef.current)
            animatingRef.current = false
            idxRef.current = 0
            setDisplayed('')
        }
    }, [targetText])

    // Animate when new text arrives during streaming
    useEffect(() => {
        if (!isStreaming || !targetText || animatingRef.current) return
        if (idxRef.current >= targetText.length) return

        animatingRef.current = true
        let last = performance.now()

        const step = (now: number) => {
            const dt = now - last
            last = now

            const target = targetRef.current
            const buffer = target.length - idxRef.current

            // Adaptive rate: speed up if falling behind, slow down when caught up
            const rate = buffer > 100
                ? BASE_CHARS_PER_MS * 3
                : buffer > 50
                    ? BASE_CHARS_PER_MS * 2
                    : BASE_CHARS_PER_MS

            const add = Math.max(1, Math.floor(dt * rate))
            const next = Math.min(idxRef.current + add, target.length)

            if (next !== idxRef.current) {
                idxRef.current = next
                setDisplayed(target.slice(0, next))
            }

            if (idxRef.current < target.length) {
                rafRef.current = requestAnimationFrame(step)
            } else {
                animatingRef.current = false
            }
        }

        rafRef.current = requestAnimationFrame(step)
    }, [targetText, isStreaming])

    return displayed
}
