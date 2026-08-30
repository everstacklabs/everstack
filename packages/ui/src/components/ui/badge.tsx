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
          "border-transparent w-fit font-semibold bg-blue-500/50 text-white light:text-blue-950 [a&]:hover:bg-blue-500/90 border-blue-500",
        success:
          "border-transparent bg-emerald-900 light:bg-emerald-100 text-white light:text-emerald-900 border-emerald-500",
        warning:
          "border-transparent bg-yellow-700/20 text-white light:text-yellow-900 [a&]:hover:bg-yellow-500/90 border-yellow-800 light:border-yellow-600",
        error:
          "border-transparent bg-rose-900 light:bg-rose-100 text-white light:text-rose-900 border-rose-500",
        active:
          "border-brand-secondary-500/35 bg-brand-secondary-500/15 text-brand-secondary-100 light:border-brand-secondary-300 light:bg-brand-secondary-200 light:text-brand-secondary-900 [a&]:hover:bg-brand-secondary-500/20 light:[a&]:hover:bg-brand-secondary-300",
        destructive:
          "border-transparent bg-destructive text-white [a&]:hover:bg-destructive/90 focus-visible:ring-destructive/20 dark:focus-visible:ring-destructive/40 dark:bg-destructive/60",
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
