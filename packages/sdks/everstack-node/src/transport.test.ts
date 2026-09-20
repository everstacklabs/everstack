import { strict as assert } from "node:assert";
import test from "node:test";

import { createCompatibleFetch, createTransport, detectRuntime } from "./transport.js";

const baseOptions = { baseUrl: "https://gateway.example", apiKey: "pk_test" };

test("detectRuntime identifies the host node process", () => {
  assert.equal(detectRuntime(), "node");
});

// Bun reports a process.versions.node of its own, so a naive Node check would
// route it onto the HTTP/2 transport, where it stalls.
test("detectRuntime does not mistake Bun for Node", () => {
  const original = Object.getOwnPropertyDescriptor(process, "versions")!;
  Object.defineProperty(process, "versions", {
    value: { node: "22.0.0", bun: "1.2.5" },
    configurable: true,
  });

  try {
    assert.equal(detectRuntime(), "bun");
  } finally {
    Object.defineProperty(process, "versions", original);
  }
});

test("createCompatibleFetch rewrites the redirect mode Workers rejects", async () => {
  const seen: RequestInit[] = [];
  const spy = (async (_input: unknown, init?: RequestInit) => {
    seen.push(init ?? {});
    return new Response("{}");
  }) as unknown as typeof globalThis.fetch;

  const wrapped = createCompatibleFetch(spy);
  await wrapped("https://gateway.example", { redirect: "error", method: "POST" });

  assert.equal(seen[0]?.redirect, "manual");
  assert.equal(seen[0]?.method, "POST", "unrelated init fields must survive");
});

test("createCompatibleFetch leaves other requests untouched", async () => {
  const seen: RequestInit[] = [];
  const spy = (async (_input: unknown, init?: RequestInit) => {
    seen.push(init ?? {});
    return new Response("{}");
  }) as unknown as typeof globalThis.fetch;

  await createCompatibleFetch(spy)("https://gateway.example", { redirect: "follow" });

  assert.equal(seen[0]?.redirect, "follow");
});

// The picker is the whole defect: every runtime that is not Node used to be
// handed an HTTP/2 transport that cannot work there.
test("createTransport routes non-Node runtimes onto fetch", () => {
  for (const runtime of ["bun", "deno", "edge"] as const) {
    const transport = createTransport({ ...baseOptions, runtime });
    assert.ok(transport, `${runtime} must resolve a transport`);
  }
});

test("createTransport still gives Node and browsers their existing transports", () => {
  assert.ok(createTransport({ ...baseOptions, runtime: "node" }));
  assert.ok(createTransport({ ...baseOptions, runtime: "browser" }));
});
