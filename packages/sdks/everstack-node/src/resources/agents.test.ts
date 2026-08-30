import { strict as assert } from "node:assert";
import test from "node:test";

import { NotFoundError } from "../errors.js";
import { AgentStream, Agents } from "./agents.js";

test("agents.definitions.create forwards to createAgent", async () => {
  const calls: string[] = [];
  const response = { ok: true };

  const raw = {
    createAgent: async (request: unknown) => {
      calls.push("createAgent");
      return { ...response, request };
    },
  } as unknown as ConstructorParameters<typeof Agents>[0];

  const agents = new Agents(raw);
  const request = { name: "agent-1" } as never;
  const result = await agents.definitions.create(request);

  assert.deepEqual(calls, ["createAgent"]);
  assert.deepEqual(result, { ok: true, request });
});

test("agents.sessions.runTurn forwards to runTurn", async () => {
  const expected = { sessionStatus: 0 } as const;
  const raw = {
    runTurn: async () => expected,
  } as unknown as ConstructorParameters<typeof Agents>[0];

  const agents = new Agents(raw);
  const result = await agents.sessions.runTurn({} as never);

  assert.deepEqual(result, expected);
});

test("agents maps connect not found to NotFoundError", async () => {
  const raw = {
    getAgent: async () => {
      throw { code: 5, message: "missing" };
    },
  } as unknown as ConstructorParameters<typeof Agents>[0];

  const agents = new Agents(raw);

  await assert.rejects(async () => {
    await agents.definitions.get({} as never);
}, NotFoundError);
});

test("agents.update explicitly clears an empty tool list", async () => {
  let captured: { tools?: string[]; clearTools?: boolean } | undefined;
  const raw = {
    updateAgent: async (request: { tools?: string[]; clearTools?: boolean }) => {
      captured = request;
      return {
        agent: {
          id: "agent-1",
          tenantId: "tenant-1",
          name: "support",
          description: "",
          model: "model",
          systemPrompt: "",
          tools: [],
          maxTurns: 0,
          maxToolCallsPerTurn: 0,
          enabled: true,
          mode: 0,
          maxSteps: 0,
          taskPermissionMode: 0,
          hidden: false,
          lifecycleMode: 0,
          lifecycleStatus: 0,
          sandboxId: "",
          primarySessionId: "",
        },
      };
    },
  } as unknown as ConstructorParameters<typeof Agents>[0];

  const agents = new Agents(raw);
  await agents.update({ id: "agent-1", tools: [] });

  assert.deepEqual(captured?.tools, []);
  assert.equal(captured?.clearTools, true);
});

test("agents.create forwards JSON config as a protobuf Struct", async () => {
  let captured: { config?: unknown } | undefined;
  const raw = {
    createAgent: async (request: { config?: unknown }) => {
      captured = request;
      return {
        agent: {
          id: "agent-1",
          tenantId: "tenant-1",
          name: "support",
          model: "gpt-5.6",
          tools: [],
          enabled: true,
          mode: 0,
          taskPermissionMode: 0,
          lifecycleMode: 0,
        },
      };
    },
  } as unknown as ConstructorParameters<typeof Agents>[0];

  const agents = new Agents(raw);
  await agents.create({
    name: "support",
    model: "gpt-5.6",
    config: { reasoning_effort: "none", nested: { enabled: true } },
  });

  assert.ok(captured?.config);
  assert.deepEqual(captured.config, {
    reasoning_effort: "none",
    nested: { enabled: true },
  });
});

test("agent stream exposes canonical runtime error events", async () => {
  async function* rawEvents() {
    yield {
      type: "session.error",
      sessionId: "session-1",
      turnNumber: 1,
      error: "provider rejected the request",
    } as never;
  }

  const events = [];
  for await (const event of new AgentStream(rawEvents())) {
    events.push(event);
  }

  assert.deepEqual(events, [
    {
      type: "error",
      source_type: "session.error",
      session_id: "session-1",
      turn_number: 1,
      error: "provider rejected the request",
    },
  ]);
});

test("agent stream accepts turn.end events without an embedded turn", async () => {
  async function* rawEvents() {
    yield {
      type: "turn.end",
      sessionId: "session-1",
      turnNumber: 1,
      finishReason: "stop",
    } as never;
  }

  const stream = new AgentStream(rawEvents());
  const events = [];
  for await (const event of stream) events.push(event);

  assert.equal(events[0]?.type, "turn.end");
  assert.equal((events[0] as { turn?: unknown }).turn, undefined);
  await assert.rejects(stream.finalTurn(), /without an embedded final turn/);
});
