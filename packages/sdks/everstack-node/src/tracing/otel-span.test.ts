import { test } from "node:test";
import assert from "node:assert/strict";

import { OtelSpan, type OtelSpan as OtelSpanType } from "./otel-span.js";
import { Traces } from "../resources/traces.js";

const noopSink = async () => {};

/** Returns an asserting getter for a span's attributes, keyed by attribute name. */
function attrs(span: OtelSpanType) {
  const map = new Map(
    span.toOtlpSpan().attributes.map((a) => [a.key, a.value]),
  );
  return (key: string): { stringValue?: string; intValue?: string } => {
    const v = map.get(key);
    assert.ok(v, `attribute "${key}" is present`);
    return v as { stringValue?: string; intValue?: string };
  };
}

test("trace/span ids are hex internally and base64 (correct byte length) on the wire", () => {
  const span = new OtelSpan({ name: "s" }, noopSink, "svc");
  assert.match(span.traceId, /^[0-9a-f]{32}$/, "traceId is 32 hex chars");
  assert.match(span.spanId, /^[0-9a-f]{16}$/, "spanId is 16 hex chars");

  const wire = span.toOtlpSpan();
  // protojson decodes bytes from base64; must round-trip to 16 / 8 bytes.
  assert.equal(Buffer.from(wire.traceId, "base64").length, 16);
  assert.equal(Buffer.from(wire.spanId, "base64").length, 8);
  assert.equal(
    Buffer.from(wire.traceId, "base64").toString("hex"),
    span.traceId,
  );
});

test("setters map onto the exact attribute keys the trace sheet reads", () => {
  const span = new OtelSpan(
    { name: "chat", type: "GENERATION", model: "gpt-4o" },
    noopSink,
    "svc",
  );
  span
    .setInput("# input md")
    .setOutput("# output md")
    .setMessages([{ role: "user", content: "hi" }])
    .setChoices([{ index: 0, message: { role: "assistant", content: "yo" } }])
    .setUsage({ inputTokens: 10, outputTokens: 20, totalTokens: 30 })
    .setAttribute("custom.key", "v");

  const a = attrs(span);
  assert.equal(a("observation.type").stringValue, "GENERATION");
  assert.equal(a("input.value").stringValue, "# input md");
  assert.equal(a("output.value").stringValue, "# output md");
  assert.equal(a("llm.request.model").stringValue, "gpt-4o");
  assert.equal(a("llm.response.model").stringValue, "gpt-4o");
  assert.equal(a("gen_ai.request.model").stringValue, "gpt-4o");
  assert.deepEqual(JSON.parse(a("llm.request.messages").stringValue!), [
    { role: "user", content: "hi" },
  ]);
  assert.deepEqual(JSON.parse(a("llm.response.choices").stringValue!), [
    { index: 0, message: { role: "assistant", content: "yo" } },
  ]);
  // usage is int-valued (protojson expects int64 as string)
  assert.equal(a("gen_ai.usage.input_tokens").intValue, "10");
  assert.equal(a("gen_ai.usage.total_tokens").intValue, "30");
  assert.equal(a("custom.key").stringValue, "v");
});

test("reasoning survives nested in choices", () => {
  const span = new OtelSpan({ name: "r", type: "GENERATION" }, noopSink, "svc");
  span.setChoices([
    {
      index: 0,
      message: {
        role: "assistant",
        content: "answer",
        reasoning_content: "## thinking",
      },
    },
  ]);
  const parsed = JSON.parse(attrs(span)("llm.response.choices").stringValue!);
  assert.equal(parsed[0].message.reasoning_content, "## thinking");
});

test("string input is passed through verbatim; objects are JSON-encoded", () => {
  const span = new OtelSpan({ name: "s" }, noopSink, "svc");
  span.setInput("plain string");
  span.setOutput({ a: 1 });
  const a = attrs(span);
  assert.equal(a("input.value").stringValue, "plain string");
  assert.equal(a("output.value").stringValue, '{"a":1}');
});

test("object payloads with bigint values are JSON-encoded safely", () => {
  const span = new OtelSpan({ name: "s" }, noopSink, "svc");
  span.setOutput({ id: "trigger", durationNanos: 123n });
  const parsed = JSON.parse(attrs(span)("output.value").stringValue!);
  assert.deepEqual(parsed, { id: "trigger", durationNanos: "123" });
});

test("child span shares trace id and links to parent span id", () => {
  const parent = new OtelSpan(
    { name: "root", kind: "SERVER" },
    noopSink,
    "svc",
  );
  const child = parent.startChild({ name: "gen", type: "GENERATION" });

  assert.equal(child.traceId, parent.traceId, "child inherits trace id");
  assert.notEqual(child.spanId, parent.spanId);

  const wire = child.toOtlpSpan();
  assert.ok(wire.parentSpanId, "child has a parentSpanId");
  assert.equal(
    Buffer.from(wire.parentSpanId!, "base64").toString("hex"),
    parent.spanId,
    "parentSpanId decodes to parent's hex span id",
  );

  // root has no parent
  assert.equal(parent.toOtlpSpan().parentSpanId, undefined);
});

test("attach to an existing trace id", () => {
  const tid = "abcdef0123456789abcdef0123456789";
  const span = new OtelSpan({ name: "s", traceId: tid }, noopSink, "svc");
  assert.equal(span.traceId, tid);
});

test("timestamps: end >= start and duration is non-negative", async () => {
  const span = new OtelSpan({ name: "s" }, noopSink, "svc");
  await span.end();
  const w = span.toOtlpSpan();
  assert.ok(BigInt(w.endTimeUnixNano) >= BigInt(w.startTimeUnixNano));
});

test("end() flushes exactly once (idempotent) and can set output", async () => {
  let calls = 0;
  let flushed: OtelSpanType | undefined;
  const span = new OtelSpan(
    { name: "s" },
    async (s) => {
      calls++;
      flushed = s;
    },
    "svc",
  );
  await span.end("# final");
  await span.end("# ignored");
  assert.equal(calls, 1, "sink called once");
  assert.ok(flushed);
  assert.equal(attrs(flushed)("output.value").stringValue, "# final");
});

test("setError marks status ERROR with exception attributes", () => {
  const span = new OtelSpan({ name: "s" }, noopSink, "svc");
  span.setError(new TypeError("boom"));
  const w = span.toOtlpSpan();
  assert.equal(w.status.code, 2);
  assert.equal(w.status.message, "boom");
  const a = attrs(span);
  assert.equal(a("exception.type").stringValue, "TypeError");
  assert.equal(a("exception.message").stringValue, "boom");
  assert.equal(a("observation.level").stringValue, "ERROR");
});

test("export request wraps the span with a service.name resource", () => {
  const span = new OtelSpan({ name: "s" }, noopSink, "my-svc");
  const req = span.toExportRequest();
  const rs = req.resourceSpans[0]!;
  assert.equal(rs.resource.attributes[0]!.key, "service.name");
  assert.equal(
    (rs.resource.attributes[0]!.value as { stringValue?: string }).stringValue,
    "my-svc",
  );
  assert.equal(rs.scopeSpans[0]!.spans.length, 1);
  assert.equal(rs.scopeSpans[0]!.spans[0]!.name, "s");
});

test("Traces.startSpan requires an emit config", () => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const withoutConfig = new Traces({} as any);
  assert.throws(
    () => withoutConfig.startSpan({ name: "s" }),
    /requires a configured client/,
  );

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const withConfig = new Traces({} as any, {
    baseUrl: "http://localhost:8089",
    apiKey: "sk-test",
  });
  const span = withConfig.startSpan({ name: "s", type: "GENERATION" });
  assert.ok(span instanceof OtelSpan);
});

test("emit POSTs OTLP JSON to {baseUrl}/v1/traces with bearer auth", async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    return {
      ok: true,
      status: 200,
      statusText: "OK",
      text: async () => "",
    } as Response;
  }) as typeof fetch;

  try {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const traces = new Traces({} as any, {
      baseUrl: "http://localhost:8089/",
      apiKey: "sk-test",
    });
    const span = traces.startSpan({ name: "chat", type: "GENERATION" });
    span.setOutput("# hi");
    await span.end();

    assert.equal(calls.length, 1);
    const call = calls[0]!;
    assert.equal(
      call.url,
      "http://localhost:8089/v1/traces",
      "trailing slash normalized",
    );
    const headers = call.init.headers as Record<string, string>;
    assert.equal(headers["Authorization"], "Bearer sk-test");
    assert.equal(headers["x-evs-api-key"], "sk-test");
    assert.equal(headers["Content-Type"], "application/json");
    const body = JSON.parse(call.init.body as string);
    assert.equal(body.resourceSpans[0].scopeSpans[0].spans[0].name, "chat");
  } finally {
    globalThis.fetch = originalFetch;
  }
});
