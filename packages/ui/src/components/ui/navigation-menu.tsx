"use client"

import * as React from "react"
import * as NavigationMenuPrimitive from "@radix-ui/react-navigation-menu"
import { ChevronDownIcon } from "lucide-react"

import { cn } from "../../lib/utils.js"

function NavigationMenu({
  className,
  children,
  ...props
}: React.ComponentProps<typeof NavigationMenuPrimitive.Root>) {
  return (
    <NavigationMenuPrimitive.Root
      data-slot="navigation-menu"
      className={cn("relative z-[60] flex items-center justify-center", className)}
      {...props}
    >
      {children}
      <NavigationMenuViewport />
    </NavigationMenuPrimitive.Root>
  )
}

function NavigationMenuList({
  className,
  ...props
}: React.ComponentProps<typeof NavigationMenuPrimitive.List>) {
  return (
    <NavigationMenuPrimitive.List
      data-slot="navigation-menu-list"
      className={cn("group flex w-full list-none items-center justify-center gap-1", className)}
      {...props}
    />
  )
}

function NavigationMenuItem({
  ...props
}: React.ComponentProps<typeof NavigationMenuPrimitive.Item>) {
  return (
    <NavigationMenuPrimitive.Item
      data-slot="navigation-menu-item"
      className="flex juctify-center items-center "
      {...props}
    />
  )
}

function NavigationMenuTrigger({
  className,
  children,
  showChevron = true,
  ...props
}: React.ComponentProps<typeof NavigationMenuPrimitive.Trigger> & {
  showChevron?: boolean
}) {
  return (
    <NavigationMenuPrimitive.Trigger
      data-slot="navigation-menu-trigger"
      className={cn(
        "group inline-flex items-center justify-center rounded px-2.5 pb-1 pt-1.5 text-[13px] font-medium transition-colors",
        "text-brand-main-300 border border-transparent",
        "hover:bg-brand-main-300/20 hover:text-brand-main-50 hover:border-brand-main-300/25",
        "data-[state=open]:bg-brand-main-300/20 data-[state=open]:text-brand-main-50 data-[state=open]:border-brand-main-300/25",
        "focus:outline-none",
        className,
      )}
      {...props}
    >
      {children}
      {showChevron && (
        <ChevronDownIcon
          className="relative top-px ml-0.5 mb-1 size-3 transition-transform duration-200 group-data-[state=open]:rotate-180"
          aria-hidden="true"
        />
      )}
    </NavigationMenuPrimitive.Trigger>
  )
}

function NavigationMenuContent({
  className,
  ...props
}: React.ComponentProps<typeof NavigationMenuPrimitive.Content>) {
  return (
    <NavigationMenuPrimitive.Content
      data-slot="navigation-menu-content"
      className={cn(
        "absolute top-0 left-0 w-full",
        "data-[motion^=from-]:animate-in data-[motion^=to-]:animate-out",
        "data-[motion^=from-]:fade-in data-[motion^=to-]:fade-out",
        "data-[motion=from-end]:slide-in-from-right-52 data-[motion=from-start]:slide-in-from-left-52",
        "data-[motion=to-end]:slide-out-to-right-52 data-[motion=to-start]:slide-out-to-left-52",
        className,
      )}
      {...props}
    />
  )
}

function NavigationMenuLink({
  className,
  ...props
}: React.ComponentProps<typeof NavigationMenuPrimitive.Link>) {
  return (
    <NavigationMenuPrimitive.Link
      data-slot="navigation-menu-link"
      className={cn(
        "inline-flex items-center justify-center rounded px-2.5 pb-1 pt-1.5 text-[13px] font-medium transition-colors",
        "text-brand-main-300 border border-transparent",
        "hover:bg-brand-main-300/20 hover:text-brand-main-50 hover:border-brand-main-300/25",
        "focus:outline-none",
        className,
      )}
      {...props}
    />
  )
}

function NavigationMenuViewport({
  className,
  ...props
}: React.ComponentProps<typeof NavigationMenuPrimitive.Viewport>) {
  return (
    <div className="absolute top-full left-0 z-[70] flex w-full justify-start perspective-[2000px]">
      <NavigationMenuPrimitive.Viewport
        data-slot="navigation-menu-viewport"
        className={cn(
          "origin-[top_center] relative mt-4.5 h-[var(--radix-navigation-menu-viewport-height)] w-full max-w-[calc(100vw-2rem)] overflow-hidden rounded-xl",
          "border border-white/[0.08] light:border-black/[0.08] bg-brand-main-950/92 backdrop-blur-lg shadow-[0_24px_60px_-24px_rgba(0,0,0,0.75)] light:shadow-[0_24px_60px_-24px_rgba(0,0,0,0.25)]",
          "data-[state=open]:animate-in data-[state=closed]:animate-out",
          "data-[state=open]:fade-in-0 data-[state=closed]:fade-out-0",
          "data-[state=open]:zoom-in-[0.96] data-[state=closed]:zoom-out-[0.96]",
          "transition-[height] duration-200",
          className,
        )}
        {...props}
      />
    </div>
  )
}

function NavigationMenuIndicator({
  className,
  ...props
}: React.ComponentProps<typeof NavigationMenuPrimitive.Indicator>) {
  return (
    <NavigationMenuPrimitive.Indicator
      data-slot="navigation-menu-indicator"
      className={cn(
        "top-full z-[1] flex h-1.5 items-end justify-center overflow-hidden",
        "data-[state=visible]:animate-in data-[state=hidden]:animate-out",
        "data-[state=visible]:fade-in data-[state=hidden]:fade-out",
        className,
      )}
      {...props}
    >
      <div className="relative top-[60%] size-2 rotate-45 rounded-tl-sm bg-white/10 light:bg-black/10" />
    </NavigationMenuPrimitive.Indicator>
  )
}

export {
  NavigationMenu,
  NavigationMenuList,
  NavigationMenuItem,
  NavigationMenuTrigger,
  NavigationMenuContent,
  NavigationMenuLink,
  NavigationMenuViewport,
  NavigationMenuIndicator,
}
