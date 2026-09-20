import assert from "node:assert/strict";
import test from "node:test";
import { OpenAIAgents } from "./openai-agents.js";
import { APIError } from "../errors.js";

test("session mutations preserve request identity and never retry on an ambiguous failure", async (t) => {
  let calls = 0;
  const body = {
    connection_id: "c",
    idempotency_key: "durable-intent",
    model: "model",
    environment: { type: "none" as const },
    input: "hello",
  };
  t.mock.method(
    globalThis,
    "fetch",
    async (url: string, options: RequestInit) => {
      calls++;
      assert.equal(url, "https://instance.example/v1/openai-agents/sessions");
      assert.equal(options.redirect, "error");
      assert.equal(
        new Headers(options.headers).get("x-evs-api-key"),
        "instance-key",
      );
      assert.equal(new Headers(options.headers).get("x-evs-org-id"), "org");
      assert.deepEqual(JSON.parse(String(options.body)), body);
      throw new TypeError("connection lost");
    },
  );
  const api = new OpenAIAgents({
    baseUrl: "https://instance.example/",
    apiKey: "instance-key",
    orgId: "org",
  });
  await assert.rejects(api.sessions.create(body), /connection lost/);
  assert.equal(calls, 1);
});

test("event retrieval uses the durable Everstack cursor and retains provider error codes", async (t) => {
  t.mock.method(globalThis, "fetch", async (url: string) => {
    assert.equal(
      url,
      "https://instance.example/v1/openai-agents/sessions/session%2Fone/events?after=12&limit=50",
    );
    return new Response(
      JSON.stringify({
        error: { message: "Access revoked", code: "permission_denied" },
      }),
      { status: 403 },
    );
  });
  const api = new OpenAIAgents({
    baseUrl: "https://instance.example",
    apiKey: "instance-key",
  });
  await assert.rejects(
    api.sessions.events("session/one", { after: 12, limit: 50 }),
    (error: unknown) =>
      error instanceof APIError &&
      error.status === 403 &&
      error.code === "permission_denied",
  );
});

test("request timeout aborts the transport without a second mutation", async (t) => {
  let calls = 0;
  t.mock.method(globalThis, "fetch", (_url: string, options: RequestInit) => {
    calls++;
    return new Promise<Response>((_resolve, reject) => {
      options.signal?.addEventListener(
        "abort",
        () => reject(new Error("aborted")),
        { once: true },
      );
    });
  });
  const api = new OpenAIAgents({
    baseUrl: "https://instance.example",
    apiKey: "instance-key",
    timeout: 5,
  });
  await assert.rejects(api.sessions.cancel("s"), /aborted/);
  assert.equal(calls, 1);
});

test("refreshes bearer credentials per request and does not also send an API key", async (t) => {
  let refreshes = 0;
  let requests = 0;
  t.mock.method(
    globalThis,
    "fetch",
    async (_url: string, options: RequestInit) => {
      requests++;
      const headers = new Headers(options.headers);
      assert.equal(
        headers.get("Authorization"),
        `Bearer refreshed-${requests}`,
      );
      assert.equal(headers.has("x-evs-api-key"), false);
      return new Response(JSON.stringify({ data: [] }));
    },
  );
  const api = new OpenAIAgents({
    baseUrl: "https://instance.example",
    apiKey: "read-only-key",
    accessToken: async () => `refreshed-${++refreshes}`,
  });
  await api.sessions.list();
  await api.approvals.list();
  assert.equal(refreshes, 2);
});
