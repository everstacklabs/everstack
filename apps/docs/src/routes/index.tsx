import { createFileRoute } from "@tanstack/react-router";
import { DocsShell } from "@/components/docs-shell";
import { buildDocsHead } from "@/lib/docs-head";

// The docs home used to answer with a 302 to /getting-started, both here and
// in public/_redirects. That handed search engines a redirect where the most
// linked-to URL on the site should be, and content/docs/index.mdx (the
// "Introduction" page, with its own title, description, and section cards) was
// never reachable. Rendering it directly makes the root a real indexable page
// and costs nothing: /getting-started keeps working exactly as before.
export const Route = createFileRoute("/")({
  component: DocsHome,
  head: () => buildDocsHead([]),
});

function DocsHome() {
  return <DocsShell slugs={[]} />;
}
