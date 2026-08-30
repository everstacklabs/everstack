import { Loader2Icon } from "lucide-react"

import { cn } from "../../lib/utils.js"

function Spinner({
  className,
  ...props
}: Omit<React.ComponentProps<typeof Loader2Icon>, "className"> & {
  className?: string
}) {
  return (
    <Loader2Icon
      role="status"
      aria-label="Loading"
      className={cn("size-4 animate-spin text-gray-300", className)}
      {...props}
    />
  )
}

export { Spinner }
