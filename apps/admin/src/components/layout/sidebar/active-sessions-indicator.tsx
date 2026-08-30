import { useState, useEffect, useRef } from 'react'
import { useAgentSessionStore } from '@/stores/agent-session-store'

function getStreamingCount() {
    const sessions = useAgentSessionStore.getState().sessions
    return Object.values(sessions).filter((entry) => entry.isStreaming).length
}

export function ActiveSessionsBadge() {
    const [count, setCount] = useState(getStreamingCount)
    const countRef = useRef(count)

    useEffect(() => {
        const current = getStreamingCount()
        if (current !== countRef.current) {
            countRef.current = current
            setCount(current)
        }

        return useAgentSessionStore.subscribe(() => {
            const next = getStreamingCount()
            if (next !== countRef.current) {
                countRef.current = next
                setCount(next)
            }
        })
    }, [])

    if (count === 0) return null

    return (
        <span className="flex items-center gap-1 rounded-full bg-blue-500/15 px-1.5  text-[10px] font-medium text-blue-400 light:text-blue-600">
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse" />
            {count}
        </span>
    )
}
