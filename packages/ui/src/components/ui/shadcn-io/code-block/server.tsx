import {
  transformerNotationDiff,
  transformerNotationErrorLevel,
  transformerNotationFocus,
  transformerNotationHighlight,
  transformerNotationWordHighlight,
} from '@shikijs/transformers';
import type { HTMLAttributes } from 'react';
import { highlightClientCode } from './client-highlighter.js';

export type CodeBlockContentProps = HTMLAttributes<HTMLDivElement> & {
  themes?: { light: string; dark: string };
  language?: string;
  children: string;
  syntaxHighlighting?: boolean;
};

export const CodeBlockContent = async ({
  children,
  themes,
  language,
  syntaxHighlighting = true,
  ...props
}: CodeBlockContentProps) => {
  const html = syntaxHighlighting
    ? await highlightClientCode(
        children,
        language ?? 'typescript',
        themes ?? {
          light: 'vitesse-light',
          dark: 'vitesse-dark',
        },
        [
          transformerNotationDiff({
            matchAlgorithm: 'v3',
          }),
          transformerNotationHighlight({
            matchAlgorithm: 'v3',
          }),
          transformerNotationWordHighlight({
            matchAlgorithm: 'v3',
          }),
          transformerNotationFocus({
            matchAlgorithm: 'v3',
          }),
          transformerNotationErrorLevel({
            matchAlgorithm: 'v3',
          }),
        ]
      )
    : null;

  if (!html) {
    return (
      <div {...props}>
        <pre>
          <code>{children}</code>
        </pre>
      </div>
    );
  }

  return (
    <div
      // biome-ignore lint/security/noDangerouslySetInnerHtml: "Kinda how Shiki works"
      dangerouslySetInnerHTML={{ __html: html }}
      {...props}
    />
  );
};
