import * as React from "react"
import * as CheckboxPrimitive from "@radix-ui/react-checkbox"
import { CheckIcon, MinusIcon } from "lucide-react"

import { cn } from "../../lib/utils.js"

function Checkbox({
  className,
  ...props
}: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "peer border-brand-main-400 data-[state=checked]:bg-brand-secondary-500/50 light:data-[state=checked]:bg-brand-secondary-200 data-[state=checked]:text-white light:data-[state=checked]:text-brand-secondary-900 dark:data-[state=checked]:bg-primary data-[state=checked]:border-brand-secondary-500 light:data-[state=checked]:border-brand-secondary-400 data-[state=indeterminate]:bg-brand-secondary-500/50 light:data-[state=indeterminate]:bg-brand-secondary-200 data-[state=indeterminate]:text-white light:data-[state=indeterminate]:text-brand-secondary-900 data-[state=indeterminate]:border-brand-secondary-500 light:data-[state=indeterminate]:border-brand-secondary-400 focus-visible:border-0 focus-visible:ring-0 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive size-4 shrink-0 rounded-[4px] border shadow-xs transition-shadow outline-none focus-visible:ring-transparent focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
        className
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator
        data-slot="checkbox-indicator"
        className="grid place-content-center text-current transition-none"
      >
        {props.checked === "indeterminate" ? (
          <MinusIcon className="size-3.5" />
        ) : (
          <CheckIcon className="size-3.5" />
        )}
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
}

export { Checkbox }
