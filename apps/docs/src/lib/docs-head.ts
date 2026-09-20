import { source } from "@/lib/source";

export const DOCS_SITE_URL = "https://docs.everstack.ai";

const SITE_NAME = "Everstack Documentation";
const FALLBACK_DESCRIPTION =
  "Guides, SDK references, and the full API surface for Everstack: the unified AI infrastructure platform for LLM routing, agents, sandboxes, workflows, memory, and observability.";

/**
 * Turn the `/$` splat into the slug array fumadocs indexes pages by. The root
 * route passes an empty array, which resolves to content/docs/index.mdx.
 */
export function slugsFromSplat(splat: string | undefined): string[] {
  return splat?.split("/").filter(Boolean) ?? [];
}

/**
 * The one URL rule for this site, applied everywhere a docs URL is written.
 *
 * docs.everstack.ai is a static asset deploy, and Workers Assets serves a page
 * written to `<slug>/index.html` at `/<slug>/`, answering the slashless form
 * with a 307. The canonical URL is therefore the trailing-slash form for every
 * page except the root. scripts/generate-llms.mts builds the sitemap with the
 * same rule; the two have to agree byte for byte or the sitemap advertises
 * URLs the pages themselves disown.
 */
export function canonicalUrl(pageUrl: string): string {
  if (pageUrl === "/") return `${DOCS_SITE_URL}/`;
  return `${DOCS_SITE_URL}${pageUrl}/`;
}

/**
 * Per-page title, description, canonical, and social metadata.
 *
 * Without this every one of the 350+ docs pages shipped the root route's
 * single static `<title>Everstack Documentation</title>` and no description or
 * canonical at all, which is indistinguishable from 350 duplicates to a search
 * engine.
 *
 * The not-found branch matters just as much. The `/$` route matches any path,
 * so the prerender crawler has been writing real 200-status HTML for paths
 * that resolve to no page (duplicate-segment links inside the API reference
 * produce roughly 200 of them). Marking those noindex keeps soft 404s out of
 * the index without touching routing.
 */
export function buildDocsHead(slugs: string[]) {
  const page = source.getPage(slugs);

  if (!page) {
    return {
      meta: [
        { title: `Page not found | ${SITE_NAME}` },
        { name: "robots", content: "noindex, follow" },
      ],
    };
  }

  const pageTitle = (page.data as { title?: string }).title ?? SITE_NAME;
  const description =
    (page.data as { description?: string }).description || FALLBACK_DESCRIPTION;
  const title = page.url === "/" ? SITE_NAME : `${pageTitle} | ${SITE_NAME}`;
  const url = canonicalUrl(page.url);

  return {
    meta: [
      { title },
      { name: "description", content: description },
      {
        name: "robots",
        content: "index, follow, max-image-preview:large, max-snippet:-1",
      },
      { property: "og:type", content: "article" },
      { property: "og:site_name", content: SITE_NAME },
      { property: "og:title", content: title },
      { property: "og:description", content: description },
      { property: "og:url", content: url },
      { name: "twitter:card", content: "summary_large_image" },
      { name: "twitter:title", content: title },
      { name: "twitter:description", content: description },
    ],
    links: [{ rel: "canonical", href: url }],
  };
}
