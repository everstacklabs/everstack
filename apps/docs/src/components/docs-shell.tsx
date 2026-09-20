import { Accordion, Accordions } from "fumadocs-ui/components/accordion";
import { File, Files, Folder } from "fumadocs-ui/components/files";
import { Step, Steps } from "fumadocs-ui/components/steps";
import { Tab, Tabs } from "fumadocs-ui/components/tabs";
import { TypeTable } from "fumadocs-ui/components/type-table";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import {
  DocsBody,
  DocsDescription,
  DocsPage,
  DocsTitle,
} from "fumadocs-ui/layouts/docs/page";
import defaultMdxComponents from "fumadocs-ui/mdx";
import { BookOpen, Boxes, Code, Rocket, Terminal } from "lucide-react";
import { useMemo } from "react";
import type { MDXContent } from "mdx/types";
import {
  APIContent,
  APIHeader,
  APILayout,
  APIRequest,
  APIResponse,
  APITryIt,
} from "@/components/api-layout";
import { APIPlayground } from "@/components/api-playground";
import { Mermaid } from "@/components/mermaid";
import { addMethodBadges } from "@/components/method-badge";
import * as ProviderIcons from "@/components/provider-icons";
import { baseOptions } from "@/lib/layout.shared";
import { source } from "@/lib/source";

const ROOT_INDEX_URLS = new Set([
  "/",
  "/getting-started",
  "/sdks",
  "/deployment",
  "/api-reference",
  "/cli",
]);

const mdxComponents = {
  ...defaultMdxComponents,
  Tabs,
  Tab,
  Steps,
  Step,
  Accordion,
  Accordions,
  File,
  Files,
  Folder,
  TypeTable,
  APIPlayground,
  APILayout,
  APIHeader,
  APIContent,
  APITryIt,
  APIRequest,
  APIResponse,
  Mermaid,
  ...ProviderIcons,
};

/**
 * Renders one docs page inside the shared layout.
 *
 * Shared by the `/$` splat route and the `/` index route, so that the docs
 * home can serve content/docs/index.mdx directly instead of redirecting to
 * /getting-started.
 */
export function DocsShell({ slugs }: { slugs: string[] }) {
  const page = source.getPage(slugs);
  const tree = useMemo(() => addMethodBadges(source.getPageTree()), []);

  if (!page) {
    return (
      <DocsLayout {...baseOptions()} tree={tree}>
        <DocsPage>
          <DocsTitle>Page not found</DocsTitle>
        </DocsPage>
      </DocsLayout>
    );
  }

  const MDX = page.data.body as MDXContent;
  const isFull = (page.data as { full?: boolean }).full === true;
  const includeRoot = !ROOT_INDEX_URLS.has(page.url);

  return (
    <DocsLayout
      {...baseOptions()}
      tree={tree}
      sidebar={{
        tabs: {
          transform(option) {
            const iconClass = "w-6 h-6 mt-1.5 lg:w-4.5 lg:h-4.5 lg:mt-0.5";
            const icons: Record<string, React.ReactNode> = {
              "getting-started": <BookOpen className={iconClass} />,
              sdks: <Boxes className={iconClass} />,
              deployment: <Rocket className={iconClass} />,
              "api-reference": <Code className={iconClass} />,
              cli: <Terminal className={iconClass} />,
            };
            const segment = option.url.split("/").filter(Boolean).pop() ?? "";
            return { ...option, icon: icons[segment] ?? option.icon };
          },
        },
        defaultOpenLevel: 0,
        collapsible: true,
      }}
    >
      <DocsPage
        {...(isFull
          ? { full: true, breadcrumb: { includeRoot, includePage: true } }
          : {
              toc: page.data.toc as DocsPageProps["toc"],
              tableOfContent: { style: "clerk" as const },
              breadcrumb: { includeRoot, includePage: true },
            })}
      >
        {!isFull && <DocsTitle>{page.data.title as string}</DocsTitle>}
        {!isFull && (
          <DocsDescription>{page.data.description as string}</DocsDescription>
        )}
        <DocsBody>
          <MDX components={mdxComponents} />
        </DocsBody>
      </DocsPage>
    </DocsLayout>
  );
}

type DocsPageProps = Parameters<typeof DocsPage>[0];
