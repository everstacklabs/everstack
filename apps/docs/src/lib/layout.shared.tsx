import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <>
          <span
            role="img"
            aria-label="Everstack"
            className="relative inline-block shrink-0 overflow-hidden"
            style={{ width: 23, height: 24 }}
          >
            <img
              src={`${import.meta.env.BASE_URL}everstack-mark-dark.png`}
              alt=""
              aria-hidden="true"
              className="absolute inset-0 size-full object-contain dark:hidden"
            />
            <img
              src={`${import.meta.env.BASE_URL}everstack-mark-light.png`}
              alt=""
              aria-hidden="true"
              className="absolute inset-0 size-full object-contain hidden dark:block"
            />
          </span>
          <span>Everstack</span>
        </>
      ),
      transparentMode: "top",
    },
    // The docs sat one link away from the product site (a single pricing link
    // buried in the CLI reference), so every docs page was a dead end for a
    // reader who arrived from search and wanted to know what the thing costs
    // or what else it does. One menu, four destinations, on every page.
    links: [
      {
        type: "menu",
        text: "Product",
        items: [
          {
            text: "AI Gateway",
            description:
              "One OpenAI-compatible endpoint in front of every provider.",
            url: "https://everstack.ai/gateway/",
            external: true,
          },
          {
            text: "Sandboxes",
            description:
              "Isolated microVM and container runtimes for agent code.",
            url: "https://everstack.ai/sandboxes/",
            external: true,
          },
          {
            text: "Observability",
            description:
              "OpenTelemetry-native traces, logs, and metrics for LLM traffic.",
            url: "https://everstack.ai/observability/",
            external: true,
          },
          {
            text: "Pricing",
            description: "Plans, usage rates, and what the free tier includes.",
            url: "https://everstack.ai/pricing/",
            external: true,
          },
        ],
      },
    ],
    githubUrl: "https://github.com/everstackhq/everstack",
  };
}
