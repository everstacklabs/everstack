"use client";

import * as React from "react";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import { cn } from "../../lib/utils.js";

const DEFAULT_TOOLTIP_DELAY_MS = 300;

export function TooltipProvider({ children }: { children: React.ReactNode }) {
  return (
    <TooltipPrimitive.Provider delayDuration={DEFAULT_TOOLTIP_DELAY_MS}>
      {children}
    </TooltipPrimitive.Provider>
  );
}

export interface TooltipProps
  extends Omit<TooltipPrimitive.TooltipContentProps, "content"> {
  content:
  | React.ReactNode
  | string
  | ((props: { setOpen: (open: boolean) => void }) => React.ReactNode);
  contentClassName?: string;
  disabled?: boolean;
  disableHoverableContent?: TooltipPrimitive.TooltipProps["disableHoverableContent"];
  delayDuration?: TooltipPrimitive.TooltipProps["delayDuration"];
  forceOpen?: boolean;
  openOnClick?: boolean;
  children: React.ReactNode;
}

export function Tooltip({
  children,
  content,
  contentClassName,
  disabled,
  side = "top",
  disableHoverableContent,
  delayDuration = DEFAULT_TOOLTIP_DELAY_MS,
  forceOpen,
  openOnClick = false,
  ...rest
}: TooltipProps) {
  const [open, setOpen] = React.useState(false);
  const effectiveOpen = disabled ? false : forceOpen ?? open;

  return (
    <TooltipPrimitive.Root
      open={effectiveOpen}
      onOpenChange={setOpen}
      delayDuration={delayDuration}
      disableHoverableContent={disableHoverableContent}
    >
      <TooltipPrimitive.Trigger
        asChild
        onClick={() => {
          if (openOnClick) setOpen(true);
        }}
        onPointerDown={() => {
          if (!openOnClick) setOpen(false);
        }}
        onBlur={() => {
          setOpen(false);
        }}
      >
        {children}
      </TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content
          data-slot="tooltip-content"
          sideOffset={8}
          side={side}
          className={cn(
            "data-[state=delayed-open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=delayed-open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=delayed-open]:zoom-in-95 pointer-events-auto z-[99] items-center overflow-hidden rounded border border-white/5 light:border-black/5 bg-brand-main-600 shadow-sm",
            contentClassName
          )}
          collisionPadding={8}
          {...rest}
        >
          {typeof content === "string" ? (
            <span
              className={cn(
                "block max-w-xs text-pretty px-2 py-1 text-center text-xs text-white light:text-brand-main-50"
              )}
            >
              {content}
            </span>
          ) : typeof content === "function" ? (
            content({ setOpen })
          ) : (
            content
          )}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}

export function TooltipContent({
  title,
  cta,
  href,
  target,
  onClick,
}: {
  title: React.ReactNode;
  cta?: string;
  href?: string;
  target?: string;
  onClick?: () => void;
}) {
  return (
    <div className="flex max-w-xs flex-col items-center space-y-3 p-4 text-center">
      <p className="text-sm text-white light:text-brand-main-50">{title}</p>
      {cta &&
        (href ? (
          <a
            href={href}
            {...(target ? { target } : {})}
            className="flex h-9 w-full items-center justify-center whitespace-nowrap rounded-lg border border-brand-secondary-500 bg-brand-secondary-200 px-4 text-sm font-medium text-brand-secondary-800 hover:bg-brand-secondary-400"
          >
            {cta}
          </a>
        ) : onClick ? (
          <button
            onClick={onClick}
            className="flex h-9 w-full items-center justify-center whitespace-nowrap rounded-lg border border-brand-secondary-500 bg-brand-secondary-200 px-4 text-sm font-medium text-brand-secondary-800 hover:bg-brand-secondary-400"
          >
            {cta}
          </button>
        ) : null)}
    </div>
  );
}

export function SimpleTooltipContent({
  title,
  cta,
  href,
}: {
  title: string;
  cta?: string;
  href?: string;
}) {
  return (
    <div className="max-w-xs px-4 py-2 text-center text-sm text-white light:text-brand-main-50">
      {title}
      {cta && href && (
        <>
          {" "}
          <a
            href={href}
            target="_blank"
            rel="noopener noreferrer"
            onClick={(e) => e.stopPropagation()}
            className="inline-flex text-brand-main-200 underline underline-offset-4 hover:text-white light:hover:text-brand-main-50"
          >
            {cta}
          </a>
        </>
      )}
    </div>
  );
}
