import { config as loadEnv } from "dotenv";
import { readFileSync } from "node:fs";
import { argv } from "node:process";
import { fileURLToPath } from "node:url";

// Shared config lives at examples/sdk-tests/.env; a node/.env overrides it.
// Exported shell env always wins (dotenv never overrides what is already set).
loadEnv({ path: "../.env" });
loadEnv();

import { Everstack } from "@everstack/node";
import type { AllModels, TransportProtocol } from "@everstack/node";

/** Read a required env var or exit with a clear message. */
export function requireEnv(name: string): string {
  const value = process.env[name];
  if (!value) {
    console.error(
      `Missing required env var: ${name}\n` +
        "Copy .env.example to .env and fill it in (gateway URL + API key).",
    );
    process.exit(1);
  }
  return value;
}

/** Resolve the version of the @everstack/node we actually installed. */
export function sdkVersion(): string {
  try {
    const pkgUrl = new URL(
      "../../node_modules/@everstack/node/package.json",
      import.meta.url,
    );
    return JSON.parse(readFileSync(pkgUrl, "utf8")).version as string;
  } catch {
    return "unknown";
  }
}

export interface ClientConfig {
  apiKey: string;
  baseUrl: string;
  modelOverride?: string;
}

export function getConfig(): ClientConfig {
  return {
    apiKey: requireEnv("EVERSTACK_API_KEY"),
    baseUrl: requireEnv("EVERSTACK_GATEWAY_URL"),
    modelOverride: process.env.EVERSTACK_TEST_MODEL,
  };
}

/**
 * Build a client. With no options this exercises the SDK default transport
 * (Connect JSON over HTTP/1.1 as of 0.2.2). Pass `{ transport: "grpc" }` to
 * force the legacy path and reproduce the 0.2.1 failure mode.
 */
export function makeClient(opts?: { transport?: TransportProtocol }): Everstack {
  const { apiKey, baseUrl } = getConfig();
  return new Everstack({
    apiKey,
    baseUrl,
    ...(opts?.transport ? { transport: opts.transport } : {}),
  });
}

/** Pick a chat model: env override first, otherwise discover from the gateway. */
export async function resolveChatModel(client: Everstack): Promise<AllModels> {
  const override = process.env.EVERSTACK_TEST_MODEL;
  if (override) return override as AllModels;

  const models = await client.models.list();
  const data = models.data;
  const pick =
    data.find((m) => m.owned_by === "openai" && !m.id.includes("embedding")) ??
    data.find((m) => m.owned_by === "anthropic") ??
    data[0];

  if (!pick) {
    throw new Error(
      "Gateway returned no models. Set EVERSTACK_TEST_MODEL to test a specific model.",
    );
  }
  return pick.id as AllModels;
}

/** True when this module is the script being executed (not imported). */
export function isMain(importMetaUrl: string): boolean {
  return fileURLToPath(importMetaUrl) === argv[1];
}

export function banner(title: string): void {
  console.log(`\n=== ${title} ===`);
  console.log(`@everstack/node v${sdkVersion()}  →  ${process.env.EVERSTACK_GATEWAY_URL ?? "(no gateway set)"}`);
}
