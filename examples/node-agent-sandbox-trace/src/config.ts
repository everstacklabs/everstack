import Everstack from "@everstack/node";

import { DEFAULT_BENCHMARK_PROMPT } from "./benchmark.js";
import type { ExampleConfig, EverstackClient, Route } from "./types.js";

const DEFAULT_SANDBOX_IMAGE = "ghcr.io/everstacklabs/sandbox:node";
const DEFAULT_TRACE_WAIT_MS = 30_000;

export function readConfig(
  argv: string[] = process.argv,
  env: NodeJS.ProcessEnv = process.env,
): ExampleConfig {
  const cliPrompt = argv
    .slice(2)
    .filter((arg) => arg !== "--")
    .join(" ")
    .trim();
  const environmentPrompt = optionalEnv(env, "EVERSTACK_PROMPT");
  const promptOverride = cliPrompt || environmentPrompt;

  return {
    apiKey: requireEnv(env, "EVERSTACK_API_KEY"),
    baseUrl: normalizeHttpUrl(
      requireEnv(env, "EVERSTACK_GATEWAY_URL"),
      "EVERSTACK_GATEWAY_URL",
    ),
    orgId: optionalEnv(env, "EVERSTACK_ORG_ID"),
    userId: optionalEnv(env, "EVERSTACK_USER_ID"),
    model: optionalEnv(env, "EVERSTACK_MODEL"),
    provider: normalizeProvider(optionalEnv(env, "EVERSTACK_PROVIDER")),
    sandboxImage:
      optionalEnv(env, "EVERSTACK_SANDBOX_IMAGE") ?? DEFAULT_SANDBOX_IMAGE,
    prompt: promptOverride || DEFAULT_BENCHMARK_PROMPT,
    benchmarkMode: !promptOverride,
    traceWaitMs: parsePositiveInteger(env.TRACE_WAIT_MS, DEFAULT_TRACE_WAIT_MS),
    keepResources: envFlag(env, "KEEP_RESOURCES"),
  };
}

export function makeClient(
  config: ExampleConfig,
  headers?: Record<string, string>,
): EverstackClient {
  return new Everstack<string>({
    apiKey: config.apiKey,
    baseUrl: config.baseUrl,
    orgId: config.orgId,
    userId: config.userId,
    provider: config.provider,
    headers,
  });
}

export function assertSdkCapabilities(client: EverstackClient): void {
  const missing: string[] = [];
  if (typeof client.identity?.whoami !== "function") {
    missing.push("identity.whoami");
  }
  if (typeof client.models?.list !== "function") {
    missing.push("models.list");
  }
  if (typeof client.chat?.completions?.create !== "function") {
    missing.push("chat.completions.create");
  }
  if (typeof client.agents?.create !== "function") {
    missing.push("agents.create");
  }
  if (typeof client.agents?.runTurnStream !== "function") {
    missing.push("agents.runTurnStream");
  }
  if (typeof client.agents?.sessions?.complete !== "function") {
    missing.push("agents.sessions.complete");
  }
  if (typeof client.agents?.sessions?.cancel !== "function") {
    missing.push("agents.sessions.cancel");
  }
  if (typeof client.traces?.startSpan !== "function") {
    missing.push("traces.startSpan");
  }
  if (typeof client.traces?.getTree !== "function") {
    missing.push("traces.getTree");
  }
  if (typeof client.traces?.scores?.create !== "function") {
    missing.push("traces.scores.create");
  }

  if (missing.length > 0) {
    throw new Error(
      `Installed @everstack/node is missing required APIs: ${missing.join(", ")}. ` +
        "Build packages/sdks/everstack-node, then reinstall this example's dependencies.",
    );
  }
}

export async function resolveIdentityContext(
  client: EverstackClient,
  config: ExampleConfig,
): Promise<ExampleConfig> {
  if (config.orgId) return config;

  const identity = await client.identity.whoami();
  if (!identity.org_id) {
    throw new Error(
      "The API key did not resolve to an organization. Set EVERSTACK_ORG_ID explicitly.",
    );
  }

  return {
    ...config,
    orgId: identity.org_id,
    userId: config.userId ?? (identity.user_id || undefined),
  };
}

export async function resolveRoute(
  client: EverstackClient,
  config: ExampleConfig,
): Promise<Route> {
  if (config.model) {
    const modelProvider = providerFromModel(config.model);
    if (
      config.provider &&
      modelProvider &&
      config.provider.toLowerCase() !== modelProvider.toLowerCase()
    ) {
      throw new Error(
        `EVERSTACK_PROVIDER ${config.provider} conflicts with model ${config.model}.`,
      );
    }
    return {
      model: config.model,
      provider: config.provider ?? modelProvider,
    };
  }

  const models = await client.models.list();
  const requestedProvider = providerKey(config.provider);
  const candidates = models.data.filter((model) => isLikelyChatModel(model.id));
  const selected =
    (requestedProvider
      ? candidates.find(
          (model) => providerKey(model.owned_by) === requestedProvider,
        )
      : undefined) ??
    candidates.find((model) => providerKey(model.owned_by) === "openai") ??
    candidates[0];

  if (!selected) {
    throw new Error(
      "The instance returned no likely chat model. Set EVERSTACK_MODEL explicitly.",
    );
  }

  return {
    model: selected.id,
    provider:
      config.provider ??
      (selected.owned_by
        ? normalizeProvider(selected.owned_by)
        : providerFromModel(selected.id)),
  };
}

export function providerNativeModel(model: string): string {
  const match = model.match(/^@[^/]+\/(.+)$/);
  return match?.[1] ?? model;
}

export function normalizeProvider(
  provider: string | undefined,
): string | undefined {
  if (!provider) return undefined;
  const trimmed = provider.trim();
  if (!trimmed) return undefined;
  return trimmed.startsWith("@") ? trimmed : `@${trimmed}`;
}

export function providerFromModel(model: string): string | undefined {
  const match = model.match(/^@([^/]+)\//);
  return match?.[1] ? normalizeProvider(match[1]) : undefined;
}

function providerKey(provider: string | undefined): string | undefined {
  return normalizeProvider(provider)?.slice(1).toLowerCase();
}

export function isLikelyChatModel(model: string): boolean {
  const value = model.toLowerCase();
  return ![
    "audio",
    "dall-e",
    "embedding",
    "image",
    "moderation",
    "realtime",
    "rerank",
    "sora",
    "speech",
    "transcrib",
    "tts",
    "video",
    "whisper",
  ].some((marker) => value.includes(marker));
}

function requireEnv(env: NodeJS.ProcessEnv, name: string): string {
  const value = optionalEnv(env, name);
  if (!value) {
    throw new Error(
      `Missing ${name}. Copy .env.example to .env and configure the live instance.`,
    );
  }
  return value;
}

function optionalEnv(env: NodeJS.ProcessEnv, name: string): string | undefined {
  const value = env[name]?.trim();
  return value || undefined;
}

function normalizeHttpUrl(value: string, name: string): string {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error(`${name} must be an absolute HTTP or HTTPS URL.`);
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error(`${name} must use HTTP or HTTPS.`);
  }
  return value.replace(/\/+$/, "");
}

function envFlag(env: NodeJS.ProcessEnv, name: string): boolean {
  return ["1", "true", "yes", "on"].includes(
    (env[name] ?? "").trim().toLowerCase(),
  );
}

function parsePositiveInteger(
  raw: string | undefined,
  fallback: number,
): number {
  if (!raw) return fallback;
  const parsed = Number(raw);
  if (!Number.isSafeInteger(parsed) || parsed <= 0) {
    throw new Error(`Expected a positive integer, received ${raw}.`);
  }
  return parsed;
}
