import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded text font-medium transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150 ease-out active:scale-[0.98] motion-reduce:active:scale-100 disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg:not([class*='size-'])]:size-4 shrink-0 [&_svg]:shrink-0 outline-none focus-visible:border-ring focus-visible:ring-brand-secondary-500/50 focus-visible:ring-[3px] aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive",
  {
    variants: {
      variant: {
        default:
          "bg-brand-secondary-200 border border-brand-secondary-500 font-medium text-brand-secondary-800 light:text-brand-secondary-900 hover:bg-brand-secondary-400",
        transparent:
          "bg-transparent border border-transparent hover:bg-transparent hover:text-brand-main-100 hover:border-transparent hover:bg-brand-main-800/50 transition-colors",
        destructive:
          "bg-red-500/20 border border-red-500/50 text-white light:text-red-700 hover:bg-destructive/90 light:hover:text-white focus-visible:ring-destructive/20 focus-visible:ring-destructive/40",
        outline:
          "border border-brand-main-500 bg-brand-main-950/10 font-medium text-brand-main-50 shadow-xs hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 hover:border-brand-main-600",
        secondary:
          "bg-brand-main-500 font-medium active:bg-brand-main-600 border border-brand-main-500 text-brand-main-50 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-500",
        muted:
          "hover:bg-accent hover:text-accent-foreground text-white light:text-brand-main-50 border border-transparent bg-brand-main-700 light:bg-brand-main-800 hover:border-brand-secondary-500/10",
        ghost:
          "hover:bg-accent bg-brand-main-800 light:bg-brand-main-950 hover:text-white light:hover:text-brand-main-50 border border-brand-main-600 hover:bg-brand-main-700 light:hover:bg-brand-main-800 hover:border-brand-main-500",
        link: "text-primary underline-offset-4 hover:underline",
      },
      size: {
        default: "h-8 px-3 py-1 has-[>svg]:px-2 text-sm",
        sm: "h-8 rounded gap-1.5 px-3 text-sm has-[>svg]:px-2.5",
        lg: "h-10 rounded px-6 text-sm has-[>svg]:px-4",
        xs: "h-7 rounded px-0.5 py-1 text-xs has-[>svg]:px-1.5",
        icon: "size-8",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

function Button({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
  }) {
  const Comp = asChild ? Slot : "button";

  return (
    <Comp
      data-slot="button"
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  );
}

export { Button, buttonVariants };
