import assert from "node:assert/strict";
import test from "node:test";

import {
  isLikelyChatModel,
  normalizeProvider,
  providerNativeModel,
  readConfig,
  resolveIdentityContext,
  resolveRoute,
} from "./config.js";
import type { EverstackClient } from "./types.js";

test("normalizes providers and sends provider-native agent model IDs", () => {
  assert.equal(normalizeProvider("openai"), "@openai");
  assert.equal(normalizeProvider("@anthropic"), "@anthropic");
  assert.equal(providerNativeModel("gpt-4o-mini"), "gpt-4o-mini");
  assert.equal(
    providerNativeModel("@anthropic/claude-sonnet-4"),
    "claude-sonnet-4",
  );
});

test("filters non-chat model families during discovery", () => {
  assert.equal(isLikelyChatModel("gpt-4o-mini"), true);
  assert.equal(isLikelyChatModel("text-embedding-3-small"), false);
  assert.equal(isLikelyChatModel("gpt-4o-realtime-preview"), false);
  assert.equal(isLikelyChatModel("whisper-1"), false);
});

test("reads and normalizes environment configuration", () => {
  const config = readConfig(["node", "index.ts", "--", "Compute", "42"], {
    EVERSTACK_GATEWAY_URL: "https://dev.example.com/",
    EVERSTACK_API_KEY: "pk_test",
    EVERSTACK_PROVIDER: "openai",
    TRACE_WAIT_MS: "45000",
    KEEP_RESOURCES: "yes",
  });

  assert.equal(config.baseUrl, "https://dev.example.com");
  assert.equal(config.provider, "@openai");
  assert.equal(config.prompt, "Compute 42");
  assert.equal(config.benchmarkMode, false);
  assert.equal(config.traceWaitMs, 45_000);
  assert.equal(config.keepResources, true);
});

test("uses the qualification suite when no custom prompt is provided", () => {
  const config = readConfig(["node", "index.ts"], {
    EVERSTACK_GATEWAY_URL: "https://dev.example.com",
    EVERSTACK_API_KEY: "pk_test",
  });

  assert.equal(config.benchmarkMode, true);
  assert.match(config.prompt, /Sieve of Eratosthenes/);
  assert.match(config.prompt, /EVERSTACK_BENCHMARK_RESULT/);
});

test("rejects conflicting model and provider routes", async () => {
  const config = readConfig(["node", "index.ts"], {
    EVERSTACK_GATEWAY_URL: "https://dev.example.com",
    EVERSTACK_API_KEY: "pk_test",
    EVERSTACK_PROVIDER: "openai",
    EVERSTACK_MODEL: "@anthropic/claude-sonnet-4",
  });

  await assert.rejects(
    resolveRoute({} as EverstackClient, config),
    /conflicts with model/,
  );
});

test("rejects malformed URLs and partial integer values", () => {
  assert.throws(
    () =>
      readConfig(["node", "index.ts"], {
        EVERSTACK_GATEWAY_URL: "dev.example.com",
        EVERSTACK_API_KEY: "pk_test",
      }),
    /absolute HTTP or HTTPS URL/,
  );
  assert.throws(
    () =>
      readConfig(["node", "index.ts"], {
        EVERSTACK_GATEWAY_URL: "https://dev.example.com",
        EVERSTACK_API_KEY: "pk_test",
        TRACE_WAIT_MS: "15seconds",
      }),
    /positive integer/,
  );
});

test("resolves organization context from an API key", async () => {
  const config = readConfig(["node", "index.ts"], {
    EVERSTACK_GATEWAY_URL: "https://dev.example.com",
    EVERSTACK_API_KEY: "pk_test",
  });
  const client = {
    identity: {
      whoami: async () => ({
        user_id: "user-1",
        email: "dev@example.com",
        org_id: "org-1",
        org_slug: "example",
        plan_tier: "dev",
      }),
    },
  } as unknown as EverstackClient;

  const resolved = await resolveIdentityContext(client, config);

  assert.equal(resolved.orgId, "org-1");
  assert.equal(resolved.userId, "user-1");
});
