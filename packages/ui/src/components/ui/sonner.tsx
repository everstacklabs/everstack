import { useTheme } from "next-themes"
import { Toaster as Sonner, ToasterProps, toast } from "sonner"

const Toaster = ({ ...props }: ToasterProps) => {
  const { theme = "system" } = useTheme()

  return (
    <Sonner
      theme={theme as ToasterProps["theme"]}
      className="toaster group"
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--sidebar-primary)",
          "--normal-border": "var(--border)",
          // richColors error/warning toasts: Sonner's defaults are saturated
          // reds and yellows, so route them through the status tokens. The
          // tint is mixed into the popover colour because toasts must be opaque.
          "--error-bg":
            "color-mix(in oklab, var(--color-danger-solid) 12%, var(--popover))",
          "--error-border": "var(--color-danger-border)",
          "--error-text": "var(--color-danger)",
          "--warning-bg":
            "color-mix(in oklab, var(--color-warning-solid) 10%, var(--popover))",
          "--warning-border": "var(--color-warning-border)",
          "--warning-text": "var(--color-warning)",
        } as ToasterProps["style"]
      }
      {...props}
    />
  )
}

export { Toaster, toast }
