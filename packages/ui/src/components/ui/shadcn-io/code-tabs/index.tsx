'use client';

import * as React from 'react';
import { useTheme } from 'next-themes';

import { cn } from '../../../../lib/utils.js';
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  TabsContents,
  useTabs,
  type TabsProps,
} from '../tabs/index.js';
import { CopyButton } from '../copy-button/index.js';
import { highlightClientCode } from '../code-block/client-highlighter.js';

type CodeTabsProps = {
  codes: Record<string, string>;
  lang?: string;
  themes?: {
    light: string;
    dark: string;
  };
  copyButton?: boolean;
  onCopy?: (content: string) => void;
} & Omit<TabsProps, 'children'>;

function CodeTabsContent({
  codes,
  lang = 'bash',
  themes = {
    light: 'github-light',
    dark: 'github-dark',
  },
  copyButton = true,
  onCopy,
}: {
  codes: Record<string, string>;
  lang?: string;
  themes?: { light: string; dark: string };
  copyButton?: boolean;
  onCopy?: (content: string) => void;
}) {
  const { resolvedTheme } = useTheme();
  const { activeValue } = useTabs();

  const [highlightedCodes, setHighlightedCodes] = React.useState<Record<
    string,
    string
  >>(codes); // Start with raw codes for instant rendering

  const themeLight = themes.light;
  const themeDark = themes.dark;

  React.useEffect(() => {
    async function loadHighlightedCode() {
      try {
        const newHighlightedCodes: Record<string, string> = {};

        for (const [command, val] of Object.entries(codes)) {
          const highlighted = await highlightClientCode(val, lang, {
            light: themeLight,
            dark: themeDark,
          });

          newHighlightedCodes[command] = highlighted ?? val;
        }

        setHighlightedCodes(newHighlightedCodes);
      } catch (error) {
        console.error('Error highlighting codes', error);
      }
    }
    loadHighlightedCode();
  }, [resolvedTheme, lang, themeLight, themeDark, codes]);

  return (
    <>
      <TabsList
        data-slot="install-tabs-list"
        className="w-full relative justify-between rounded-none h-10 bg-muted border-b border-border/75 dark:border-border/50 text-current py-0 px-4"
        activeClassName="rounded-none shadow-none bg-transparent after:content-[''] after:absolute after:inset-x-0 after:h-0.5 after:bottom-0 dark:after:bg-white after:bg-black after:rounded-t-full"
      >
        <div className="flex gap-x-3 h-full">
          {Object.keys(codes).map((code) => (
            <TabsTrigger
              key={code}
              value={code}
              className="text-muted-foreground data-[state=active]:text-current px-0"
            >
              {code}
            </TabsTrigger>
          ))}
        </div>

        {copyButton && (
          <CopyButton
            content={codes[activeValue]}
            size="sm"
            variant="ghost"
            className="-me-2 bg-transparent hover:bg-black/5 dark:hover:bg-white/10"
            onCopy={onCopy}
          />
        )}
      </TabsList>
      <TabsContents data-slot="install-tabs-contents">
        {Object.entries(codes).map(([code, rawCode]) => (
          <TabsContent
            data-slot="install-tabs-content"
            key={code}
            className="w-full text-sm flex items-center p-4 overflow-auto"
            value={code}
          >
            <div className="w-full [&>pre]:m-0 [&>pre]:p-0 [&>pre]:bg-transparent! [&>pre]:border-none [&>pre]:text-[13px] [&>pre]:leading-relaxed [&_code]:text-[13px] [&_code]:leading-relaxed [&_code]:bg-transparent! [&_.shiki]:bg-transparent!">
              {highlightedCodes[code] !== rawCode ? (
                <div dangerouslySetInnerHTML={{ __html: highlightedCodes[code] as string }} />
              ) : (
                <pre>
                  <code>{rawCode}</code>
                </pre>
              )}
            </div>
          </TabsContent>
        ))}
      </TabsContents>
    </>
  );
}

function CodeTabs({
  codes,
  lang = 'bash',
  themes = {
    light: 'github-light',
    dark: 'github-dark',
  },
  className,
  defaultValue,
  value,
  onValueChange,
  copyButton = true,
  onCopy,
  ...props
}: CodeTabsProps) {
  const firstKey = React.useMemo(() => Object.keys(codes)[0] ?? '', [codes]);

  // Handle controlled vs uncontrolled properly
  const tabsProps = value !== undefined
      ? { value, onValueChange }
      : { defaultValue: defaultValue ?? firstKey };

  return (
    <Tabs
      data-slot="install-tabs"
      className={cn(
        'w-full gap-0 bg-muted/50 rounded-xl border overflow-hidden',
        className,
      )}
      {...tabsProps}
      {...props}
    >
      <CodeTabsContent
        codes={codes}
        lang={lang}
        themes={themes}
        copyButton={copyButton}
        onCopy={onCopy}
      />
    </Tabs>
  );
}

export { CodeTabs, type CodeTabsProps };
