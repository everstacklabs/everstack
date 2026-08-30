import type { Node, Root } from "fumadocs-core/page-tree";
import { createElement } from "react";

const METHOD_STYLES: Record<string, string> = {
  GET: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  POST: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  PUT: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  PATCH: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  DELETE: "bg-red-500/15 text-red-600 dark:text-red-400",
};

const METHODS = ["GET", "POST", "PUT", "DELETE", "PATCH"] as const;

export function MethodBadge({ method }: { method: string }) {
  return createElement(
    "span",
    {
      className: `inline-flex items-center justify-center rounded text-[9px] font-semibold leading-none px-1 py-0.5 ${METHOD_STYLES[method] ?? ""}`,
      style: { minWidth: "1.75rem", textAlign: "center" as const },
    },
    method,
  );
}

function walkNodes(nodes: Node[]): Node[] {
  return nodes.map((node) => {
    if (node.type === "folder") {
      return { ...node, children: walkNodes(node.children) };
    }

    if (node.type !== "page" || !node.url.includes("/api-reference/")) {
      return node;
    }

    const name = typeof node.name === "string" ? node.name : "";
    for (const m of METHODS) {
      if (name.startsWith(`${m} `)) {
        return {
          ...node,
          name: name.slice(m.length + 1),
          icon: createElement(MethodBadge, { method: m }),
        };
      }
    }

    return node;
  });
}

export function addMethodBadges(tree: Root): Root {
  return { ...tree, children: walkNodes(tree.children) };
}
