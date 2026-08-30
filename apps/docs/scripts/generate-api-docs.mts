// biome-ignore-all lint/suspicious/noExplicitAny: this script walks an
// arbitrary OpenAPI document structurally ($ref chasing, allOf/oneOf
// flattening). Modelling the spec would not make the traversal safer,
// only more casts.
/**
 * Generates rich MDX API reference pages from Swagger 2.0 specs.
 *
 * Layout mirrors fumadocs OpenAPI style:
 *   Left column:  description, parameters, request body schema, response schemas
 *   Right column: code samples (cURL/JS/Python tabs), response JSON examples, playground
 *
 * Flow:
 *   1. Convert each *_service.swagger.json → OpenAPI 3.0
 *   2. Write converted spec to src/openapi/<id>.json
 *   3. Generate per-operation MDX pages in content/docs/api-reference/<id>/
 *
 * Usage:
 *   pnpm generate:api          — standalone
 *   make core_api_dev          — runs after buf generate
 *   make docs_api              — standalone shortcut
 */

import {
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
  // @ts-expect-error
} from "node:fs";
// @ts-expect-error
import { dirname, resolve } from "node:path";
// @ts-expect-error
import { fileURLToPath } from "node:url";
import convertSwagger from "swagger2openapi";

const __dirname = dirname(fileURLToPath(import.meta.url));
const OPENAPI_BASE = resolve(__dirname, "../../../openapi/v1/everstack");
const SPEC_DIR = resolve(__dirname, "../src/openapi");
const OUT_BASE = resolve(__dirname, "../content/docs/api-reference");

const SERVICES = [
  {
    id: "gateway",
    file: "gateway/v1/gateway_service.swagger.json",
    title: "Gateway",
  },
  {
    id: "agents",
    file: "agents/v1/agents_service.swagger.json",
    title: "Agents",
  },
  {
    id: "workflows",
    file: "workflows/v1/workflows_service.swagger.json",
    title: "Workflows",
  },
  {
    id: "memory",
    file: "memory/v1/memory_service.swagger.json",
    title: "Memory",
  },
  {
    id: "functions",
    file: "functions/v1/functions_service.swagger.json",
    title: "Functions",
  },
  {
    id: "providers",
    file: "providers/providers_service.swagger.json",
    title: "Providers",
  },
  {
    id: "api-keys",
    file: "api_key/v1/api_key_service.swagger.json",
    title: "API Keys",
  },
  { id: "mcp", file: "mcp/v1/mcp_service.swagger.json", title: "MCP" },
  {
    id: "config",
    file: "config/v1/config_service.swagger.json",
    title: "Config",
  },
  { id: "auth", file: "auth/v1/auth_service.swagger.json", title: "Auth" },
] as const;

// ─── Swagger → OpenAPI 3 conversion ──────────────────────────────────

async function toOpenAPI3(swaggerDoc: object): Promise<any> {
  return new Promise((res, rej) => {
    convertSwagger.convertObj(
      swaggerDoc as Parameters<typeof convertSwagger.convertObj>[0],
      { patch: true, warnOnly: true },
      (err, opts) => {
        if (err) rej(err);
        else res(opts.openapi);
      },
    );
  });
}

// ─── $ref resolution ─────────────────────────────────────────────────

let _spec: any;

function resolveRef(obj: any, depth = 0): any {
  if (!obj || depth > 8) return obj;
  if (obj.$ref && typeof obj.$ref === "string") {
    const parts = obj.$ref.replace(/^#\//, "").split("/");
    let resolved = _spec;
    for (const part of parts) {
      resolved = resolved?.[part];
    }
    return resolveRef(resolved, depth + 1) ?? obj;
  }
  return obj;
}

function resolveSchema(schema: any, depth = 0): any {
  if (!schema || depth > 6) return schema;
  const s = resolveRef(schema, 0);
  if (!s) return s;
  const result = { ...s };
  if (result.properties) {
    result.properties = Object.fromEntries(
      Object.entries(result.properties).map(([k, v]) => [
        k,
        resolveSchema(v as any, depth + 1),
      ]),
    );
  }
  if (result.items) result.items = resolveSchema(result.items, depth + 1);
  if (result.allOf)
    result.allOf = result.allOf.map((x: any) => resolveSchema(x, depth + 1));
  if (result.oneOf)
    result.oneOf = result.oneOf.map((x: any) => resolveSchema(x, depth + 1));
  if (result.anyOf)
    result.anyOf = result.anyOf.map((x: any) => resolveSchema(x, depth + 1));
  if (
    result.additionalProperties &&
    typeof result.additionalProperties === "object"
  ) {
    result.additionalProperties = resolveSchema(
      result.additionalProperties,
      depth + 1,
    );
  }
  return result;
}

// ─── Helpers ─────────────────────────────────────────────────────────

function escMdx(s: string): string {
  if (!s) return "";
  return s.replace(/[<>{}]/g, (c) => `\\${c}`).replace(/\|/g, "\\|");
}

function escCell(s: string): string {
  if (!s) return "";
  return s.replace(/\|/g, "\\|").replace(/\n/g, " ");
}

function schemaToType(schema: any): string {
  if (!schema) return "any";
  const s = resolveRef(schema);
  if (!s) return "any";
  if (s.type === "array") return `${schemaToType(s.items)}[]`;
  if (s.enum) return s.enum.map((v: any) => `"${v}"`).join(" \\| ");
  if (s.type) return s.type;
  if (s.oneOf) return s.oneOf.map(schemaToType).join(" \\| ");
  if (s.anyOf) return s.anyOf.map(schemaToType).join(" \\| ");
  if (s.allOf) return "object";
  if (s.properties) return "object";
  return "object";
}

function generateExampleFromSchema(schema: any, depth = 0): any {
  if (!schema || depth > 4) return null;
  const s = resolveRef(schema);
  if (!s) return null;
  if (s.example !== undefined) return s.example;
  if (s.default !== undefined) return s.default;
  if (s.enum) return s.enum[0];
  switch (s.type) {
    case "string":
      return s.format === "date-time" ? "2024-01-01T00:00:00Z" : "string";
    case "integer":
      return 0;
    case "number":
      return 0.0;
    case "boolean":
      return true;
    case "array":
      if (s.items) {
        const item = generateExampleFromSchema(s.items, depth + 1);
        return item !== null ? [item] : [];
      }
      return [];
    case "object": {
      const obj: Record<string, any> = {};
      if (s.properties) {
        for (const [key, prop] of Object.entries(s.properties)) {
          const val = generateExampleFromSchema(prop as any, depth + 1);
          if (val !== null) obj[key] = val;
        }
      }
      return Object.keys(obj).length > 0 ? obj : null;
    }
    default:
      if (s.properties) {
        const obj: Record<string, any> = {};
        for (const [key, prop] of Object.entries(s.properties)) {
          const val = generateExampleFromSchema(prop as any, depth + 1);
          if (val !== null) obj[key] = val;
        }
        return Object.keys(obj).length > 0 ? obj : null;
      }
      return null;
  }
}

// ─── Left column content generators ──────────────────────────────────

function renderHeader(
  method: string,
  path: string,
  title: string,
  description: string,
): string {
  const lines: string[] = [];
  const colors: Record<string, string> = {
    get: "bg-green-500/10 text-green-600 dark:text-green-400",
    post: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
    put: "bg-yellow-500/10 text-yellow-600 dark:text-yellow-400",
    patch: "bg-orange-500/10 text-orange-600 dark:text-orange-400",
    delete: "bg-red-500/10 text-red-600 dark:text-red-400",
  };
  const color = colors[method] || colors.get;

  lines.push(`<div className="mb-6">`);
  lines.push(`  <h1 className="mb-2 text-2xl font-bold">${escMdx(title)}</h1>`);
  lines.push(`  <div className="flex items-center gap-2">`);
  lines.push(
    `    <span className="rounded-md px-2 py-0.5 text-xs font-bold uppercase ${color}">${method.toUpperCase()}</span>`,
  );
  lines.push(
    `    <code className="text-sm text-fd-muted-foreground">${path}</code>`,
  );
  lines.push(`  </div>`);
  if (description) {
    lines.push(
      `  <p className="mt-3 text-fd-muted-foreground">${escMdx(description)}</p>`,
    );
  }
  lines.push(`</div>`);
  lines.push("");
  return lines.join("\n");
}

function renderParamsTable(params: any[]): string {
  if (!params || params.length === 0) return "";
  const resolved = params.map((p) => resolveRef(p)).filter(Boolean);
  if (resolved.length === 0) return "";
  const rows = resolved.map((p) => {
    const required = p.required ? "**Yes**" : "No";
    const type = schemaToType(p.schema);
    const desc = escCell(p.description || "");
    return `| \`${p.name}\` | ${p.in} | \`${type}\` | ${required} | ${desc} |`;
  });
  return [
    "",
    "### Parameters",
    "",
    "| Name | In | Type | Required | Description |",
    "|------|-----|------|----------|-------------|",
    ...rows,
    "",
  ].join("\n");
}

function renderRequestBody(body: any): string {
  if (!body) return "";
  const content = body.content;
  if (!content) return "";

  const lines: string[] = ["", "### Request Body", ""];
  if (body.description) lines.push(body.description, "");

  const contentTypes = Object.keys(content);
  for (const ct of contentTypes) {
    let schema = content[ct]?.schema;
    if (!schema) continue;
    schema = resolveSchema(schema);

    if (schema.properties) {
      lines.push("| Property | Type | Required | Description |");
      lines.push("|----------|------|----------|-------------|");
      const required = new Set(schema.required || []);
      for (const [name, prop] of Object.entries<any>(schema.properties)) {
        const req = required.has(name) ? "**Yes**" : "No";
        const desc = escCell(prop.description || "");
        lines.push(
          `| \`${name}\` | \`${schemaToType(prop)}\` | ${req} | ${desc} |`,
        );
      }
      lines.push("");
    }
  }

  return lines.join("\n");
}

function renderResponseSchemas(responses: any): string {
  if (!responses) return "";
  const codes = Object.keys(responses);
  if (codes.length === 0) return "";

  const lines: string[] = ["", "### Responses", ""];

  for (const code of codes) {
    const resp = responses[code];
    const desc = resp.description || "";
    lines.push(`#### ${code} ${desc}`, "");

    const content = resp.content;
    if (content) {
      for (const [, media] of Object.entries<any>(content)) {
        if (media.schema) {
          const schema = resolveSchema(media.schema);
          if (schema.properties) {
            lines.push("| Property | Type | Description |");
            lines.push("|----------|------|-------------|");
            for (const [name, prop] of Object.entries<any>(schema.properties)) {
              const d = escCell((prop as any).description || "");
              lines.push(`| \`${name}\` | \`${schemaToType(prop)}\` | ${d} |`);
            }
            lines.push("");
          }
        }
      }
    }
  }

  return lines.join("\n");
}

// ─── Right column content generators ─────────────────────────────────

function renderCodeSamples(method: string, path: string, body: any): string {
  const example = getBodyExample(body);
  const jsonBody = example ? JSON.stringify(example) : "";
  const jsonBodyPretty = example ? JSON.stringify(example, null, 2) : "";

  // cURL
  const curlLines = [
    `curl -X ${method.toUpperCase()} "http://localhost:8089${path}"`,
  ];
  curlLines.push(`  -H "Authorization: Bearer YOUR_API_KEY"`);
  curlLines.push(`  -H "Content-Type: application/json"`);
  if (jsonBody) curlLines.push(`  -d '${jsonBody}'`);
  const curl = curlLines.join(" \\\n");

  // JavaScript fetch
  const jsLines = [
    `const response = await fetch("http://localhost:8089${path}", {`,
  ];
  jsLines.push(`  method: "${method.toUpperCase()}",`);
  jsLines.push(`  headers: {`);
  jsLines.push(`    "Authorization": "Bearer YOUR_API_KEY",`);
  jsLines.push(`    "Content-Type": "application/json",`);
  jsLines.push(`  },`);
  if (jsonBodyPretty) {
    jsLines.push(`  body: JSON.stringify(${jsonBodyPretty}),`);
  }
  jsLines.push(`});`);
  jsLines.push(`const data = await response.json();`);
  const js = jsLines.join("\n");

  // Python requests
  const pyLines = [`import requests`];
  pyLines.push("");
  pyLines.push(`response = requests.${method.toLowerCase()}(`);
  pyLines.push(`    "http://localhost:8089${path}",`);
  pyLines.push(`    headers={`);
  pyLines.push(`        "Authorization": "Bearer YOUR_API_KEY",`);
  pyLines.push(`        "Content-Type": "application/json",`);
  pyLines.push(`    },`);
  if (jsonBodyPretty) {
    pyLines.push(`    json=${jsonBodyPretty},`);
  }
  pyLines.push(`)`);
  pyLines.push(`data = response.json()`);
  const py = pyLines.join("\n");

  const lines: string[] = [];
  lines.push(`<Tabs items={["cURL", "JavaScript", "Python"]}>`);
  lines.push(`<Tab value="cURL">`);
  lines.push("");
  lines.push("```bash");
  lines.push(curl);
  lines.push("```");
  lines.push("");
  lines.push(`</Tab>`);
  lines.push(`<Tab value="JavaScript">`);
  lines.push("");
  lines.push("```js");
  lines.push(js);
  lines.push("```");
  lines.push("");
  lines.push(`</Tab>`);
  lines.push(`<Tab value="Python">`);
  lines.push("");
  lines.push("```python");
  lines.push(py);
  lines.push("```");
  lines.push("");
  lines.push(`</Tab>`);
  lines.push(`</Tabs>`);

  return lines.join("\n");
}

function renderResponseExamples(responses: any): string {
  if (!responses) return "";
  const lines: string[] = [];

  const codes = Object.keys(responses).filter((c) => c !== "default");
  const defaultResp = responses.default;
  if (defaultResp) codes.push("default");

  if (codes.length === 0) return "";

  lines.push("");
  lines.push(`<Tabs items={[${codes.map((c) => `"${c}"`).join(", ")}]}>`);

  for (const code of codes) {
    const resp = responses[code];
    const content = resp.content;
    lines.push(`<Tab value="${code}">`);
    lines.push("");

    if (content) {
      for (const [, media] of Object.entries<any>(content)) {
        if (media.schema) {
          const schema = resolveSchema(media.schema);
          const example = generateExampleFromSchema(schema);
          if (example) {
            lines.push("```json");
            lines.push(JSON.stringify(example, null, 2));
            lines.push("```");
          } else {
            lines.push("*No example available*");
          }
        }
      }
    } else {
      lines.push(`*${resp.description || "No content"}*`);
    }

    lines.push("");
    lines.push(`</Tab>`);
  }

  lines.push(`</Tabs>`);
  return lines.join("\n");
}

function getBodyExample(body: any): any {
  if (!body?.content) return null;
  const schema = Object.values<any>(body.content)[0]?.schema;
  if (!schema) return null;
  return generateExampleFromSchema(resolveSchema(schema));
}

function renderPlayground(op: OperationInfo): string {
  const params = (op.parameters || []).map((p) => {
    const rp = resolveRef(p);
    return {
      name: rp.name,
      in: rp.in,
      required: !!rp.required,
      schema: { type: schemaToType(rp.schema) },
    };
  });

  let defaultBody = "";
  const example = getBodyExample(op.requestBody);
  if (example) {
    defaultBody = JSON.stringify(example, null, 2);
  }

  const paramsJson = JSON.stringify(params);
  const bodyEscaped = defaultBody.replace(/`/g, "\\`").replace(/\$/g, "\\$");

  const lines: string[] = [];
  lines.push(`<APIPlayground`);
  lines.push(`  method="${op.method}"`);
  lines.push(`  path="${op.path}"`);
  lines.push(`  parameters={${paramsJson}}`);
  if (defaultBody) {
    lines.push(`  defaultBody={\`${bodyEscaped}\`}`);
  }
  lines.push(`/>`);
  return lines.join("\n");
}

// ─── Operation info ──────────────────────────────────────────────────

interface OperationInfo {
  slug: string;
  title: string;
  method: string;
  path: string;
  summary: string;
  description: string;
  parameters: any[];
  requestBody: any;
  responses: any;
  tags: string[];
}

function cleanOperationTitle(summary: string, operationId: string): string {
  let title = summary || operationId || "Untitled";
  title = title.replace(/^[A-Za-z]+Service_/, "");
  title = title.replace(/([a-z])([A-Z])/g, "$1 $2");
  return title;
}

function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
}

function extractOperations(spec: any): OperationInfo[] {
  const ops: OperationInfo[] = [];
  const paths = spec.paths || {};
  const methods = ["get", "post", "put", "patch", "delete"];

  for (const [path, pathItem] of Object.entries<any>(paths)) {
    for (const method of methods) {
      const operation = pathItem[method];
      if (!operation) continue;

      const title = cleanOperationTitle(
        operation.summary || "",
        operation.operationId || "",
      );
      const slug = slugify(title);

      ops.push({
        slug,
        title,
        method,
        path,
        summary: operation.summary || "",
        description: operation.description || "",
        parameters: [
          ...(pathItem.parameters || []),
          ...(operation.parameters || []),
        ],
        requestBody: operation.requestBody,
        responses: operation.responses,
        tags: operation.tags || ["default"],
      });
    }
  }

  return ops;
}

// ─── Generate per-operation MDX ──────────────────────────────────────

function generateOperationMdx(op: OperationInfo): string {
  const lines: string[] = [];
  const frontmatterTitle = `${op.method.toUpperCase()} ${op.title}`;

  // Frontmatter — full: true hides TOC
  lines.push("---");
  lines.push(`title: "${escMdx(frontmatterTitle)}"`);
  if (op.summary) lines.push(`description: "${escMdx(op.summary)}"`);
  lines.push("full: true");
  lines.push("---");
  lines.push("");

  // Layout: header → top grid (Try It 2/3 | Request+Response 1/3) → docs tables below
  lines.push(`<APILayout>`);

  // Header — method badge, path, description
  lines.push(`<APIHeader>`);
  lines.push("");
  lines.push(renderHeader(op.method, op.path, op.title, op.description));
  lines.push("");
  lines.push(`</APIHeader>`);

  // Try It playground — 2/3 of top grid
  lines.push(`<APITryIt>`);
  lines.push("");
  lines.push(renderPlayground(op));
  lines.push("");
  lines.push(`</APITryIt>`);

  // Request code samples — 1/3 top
  lines.push(`<APIRequest>`);
  lines.push("");
  lines.push(
    `<p className="mb-2 text-xs font-semibold uppercase tracking-wide text-fd-muted-foreground">Request</p>`,
  );
  lines.push("");
  lines.push(renderCodeSamples(op.method, op.path, op.requestBody));
  lines.push("");
  lines.push(`</APIRequest>`);

  // Response examples — 1/3 below request
  lines.push(`<APIResponse>`);
  lines.push("");
  lines.push(
    `<p className="mb-2 text-xs font-semibold uppercase tracking-wide text-fd-muted-foreground">Response</p>`,
  );
  lines.push("");
  lines.push(renderResponseExamples(op.responses));
  lines.push("");
  lines.push(`</APIResponse>`);

  // Docs tables — full width below grid
  lines.push(`<APIContent>`);
  lines.push("");
  lines.push(renderParamsTable(op.parameters));
  lines.push(renderRequestBody(op.requestBody));
  lines.push(renderResponseSchemas(op.responses));
  lines.push("");
  lines.push(`</APIContent>`);

  lines.push(`</APILayout>`);

  return lines.join("\n");
}

// ─── Generate service index ──────────────────────────────────────────

function generateServiceIndexMdx(
  service: (typeof SERVICES)[number],
  ops: OperationInfo[],
): string {
  const lines: string[] = [];
  lines.push("---");
  lines.push(`title: ${service.title}`);
  lines.push(`description: ${service.title} API endpoints`);
  lines.push("---");
  lines.push("");
  lines.push(`# ${service.title} API`);
  lines.push("");

  const byTag = new Map<string, OperationInfo[]>();
  for (const op of ops) {
    const tag = op.tags[0] || "default";
    if (!byTag.has(tag)) byTag.set(tag, []);
    byTag.get(tag)?.push(op);
  }

  for (const [, tagOps] of byTag) {
    lines.push("| Method | Endpoint | Description |");
    lines.push("|--------|----------|-------------|");
    for (const op of tagOps) {
      const _colors: Record<string, string> = {
        get: "bg-green-500/10 text-green-600 dark:text-green-400",
        post: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
        put: "bg-yellow-500/10 text-yellow-600 dark:text-yellow-400",
        patch: "bg-orange-500/10 text-orange-600 dark:text-orange-400",
        delete: "bg-red-500/10 text-red-600 dark:text-red-400",
      };
      lines.push(
        `| \`${op.method.toUpperCase()}\` | [\`${op.path}\`](./${service.id}/${op.slug}) | ${escCell(op.summary)} |`,
      );
    }
    lines.push("");
  }

  return lines.join("\n");
}

function generateMetaJson(ops: OperationInfo[]): string {
  const pages = ops.map((op) => op.slug);
  return JSON.stringify({ pages }, null, 2);
}

// ─── Main ────────────────────────────────────────────────────────────

mkdirSync(SPEC_DIR, { recursive: true });

let ok = 0;
let failed = 0;

for (const service of SERVICES) {
  const swaggerPath = resolve(OPENAPI_BASE, service.file);

  let swagger: object;
  try {
    swagger = JSON.parse(readFileSync(swaggerPath, "utf-8"));
  } catch {
    console.warn(`⚠  Skipping ${service.id} — spec not found: ${swaggerPath}`);
    failed++;
    continue;
  }

  let spec: any;
  try {
    spec = await toOpenAPI3(swagger);
  } catch (err) {
    console.warn(`⚠  Skipping ${service.id} — conversion failed:`, err);
    failed++;
    continue;
  }

  // Write OpenAPI 3.0 JSON
  writeFileSync(
    resolve(SPEC_DIR, `${service.id}.json`),
    JSON.stringify(spec, null, 2),
  );

  // Set global spec for $ref resolution
  _spec = spec;

  // Extract operations and generate pages
  const ops = extractOperations(spec);
  const outDir = resolve(OUT_BASE, service.id);
  rmSync(outDir, { recursive: true, force: true });
  mkdirSync(outDir, { recursive: true });

  // Per-operation pages
  for (const op of ops) {
    const mdx = generateOperationMdx(op);
    writeFileSync(resolve(outDir, `${op.slug}.mdx`), mdx);
  }

  // meta.json for sidebar ordering
  writeFileSync(resolve(outDir, "meta.json"), generateMetaJson(ops));

  // Service index page
  const indexMdx = generateServiceIndexMdx(service, ops);
  writeFileSync(resolve(OUT_BASE, `${service.id}.mdx`), indexMdx);

  console.log(`✓  ${service.id}: ${ops.length} operations`);
  ok++;
}

console.log(`\nDone: ${ok} services, ${failed} skipped.`);
