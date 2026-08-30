import { useMemo, useRef, useState } from 'react'
import dayjs from 'dayjs'

type Hover = { idx: number; x: number; y: number }

// IssueSparkline renders a compact occurrence histogram (oldest to newest) as
// inline SVG bars with rounded tops, plus a Sentry-style hover tooltip showing
// each bucket's time and count. Bars inherit `currentColor` (fill-current), so
// the caller sets the tone with a text-* class from the palette (no raw hex).
export function IssueSparkline({
  data,
  from,
  to,
  className = '',
  width = 120,
  height = 28,
}: {
  data: number[]
  from?: Date
  to?: Date
  className?: string
  width?: number
  height?: number
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<Hover | null>(null)

  const { bars, colW, bucketMs } = useMemo(() => {
    if (!data.length) return { bars: [], colW: 0, bucketMs: 0 }
    const max = Math.max(1, ...data)
    const gap = 1.5
    const bw = (width - gap * (data.length - 1)) / data.length
    const ms = from && to ? (to.getTime() - from.getTime()) / data.length : 0
    const out = data.map((v, i) => {
      const h = v === 0 ? 0 : Math.max(2, Math.round((v / max) * (height - 3)))
      return { x: i * (bw + gap), y: height - h, w: bw, h }
    })
    return { bars: out, colW: width / data.length, bucketMs: ms }
  }, [data, width, height, from, to])

  if (!data.length) {
    return <div style={{ width, height }} className={className} aria-hidden />
  }

  const onMove = (e: React.MouseEvent) => {
    const rect = ref.current?.getBoundingClientRect()
    if (!rect) return
    const i = Math.min(data.length - 1, Math.max(0, Math.floor((e.clientX - rect.left) / colW)))
    setHover({ idx: i, x: e.clientX, y: rect.top })
  }

  const radius = Math.min(2, bars[0]?.w ? bars[0].w / 2 : 2)

  return (
    <div
      ref={ref}
      className={`relative ${className}`}
      style={{ width, height }}
      onMouseMove={onMove}
      onMouseLeave={() => setHover(null)}
    >
      <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} className="fill-current">
        <line x1="0" y1={height - 0.5} x2={width} y2={height - 0.5} stroke="currentColor" strokeWidth="1" strokeDasharray="2 2" className="opacity-25" />
        {bars.map((b, i) =>
          b.h === 0 ? null : (
            <rect
              key={i}
              x={b.x}
              y={b.y}
              width={b.w}
              height={b.h}
              rx={radius}
              className={hover?.idx === i ? 'opacity-100' : 'opacity-80'}
            />
          ),
        )}
      </svg>
      {hover && (
        <div
          className="pointer-events-none fixed z-50 -translate-x-1/2 -translate-y-full rounded border border-brand-main-600 bg-brand-main-800/95 px-2 py-1 text-[11px] shadow-lg"
          style={{ left: hover.x, top: hover.y - 6 }}
        >
          {bucketMs > 0 && from && (
            <div className="text-white/55 light:text-black/55">{dayjs(from.getTime() + hover.idx * bucketMs).format('MMM D, HH:mm')}</div>
          )}
          <div className="font-mono text-white light:text-brand-main-50">{data[hover.idx]} events</div>
        </div>
      )}
    </div>
  )
}
