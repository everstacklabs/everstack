import { createFileRoute } from "@tanstack/react-router";
import { DocsShell } from "@/components/docs-shell";
import { buildDocsHead, slugsFromSplat } from "@/lib/docs-head";

export const Route = createFileRoute("/$")({
  component: Page,
  head: ({ params }) => buildDocsHead(slugsFromSplat(params._splat)),
});

function Page() {
  const params = Route.useParams();
  return <DocsShell slugs={slugsFromSplat(params._splat)} />;
}
