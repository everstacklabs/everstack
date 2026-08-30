import * as React from "react"

import { cn } from "../../lib/utils.js"

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "file:text-foreground placeholder:text-muted-foreground placeholder:text-sm selection:bg-primary selection:text-primary-foreground dark:bg-brand-main-900/30 border-brand-main-500 w-full min-w-0 rounded-sm border bg-transparent px-3 py-1.5 text-sm shadow-xs transition-[color,box-shadow] outline-none file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-xs [&::-webkit-outer-spin-button]:hidden [&::-webkit-inner-spin-button]:hidden [-webkit-appearance:textfield] [appearance:textfield]",
        "focus-visible:border-brand-secondary-500 focus-visible:ring-brand-secondary-500 focus-visible:ring-[1px]",
        "aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive",
        className
      )}
      {...props}
    />
  )
}

export { Input }
