import { docs } from "fumadocs-mdx:collections/server";
import { loader } from "fumadocs-core/source";
import { icons as lucideIcons } from "lucide-react";
import { createElement, type ReactElement } from "react";
import {
  AnthropicIcon,
  ClaudeIcon,
  CursorIcon,
  GeminiIcon,
  GitHubCopilotIcon,
  MoonshotIcon,
  OpenAIIcon,
  ZhipuIcon,
} from "@/components/provider-icons";

// Coding-agent / provider brand logos usable as a page's `icon` (renders in the
// sidebar tree and breadcrumb). Any name not listed here falls back to a Lucide
// icon, preserving the previous behaviour for every other page.
const providerIcons: Record<string, () => ReactElement> = {
  claude: ClaudeIcon,
  anthropic: AnthropicIcon,
  gemini: GeminiIcon,
  openai: OpenAIIcon,
  moonshot: MoonshotIcon,
  zhipu: ZhipuIcon,
  cursor: CursorIcon,
  copilot: GitHubCopilotIcon,
};

export const source = loader({
  source: docs.toFumadocsSource(),
  baseUrl: "/",
  icon(icon) {
    if (!icon) return undefined;
    const Provider = providerIcons[icon];
    // Brand logos render heavier than the outline Lucide icons, so size them a
    // step down (14px) in the sidebar tree + breadcrumb. The body/cards use the
    // components directly at their default 16px.
    if (Provider) {
      return createElement(
        "span",
        { className: "inline-flex items-center [&>img]:size-3.5" },
        createElement(Provider),
      );
    }
    if (icon in lucideIcons) {
      return createElement(lucideIcons[icon as keyof typeof lucideIcons]);
    }
    return undefined;
  },
});
