import { useRef, useState } from 'react'
import { Button } from '@everstack/ui/components'
import { Icon } from '@iconify/react'

interface AudioPlayerProps {
    src: string
    className?: string
}

export function AudioPlayer({ src, className = '' }: AudioPlayerProps) {
    const audioRef = useRef<HTMLAudioElement>(null)
    const [playing, setPlaying] = useState(false)

    const toggle = () => {
        const audio = audioRef.current
        if (!audio) return

        if (playing) {
            audio.pause()
        } else {
            audio.play()
        }
        setPlaying(!playing)
    }

    return (
        <div className={`flex items-center gap-2 ${className}`}>
            <audio
                ref={audioRef}
                src={src}
                onEnded={() => setPlaying(false)}
                onPause={() => setPlaying(false)}
            />
            <Button variant="ghost" size="icon" onClick={toggle}>
                <Icon
                    icon={playing ? 'lucide:pause' : 'lucide:play'}
                    className="h-4 w-4"
                />
            </Button>
        </div>
    )
}
