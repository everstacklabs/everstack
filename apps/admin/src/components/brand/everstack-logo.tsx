import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

type EverstackLogoVariant = 'mark' | 'wordmark'
type EverstackLogoSize = 'sm' | 'md' | 'lg'

interface EverstackLogoProps
  extends Omit<ComponentProps<'span'>, 'children'> {
  variant?: EverstackLogoVariant
  size?: EverstackLogoSize
  label?: string
}

const logoDimensions = {
  mark: {
    sm: { width: 23, height: 24 },
    md: { width: 30, height: 32 },
    lg: { width: 38, height: 40 },
  },
  wordmark: {
    sm: { width: 112, height: 18 },
    md: { width: 124, height: 20 },
    lg: { width: 149, height: 24 },
  },
} satisfies Record<
  EverstackLogoVariant,
  Record<EverstackLogoSize, { width: number; height: number }>
>

export function EverstackLogo({
  variant = 'mark',
  size = 'md',
  label = 'Everstack',
  className,
  style,
  ...props
}: EverstackLogoProps) {
  const asset = variant === 'mark' ? 'everstack-mark' : 'everstack-wordmark'
  const dimensions = logoDimensions[variant][size]

  return (
    <span
      {...props}
      role={label ? 'img' : undefined}
      aria-label={label || undefined}
      aria-hidden={label ? undefined : true}
      className={cn('relative inline-block shrink-0 overflow-hidden', className)}
      style={{ ...style, width: dimensions.width, height: dimensions.height }}
    >
      <img
        src={`/${asset}-light.png`}
        alt=""
        aria-hidden="true"
        className="absolute inset-0 block size-full object-contain light:hidden"
      />
      <img
        src={`/${asset}-dark.png`}
        alt=""
        aria-hidden="true"
        className="absolute inset-0 hidden size-full object-contain light:block"
      />
    </span>
  )
}
