import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "../../lib/utils.js"

const badgeVariants = cva(
  "flex items-center justify-center rounded border px-2 text-xs font-medium w-fit whitespace-nowrap shrink-0 [&>svg]:size-3 py-0.5 [&>svg]:pointer-events-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive transition-[color,box-shadow] overflow-hidden",
  {
    variants: {
      variant: {
        default:
          "border-brand-secondary-500/35 bg-brand-secondary-500/15 text-brand-secondary-100 light:border-brand-secondary-300 light:bg-brand-secondary-200 light:text-brand-secondary-900 [a&]:hover:bg-brand-secondary-500/20 light:[a&]:hover:bg-brand-secondary-300",
        secondary:
          "border-brand-main-500 bg-brand-main-700 text-brand-main-100 light:border-brand-main-600 light:bg-brand-main-800 light:text-brand-main-100 [a&]:hover:bg-brand-main-600 light:[a&]:hover:bg-brand-main-700",
        chatType:
          "w-fit font-semibold border-info-border bg-info-surface-strong text-info [a&]:hover:bg-info-surface-strong",
        success:
          "border-success-border bg-success-surface text-success [a&]:hover:bg-success-surface-strong",
        warning:
          "border-warning-border bg-warning-surface text-warning [a&]:hover:bg-warning-surface-strong",
        error:
          "border-danger-border bg-danger-surface text-danger [a&]:hover:bg-danger-surface-strong",
        active:
          "border-brand-secondary-500/35 bg-brand-secondary-500/15 text-brand-secondary-100 light:border-brand-secondary-300 light:bg-brand-secondary-200 light:text-brand-secondary-900 [a&]:hover:bg-brand-secondary-500/20 light:[a&]:hover:bg-brand-secondary-300",
        destructive:
          "border-transparent bg-destructive text-destructive-foreground [a&]:hover:bg-destructive/90 focus-visible:ring-danger-border",
        outline:
          "border-brand-main-600 bg-brand-main-950/40 text-brand-main-100 light:bg-brand-main-950 light:text-brand-main-100 [a&]:hover:bg-accent [a&]:hover:text-accent-foreground",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function Badge({
  className,
  variant,
  asChild = false,
  ...props
}: React.ComponentProps<"span"> &
  VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "span"

  return (
    <Comp
      data-slot="badge"
      className={cn(badgeVariants({ variant }), className)}
      {...props}
    />
  )
}

export { Badge, badgeVariants }
