import * as React from "react"
import { cn } from "../../lib/utils.js"

export interface InputWithIconProps extends React.ComponentProps<"input"> {
    icon?: React.ReactNode
    iconPosition?: "left" | "right"
    iconClassName?: string
    containerClassName?: string
    iconSize?: number
}

const InputWithIcon = React.forwardRef<HTMLDivElement, InputWithIconProps>(
    (
        {
            className,
            type,
            icon,
            iconPosition = "left",
            iconClassName,
            containerClassName,
            iconSize,
            ...props
        },
        ref
    ) => {
        return (
            <div
                ref={ref}
                className={cn(
                    "flex items-center border-brand-main-600 text-white light:text-brand-main-50 placeholder:text-white/50 light:placeholder:text-black/50 focus:border-brand-main-400 border rounded w-full focus-within:border-brand-secondary-500 transition-colors px-2",
                    iconPosition === "left" ? "pl-3" : "pr-3",
                    containerClassName
                )}
            >
                {icon && iconPosition === "left" && (
                    <div className={cn("shrink-0", iconClassName)}>
                        {React.isValidElement(icon) && iconSize
                            ? React.cloneElement(icon as React.ReactElement<{ size?: number }>, {
                                size: iconSize,
                            })
                            : icon}
                    </div>
                )}
                <input
                    type={type}
                    data-slot="input"
                    className={cn(
                        "file:text-foreground placeholder:text-muted-foreground placeholder:text-sm selection:bg-primary selection:text-primary-foreground dark:bg-input/30 border-transparent w-full min-w-0 rounded border bg-transparent px-3 py-1 text-sm shadow-xs transition-[color,box-shadow] outline-none file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-xs",
                        className
                    )}
                    {...props}
                />
                {icon && iconPosition === "right" && (
                    <div className={cn("shrink-0", iconClassName)}>
                        {React.isValidElement(icon) && iconSize
                            ? React.cloneElement(icon as React.ReactElement<{ size?: number }>, {
                                size: iconSize,
                            })
                            : icon}
                    </div>
                )}
            </div>
        )
    }
)

InputWithIcon.displayName = "InputWithIcon"

export { InputWithIcon }

