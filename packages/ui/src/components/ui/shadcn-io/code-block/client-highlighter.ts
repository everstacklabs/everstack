// @ts-expect-error Shiki exposes this runtime subpath through package exports.
import { createHighlighterCore, type ShikiTransformer } from 'shiki/core';
// @ts-expect-error Shiki exposes this runtime subpath through package exports.
import { createOnigurumaEngine } from 'shiki/engine/oniguruma';

const LANGUAGE_ALIASES: Record<string, string> = {
  js: 'javascript',
  py: 'python',
  sh: 'bash',
  shell: 'bash',
  ts: 'typescript',
  yml: 'yaml',
  proto: 'protobuf',
};

const SUPPORTED_LANGUAGES = new Set([
  'bash',
  'go',
  'javascript',
  'json',
  'markdown',
  'protobuf',
  'python',
  'toml',
  'typescript',
  'yaml',
]);

const SUPPORTED_THEMES = new Set([
  'catppuccin-mocha',
  'github-dark',
  'github-light',
  'vitesse-dark',
  'vitesse-light',
]);

let highlighter: ReturnType<typeof createHighlighterCore> | undefined;

function getHighlighter() {
  highlighter ??= createHighlighterCore({
    engine: createOnigurumaEngine(
      // @ts-expect-error Shiki exposes this runtime subpath through package exports.
      import('shiki/wasm')
    ),
    langs: [
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/bash.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/go.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/javascript.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/json.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/markdown.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/protobuf.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/python.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/toml.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/typescript.mjs'),
      // @ts-expect-error Shiki exposes language modules through package exports.
      import('shiki/langs/yaml.mjs'),
    ],
    themes: [
      // @ts-expect-error Shiki exposes theme modules through package exports.
      import('shiki/themes/catppuccin-mocha.mjs'),
      // @ts-expect-error Shiki exposes theme modules through package exports.
      import('shiki/themes/github-dark.mjs'),
      // @ts-expect-error Shiki exposes theme modules through package exports.
      import('shiki/themes/github-light.mjs'),
      // @ts-expect-error Shiki exposes theme modules through package exports.
      import('shiki/themes/vitesse-dark.mjs'),
      // @ts-expect-error Shiki exposes theme modules through package exports.
      import('shiki/themes/vitesse-light.mjs'),
    ],
  });
  return highlighter;
}

function normalizeLanguage(language: string) {
  const normalized = language.trim().toLowerCase();
  return LANGUAGE_ALIASES[normalized] ?? normalized;
}

function normalizeTheme(theme: string) {
  return SUPPORTED_THEMES.has(theme) ? theme : 'catppuccin-mocha';
}

export async function highlightClientCode(
  code: string,
  language: string,
  themes: { light: string; dark: string },
  transformers: ShikiTransformer[] = []
) {
  const normalizedLanguage = normalizeLanguage(language);
  if (!SUPPORTED_LANGUAGES.has(normalizedLanguage)) return null;

  const instance = await getHighlighter();
  return instance.codeToHtml(code, {
    lang: normalizedLanguage,
    themes: {
      light: normalizeTheme(themes.light),
      dark: normalizeTheme(themes.dark),
    },
    transformers,
  });
}
