"use client";

import mermaid from "mermaid";
import { useEffect, useRef, useState } from "react";

let initialized = false;

function initMermaid() {
  if (initialized) return;
  initialized = true;
  mermaid.initialize({
    startOnLoad: false,
    theme: "dark",
    themeVariables: {
      primaryColor: "#1e293b",
      primaryTextColor: "#e2e8f0",
      primaryBorderColor: "#475569",
      lineColor: "#64748b",
      secondaryColor: "#0f172a",
      tertiaryColor: "#1e293b",
      fontFamily: "ui-sans-serif, system-ui, sans-serif",
      fontSize: "14px",
    },
    flowchart: {
      curve: "monotoneX",
      padding: 16,
      htmlLabels: true,
    },
    sequence: {
      mirrorActors: false,
    },
  });
}

let counter = 0;

export function Mermaid({ chart }: { chart: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState<string>("");
  const idRef = useRef(`mermaid-${counter++}`);

  useEffect(() => {
    initMermaid();

    let cancelled = false;

    mermaid
      .render(idRef.current, chart)
      .then(({ svg: rendered }) => {
        if (!cancelled) setSvg(rendered);
      })
      .catch((err) => {
        console.error("Mermaid render error:", err);
      });

    return () => {
      cancelled = true;
    };
  }, [chart]);

  return (
    <div
      ref={containerRef}
      className="my-6 flex justify-center overflow-x-auto rounded-lg border border-fd-border bg-fd-card p-4"
      // biome-ignore lint: dangerouslySetInnerHTML is required for mermaid SVG
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
