/**
 * App-wide theme switching.
 *
 * `EverstackThemeProvider` wraps next-themes configured for the platform:
 * the theme lands as `data-theme="dark" | "light"` on <html>, persisted under
 * the `everstack-theme` localStorage key (the same key each app's index.html
 * bootstrap script reads pre-paint to avoid a flash of the wrong theme).
 * Brand token remaps live in `@everstack/tailwind-config/shared-styles.css`.
 *
 * `ThemeToggle` is the standard sun/moon icon button for app chrome.
 */
import * as React from "react"
import { ThemeProvider as NextThemesProvider, useTheme } from "next-themes"
import { Icon } from "@iconify/react"
import { Button } from "./button.js"

export const THEME_STORAGE_KEY = "everstack-theme"

export function EverstackThemeProvider({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <NextThemesProvider
      attribute="data-theme"
      defaultTheme="dark"
      storageKey={THEME_STORAGE_KEY}
      enableSystem
      disableTransitionOnChange
    >
      {children}
    </NextThemesProvider>
  )
}

export function ThemeToggle({ className }: { className?: string }) {
  const { resolvedTheme, setTheme } = useTheme()
  // resolvedTheme is undefined until after mount; render the dark-mode icon
  // (the default theme) until then so SSR/first paint markup is stable.
  const [mounted, setMounted] = React.useState(false)
  React.useEffect(() => setMounted(true), [])
  const isLight = mounted && resolvedTheme === "light"

  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={isLight ? "Switch to dark theme" : "Switch to light theme"}
      onClick={() => setTheme(isLight ? "dark" : "light")}
      className={className}
    >
      <Icon icon={isLight ? "lucide:moon" : "lucide:sun"} className="size-4" />
    </Button>
  )
}

export { useTheme }
