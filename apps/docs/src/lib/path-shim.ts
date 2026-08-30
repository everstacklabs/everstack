// Minimal browser shim for node:path — only the functions fumadocs-core uses.

export function dirname(p: string): string {
  const idx = p.lastIndexOf("/");
  return idx <= 0 ? "/" : p.slice(0, idx);
}

export function join(...parts: string[]): string {
  return parts.join("/").replace(/\/+/g, "/").replace(/\/$/, "");
}

export function basename(p: string, ext?: string): string {
  const base = p.slice(p.lastIndexOf("/") + 1);
  if (ext && base.endsWith(ext)) return base.slice(0, -ext.length);
  return base;
}

export function extname(p: string): string {
  const base = basename(p);
  const idx = base.lastIndexOf(".");
  return idx <= 0 ? "" : base.slice(idx);
}

export const sep = "/";

export default { dirname, join, basename, extname, sep };
