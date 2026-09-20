import { readdir, readFile, stat, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");
const CONTENT_DIR = resolve(ROOT, "content/docs");
const PUBLIC_DIR = resolve(ROOT, "public");
const SITE_URL =
  process.env.DOCS_SITE_URL?.replace(/\/$/, "") ?? "https://docs.everstack.ai";

interface Frontmatter {
  title?: string;
  description?: string;
}

interface MetaJson {
  title?: string;
  description?: string;
  root?: boolean;
  pages?: string[];
}

interface Page {
  url: string;
  title: string;
  description: string;
  body: string;
}

function parseFrontmatter(raw: string): { data: Frontmatter; body: string } {
  if (!raw.startsWith("---")) return { data: {}, body: raw };
  const end = raw.indexOf("\n---", 3);
  if (end === -1) return { data: {}, body: raw };
  const head = raw.slice(3, end).trim();
  const body = raw.slice(end + 4).replace(/^\n/, "");
  const data: Frontmatter = {};
  for (const line of head.split("\n")) {
    const m = line.match(/^(\w+):\s*(.*)$/);
    if (!m) continue;
    const [, key, val] = m;
    const v = val.trim().replace(/^['"]|['"]$/g, "");
    if (key === "title" || key === "description") data[key] = v;
  }
  return { data, body };
}

async function walk(dir: string, out: string[] = []): Promise<string[]> {
  for (const entry of await readdir(dir)) {
    const full = join(dir, entry);
    const s = await stat(full);
    if (s.isDirectory()) await walk(full, out);
    else if (entry.endsWith(".mdx")) out.push(full);
  }
  return out;
}

function fileToUrl(file: string): string {
  const rel = relative(CONTENT_DIR, file).replace(/\\/g, "/");
  const noExt = rel.replace(/\.mdx$/, "");
  const noIndex = noExt.replace(/\/index$/, "");
  if (noIndex === "index" || noIndex === "") return "/";
  return `/${noIndex}`;
}

async function readMeta(dir: string): Promise<MetaJson | null> {
  try {
    const raw = await readFile(join(dir, "meta.json"), "utf8");
    return JSON.parse(raw) as MetaJson;
  } catch {
    return null;
  }
}

async function loadPages(): Promise<Page[]> {
  const files = await walk(CONTENT_DIR);
  files.sort();
  const pages: Page[] = [];
  for (const file of files) {
    const raw = await readFile(file, "utf8");
    const { data, body } = parseFrontmatter(raw);
    const url = fileToUrl(file);
    pages.push({
      url,
      title: data.title ?? url,
      description: data.description ?? "",
      body: body.trim(),
    });
  }
  return pages;
}

function groupBySection(pages: Page[]): Record<string, Page[]> {
  const groups: Record<string, Page[]> = {};
  for (const p of pages) {
    if (p.url === "/") continue;
    const segs = p.url.replace(/^\//, "").split("/");
    const section = segs[0] ?? "";
    groups[section] ??= [];
    groups[section].push(p);
  }
  return groups;
}

async function sectionTitle(section: string): Promise<string> {
  const meta = await readMeta(join(CONTENT_DIR, section));
  return meta?.title ?? section;
}

// docs.everstack.ai is a static asset deploy, and Workers Assets serves a page
// written to `<slug>/index.html` at `/<slug>/`, redirecting the slashless form
// with a 307. The canonical URL is therefore the trailing-slash form for every
// page except the root. canonicalUrl() in src/lib/docs-head.ts builds the same
// string for `rel=canonical`; the two have to agree byte for byte or the
// sitemap advertises URLs the pages themselves disown.
function canonicalUrl(url: string): string {
  if (url === "/") return `${SITE_URL}/`;
  return `${SITE_URL}${url}/`;
}

function escapeXml(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&apos;");
}

// Built from the content tree rather than from the prerender output, because
// the prerender crawls links and picks up duplicate-segment paths that resolve
// to a "Page not found" body. Only real pages belong in a sitemap.
function renderSitemap(pages: Page[]): string {
  const urls = pages
    .map(
      (p) =>
        `  <url>\n    <loc>${escapeXml(canonicalUrl(p.url))}</loc>\n  </url>`,
    )
    .join("\n");
  return `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${urls}
</urlset>
`;
}

async function generate() {
  const pages = await loadPages();
  const rootMeta = await readMeta(CONTENT_DIR);
  const rootPage = pages.find((p) => p.url === "/");
  const siteTitle = rootMeta?.title ?? "Everstack Documentation";
  const siteDescription = rootMeta?.description ?? rootPage?.description ?? "";

  const groups = groupBySection(pages);
  const sectionOrder =
    rootMeta?.pages?.filter(
      (name) => !name.startsWith("---") && name !== "index",
    ) ?? Object.keys(groups);

  const lines: string[] = [];
  lines.push(`# ${siteTitle}`);
  lines.push("");
  if (siteDescription) {
    lines.push(`> ${siteDescription}`);
    lines.push("");
  }

  for (const section of sectionOrder) {
    const entries = groups[section];
    if (!entries) continue;
    const title = await sectionTitle(section);
    lines.push(`## ${title}`);
    lines.push("");
    entries.sort((a, b) => a.url.localeCompare(b.url));
    for (const p of entries) {
      const full = `${SITE_URL}${p.url}`;
      const desc = p.description ? `: ${p.description}` : "";
      lines.push(`- [${p.title}](${full})${desc}`);
    }
    lines.push("");
  }

  const llms = lines.join("\n");
  await writeFile(join(PUBLIC_DIR, "llms.txt"), llms, "utf8");

  const fullLines: string[] = [];
  fullLines.push(`# ${siteTitle}`);
  fullLines.push("");
  if (siteDescription) {
    fullLines.push(`> ${siteDescription}`);
    fullLines.push("");
  }
  for (const p of pages) {
    const full = `${SITE_URL}${p.url}`;
    fullLines.push(`# ${p.title}`);
    fullLines.push("");
    fullLines.push(`Source: ${full}`);
    fullLines.push("");
    if (p.description) {
      fullLines.push(p.description);
      fullLines.push("");
    }
    fullLines.push(p.body);
    fullLines.push("");
    fullLines.push("---");
    fullLines.push("");
  }
  await writeFile(
    join(PUBLIC_DIR, "llms-full.txt"),
    fullLines.join("\n"),
    "utf8",
  );

  const sorted = [...pages].sort((a, b) => a.url.localeCompare(b.url));
  await writeFile(
    join(PUBLIC_DIR, "sitemap.xml"),
    renderSitemap(sorted),
    "utf8",
  );

  console.log(
    `Wrote llms.txt (${llms.length} bytes), llms-full.txt (${fullLines.join("\n").length} bytes), and sitemap.xml (${pages.length} urls) to ${PUBLIC_DIR}`,
  );
}

void generate();
