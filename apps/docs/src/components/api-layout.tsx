import {
  Children,
  isValidElement,
  type ReactElement,
  type ReactNode,
} from "react";

/** Page header — method badge, path, description. Renders above everything. */
export function APIHeader({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

/** Try It playground — takes 2/3 of the top grid. */
export function APITryIt({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

/** Request code samples — 1/3 of the top grid, above response. */
export function APIRequest({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

/** Response examples — 1/3 of the top grid, below request. */
export function APIResponse({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

/** Docs tables (params, request body, response schemas) — full width, below grid. */
export function APIContent({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

/**
 * API operation page layout.
 *
 * Vertical flow:
 *   1. Try It (2/3) + Request/Response (1/3)  — top grid
 *   2. Docs content (params, body, schemas)   — below, full width
 */
export function APILayout({ children }: { children: ReactNode }) {
  let headerNode: ReactNode = null;
  let contentNode: ReactNode = null;
  let tryItNode: ReactNode = null;
  let requestNode: ReactNode = null;
  let responseNode: ReactNode = null;

  Children.forEach(children, (child) => {
    if (!isValidElement(child)) return;
    const type = (child as ReactElement).type;
    const props = (child as ReactElement).props as { children?: ReactNode };
    if (type === APIHeader) headerNode = props.children;
    else if (type === APIContent) contentNode = props.children;
    else if (type === APITryIt) tryItNode = props.children;
    else if (type === APIRequest) requestNode = props.children;
    else if (type === APIResponse) responseNode = props.children;
  });

  return (
    <div className="flex flex-col gap-8 text-sm">
      {/* Header — method badge, path, description */}
      {headerNode && <div>{headerNode}</div>}

      {/* Grid: left 2/3 (Try It + docs) | right 1/3 (Request + Response) */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[2fr_1fr]">
        <div className="flex min-w-0 flex-col gap-4">
          <div>{tryItNode}</div>
          {contentNode && <div>{contentNode}</div>}
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          <div>{requestNode}</div>
          <div>{responseNode}</div>
        </div>
      </div>
    </div>
  );
}
